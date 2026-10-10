// Package slas owns SLA definitions, their live reports and issued (frozen)
// report snapshots. Every availability number comes from
// shared/analytics IntegrateTimeline — the engine behind the monitor
// headline — so an SLA over one monitor and that monitor's own availability
// cannot disagree (docs/state-semantics.md S-U1, S-U6, S-U7).
package slas

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // SLA timezones must resolve even on images without zoneinfo

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/db"
)

var (
	ErrNotFound = errors.New("SLA not found")
	ErrConflict = errors.New("conflict")
)

// Invalid is a validation failure shown to the caller verbatim.
type Invalid struct{ Message string }

func (e Invalid) Error() string { return e.Message }

func invalid(format string, args ...any) error { return Invalid{Message: fmt.Sprintf(format, args...)} }

// Conflict wraps ErrConflict with a caller-facing message.
type Conflict struct{ Message string }

func (e Conflict) Error() string { return e.Message }
func (e Conflict) Unwrap() error { return ErrConflict }

type Service struct {
	db        *db.Client
	analytics *sharedanalytics.Repository
	now       func() time.Time
}

func NewService(dbClient *db.Client, analytics *sharedanalytics.Repository) *Service {
	return &Service{db: dbClient, analytics: analytics, now: func() time.Time { return time.Now().UTC() }}
}

const slaColumns = `s.id, s.tenant_id, s.name, s.description, s.target_pct::float8, s.aggregation, s.period,
	s.timezone, s.degraded_counts_as_down, s.tags, s.created_at, s.updated_at,
	COALESCE((SELECT array_agg(sm.monitor_id ORDER BY sm.monitor_id) FROM sla_monitors sm
		JOIN monitors m ON m.id = sm.monitor_id AND m.deleted_at IS NULL WHERE sm.sla_id = s.id), '{}')`

func scanSLA(row interface{ Scan(...any) error }) (*models.SLA, error) {
	var s models.SLA
	var tags pq.StringArray
	var ids pq.StringArray
	if err := row.Scan(&s.ID, &s.TenantID, &s.Name, &s.Description, &s.TargetPct, &s.Aggregation, &s.Period,
		&s.Timezone, &s.DegradedCountsAsDown, &tags, &s.CreatedAt, &s.UpdatedAt, &ids); err != nil {
		return nil, err
	}
	s.Tags = []string(tags)
	s.MonitorIDs = make([]uuid.UUID, 0, len(ids))
	for _, raw := range ids {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("parse SLA monitor id: %w", err)
		}
		s.MonitorIDs = append(s.MonitorIDs, id)
	}
	return &s, nil
}

// Get returns one SLA of the tenant.
func (s *Service) Get(ctx context.Context, tenantID, id uuid.UUID) (*models.SLA, error) {
	sla, err := scanSLA(s.db.QueryRowContext(ctx, `SELECT `+slaColumns+` FROM slas s WHERE s.tenant_id = $1 AND s.id = $2`, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get SLA: %w", err)
	}
	return sla, nil
}

// List returns the tenant's SLAs with their running period's status. With a
// monitorID, only SLAs whose resolved membership includes it.
func (s *Service) List(ctx context.Context, tenantID, monitorID uuid.UUID) ([]models.SLA, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+slaColumns+` FROM slas s WHERE s.tenant_id = $1 ORDER BY s.name`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list SLAs: %w", err)
	}
	var slas []models.SLA
	for rows.Next() {
		sla, err := scanSLA(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan SLA: %w", err)
		}
		slas = append(slas, *sla)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("iterate SLAs: %w", err)
	}

	out := make([]models.SLA, 0, len(slas))
	for i := range slas {
		members, err := s.resolveMembers(ctx, tenantID, &slas[i])
		if err != nil {
			return nil, err
		}
		if monitorID != uuid.Nil && !containsMember(members, monitorID) {
			continue
		}
		status, err := s.currentStatus(ctx, tenantID, &slas[i], members)
		if err != nil {
			return nil, err
		}
		slas[i].Current = status
		out = append(out, slas[i])
	}
	return out, nil
}

func containsMember(members []member, id uuid.UUID) bool {
	for _, m := range members {
		if m.ID == id {
			return true
		}
	}
	return false
}

// Create validates and stores an SLA.
func (s *Service) Create(ctx context.Context, tenantID uuid.UUID, req *models.CreateSLARequest) (*models.SLA, error) {
	def := definition{
		Name:                 req.Name,
		Description:          req.Description,
		TargetPct:            req.TargetPct,
		Aggregation:          req.Aggregation,
		Period:               req.Period,
		Timezone:             req.Timezone,
		DegradedCountsAsDown: req.DegradedCountsAsDown,
		Tags:                 req.Tags,
	}
	monitorIDs, err := parseMonitorIDs(req.MonitorIDs)
	if err != nil {
		return nil, err
	}
	if err := def.normalize(len(monitorIDs)); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := validateMonitorsTx(ctx, tx, tenantID, monitorIDs); err != nil {
		return nil, err
	}
	id := uuid.New()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO slas (id, tenant_id, name, description, target_pct, aggregation, period, timezone, degraded_counts_as_down, tags)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, id, tenantID, def.Name, def.Description, def.TargetPct, def.Aggregation, def.Period, def.Timezone,
		def.DegradedCountsAsDown, pq.Array(def.Tags)); err != nil {
		return nil, mapUniqueViolation(err, "an SLA with this name already exists")
	}
	if err := replaceMonitorsTx(ctx, tx, id, monitorIDs); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit SLA: %w", err)
	}
	return s.Get(ctx, tenantID, id)
}

// Update applies a partial update; MonitorIDs, when present, replaces the
// explicit monitor set.
func (s *Service) Update(ctx context.Context, tenantID, id uuid.UUID, req *models.UpdateSLARequest) (*models.SLA, error) {
	existing, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	def := definitionOf(existing)
	if req.Name != nil {
		def.Name = *req.Name
	}
	if req.Description != nil {
		def.Description = *req.Description
	}
	if req.TargetPct != nil {
		def.TargetPct = *req.TargetPct
	}
	if req.Aggregation != nil {
		def.Aggregation = *req.Aggregation
	}
	if req.Period != nil {
		def.Period = *req.Period
	}
	if req.Timezone != nil {
		def.Timezone = *req.Timezone
	}
	if req.DegradedCountsAsDown != nil {
		def.DegradedCountsAsDown = *req.DegradedCountsAsDown
	}
	if req.Tags != nil {
		def.Tags = *req.Tags
	}
	monitorIDs := existing.MonitorIDs
	if req.MonitorIDs != nil {
		if monitorIDs, err = parseMonitorIDs(*req.MonitorIDs); err != nil {
			return nil, err
		}
	}
	if err := def.normalize(len(monitorIDs)); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if req.MonitorIDs != nil {
		if err := validateMonitorsTx(ctx, tx, tenantID, monitorIDs); err != nil {
			return nil, err
		}
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE slas SET name = $3, description = $4, target_pct = $5, aggregation = $6, period = $7,
			timezone = $8, degraded_counts_as_down = $9, tags = $10, updated_at = NOW()
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, id, def.Name, def.Description, def.TargetPct, def.Aggregation, def.Period, def.Timezone,
		def.DegradedCountsAsDown, pq.Array(def.Tags))
	if err != nil {
		return nil, mapUniqueViolation(err, "an SLA with this name already exists")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	if req.MonitorIDs != nil {
		if err := replaceMonitorsTx(ctx, tx, id, monitorIDs); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit SLA: %w", err)
	}
	return s.Get(ctx, tenantID, id)
}

// Delete removes an SLA and its issued reports.
func (s *Service) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM slas WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("delete SLA: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// definition is the validated, normalized form of an SLA's settings.
type definition struct {
	Name                 string
	Description          string
	TargetPct            float64
	Aggregation          string
	Period               string
	Timezone             string
	DegradedCountsAsDown bool
	Tags                 []string
}

func definitionOf(s *models.SLA) definition {
	return definition{
		Name: s.Name, Description: s.Description, TargetPct: s.TargetPct, Aggregation: s.Aggregation,
		Period: s.Period, Timezone: s.Timezone, DegradedCountsAsDown: s.DegradedCountsAsDown, Tags: s.Tags,
	}
}

func (d *definition) normalize(monitorCount int) error {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		return invalid("name is required")
	}
	if len(d.Name) > 200 {
		return invalid("name must be at most 200 characters")
	}
	d.Description = strings.TrimSpace(d.Description)
	if math.IsNaN(d.TargetPct) || d.TargetPct <= 0 || d.TargetPct >= 100 {
		return invalid("target_pct must be between 0 and 100 (exclusive)")
	}
	// NUMERIC(7,4): keep what is stored and what is compared identical.
	d.TargetPct = math.Round(d.TargetPct*10000) / 10000
	if d.Aggregation == "" {
		d.Aggregation = models.SLAAggregationSerial
	}
	if d.Aggregation != models.SLAAggregationSerial && d.Aggregation != models.SLAAggregationMean {
		return invalid("aggregation must be serial or mean")
	}
	if d.Period == "" {
		d.Period = string(sharedanalytics.PeriodMonthly)
	}
	if !sharedanalytics.PeriodKind(d.Period).Valid() {
		return invalid("period must be weekly, monthly or quarterly")
	}
	d.Timezone = strings.TrimSpace(d.Timezone)
	if d.Timezone == "" {
		d.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(d.Timezone); err != nil || d.Timezone == "Local" {
		return invalid("timezone %q is not an IANA timezone", d.Timezone)
	}
	seen := map[string]bool{}
	tags := make([]string, 0, len(d.Tags))
	for _, t := range d.Tags {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		tags = append(tags, t)
	}
	sort.Strings(tags)
	d.Tags = tags
	if monitorCount == 0 && len(tags) == 0 {
		return invalid("select at least one monitor or tag")
	}
	return nil
}

func parseMonitorIDs(raw []string) ([]uuid.UUID, error) {
	seen := map[uuid.UUID]bool{}
	out := make([]uuid.UUID, 0, len(raw))
	for _, r := range raw {
		id, err := uuid.Parse(strings.TrimSpace(r))
		if err != nil {
			return nil, invalid("invalid monitor id %q", r)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func validateMonitorsTx(ctx context.Context, tx *sql.Tx, tenantID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	var found int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM monitors WHERE tenant_id = $1 AND id = ANY($2) AND deleted_at IS NULL
	`, tenantID, pq.Array(ids)).Scan(&found); err != nil {
		return fmt.Errorf("validate SLA monitors: %w", err)
	}
	if found != len(ids) {
		return invalid("one or more monitors not found")
	}
	return nil
}

func replaceMonitorsTx(ctx context.Context, tx *sql.Tx, slaID uuid.UUID, ids []uuid.UUID) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM sla_monitors WHERE sla_id = $1`, slaID); err != nil {
		return fmt.Errorf("clear SLA monitors: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sla_monitors (sla_id, monitor_id) SELECT $1, unnest($2::uuid[])
	`, slaID, pq.Array(ids)); err != nil {
		return fmt.Errorf("insert SLA monitors: %w", err)
	}
	return nil
}

func mapUniqueViolation(err error, message string) error {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return Conflict{Message: message}
	}
	return fmt.Errorf("write SLA: %w", err)
}

// member is one leaf monitor an SLA covers.
type member struct {
	ID   uuid.UUID
	Name string
	Type string
}

// resolveMembers expands explicit monitors and tag matches to leaf
// monitors: groups contribute their (recursive) members, never themselves —
// group monitors have no timeline (S-U4).
func (s *Service) resolveMembers(ctx context.Context, tenantID uuid.UUID, sla *models.SLA) ([]member, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE seed AS (
			SELECT m.id FROM monitors m
			WHERE m.tenant_id = $1 AND m.deleted_at IS NULL AND m.id = ANY($2)
			UNION
			SELECT m.id FROM monitors m
			WHERE m.tenant_id = $1 AND m.deleted_at IS NULL
			  AND cardinality($3::text[]) > 0 AND m.tags && $3::text[]
		),
		tree AS (
			SELECT id FROM seed
			UNION
			SELECT mg.monitor_id
			FROM tree t
			JOIN monitor_groups mg ON mg.group_id = t.id
			JOIN monitors c ON c.id = mg.monitor_id AND c.tenant_id = $1 AND c.deleted_at IS NULL
		)
		SELECT m.id, m.name, m.type
		FROM monitors m JOIN tree t ON t.id = m.id
		WHERE m.tenant_id = $1 AND m.type <> 'group'
		ORDER BY m.name, m.id
	`, tenantID, pq.Array(sla.MonitorIDs), pq.Array(sla.Tags))
	if err != nil {
		return nil, fmt.Errorf("resolve SLA members: %w", err)
	}
	defer rows.Close()
	var out []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.ID, &m.Name, &m.Type); err != nil {
			return nil, fmt.Errorf("scan SLA member: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// currentStatus is the running period's headline, computed like a report
// but without the daily, outage and response sections.
func (s *Service) currentStatus(ctx context.Context, tenantID uuid.UUID, sla *models.SLA, members []member) (*models.SLAStatus, error) {
	loc, err := time.LoadLocation(sla.Timezone)
	if err != nil {
		return nil, fmt.Errorf("SLA %s timezone: %w", sla.ID, err)
	}
	now := s.now()
	period, err := sharedanalytics.PeriodContaining(sharedanalytics.PeriodKind(sla.Period), loc, now)
	if err != nil {
		return nil, err
	}
	summary, budget, err := s.headline(ctx, tenantID, sla, members, period, now)
	if err != nil {
		return nil, err
	}
	return &models.SLAStatus{
		PeriodKey:          period.Key,
		PeriodStart:        period.Start.UTC(),
		PeriodEnd:          period.End.UTC(),
		MonitorCount:       len(members),
		HasData:            summary.HasData,
		AvailabilityPct:    summary.AvailabilityPct,
		CoveragePct:        summary.CoveragePct,
		Met:                summary.Met,
		BudgetRemainingPct: budget.RemainingPct,
	}, nil
}

func (s *Service) headline(ctx context.Context, tenantID uuid.UUID, sla *models.SLA, members []member, period sharedanalytics.Period, now time.Time) (models.SLAReportSummary, models.SLAReportBudget, error) {
	end := period.End
	if end.After(now) {
		end = now
	}
	window := sharedanalytics.TimeRange{Start: period.Start, End: end}
	if !window.End.After(window.Start) || len(members) == 0 {
		return models.SLAReportSummary{}, budgetFor(sla.TargetPct, models.SLAReportSummary{}, period), nil
	}
	res, err := s.analytics.IntegrateTimeline(ctx, tenantID, memberIDs(members), []sharedanalytics.TimeRange{window},
		sharedanalytics.TimelineOpts{DegradedCountsAsDown: sla.DegradedCountsAsDown})
	if err != nil {
		return models.SLAReportSummary{}, models.SLAReportBudget{}, err
	}
	summary := summarize(sla, res, 0, members)
	return summary, budgetFor(sla.TargetPct, summary, period), nil
}

func memberIDs(members []member) []uuid.UUID {
	ids := make([]uuid.UUID, len(members))
	for i, m := range members {
		ids[i] = m.ID
	}
	return ids
}

// ListReports returns the SLA's issued reports, newest first.
func (s *Service) ListReports(ctx context.Context, tenantID, slaID uuid.UUID) ([]models.SLAReportRef, error) {
	if _, err := s.Get(ctx, tenantID, slaID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.sla_id, r.period_key, r.period_start, r.period_end, r.availability_pct,
			r.target_pct::float8, r.met, r.issued_at, `+issuedByExpr+`
		FROM sla_reports r
		LEFT JOIN admin_users u ON u.id = r.issued_by_admin_id
		WHERE r.tenant_id = $1 AND r.sla_id = $2
		ORDER BY r.period_start DESC
	`, tenantID, slaID)
	if err != nil {
		return nil, fmt.Errorf("list SLA reports: %w", err)
	}
	defer rows.Close()
	out := []models.SLAReportRef{}
	for rows.Next() {
		var ref models.SLAReportRef
		var avail sql.NullFloat64
		var met sql.NullBool
		if err := rows.Scan(&ref.ID, &ref.SLAID, &ref.PeriodKey, &ref.PeriodStart, &ref.PeriodEnd, &avail,
			&ref.TargetPct, &met, &ref.IssuedAt, &ref.IssuedBy); err != nil {
			return nil, fmt.Errorf("scan SLA report: %w", err)
		}
		if avail.Valid {
			ref.AvailabilityPct = &avail.Float64
		}
		if met.Valid {
			ref.Met = &met.Bool
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

const issuedByExpr = `COALESCE(u.username, CASE WHEN r.issued_by_api_key_id IS NOT NULL THEN 'API key' ELSE 'deleted user' END)`

// Issuer identifies who issues a report.
type Issuer struct {
	AdminID  uuid.UUID
	APIKeyID uuid.UUID
}

// IssueReport freezes the report of a closed period. periodKey "" means the
// last closed period.
func (s *Service) IssueReport(ctx context.Context, tenantID, slaID uuid.UUID, periodKey string, by Issuer) (*models.SLAReport, error) {
	sla, err := s.Get(ctx, tenantID, slaID)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(sla.Timezone)
	if err != nil {
		return nil, fmt.Errorf("SLA timezone: %w", err)
	}
	now := s.now()
	kind := sharedanalytics.PeriodKind(sla.Period)
	var period sharedanalytics.Period
	if strings.TrimSpace(periodKey) == "" {
		cur, err := sharedanalytics.PeriodContaining(kind, loc, now)
		if err != nil {
			return nil, err
		}
		period = cur.Previous(loc)
	} else if period, err = sharedanalytics.ParsePeriod(kind, loc, periodKey); err != nil {
		return nil, invalid("%s", err.Error())
	}
	if period.End.After(now) {
		return nil, invalid("period %s has not ended yet; only closed periods can be issued", period.Key)
	}

	report, err := s.buildReport(ctx, tenantID, sla, period, false, now)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("encode SLA report: %w", err)
	}
	id := uuid.New()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO sla_reports (id, tenant_id, sla_id, period_key, period_start, period_end, availability_pct,
			target_pct, met, issued_by_admin_id, issued_by_api_key_id, data)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, id, tenantID, slaID, period.Key, period.Start.UTC(), period.End.UTC(), report.Summary.AvailabilityPct,
		sla.TargetPct, report.Summary.Met, nullUUID(by.AdminID), nullUUID(by.APIKeyID), data); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return nil, Conflict{Message: fmt.Sprintf("a report for %s was already issued", period.Key)}
		}
		return nil, fmt.Errorf("store SLA report: %w", err)
	}
	return s.GetIssuedReport(ctx, tenantID, id)
}

func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// GetIssuedReport returns a frozen report exactly as issued.
func (s *Service) GetIssuedReport(ctx context.Context, tenantID, reportID uuid.UUID) (*models.SLAReport, error) {
	var data []byte
	var issue models.SLAReportIssue
	err := s.db.QueryRowContext(ctx, `
		SELECT r.data, r.id, r.issued_at, `+issuedByExpr+`
		FROM sla_reports r
		LEFT JOIN admin_users u ON u.id = r.issued_by_admin_id
		WHERE r.tenant_id = $1 AND r.id = $2
	`, tenantID, reportID).Scan(&data, &issue.ID, &issue.IssuedAt, &issue.IssuedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get SLA report: %w", err)
	}
	var report models.SLAReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("decode SLA report: %w", err)
	}
	report.Issued = &issue
	return &report, nil
}

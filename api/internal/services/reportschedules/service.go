// Package reportschedules emails SLA reports when their period closes.
//
// Each SLA has at most one schedule: a recipient list (anyone, inside the
// workspace or not), whether the CSV is attached next to the PDF, and
// whether it is enabled. When a period closes (plus ReportIssueGrace) the
// API's Runner issues the period's report (slas.IssueReport, so it is
// frozen) and mails it to every recipient who has not unsubscribed.
//
// Delivery is at most once per recipient and period, and never silently
// dropped:
//   - planning claims the schedule FOR UPDATE SKIP LOCKED and commits one
//     delivery row per recipient (and the next run time) before anyone is
//     mailed, so any number of API replicas plan each period once;
//   - each delivery is claimed (status sending) and committed before its
//     message goes out, and its outcome recorded after; a failure is retried
//     with backoff and shown on the schedule (last_error, per-recipient
//     status); a message that may have reached the relay (connection lost
//     after the data, or an API that died mid-send) becomes uncertain and is
//     never sent again.
//
// Every message goes to one recipient and carries that recipient's signed
// unsubscribe link (shared/actionlink, ActionUnsubscribeReport), in the body
// and as List-Unsubscribe with one-click POST (RFC 8058). Schedules therefore
// need the platform mailer (SMTP_*), APP_BASE_URL and PROBARA_SECRETS_KEY;
// without any of them a schedule cannot be enabled and the Runner does not
// start.
package reportschedules

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/slas"
	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/mail"
)

// MaxRecipients bounds one schedule's recipient list (also a CHECK in
// migration 000116).
const MaxRecipients = 50

var ErrNotFound = errors.New("not found")

// Invalid is a validation failure shown to the caller verbatim.
type Invalid struct{ Message string }

func (e Invalid) Error() string { return e.Message }

func invalid(format string, args ...any) error { return Invalid{Message: fmt.Sprintf(format, args...)} }

// Reports is the slice of the SLA service schedules use.
type Reports interface {
	Get(ctx context.Context, tenantID, id uuid.UUID) (*models.SLA, error)
	IssueReport(ctx context.Context, tenantID, slaID uuid.UUID, periodKey string, by slas.Issuer) (*models.SLAReport, error)
	IssuedReportForPeriod(ctx context.Context, tenantID, slaID uuid.UUID, periodKey string) (*models.SLAReport, error)
}

// Links mints and verifies unsubscribe tokens (an *actionlink.Signer).
type Links interface {
	ActionToken(action, tenantID, alertID, channelID string, issuedAt time.Time) (string, error)
	Verify(token, action string) (actionlink.Claims, error)
}

// AuditRecorder records what happens outside an audited request: the
// runner's sends and the public unsubscribes.
type AuditRecorder interface {
	Record(event audit.Event)
}

// Service manages schedules and sends their reports.
type Service struct {
	db         *db.Client
	reports    Reports
	mailer     mail.Sender
	links      Links
	appBaseURL string
	audit      AuditRecorder
	log        *logger.Logger
	now        func() time.Time
}

// Config is what the API process provides. Mailer may be nil (no SMTP),
// Links nil (no PROBARA_SECRETS_KEY) and AppBaseURL empty: schedules can
// then be read and kept but not enabled. Audit may be nil.
type Config struct {
	Mailer     mail.Sender
	Links      *actionlink.Signer
	AppBaseURL string
	Audit      AuditRecorder
	Log        *logger.Logger
}

// NewService wires the service.
func NewService(dbClient *db.Client, reports Reports, cfg Config) *Service {
	s := &Service{
		db: dbClient, reports: reports, mailer: cfg.Mailer, appBaseURL: strings.TrimRight(cfg.AppBaseURL, "/"),
		audit: cfg.Audit, log: cfg.Log, now: func() time.Time { return time.Now().UTC() },
	}
	// A nil *Signer in the interface would read as configured.
	if cfg.Links != nil {
		s.links = cfg.Links
	}
	return s
}

// MailProblems says why this API cannot send scheduled reports; empty when
// it can.
func (s *Service) MailProblems() []string {
	problems := []string{}
	if s.mailer == nil {
		problems = append(problems, "SMTP is not configured on the API: set SMTP_HOST and SMTP_FROM.")
	}
	if s.appBaseURL == "" {
		problems = append(problems, "APP_BASE_URL is not set on the API: report emails link to the report and to their unsubscribe page there.")
	}
	if s.links == nil {
		problems = append(problems, "PROBARA_SECRETS_KEY is not set on the API: unsubscribe links are signed with it.")
	}
	return problems
}

// Ready reports whether scheduled reports can be sent.
func (s *Service) Ready() bool { return len(s.MailProblems()) == 0 }

// row is a schedule as stored.
type row struct {
	models.SLAReportSchedule
	TenantID uuid.UUID
}

const scheduleColumns = `id, tenant_id, sla_id, enabled, recipients, include_csv, next_run_at,
	COALESCE(last_period_key, ''), last_run_at, COALESCE(last_error, ''), created_at, updated_at`

func scanRow(r interface{ Scan(...any) error }) (*row, error) {
	var sc row
	var recipients pq.StringArray
	var lastRun sql.NullTime
	if err := r.Scan(&sc.ID, &sc.TenantID, &sc.SLAID, &sc.Enabled, &recipients, &sc.IncludeCSV, &sc.NextRunAt,
		&sc.LastPeriodKey, &lastRun, &sc.LastError, &sc.CreatedAt, &sc.UpdatedAt); err != nil {
		return nil, err
	}
	sc.Recipients = []string(recipients)
	if lastRun.Valid {
		t := lastRun.Time
		sc.LastRunAt = &t
	}
	return &sc, nil
}

// Get returns the SLA's schedule (Schedule nil when it has none). The SLA
// must be the tenant's.
func (s *Service) Get(ctx context.Context, tenantID, slaID uuid.UUID) (*models.SLAReportScheduleResponse, error) {
	if _, err := s.reports.Get(ctx, tenantID, slaID); err != nil {
		return nil, mapSLAErr(err)
	}
	resp := &models.SLAReportScheduleResponse{MailProblems: s.MailProblems()}
	resp.MailReady = len(resp.MailProblems) == 0
	sc, err := scanRow(s.db.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM sla_report_schedules
		WHERE tenant_id = $1 AND sla_id = $2`, tenantID, slaID))
	if errors.Is(err, sql.ErrNoRows) {
		return resp, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get report schedule: %w", err)
	}
	if err := s.fill(ctx, sc); err != nil {
		return nil, err
	}
	resp.Schedule = &sc.SLAReportSchedule
	return resp, nil
}

// fill adds the unsubscribed recipients and the last period's deliveries.
func (s *Service) fill(ctx context.Context, sc *row) error {
	opted, err := unsubscribedKeys(ctx, s.db, sc.ID)
	if err != nil {
		return err
	}
	sc.Unsubscribed = []string{}
	for _, r := range sc.Recipients {
		if opted[addressKey(sc.ID, r)] {
			sc.Unsubscribed = append(sc.Unsubscribed, r)
		}
	}
	sc.Deliveries = []models.SLAReportDelivery{}
	if sc.LastPeriodKey == "" {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT period_key, address, status, attempts, next_attempt_at, sent_at, COALESCE(last_error, '')
		FROM sla_report_deliveries
		WHERE schedule_id = $1 AND period_key = $2
		ORDER BY address
	`, sc.ID, sc.LastPeriodKey)
	if err != nil {
		return fmt.Errorf("list report deliveries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d models.SLAReportDelivery
		var next time.Time
		var sent sql.NullTime
		if err := rows.Scan(&d.PeriodKey, &d.Address, &d.Status, &d.Attempts, &next, &sent, &d.LastError); err != nil {
			return fmt.Errorf("scan report delivery: %w", err)
		}
		if d.Status == models.SLAReportDeliveryPending && d.Attempts > 0 {
			d.NextAttemptAt = &next
		}
		if sent.Valid {
			t := sent.Time
			d.SentAt = &t
		}
		sc.Deliveries = append(sc.Deliveries, d)
	}
	return rows.Err()
}

// Put creates or replaces the SLA's schedule. Enabling one needs mail to be
// configured (MailProblems). Enabling plans the first send at the running
// period's close; recipients added to the list, and those named in
// Resubscribe, are re-subscribed; disabling it, or removing a recipient,
// cancels what was still waiting to be sent to them.
func (s *Service) Put(ctx context.Context, tenantID, slaID uuid.UUID, req *models.PutSLAReportScheduleRequest) (*models.SLAReportScheduleResponse, error) {
	sla, err := s.reports.Get(ctx, tenantID, slaID)
	if err != nil {
		return nil, mapSLAErr(err)
	}
	recipients, err := normalizeRecipients(req.Recipients)
	if err != nil {
		return nil, err
	}
	enabled := req.Enabled == nil || *req.Enabled
	if problems := s.MailProblems(); enabled && len(problems) > 0 {
		return nil, invalid("Scheduled reports cannot be enabled on this installation. %s", strings.Join(problems, " "))
	}
	resubscribe := make([]string, 0, len(req.Resubscribe))
	listed := map[string]bool{}
	for _, r := range recipients {
		listed[r] = true
	}
	for _, raw := range req.Resubscribe {
		addr := strings.ToLower(strings.TrimSpace(raw))
		if !listed[addr] {
			return nil, invalid("resubscribe: %q is not a recipient", raw)
		}
		resubscribe = append(resubscribe, addr)
	}
	now := s.now()
	firstRun, err := slas.ReportRunAt(sla.Period, sla.Timezone, now)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	old, err := scanRow(tx.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM sla_report_schedules
		WHERE tenant_id = $1 AND sla_id = $2 FOR UPDATE`, tenantID, slaID))
	var id uuid.UUID
	var before []string
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id = uuid.New()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sla_report_schedules (id, tenant_id, sla_id, enabled, recipients, include_csv, next_run_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, id, tenantID, slaID, enabled, pq.Array(recipients), req.IncludeCSV, firstRun); err != nil {
			return nil, fmt.Errorf("create report schedule: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("lock report schedule: %w", err)
	default:
		id, before = old.ID, old.Recipients
		next := old.NextRunAt
		if enabled && !old.Enabled {
			// Periods that closed while disabled are not sent late.
			next = firstRun
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE sla_report_schedules SET enabled = $2, recipients = $3, include_csv = $4, next_run_at = $5, updated_at = NOW()
			WHERE id = $1
		`, id, enabled, pq.Array(recipients), req.IncludeCSV, next); err != nil {
			return nil, fmt.Errorf("update report schedule: %w", err)
		}
	}

	was := map[string]bool{}
	for _, r := range before {
		was[r] = true
	}
	var clear, removed []string
	for _, r := range recipients {
		if !was[r] {
			clear = append(clear, addressKey(id, r))
		}
	}
	for _, r := range resubscribe {
		clear = append(clear, addressKey(id, r))
	}
	for _, r := range before {
		if !listed[r] {
			removed = append(removed, addressKey(id, r))
		}
	}
	if len(clear) > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sla_report_unsubscribes WHERE schedule_id = $1 AND address_key = ANY($2)`,
			id, pq.Array(clear)); err != nil {
			return nil, fmt.Errorf("re-subscribe report recipients: %w", err)
		}
	}
	// Cancel what was still waiting: everything when disabled, else the
	// removed recipients' copies.
	cancel, reason := removed, "removed from the recipients"
	if !enabled {
		cancel, reason = nil, "schedule disabled"
	}
	if !enabled || len(cancel) > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE sla_report_deliveries SET status = 'cancelled', last_error = $3, updated_at = NOW()
			WHERE schedule_id = $1 AND status = 'pending' AND ($2::text[] IS NULL OR address_key = ANY($2))
		`, id, pq.Array(cancel), reason); err != nil {
			return nil, fmt.Errorf("cancel report deliveries: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit report schedule: %w", err)
	}
	return s.Get(ctx, tenantID, slaID)
}

// Delete removes the SLA's schedule, its pending deliveries and its
// unsubscribes; every unsubscribe link it mailed stops working.
func (s *Service) Delete(ctx context.Context, tenantID, slaID uuid.UUID) error {
	if _, err := s.reports.Get(ctx, tenantID, slaID); err != nil {
		return mapSLAErr(err)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM sla_report_schedules WHERE tenant_id = $1 AND sla_id = $2`, tenantID, slaID)
	if err != nil {
		return fmt.Errorf("delete report schedule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func mapSLAErr(err error) error {
	if errors.Is(err, slas.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// normalizeRecipients trims, lower-cases and de-duplicates the list and
// accepts only bare plain addresses (mail.ParseRecipient's rules, without a
// display name), 1 to MaxRecipients of them.
func normalizeRecipients(raw []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		addr := strings.ToLower(strings.TrimSpace(r))
		if addr == "" || seen[addr] {
			continue
		}
		parsed, err := mail.ParseRecipient(addr)
		if err != nil || parsed.Address != addr || parsed.Name != "" {
			return nil, invalid("%q is not a plain email address", strings.TrimSpace(r))
		}
		seen[addr] = true
		out = append(out, addr)
	}
	if len(out) == 0 {
		return nil, invalid("add at least one recipient")
	}
	if len(out) > MaxRecipients {
		return nil, invalid("a schedule has at most %d recipients", MaxRecipients)
	}
	return out, nil
}

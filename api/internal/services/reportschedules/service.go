// Package reportschedules emails SLA reports when their period closes.
//
// A schedule belongs to one SLA and names recipients (anyone, inside the
// workspace or not), the attached formats and whether the closed period is
// issued (frozen through slas.IssueReport) before it is sent. The API's
// Runner claims due schedules with FOR UPDATE SKIP LOCKED and commits the
// claimed period before mailing anyone, so every API replica can run it and
// each closed period goes out at most once: a run cut short (shutdown,
// timeout, crash) is never sent again automatically; Send now resends.
//
// Every message goes to one recipient (mail.Message has no Bcc) and carries a
// per-recipient unsubscribe link, in the body and as List-Unsubscribe with
// one-click POST (RFC 8058). The link is an HMAC under a per-schedule key, so
// nothing is stored until someone follows it, and deleting the schedule
// revokes every link it mailed. Schedules therefore need both the platform
// mailer (SMTP_*) and APP_BASE_URL; without them one cannot be enabled.
package reportschedules

import (
	"context"
	"crypto/rand"
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
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/mail"
)

// Limits and defaults.
const (
	// MaxRecipients bounds one schedule's recipient list.
	MaxRecipients = 50
	// MaxPerSLA bounds the schedules of one SLA.
	MaxPerSLA = 10
	// IssueGrace is how long after a period closes the runner waits before
	// issuing and sending it, so late check results (queue lag, a
	// confirmation recheck) land in the frozen report.
	IssueGrace = slas.ReportIssueGrace
)

// Formats a schedule can attach.
const (
	FormatPDF = slas.FormatPDF
	FormatCSV = slas.FormatCSV
)

var (
	ErrNotFound = errors.New("report schedule not found")
	// ErrSLANotFound: the schedule's SLA is not in the workspace.
	ErrSLANotFound = errors.New("SLA not found")
)

// Invalid is a validation failure shown to the caller verbatim.
type Invalid struct{ Message string }

func (e Invalid) Error() string { return e.Message }

func invalid(format string, args ...any) error { return Invalid{Message: fmt.Sprintf(format, args...)} }

// Schedule is one SLA's emailed report schedule.
type Schedule struct {
	ID         uuid.UUID `json:"id"`
	TenantID   uuid.UUID `json:"tenant_id"`
	SLAID      uuid.UUID `json:"sla_id"`
	Recipients []string  `json:"recipients"`
	// Unsubscribed are the recipients who followed their unsubscribe link;
	// they get nothing until the schedule is deleted and created again.
	Unsubscribed []string `json:"unsubscribed"`
	Formats      []string `json:"formats"`
	AutoIssue    bool     `json:"auto_issue"`
	Enabled      bool     `json:"enabled"`
	// NextRunAt is when the running period's report goes out (its close plus
	// IssueGrace); meaningful only while enabled.
	NextRunAt     time.Time  `json:"next_run_at"`
	LastRunAt     *time.Time `json:"last_run_at"`
	LastPeriodKey string     `json:"last_period_key"`
	LastSentCount int        `json:"last_sent_count"`
	LastError     string     `json:"last_error"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`

	unsubscribeKey []byte
}

// CreateRequest creates a schedule. Formats defaults to ["pdf"], AutoIssue
// and Enabled to true.
type CreateRequest struct {
	SLAID      string   `json:"sla_id"`
	Recipients []string `json:"recipients"`
	Formats    []string `json:"formats"`
	AutoIssue  *bool    `json:"auto_issue"`
	Enabled    *bool    `json:"enabled"`
}

// UpdateRequest is a partial update; Recipients and Formats replace the
// lists.
type UpdateRequest struct {
	Recipients *[]string `json:"recipients"`
	Formats    *[]string `json:"formats"`
	AutoIssue  *bool     `json:"auto_issue"`
	Enabled    *bool     `json:"enabled"`
}

// ListResponse is a schedule list plus whether this API can send at all, so
// the UI can explain a disabled toggle before anyone tries.
type ListResponse struct {
	Items []Schedule `json:"items"`
	// MailReady is false when SMTP or APP_BASE_URL is missing;
	// MailProblem then says which.
	MailReady   bool   `json:"mail_ready"`
	MailProblem string `json:"mail_problem,omitempty"`
}

// Reports is the slice of the SLA service schedules use.
type Reports interface {
	Get(ctx context.Context, tenantID, id uuid.UUID) (*models.SLA, error)
	Report(ctx context.Context, tenantID, slaID uuid.UUID, q slas.ReportQuery) (*models.SLAReport, error)
	IssueReport(ctx context.Context, tenantID, slaID uuid.UUID, periodKey string, by slas.Issuer) (*models.SLAReport, error)
	IssuedReportForPeriod(ctx context.Context, tenantID, slaID uuid.UUID, periodKey string) (*models.SLAReport, error)
}

// AuditRecorder records the runner's sends and unsubscribes (they happen
// outside the audited request path).
type AuditRecorder interface {
	Record(event audit.Event)
}

// Service manages schedules and sends their reports.
type Service struct {
	db         *db.Client
	reports    Reports
	mailer     mail.Sender
	appBaseURL string
	audit      AuditRecorder
	log        *logger.Logger
	now        func() time.Time
}

// NewService wires the service. mailer may be nil (no SMTP) and appBaseURL
// empty: schedules can then be listed and kept, but not enabled, and the
// runner records why nothing went out. recorder may be nil.
func NewService(dbClient *db.Client, reports Reports, mailer mail.Sender, appBaseURL string, recorder AuditRecorder, log *logger.Logger) *Service {
	return &Service{
		db: dbClient, reports: reports, mailer: mailer, appBaseURL: strings.TrimRight(appBaseURL, "/"),
		audit: recorder, log: log, now: func() time.Time { return time.Now().UTC() },
	}
}

// mailProblem says why this API cannot send scheduled reports, or "".
func (s *Service) mailProblem() string {
	switch {
	case s.mailer == nil:
		return "Scheduled reports need SMTP: set SMTP_HOST and SMTP_FROM on the API."
	case s.appBaseURL == "":
		return "Scheduled reports need APP_BASE_URL on the API: every report email carries an unsubscribe link to it."
	}
	return ""
}

const scheduleColumns = `s.id, s.tenant_id, s.sla_id, s.recipients, s.formats, s.auto_issue, s.enabled, s.next_run_at,
	s.last_run_at, COALESCE(s.last_period_key, ''), s.last_sent_count, COALESCE(s.last_error, ''), s.created_at, s.updated_at,
	s.unsubscribe_key`

func scanSchedule(row interface{ Scan(...any) error }) (*Schedule, error) {
	var sc Schedule
	var recipients, formats pq.StringArray
	var lastRun sql.NullTime
	if err := row.Scan(&sc.ID, &sc.TenantID, &sc.SLAID, &recipients, &formats, &sc.AutoIssue, &sc.Enabled, &sc.NextRunAt,
		&lastRun, &sc.LastPeriodKey, &sc.LastSentCount, &sc.LastError, &sc.CreatedAt, &sc.UpdatedAt, &sc.unsubscribeKey); err != nil {
		return nil, err
	}
	sc.Recipients = []string(recipients)
	sc.Formats = []string(formats)
	sc.Unsubscribed = []string{}
	if lastRun.Valid {
		t := lastRun.Time
		sc.LastRunAt = &t
	}
	return &sc, nil
}

// fillUnsubscribed marks the recipients who opted out.
func (s *Service) fillUnsubscribed(ctx context.Context, q db.Querier, sc *Schedule) error {
	hashes, err := unsubscribedHashes(ctx, q, sc.ID)
	if err != nil {
		return err
	}
	sc.Unsubscribed = []string{}
	for _, r := range sc.Recipients {
		if hashes[emailHash(r)] {
			sc.Unsubscribed = append(sc.Unsubscribed, r)
		}
	}
	return nil
}

func unsubscribedHashes(ctx context.Context, q db.Querier, scheduleID uuid.UUID) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT email_hash FROM report_unsubscribes WHERE schedule_id = $1`, scheduleID)
	if err != nil {
		return nil, fmt.Errorf("load unsubscribes: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("scan unsubscribe: %w", err)
		}
		out[h] = true
	}
	return out, rows.Err()
}

// List returns the workspace's schedules, optionally of one SLA.
func (s *Service) List(ctx context.Context, tenantID, slaID uuid.UUID) (*ListResponse, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+scheduleColumns+` FROM sla_report_schedules s
		WHERE s.tenant_id = $1 AND ($2::uuid IS NULL OR s.sla_id = $2)
		ORDER BY s.created_at, s.id`, tenantID, nullUUID(slaID))
	if err != nil {
		return nil, fmt.Errorf("list report schedules: %w", err)
	}
	items := []Schedule{}
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan report schedule: %w", err)
		}
		items = append(items, *sc)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("iterate report schedules: %w", err)
	}
	for i := range items {
		if err := s.fillUnsubscribed(ctx, s.db, &items[i]); err != nil {
			return nil, err
		}
	}
	problem := s.mailProblem()
	return &ListResponse{Items: items, MailReady: problem == "", MailProblem: problem}, nil
}

// Get returns one schedule of the workspace.
func (s *Service) Get(ctx context.Context, tenantID, id uuid.UUID) (*Schedule, error) {
	sc, err := scanSchedule(s.db.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM sla_report_schedules s
		WHERE s.tenant_id = $1 AND s.id = $2`, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get report schedule: %w", err)
	}
	if err := s.fillUnsubscribed(ctx, s.db, sc); err != nil {
		return nil, err
	}
	return sc, nil
}

// Create validates and stores a schedule. Its first report is the running
// period's.
func (s *Service) Create(ctx context.Context, tenantID uuid.UUID, req *CreateRequest) (*Schedule, error) {
	slaID, err := uuid.Parse(strings.TrimSpace(req.SLAID))
	if err != nil {
		return nil, invalid("sla_id must be an SLA ID")
	}
	recipients, err := normalizeRecipients(req.Recipients)
	if err != nil {
		return nil, err
	}
	formats, err := normalizeFormats(req.Formats)
	if err != nil {
		return nil, err
	}
	autoIssue := req.AutoIssue == nil || *req.AutoIssue
	enabled := req.Enabled == nil || *req.Enabled
	if enabled {
		if p := s.mailProblem(); p != "" {
			return nil, invalid("%s", p)
		}
	}
	sla, err := s.reports.Get(ctx, tenantID, slaID)
	if errors.Is(err, slas.ErrNotFound) {
		return nil, ErrSLANotFound
	}
	if err != nil {
		return nil, err
	}
	next, err := nextRun(sla, s.now())
	if err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate unsubscribe key: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	// Serialize creates per SLA so the cap holds under concurrency.
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM slas WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenantID, slaID); err != nil {
		return nil, fmt.Errorf("lock SLA: %w", err)
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sla_report_schedules WHERE tenant_id = $1 AND sla_id = $2`,
		tenantID, slaID).Scan(&count); err != nil {
		return nil, fmt.Errorf("count report schedules: %w", err)
	}
	if count >= MaxPerSLA {
		return nil, invalid("an SLA has at most %d report schedules", MaxPerSLA)
	}
	id := uuid.New()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sla_report_schedules (id, tenant_id, sla_id, recipients, formats, auto_issue, enabled, next_run_at, unsubscribe_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, id, tenantID, slaID, pq.Array(recipients), pq.Array(formats), autoIssue, enabled, next, key); err != nil {
		return nil, fmt.Errorf("create report schedule: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit report schedule: %w", err)
	}
	return s.Get(ctx, tenantID, id)
}

// Update applies a partial update. Enabling a schedule (again) points it at
// the running period, so it never sends a backlog of periods it slept
// through.
func (s *Service) Update(ctx context.Context, tenantID, id uuid.UUID, req *UpdateRequest) (*Schedule, error) {
	existing, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	recipients, formats := existing.Recipients, existing.Formats
	if req.Recipients != nil {
		if recipients, err = normalizeRecipients(*req.Recipients); err != nil {
			return nil, err
		}
	}
	if req.Formats != nil {
		if formats, err = normalizeFormats(*req.Formats); err != nil {
			return nil, err
		}
	}
	autoIssue, enabled := existing.AutoIssue, existing.Enabled
	if req.AutoIssue != nil {
		autoIssue = *req.AutoIssue
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if enabled {
		if p := s.mailProblem(); p != "" {
			return nil, invalid("%s", p)
		}
	}
	next := existing.NextRunAt
	if enabled && !existing.Enabled {
		sla, err := s.reports.Get(ctx, tenantID, existing.SLAID)
		if err != nil {
			return nil, err
		}
		if next, err = nextRun(sla, s.now()); err != nil {
			return nil, err
		}
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE sla_report_schedules SET recipients = $3, formats = $4, auto_issue = $5, enabled = $6, next_run_at = $7, updated_at = NOW()
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, id, pq.Array(recipients), pq.Array(formats), autoIssue, enabled, next)
	if err != nil {
		return nil, fmt.Errorf("update report schedule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.Get(ctx, tenantID, id)
}

// Delete removes a schedule; every unsubscribe link it mailed stops working
// and its opt-outs go with it.
func (s *Service) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sla_report_schedules WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("delete report schedule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// normalizeRecipients keeps bare, lower-cased, de-duplicated addresses.
func normalizeRecipients(raw []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		addr, err := mail.ParseRecipient(r)
		if err != nil {
			return nil, invalid("%q is not an email address", r)
		}
		a := strings.ToLower(addr.Address)
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil, invalid("add at least one recipient")
	}
	if len(out) > MaxRecipients {
		return nil, invalid("a schedule has at most %d recipients", MaxRecipients)
	}
	return out, nil
}

func normalizeFormats(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return []string{FormatPDF}, nil
	}
	seen := map[string]bool{}
	for _, f := range raw {
		f = strings.ToLower(strings.TrimSpace(f))
		if f != FormatPDF && f != FormatCSV {
			return nil, invalid("formats must be pdf and/or csv")
		}
		seen[f] = true
	}
	out := make([]string, 0, len(seen))
	for _, f := range []string{FormatPDF, FormatCSV} {
		if seen[f] {
			out = append(out, f)
		}
	}
	return out, nil
}

// nextRun is when the running period's report is due: its close plus
// IssueGrace (S-U7 periods in the SLA's timezone). slas.Service.Update
// applies the same rule when the SLA's period or timezone changes.
func nextRun(sla *models.SLA, now time.Time) (time.Time, error) {
	return slas.ReportRunAt(sla.Period, sla.Timezone, now)
}

// lastClosedPeriod is the newest period that has closed by now.
func lastClosedPeriod(sla *models.SLA, now time.Time) (sharedanalytics.Period, error) {
	loc, err := time.LoadLocation(sla.Timezone)
	if err != nil {
		return sharedanalytics.Period{}, fmt.Errorf("SLA timezone: %w", err)
	}
	cur, err := sharedanalytics.PeriodContaining(sharedanalytics.PeriodKind(sla.Period), loc, now)
	if err != nil {
		return sharedanalytics.Period{}, err
	}
	return cur.Previous(loc), nil
}

func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

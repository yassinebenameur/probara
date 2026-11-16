package reportschedules

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/textproto"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/slas"
	"github.com/yassinebenameur/probara/shared/mail"
)

const (
	// runInterval is how often the runner looks for due schedules and
	// deliveries.
	runInterval = time.Minute
	// maxPlansPerTick and maxDeliveriesPerTick bound one tick; the rest
	// wait for the next tick or another replica.
	maxPlansPerTick      = 50
	maxDeliveriesPerTick = 500
	deliveryBatch        = 50
	// planRetryDelay is how long a schedule whose period could not be
	// issued waits before trying again.
	planRetryDelay = 15 * time.Minute
	// staleSendingAfter closes a delivery claimed this long ago without an
	// outcome as uncertain: its API died mid-send. Far above the mailer's
	// 30-second bound per message.
	staleSendingAfter = 15 * time.Minute
	// maxAttempts is the attempts a recipient gets before it is failed.
	maxAttempts = 5
	// recordTimeout bounds writing an outcome, which deliberately outlives
	// the run's own cancellation.
	recordTimeout = 30 * time.Second
	// maxErrorLen bounds a stored error.
	maxErrorLen = 500
)

// retryBackoff is the wait after the nth failed attempt (1-based).
var retryBackoff = []time.Duration{5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

const interruptedError = "the API stopped while sending; the message may or may not have arrived, so it is not sent again"

// RunOnce closes interrupted sends, plans every due schedule, then sends
// the due deliveries. It stops early, without an error, once ctx is
// cancelled.
func (s *Service) RunOnce(ctx context.Context) error {
	if err := s.closeInterrupted(ctx); err != nil {
		return err
	}
	for i := 0; i < maxPlansPerTick && ctx.Err() == nil; i++ {
		ran, err := s.planOne(ctx)
		if err != nil {
			return quietCancel(ctx, err)
		}
		if !ran {
			break
		}
	}
	for sent := 0; sent < maxDeliveriesPerTick && ctx.Err() == nil; {
		n, err := s.deliverBatch(ctx)
		if err != nil {
			return quietCancel(ctx, err)
		}
		if n == 0 {
			break
		}
		sent += n
	}
	return nil
}

func quietCancel(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// closeInterrupted marks deliveries left in 'sending' by an API that died
// (or was killed) mid-send as uncertain. Sending them again could mail the
// period twice; leaving them 'sending' would hide them.
func (s *Service) closeInterrupted(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		UPDATE sla_report_deliveries SET status = 'uncertain', last_error = $2, updated_at = NOW()
		WHERE status = 'sending' AND claimed_at < $1
		RETURNING schedule_id, period_key
	`, s.now().Add(-staleSendingAfter), interruptedError)
	if err != nil {
		return fmt.Errorf("close interrupted report deliveries: %w", err)
	}
	touched, err := scanPeriods(rows)
	if err != nil {
		return err
	}
	for _, p := range touched {
		if err := s.refreshError(ctx, p.scheduleID, p.periodKey); err != nil {
			return err
		}
	}
	return nil
}

type schedulePeriod struct {
	scheduleID uuid.UUID
	periodKey  string
}

func scanPeriods(rows *sql.Rows) ([]schedulePeriod, error) {
	defer rows.Close()
	seen := map[schedulePeriod]bool{}
	var out []schedulePeriod
	for rows.Next() {
		var p schedulePeriod
		if err := rows.Scan(&p.scheduleID, &p.periodKey); err != nil {
			return nil, fmt.Errorf("scan report delivery: %w", err)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// planOne claims the most overdue enabled schedule and, when its last
// closed period was not claimed yet, issues that period's report and
// commits one delivery per recipient, all before anyone is mailed. It
// reports whether a schedule was handled.
func (s *Service) planOne(ctx context.Context) (bool, error) {
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	sc, err := scanRow(tx.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM sla_report_schedules
		WHERE enabled AND next_run_at <= $1
		ORDER BY next_run_at
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, now))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim report schedule: %w", err)
	}

	sla, period, next, perr := s.plan(ctx, sc, now)
	if perr != nil {
		// Recorded and retried later rather than returned: an error here
		// would leave the row first in line and starve every other schedule.
		if _, err := tx.ExecContext(ctx, `
			UPDATE sla_report_schedules SET next_run_at = $2, last_error = $3, updated_at = NOW() WHERE id = $1
		`, sc.ID, now.Add(planRetryDelay), clip("the report could not be issued: "+perr.Error())); err != nil {
			return false, fmt.Errorf("record report schedule failure: %w", err)
		}
		return true, tx.Commit()
	}
	claimed := false
	switch {
	case now.Before(period.End.Add(slas.ReportIssueGrace)):
		// Due early (a stale next_run_at): wait out the grace of the
		// period that just closed.
		next = period.End.Add(slas.ReportIssueGrace).UTC()
	case period.Key == sc.LastPeriodKey:
		// Already claimed; only the next run time was stale.
	default:
		if err := s.claimPeriodTx(ctx, tx, sc, period.Key, now); err != nil {
			return false, err
		}
		claimed = true
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sla_report_schedules SET next_run_at = $2, updated_at = NOW() WHERE id = $1`,
		sc.ID, next); err != nil {
		return false, fmt.Errorf("reschedule report schedule: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit report schedule claim: %w", err)
	}
	if claimed {
		s.recordRun(sc, sla, period.Key)
	}
	return true, nil
}

// plan loads the schedule's SLA, issues its last closed period's report
// when it is due (reusing a copy issued by hand), and works out the next
// run.
func (s *Service) plan(ctx context.Context, sc *row, now time.Time) (*models.SLA, periodRef, time.Time, error) {
	sla, err := s.reports.Get(ctx, sc.TenantID, sc.SLAID)
	if err != nil {
		return nil, periodRef{}, time.Time{}, fmt.Errorf("load SLA: %w", err)
	}
	next, err := slas.ReportRunAt(sla.Period, sla.Timezone, now)
	if err != nil {
		return nil, periodRef{}, time.Time{}, err
	}
	p, err := slas.LastClosedPeriod(sla, now)
	if err != nil {
		return nil, periodRef{}, time.Time{}, err
	}
	period := periodRef{Key: p.Key, End: p.End}
	if now.Before(p.End.Add(slas.ReportIssueGrace)) || p.Key == sc.LastPeriodKey {
		return sla, period, next, nil
	}
	if _, err := s.issuedReport(ctx, sc.TenantID, sla.ID, p.Key); err != nil {
		return nil, periodRef{}, time.Time{}, err
	}
	return sla, period, next, nil
}

type periodRef struct {
	Key string
	End time.Time
}

// issuedReport returns the period's issued report, issuing it first when
// nobody did.
func (s *Service) issuedReport(ctx context.Context, tenantID, slaID uuid.UUID, periodKey string) (*models.SLAReport, error) {
	r, err := s.reports.IssuedReportForPeriod(ctx, tenantID, slaID, periodKey)
	if !errors.Is(err, slas.ErrNotFound) {
		return r, err
	}
	r, err = s.reports.IssueReport(ctx, tenantID, slaID, periodKey, slas.Issuer{Schedule: true})
	var conflict slas.Conflict
	if errors.As(err, &conflict) {
		// Issued by hand meanwhile.
		return s.reports.IssuedReportForPeriod(ctx, tenantID, slaID, periodKey)
	}
	return r, err
}

// claimPeriodTx records the period as claimed and adds one delivery per
// recipient: pending, or skipped for those who unsubscribed. Finished
// deliveries of older periods are pruned, so the table stays small.
func (s *Service) claimPeriodTx(ctx context.Context, tx *sql.Tx, sc *row, periodKey string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM sla_report_deliveries
		WHERE schedule_id = $1 AND period_key <> $2 AND status NOT IN ('pending', 'sending')
	`, sc.ID, periodKey); err != nil {
		return fmt.Errorf("prune report deliveries: %w", err)
	}
	keys := make([]string, len(sc.Recipients))
	for i, r := range sc.Recipients {
		keys[i] = addressKey(sc.ID, r)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sla_report_deliveries (schedule_id, tenant_id, period_key, address_key, address, status, last_error, next_attempt_at)
		SELECT $1, $2, $3, r.k, r.a,
			CASE WHEN u.address_key IS NULL THEN 'pending' ELSE 'skipped' END,
			CASE WHEN u.address_key IS NULL THEN NULL ELSE 'unsubscribed' END, $4
		FROM unnest($5::text[], $6::text[]) AS r(a, k)
		LEFT JOIN sla_report_unsubscribes u ON u.schedule_id = $1 AND u.address_key = r.k
		ON CONFLICT DO NOTHING
	`, sc.ID, sc.TenantID, periodKey, now, pq.Array(sc.Recipients), pq.Array(keys)); err != nil {
		return fmt.Errorf("add report deliveries: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sla_report_schedules SET last_period_key = $2, last_run_at = $3, last_error = NULL WHERE id = $1
	`, sc.ID, periodKey, now); err != nil {
		return fmt.Errorf("claim report schedule period: %w", err)
	}
	return nil
}

// claimedDelivery is one delivery this runner committed as sending.
type claimedDelivery struct {
	scheduleID uuid.UUID
	tenantID   uuid.UUID
	periodKey  string
	addressKey string
	address    string
	attempts   int
}

// deliverBatch claims up to deliveryBatch due deliveries of enabled
// schedules (committed as 'sending' before any message goes out), sends
// them and records each outcome. It returns how many it claimed.
func (s *Service) deliverBatch(ctx context.Context) (int, error) {
	now := s.now()
	rows, err := s.db.QueryContext(ctx, `
		UPDATE sla_report_deliveries d
		SET status = 'sending', attempts = d.attempts + 1, claimed_at = $1, updated_at = NOW()
		FROM (
			SELECT dd.schedule_id, dd.period_key, dd.address_key
			FROM sla_report_deliveries dd
			JOIN sla_report_schedules sc ON sc.id = dd.schedule_id AND sc.enabled
			WHERE dd.status = 'pending' AND dd.next_attempt_at <= $1
			ORDER BY dd.next_attempt_at
			LIMIT $2
			FOR UPDATE OF dd SKIP LOCKED
		) c
		WHERE d.schedule_id = c.schedule_id AND d.period_key = c.period_key AND d.address_key = c.address_key
		RETURNING d.schedule_id, d.tenant_id, d.period_key, d.address_key, d.address, d.attempts
	`, now, deliveryBatch)
	if err != nil {
		return 0, fmt.Errorf("claim report deliveries: %w", err)
	}
	var claimed []claimedDelivery
	for rows.Next() {
		var d claimedDelivery
		if err := rows.Scan(&d.scheduleID, &d.tenantID, &d.periodKey, &d.addressKey, &d.address, &d.attempts); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan report delivery: %w", err)
		}
		claimed = append(claimed, d)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("claim report deliveries: %w", err)
	}

	groups := map[schedulePeriod][]claimedDelivery{}
	var order []schedulePeriod
	for _, d := range claimed {
		k := schedulePeriod{d.scheduleID, d.periodKey}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], d)
	}
	for _, k := range order {
		s.sendGroup(ctx, groups[k])
		if err := s.refreshError(context.WithoutCancel(ctx), k.scheduleID, k.periodKey); err != nil {
			return len(claimed), err
		}
	}
	return len(claimed), nil
}

// sendGroup sends one period's report to its claimed recipients and records
// every outcome. Nothing here returns an error: each claimed row ends sent,
// retrying, failed, uncertain, skipped or released.
func (s *Service) sendGroup(ctx context.Context, group []claimedDelivery) {
	first := group[0]
	sc, err := scanRow(s.db.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM sla_report_schedules
		WHERE tenant_id = $1 AND id = $2`, first.tenantID, first.scheduleID))
	if errors.Is(err, sql.ErrNoRows) {
		return // deleted meanwhile, its deliveries with it
	}
	var sla *models.SLA
	var attachments []mail.Attachment
	var report *models.SLAReport
	if err == nil {
		sla, err = s.reports.Get(ctx, sc.TenantID, sc.SLAID)
	}
	if err == nil {
		report, err = s.issuedReport(ctx, sc.TenantID, sc.SLAID, first.periodKey)
	}
	if err == nil {
		attachments, err = renderAttachments(report, sc.IncludeCSV)
	}
	if err != nil {
		for _, d := range group {
			s.recordOutcome(ctx, d, fmt.Errorf("prepare the report: %w", err))
		}
		return
	}
	opted, err := unsubscribedKeys(ctx, s.db, sc.ID)
	if err != nil {
		for _, d := range group {
			s.recordOutcome(ctx, d, err)
		}
		return
	}
	listed := map[string]bool{}
	for _, r := range sc.Recipients {
		listed[addressKey(sc.ID, r)] = true
	}
	for _, d := range group {
		switch {
		case ctx.Err() != nil:
			// Stopping: hand the rest back untouched, the attempt uncounted.
			s.release(ctx, d)
			continue
		case opted[d.addressKey]:
			s.finish(ctx, d, models.SLAReportDeliverySkipped, "unsubscribed")
			continue
		case !listed[d.addressKey]:
			s.finish(ctx, d, models.SLAReportDeliveryCancelled, "removed from the recipients")
			continue
		}
		// Restart the claim's clock: a slow relay can keep a batch busy
		// longer than staleSendingAfter, and a row another replica already
		// closed as uncertain must not be sent after all.
		if !s.touch(ctx, d) {
			continue
		}
		msg, err := s.message(sc, sla, report, d.address, attachments)
		if err == nil {
			err = s.mailer.SendMessage(ctx, msg)
		}
		s.recordOutcome(ctx, d, err)
	}
}

// touch refreshes a claimed row's claimed_at and reports whether this
// runner still owns it.
func (s *Service) touch(ctx context.Context, d claimedDelivery) bool {
	res, err := s.db.ExecContext(ctx, `UPDATE sla_report_deliveries SET claimed_at = $5
		WHERE schedule_id = $1 AND period_key = $2 AND address_key = $3 AND attempts = $4 AND status = 'sending'`,
		d.scheduleID, d.periodKey, d.addressKey, d.attempts, s.now())
	if err != nil {
		if ctx.Err() == nil && s.log != nil {
			s.log.WithError(err).Error("Failed to refresh a report delivery claim")
		}
		s.release(ctx, d)
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// recordOutcome stores what one send came to (err nil: sent).
func (s *Service) recordOutcome(ctx context.Context, d claimedDelivery, err error) {
	now := s.now()
	switch {
	case err == nil:
		s.update(ctx, d, `status = 'sent', sent_at = $5, last_error = NULL`, now)
	case errors.Is(err, mail.ErrMaybeDelivered):
		s.finish(ctx, d, models.SLAReportDeliveryUncertain, "the connection was lost after the message was sent; it may have arrived, so it is not sent again: "+err.Error())
	case permanent(err) || d.attempts >= maxAttempts:
		s.finish(ctx, d, models.SLAReportDeliveryFailed, err.Error())
	default:
		wait := retryBackoff[min(d.attempts, len(retryBackoff))-1]
		s.update(ctx, d, `status = 'pending', next_attempt_at = $5, last_error = $6`, now.Add(wait), clip(err.Error()))
	}
}

// permanent reports failures a retry cannot fix: a message that can never
// be sent, or a relay that refused it outright (5xx).
func permanent(err error) bool {
	if errors.Is(err, mail.ErrInvalidMessage) {
		return true
	}
	var reply *textproto.Error
	return errors.As(err, &reply) && reply.Code >= 500
}

func (s *Service) finish(ctx context.Context, d claimedDelivery, status, reason string) {
	s.update(ctx, d, `status = $5, last_error = NULLIF($6, '')`, status, clip(reason))
}

// release hands a claimed row back without counting the attempt.
func (s *Service) release(ctx context.Context, d claimedDelivery) {
	s.update(ctx, d, `status = 'pending', attempts = attempts - 1, claimed_at = NULL`)
}

// update writes one claimed row's outcome on a context detached from the
// run's, so a run cut short still records what it did. The status guard
// keeps a stale runner from overwriting a row it no longer owns.
func (s *Service) update(ctx context.Context, d claimedDelivery, set string, args ...any) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	all := append([]any{d.scheduleID, d.periodKey, d.addressKey, d.attempts}, args...)
	if _, err := s.db.ExecContext(ctx, `UPDATE sla_report_deliveries SET `+set+`, updated_at = NOW()
		WHERE schedule_id = $1 AND period_key = $2 AND address_key = $3 AND attempts = $4 AND status = 'sending'`, all...); err != nil && s.log != nil {
		s.log.WithError(err).Error("Failed to record a report delivery")
	}
}

// refreshError recomputes the schedule's last_error from its last
// period's deliveries: what failed, is uncertain or waits for a retry;
// cleared once nothing does. Another period's rows leave it alone.
func (s *Service) refreshError(ctx context.Context, scheduleID uuid.UUID, periodKey string) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT status, attempts, COALESCE(last_error, '') FROM sla_report_deliveries
		WHERE schedule_id = $1 AND period_key = $2
		ORDER BY updated_at DESC
	`, scheduleID, periodKey)
	if err != nil {
		return fmt.Errorf("summarize report deliveries: %w", err)
	}
	counts := map[string]int{}
	total, firstErr := 0, ""
	for rows.Next() {
		var status, lastErr string
		var attempts int
		if err := rows.Scan(&status, &attempts, &lastErr); err != nil {
			rows.Close()
			return fmt.Errorf("scan report delivery: %w", err)
		}
		total++
		key := status
		if status == models.SLAReportDeliveryPending && attempts > 0 {
			key = "retrying"
		}
		counts[key]++
		if firstErr == "" && (key == "retrying" || status == models.SLAReportDeliveryFailed || status == models.SLAReportDeliveryUncertain) {
			firstErr = lastErr
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("summarize report deliveries: %w", err)
	}
	var parts []string
	for _, k := range []string{models.SLAReportDeliveryFailed, models.SLAReportDeliveryUncertain, "retrying"} {
		if counts[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
		}
	}
	var summary any
	if len(parts) > 0 {
		summary = clip(fmt.Sprintf("%s of %d recipients for %s: %s", strings.Join(parts, ", "), total, periodKey, firstErr))
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE sla_report_schedules SET last_error = $3 WHERE id = $1 AND last_period_key = $2
	`, scheduleID, periodKey, summary); err != nil {
		return fmt.Errorf("record report schedule error: %w", err)
	}
	return nil
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxErrorLen {
		return s
	}
	return strings.ToValidUTF8(s[:maxErrorLen], "") + "..."
}

// renderAttachments renders the PDF, and the CSV when asked for.
func renderAttachments(r *models.SLAReport, withCSV bool) ([]mail.Attachment, error) {
	var pdf bytes.Buffer
	if err := slas.WritePDF(&pdf, r); err != nil {
		return nil, fmt.Errorf("render PDF: %w", err)
	}
	out := []mail.Attachment{{Filename: slas.FileName(r, slas.FormatPDF), ContentType: "application/pdf", Data: pdf.Bytes()}}
	if withCSV {
		var csv bytes.Buffer
		if err := slas.WriteCSV(&csv, r); err != nil {
			return nil, fmt.Errorf("render CSV: %w", err)
		}
		out = append(out, mail.Attachment{Filename: slas.FileName(r, slas.FormatCSV), ContentType: "text/csv; charset=utf-8", Data: csv.Bytes()})
	}
	return out, nil
}

// recordRun audits a claimed period: the runner issued (or reused) the
// report and queued it for its recipients.
func (s *Service) recordRun(sc *row, sla *models.SLA, periodKey string) {
	if s.audit == nil {
		return
	}
	tenantID := sc.TenantID
	recipients := append([]string(nil), sc.Recipients...)
	sort.Strings(recipients)
	s.audit.Record(audit.Event{
		TenantID:     &tenantID,
		ActorType:    audit.ActorAnonymous,
		ActorLabel:   "report schedule",
		Action:       "sla-report-schedule.send",
		ResourceType: "sla-report-schedule",
		ResourceID:   sc.SLAID.String(),
		Outcome:      audit.OutcomeSuccess,
		Details:      map[string]any{"sla_name": sla.Name, "period": periodKey, "recipients": recipients},
	})
}

// Runner runs RunOnce on an interval, like the audit pruner. Every API
// replica runs one; SKIP LOCKED and the claims committed before sending keep
// them from sending a period twice.
type Runner struct {
	svc      *Service
	interval time.Duration
	stop     chan struct{}
	wg       sync.WaitGroup
	once     sync.Once
}

// NewRunner wraps the service in a background loop.
func NewRunner(svc *Service) *Runner {
	return &Runner{svc: svc, interval: runInterval, stop: make(chan struct{})}
}

// Start begins the loop. Without mail configured it logs why scheduled
// reports are off and does nothing: no schedule can be enabled then, and
// one enabled earlier waits (the Schedules tab says why).
func (r *Runner) Start() {
	if problems := r.svc.MailProblems(); len(problems) > 0 {
		if r.svc.log != nil {
			r.svc.log.WithField("reasons", strings.Join(problems, " ")).Info("Scheduled SLA reports are off")
		}
		return
	}
	r.wg.Add(1)
	go r.run()
}

// Stop ends the loop and waits for a run in progress, which records the
// outcome of every message it sent.
func (r *Runner) Stop() {
	r.once.Do(func() { close(r.stop) })
	r.wg.Wait()
}

func (r *Runner) run() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		r.tick()
		select {
		case <-r.stop:
			return
		case <-ticker.C:
		}
	}
}

func (r *Runner) tick() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-r.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	if err := r.svc.RunOnce(ctx); err != nil && !isMissingSchema(err) && r.svc.log != nil {
		r.svc.log.WithError(err).Error("Failed to run SLA report schedules")
	}
}

// isMissingSchema: during a rolling upgrade an API may start before its
// migration ran; the next tick finds the tables.
func isMissingSchema(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && (pqErr.Code == "42P01" || pqErr.Code == "42703")
}

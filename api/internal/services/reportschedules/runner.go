package reportschedules

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/slas"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

const (
	// runInterval is how often the runner looks for due schedules.
	runInterval = 5 * time.Minute
	// maxPerTick bounds the schedules one tick sends; the rest wait for the
	// next tick (or another replica).
	maxPerTick = 50
	// runTimeout bounds one schedule's issue, render and send.
	runTimeout = 10 * time.Minute
	// retryDelay is how long a schedule whose run could not even start
	// (its SLA failed to load) waits before the next attempt.
	retryDelay = time.Hour
)

// Outcome is what one send came to.
type Outcome struct {
	PeriodKey string `json:"period_key"`
	// Issued: the report sent is a frozen issued copy (issued now or before).
	Issued bool `json:"issued"`
	// Sent and Skipped count recipients: delivered, and left out because
	// they unsubscribed.
	Sent    int `json:"sent"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
	// Error is the sanitized reason for any failure; empty on full success.
	Error string `json:"error,omitempty"`
}

// RunDue sends every due schedule's last closed period, one claimed row at a
// time, and returns how many it ran. It stops early, without an error, once
// ctx is cancelled (Runner.Stop).
func (s *Service) RunDue(ctx context.Context) (int, error) {
	n := 0
	for n < maxPerTick && ctx.Err() == nil {
		ran, err := s.runOne(ctx)
		if err != nil || !ran {
			if ctx.Err() != nil {
				err = nil
			}
			return n, err
		}
		n++
	}
	return n, nil
}

// interruptedError is what a claimed run shows until its outcome is
// recorded; it stays only when the API died mid-send.
const interruptedError = "sending, or interrupted before the outcome was recorded: some recipients may not have received it (Send now resends)"

// recordTimeout bounds writing a run's outcome, which deliberately ignores
// the run's own cancellation.
const recordTimeout = 30 * time.Second

// claimed is a due schedule whose period runOne committed as sent.
type claimed struct {
	sc        *Schedule
	sla       *models.SLA
	periodKey string
}

// runOne claims one due schedule and sends its period. The claim (the
// period as last_period_key, next_run_at moved on) commits BEFORE any email
// leaves, and the mail goes out after that transaction, never inside it.
// Sending inside it would make delivery exactly as durable as the commit
// that follows: Runner.Stop, runTimeout or a crash after some recipients
// were mailed rolls the record back, and the next tick, on any replica,
// mails the whole period to everyone again. Committing first makes a run at
// most once; a run cut short records the recipients it failed, or keeps
// interruptedError, and Send now resends.
func (s *Service) runOne(parent context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, runTimeout)
	defer cancel()
	now := s.now()
	c, ran, err := s.claim(ctx, now)
	if err != nil || c == nil {
		return ran, err
	}
	out := s.send(ctx, c.sc, c.sla, c.periodKey, c.sc.AutoIssue)
	if err := s.recordOutcome(ctx, c.sc, out, c.periodKey); err != nil {
		return true, err
	}
	s.recordSend(c.sc, out, "report schedule")
	return true, nil
}

// claim locks the most overdue schedule, plans its run and commits it. It
// returns the claim when a period is to be sent now, and whether a
// schedule was handled at all.
func (s *Service) claim(ctx context.Context, now time.Time) (*claimed, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	sc, err := scanSchedule(tx.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM sla_report_schedules s
		WHERE s.enabled AND s.next_run_at <= $1 AND `+tenancy.LiveTenantSQL("s.tenant_id")+`
		ORDER BY s.next_run_at
		LIMIT 1
		FOR UPDATE OF s SKIP LOCKED`, now))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("claim report schedule: %w", err)
	}

	sla, next, period, err := s.plan(ctx, sc, now)
	if err != nil {
		// Record and retry later rather than return: an error here would
		// leave the row first in line and starve every other schedule.
		if _, uerr := tx.ExecContext(ctx, `
			UPDATE sla_report_schedules SET next_run_at = $2, last_error = $3 WHERE id = $1
		`, sc.ID, now.Add(retryDelay), deliverylog.SanitizeError(err.Error())); uerr != nil {
			return nil, false, fmt.Errorf("record report schedule failure: %w", uerr)
		}
		return nil, true, tx.Commit()
	}
	var c *claimed
	switch {
	case now.Before(period.End.Add(IssueGrace)):
		// Due early (a stale next_run_at): wait out the grace of the
		// period that just closed.
		next = period.End.Add(IssueGrace).UTC()
	case period.Key == sc.LastPeriodKey:
		// Already sent; only the next run time was stale.
	default:
		if _, err := tx.ExecContext(ctx, `
			UPDATE sla_report_schedules SET last_run_at = $2, last_period_key = $3, last_sent_count = 0, last_error = $4
			WHERE id = $1
		`, sc.ID, now, period.Key, interruptedError); err != nil {
			return nil, false, fmt.Errorf("claim report schedule period: %w", err)
		}
		c = &claimed{sc: sc, sla: sla, periodKey: period.Key}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sla_report_schedules SET next_run_at = $2 WHERE id = $1`, sc.ID, next); err != nil {
		return nil, false, fmt.Errorf("reschedule report schedule: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit report schedule claim: %w", err)
	}
	return c, true, nil
}

// recordOutcome stores what a claimed run came to. It runs on a context
// detached from the run's, so a run cut short by Stop or runTimeout still
// records the recipients it reached and the ones it failed. The period
// guard keeps a slow run from overwriting a later one's record.
func (s *Service) recordOutcome(ctx context.Context, sc *Schedule, out Outcome, periodKey string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE sla_report_schedules SET last_sent_count = $3, last_error = NULLIF($4, '')
		WHERE id = $1 AND last_period_key = $2
	`, sc.ID, periodKey, out.Sent, out.Error); err != nil {
		return fmt.Errorf("record report schedule run: %w", err)
	}
	return nil
}

// plan loads the schedule's SLA and works out its next run and the period
// a run now reports.
func (s *Service) plan(ctx context.Context, sc *Schedule, now time.Time) (*models.SLA, time.Time, sharedanalytics.Period, error) {
	sla, err := s.reports.Get(ctx, sc.TenantID, sc.SLAID)
	if err != nil {
		return nil, time.Time{}, sharedanalytics.Period{}, fmt.Errorf("load SLA: %w", err)
	}
	next, err := nextRun(sla, now)
	if err != nil {
		return nil, time.Time{}, sharedanalytics.Period{}, err
	}
	period, err := lastClosedPeriod(sla, now)
	if err != nil {
		return nil, time.Time{}, sharedanalytics.Period{}, err
	}
	return sla, next, period, nil
}

func isMissingSchema(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && (pqErr.Code == "42P01" || pqErr.Code == "42703")
}

// SendNow sends the last closed period's report to the schedule's
// recipients at once, without issuing it: the issued copy when there is
// one, else the live report of that period. It records the run on the
// schedule but leaves the next scheduled run alone. Like the runner it
// mails outside any transaction and records the outcome even when ctx is
// cancelled mid-send.
func (s *Service) SendNow(ctx context.Context, tenantID, id uuid.UUID) (*Outcome, error) {
	if p := s.mailProblem(); p != "" {
		return nil, invalid("%s", p)
	}
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	sc, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	sla, err := s.reports.Get(ctx, tenantID, sc.SLAID)
	if err != nil {
		return nil, err
	}
	period, err := lastClosedPeriod(sla, s.now())
	if err != nil {
		return nil, err
	}
	out := s.send(ctx, sc, sla, period.Key, false)
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer rcancel()
	if _, err := s.db.ExecContext(rctx, `
		UPDATE sla_report_schedules SET last_run_at = $3, last_sent_count = $4, last_error = NULLIF($5, '')
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, sc.ID, s.now(), out.Sent, out.Error); err != nil {
		return nil, fmt.Errorf("record report schedule run: %w", err)
	}
	return &out, nil
}

// send builds the period's report, renders the attachments and mails each
// remaining recipient. Failures land in the Outcome, never as an error: a
// run always records what happened and moves on. A cancelled ctx fails the
// recipients not yet mailed (the mailer checks it before each message).
func (s *Service) send(ctx context.Context, sc *Schedule, sla *models.SLA, periodKey string, issue bool) Outcome {
	out := Outcome{PeriodKey: periodKey}
	report, issued, err := s.periodReport(ctx, sc.TenantID, sla.ID, periodKey, issue)
	out.Issued = issued
	if err != nil {
		out.Error = "build report: " + deliverylog.SanitizeError(err.Error())
		return out
	}
	if p := s.mailProblem(); p != "" {
		out.Error = "not sent: " + p
		return out
	}
	attachments, err := renderAttachments(report, sc.Formats)
	if err != nil {
		out.Error = "render report: " + deliverylog.SanitizeError(err.Error())
		return out
	}
	opted, err := unsubscribedHashes(ctx, s.db, sc.ID)
	if err != nil {
		out.Error = deliverylog.SanitizeError(err.Error())
		return out
	}
	var firstErr string
	for _, to := range sc.Recipients {
		if opted[emailHash(to)] {
			out.Skipped++
			continue
		}
		if err := s.mailer.SendMessage(ctx, s.message(sc, sla, report, to, attachments)); err != nil {
			out.Failed++
			if firstErr == "" {
				firstErr = deliverylog.SanitizeError(err.Error())
			}
			continue
		}
		out.Sent++
	}
	if out.Failed > 0 {
		out.Error = fmt.Sprintf("%d of %d recipients failed: %s", out.Failed, out.Failed+out.Sent, firstErr)
	}
	return out
}

// periodReport is the closed period's report: issued now when issue is
// set (or reused when it already was), the issued copy when one exists,
// else the live report.
func (s *Service) periodReport(ctx context.Context, tenantID, slaID uuid.UUID, periodKey string, issue bool) (*models.SLAReport, bool, error) {
	if issue {
		r, err := s.reports.IssueReport(ctx, tenantID, slaID, periodKey, slas.Issuer{Schedule: true})
		var conflict slas.Conflict
		if err == nil {
			return r, true, nil
		}
		if !errors.As(err, &conflict) {
			return nil, false, err
		}
	}
	r, err := s.reports.IssuedReportForPeriod(ctx, tenantID, slaID, periodKey)
	if err == nil {
		return r, true, nil
	}
	if !errors.Is(err, slas.ErrNotFound) {
		return nil, false, err
	}
	r, err = s.reports.Report(ctx, tenantID, slaID, slas.ReportQuery{Period: periodKey})
	return r, false, err
}

func renderAttachments(r *models.SLAReport, formats []string) ([]mail.Attachment, error) {
	var out []mail.Attachment
	for _, f := range formats {
		var buf bytes.Buffer
		var contentType string
		switch f {
		case FormatPDF:
			if err := slas.WritePDF(&buf, r); err != nil {
				return nil, err
			}
			contentType = "application/pdf"
		case FormatCSV:
			if err := slas.WriteCSV(&buf, r); err != nil {
				return nil, err
			}
			contentType = "text/csv; charset=utf-8"
		default:
			continue
		}
		out = append(out, mail.Attachment{Filename: slas.FileName(r, f), ContentType: contentType, Data: buf.Bytes()})
	}
	return out, nil
}

// message is one recipient's email: the headline as text, the attachments,
// and the recipient's own unsubscribe link in the body and as
// List-Unsubscribe with RFC 8058 one-click POST.
func (s *Service) message(sc *Schedule, sla *models.SLA, r *models.SLAReport, to string, attachments []mail.Attachment) mail.Message {
	token := UnsubscribeToken(sc.ID, sc.unsubscribeKey, to)
	var b strings.Builder
	fmt.Fprintf(&b, "%s: availability report for %s, %s.\n\n", r.Definition.Name, r.Period.Key, slas.PeriodLabel(r))
	b.WriteString(slas.SummaryText(r))
	b.WriteString("\n")
	if r.Issued != nil {
		fmt.Fprintf(&b, "This is the issued report: it was frozen on %s and will not change.\n",
			r.Issued.IssuedAt.UTC().Format("2 Jan 2006 15:04 MST"))
	} else {
		b.WriteString("This report is computed live and was not issued; Probara can still revise it if maintenance windows or history change.\n")
	}
	names := make([]string, 0, len(attachments))
	for _, a := range attachments {
		names = append(names, a.Filename)
	}
	if len(names) > 0 {
		fmt.Fprintf(&b, "Attached: %s.\n", strings.Join(names, ", "))
	}
	fmt.Fprintf(&b, "\nWorkspace members can open it in Probara: %s/slas/%s?period=%s\n", s.appBaseURL, sla.ID, r.Period.Key)
	fmt.Fprintf(&b, "\nYou receive this because %s is on this SLA's report schedule.\nUnsubscribe: %s/report-unsubscribe/%s\n",
		to, s.appBaseURL, token)
	return mail.Message{
		To:          []string{to},
		Subject:     fmt.Sprintf("SLA report: %s, %s: %s (%s)", r.Definition.Name, r.Period.Key, slas.Verdict(r), availabilityText(r)),
		Text:        b.String(),
		Attachments: attachments,
		Headers: map[string]string{
			"List-Unsubscribe":      "<" + s.appBaseURL + "/api/v1/report-unsubscribe/" + token + ">",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
	}
}

func availabilityText(r *models.SLAReport) string {
	if r.Summary.AvailabilityPct == nil {
		return "no data"
	}
	return fmt.Sprintf("%.3f%%", *r.Summary.AvailabilityPct)
}

// recordSend audits a send that did not come through an audited request.
func (s *Service) recordSend(sc *Schedule, out Outcome, label string) {
	if s.audit == nil {
		return
	}
	tenantID := sc.TenantID
	event := audit.Event{
		TenantID:     &tenantID,
		ActorType:    audit.ActorAnonymous,
		ActorLabel:   label,
		Action:       "report-schedule.send",
		ResourceType: "report-schedule",
		ResourceID:   sc.ID.String(),
		Outcome:      audit.OutcomeSuccess,
		Details: map[string]any{
			"sla_id": sc.SLAID.String(), "period": out.PeriodKey, "issued": out.Issued,
			"sent": out.Sent, "skipped": out.Skipped, "failed": out.Failed,
		},
	}
	if out.Error != "" {
		event.Outcome = audit.OutcomeFailure
		event.Details["error"] = out.Error
	}
	s.audit.Record(event)
}

// Runner runs RunDue on an interval (the audit pruner's pattern). Every API
// replica runs one; SKIP LOCKED and the claim committed before sending keep
// them from sending a period twice.
type Runner struct {
	svc      *Service
	interval time.Duration
	stop     chan struct{}
	wg       sync.WaitGroup
}

// NewRunner wraps the service in a background loop.
func NewRunner(svc *Service) *Runner {
	return &Runner{svc: svc, interval: runInterval, stop: make(chan struct{})}
}

// Start begins the loop.
func (r *Runner) Start() {
	r.wg.Add(1)
	go r.run()
}

// Stop ends the loop and waits for a run in progress.
func (r *Runner) Stop() {
	close(r.stop)
	r.wg.Wait()
}

func (r *Runner) run() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	r.tick()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			r.tick()
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
	n, err := r.svc.RunDue(ctx)
	if err != nil && !isMissingSchema(err) && r.svc.log != nil {
		r.svc.log.WithError(err).Error("Failed to run report schedules")
	}
	if n > 0 && r.svc.log != nil {
		r.svc.log.WithFields(map[string]interface{}{"schedules": n}).Info("Ran due report schedules")
	}
}

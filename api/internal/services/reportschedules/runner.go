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
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

const (
	// runInterval is how often the runner looks for due schedules and
	// deliveries; tickTimeout bounds one look (what is unfinished is handed
	// back and picked up next time).
	runInterval = time.Minute
	tickTimeout = 10 * time.Minute
	// maxPlansPerTick and maxDeliveriesPerTick bound one tick; the rest
	// wait for the next tick or another replica.
	maxPlansPerTick      = 50
	maxDeliveriesPerTick = 500
	deliveryBatch        = 50
	// planLease keeps other replicas off a schedule while one issues its
	// report without holding a lock; a replica that dies mid-way leaves the
	// schedule due again once it lapses.
	planLease = 10 * time.Minute
	// planRetryDelay is how long a schedule whose period could not be
	// issued, or a delivery whose report could not be prepared, waits.
	planRetryDelay = 15 * time.Minute
	// claimLockTimeout bounds the claim's wait for the schedule row (an
	// operator saving it, the SLA being re-planned).
	claimLockTimeout = "5s"
	// maxCatchUp is how many closed periods a schedule sends after an
	// outage, oldest first; older ones are skipped and logged.
	maxCatchUp = 3
	// staleClaimAfter hands a reserved delivery back: its runner died or
	// is far behind, and nothing was sent. staleSendingAfter closes a
	// delivery whose send started that long ago as uncertain: far above
	// the mailer's 30-second bound per message.
	staleClaimAfter   = 15 * time.Minute
	staleSendingAfter = 15 * time.Minute
	// maxAttempts is the started sends a recipient gets before it is failed.
	maxAttempts = 5
	// recordTimeout bounds writing an outcome, which deliberately outlives
	// the run's own cancellation.
	recordTimeout = 30 * time.Second
	// maxErrorLen bounds a stored error.
	maxErrorLen = 500
)

// retryBackoff is the wait after the nth failed send (1-based).
var retryBackoff = []time.Duration{5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

const interruptedError = "the API stopped while sending; the message may or may not have arrived, so it is not sent again"

// RunOnce hands back stale claims, plans every due schedule, then sends the
// due deliveries. It stops early, without an error, once ctx is done.
func (s *Service) RunOnce(ctx context.Context) error {
	if err := s.reclaimStale(ctx); err != nil {
		return quietCancel(ctx, err)
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

// reclaimStale returns deliveries reserved by a runner that died (or fell
// far behind) to pending, since none of them was sent, and closes sends
// that started long ago without an outcome as uncertain: they may have
// arrived, so sending them again could mail the period twice.
func (s *Service) reclaimStale(ctx context.Context) error {
	now := s.now()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE sla_report_deliveries SET status = 'pending', claim_id = NULL, claimed_at = NULL, updated_at = NOW()
		WHERE status = 'claimed' AND claimed_at < $1
	`, now.Add(-staleClaimAfter)); err != nil {
		return fmt.Errorf("hand back stale report deliveries: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE sla_report_deliveries SET status = 'uncertain', claim_id = NULL, last_error = $2, updated_at = NOW()
		WHERE status = 'sending' AND claimed_at < $1
	`, now.Add(-staleSendingAfter), interruptedError); err != nil {
		return fmt.Errorf("close interrupted report deliveries: %w", err)
	}
	return nil
}

// candidate is one closed period a schedule should send.
type candidate struct {
	kind, timezone, key string
	start, end          time.Time
	carry               bool
}

// planOne picks the most overdue enabled schedule and sends its oldest
// unsent closed period on its way. No lock is held while the report is
// issued (that reads through other connections): a short transaction
// leases the schedule, the report is issued, and a second transaction,
// under lock_timeout, claims the period by recording it and adding one
// delivery per recipient, all before anyone is mailed. It reports whether a
// schedule was handled.
func (s *Service) planOne(ctx context.Context) (bool, error) {
	now := s.now()
	sc, err := s.lease(ctx, now)
	if err != nil || sc == nil {
		return false, err
	}
	sla, cand, next, perr := s.nextPeriod(ctx, sc, now)
	if perr == nil && cand != nil {
		_, perr = s.issuedReport(ctx, sc.TenantID, sc.SLAID, *cand)
	}
	if perr != nil {
		// Recorded and retried later rather than returned: an error here
		// would leave the row first in line and starve every other one.
		if _, err := s.db.ExecContext(ctx, `
			UPDATE sla_report_schedules SET next_run_at = $2, last_error = $3, updated_at = NOW() WHERE id = $1
		`, sc.ID, now.Add(planRetryDelay), clip("the report could not be issued: "+perr.Error())); err != nil {
			return false, fmt.Errorf("record report schedule failure: %w", err)
		}
		return true, nil
	}
	if cand == nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE sla_report_schedules SET next_run_at = $2 WHERE id = $1`, sc.ID, next); err != nil {
			return false, fmt.Errorf("reschedule report schedule: %w", err)
		}
		return true, nil
	}
	claimed, err := s.claimPeriod(ctx, sc.ID, *cand, now)
	if err != nil {
		return false, err
	}
	if claimed {
		s.recordRun(sc, sla, cand.key)
	}
	return true, nil
}

// lease takes the most overdue enabled schedule and moves its next_run_at
// planLease ahead, so other replicas pass it over while this one works.
func (s *Service) lease(ctx context.Context, now time.Time) (*row, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	// A deleted workspace's schedules never send again; filtered before the
	// LIMIT so they cannot hold the head of the queue.
	sc, err := scanRow(tx.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM sla_report_schedules
		WHERE enabled AND next_run_at <= $1
		  AND `+tenancy.LiveTenantSQL("sla_report_schedules.tenant_id")+`
		ORDER BY next_run_at
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, now))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lease report schedule: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sla_report_schedules SET next_run_at = $2 WHERE id = $1`, sc.ID, now.Add(planLease)); err != nil {
		return nil, fmt.Errorf("lease report schedule: %w", err)
	}
	return sc, tx.Commit()
}

// nextPeriod is the schedule's oldest closed period still to send, or nil
// with the time to look again. The rules (CLAUDE.md, website docs):
//   - a period is due once it closed plus ReportIssueGrace;
//   - only periods that ended after sends_after (set on create, re-enable
//     and a change of the SLA's period or timezone) and after the last one
//     sent are sent, so periods closed while disabled never are;
//   - after an outage the last maxCatchUp closed periods go out, oldest
//     first; older ones are skipped and logged;
//   - a period of the SLA's previous definition carried over by a change
//     within the grace hour goes first, as it was defined.
func (s *Service) nextPeriod(ctx context.Context, sc *row, now time.Time) (*models.SLA, *candidate, time.Time, error) {
	sla, err := s.reports.Get(ctx, sc.TenantID, sc.SLAID)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("load SLA: %w", err)
	}
	if sc.CarryKey != "" {
		loc, err := time.LoadLocation(sc.CarryTimezone)
		if err != nil {
			return nil, nil, time.Time{}, fmt.Errorf("carried period timezone: %w", err)
		}
		p, err := sharedanalytics.ParsePeriod(sharedanalytics.PeriodKind(sc.CarryKind), loc, sc.CarryKey)
		if err != nil {
			return nil, nil, time.Time{}, fmt.Errorf("carried period: %w", err)
		}
		if due := p.End.Add(slas.ReportIssueGrace); now.Before(due) {
			return sla, nil, due, nil
		}
		return sla, &candidate{kind: sc.CarryKind, timezone: sc.CarryTimezone, key: p.Key, start: p.Start, end: p.End, carry: true}, time.Time{}, nil
	}

	loc, err := time.LoadLocation(sla.Timezone)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("SLA timezone: %w", err)
	}
	floor := sc.SendsAfter
	if sc.LastPeriodEnd.After(floor) {
		floor = sc.LastPeriodEnd
	}
	// Due periods, newest first: closed at least the grace ago, after floor.
	running, err := slas.PeriodOf(sla.Period, sla.Timezone, now.Add(-slas.ReportIssueGrace))
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	var due []sharedanalytics.Period
	for p := running.Previous(loc); p.End.After(floor) && len(due) <= maxCatchUp; p = p.Previous(loc) {
		due = append(due, p)
	}
	if len(due) > maxCatchUp {
		due = due[:maxCatchUp]
		if s.log != nil {
			s.log.WithFields(map[string]interface{}{"sla_id": sc.SLAID.String(), "sent_from": due[maxCatchUp-1].Key}).
				Warn("Report schedule fell behind; periods before the last few closed ones are not sent")
		}
	}
	if len(due) > 0 {
		p := due[len(due)-1]
		return sla, &candidate{kind: sla.Period, timezone: sla.Timezone, key: p.Key, start: p.Start, end: p.End}, time.Time{}, nil
	}
	// Nothing due: look again when the period that just closed (if it is
	// still to send) or the running one is through its grace.
	next, err := slas.ReportRunAt(sla.Period, sla.Timezone, now)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	if cur, err := slas.PeriodOf(sla.Period, sla.Timezone, now); err == nil {
		if closed := cur.Previous(loc); closed.End.After(floor) {
			next = closed.End.Add(slas.ReportIssueGrace).UTC()
		}
	}
	return sla, nil, next, nil
}

// issuedReport returns the period's issued report, issuing it first when
// nobody did.
func (s *Service) issuedReport(ctx context.Context, tenantID, slaID uuid.UUID, c candidate) (*models.SLAReport, error) {
	r, err := s.reports.IssuedReportStarting(ctx, tenantID, slaID, c.start)
	if !errors.Is(err, slas.ErrNotFound) {
		return r, err
	}
	r, err = s.reports.IssueReportUnder(ctx, tenantID, slaID, c.kind, c.timezone, c.key, slas.Issuer{Schedule: true})
	var conflict slas.Conflict
	if errors.As(err, &conflict) {
		// Issued by hand, or by another replica, meanwhile.
		return s.reports.IssuedReportStarting(ctx, tenantID, slaID, c.start)
	}
	return r, err
}

// claimPeriod records the period as sent and adds one delivery per
// recipient (pending, or skipped for those who unsubscribed), in one
// transaction holding only the schedule row, after checking the period is
// still the one to send. Finished deliveries of other periods are pruned.
func (s *Service) claimPeriod(ctx context.Context, scheduleID uuid.UUID, c candidate, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '`+claimLockTimeout+`'`); err != nil {
		return false, fmt.Errorf("set lock timeout: %w", err)
	}
	sc, err := scanRow(tx.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM sla_report_schedules WHERE id = $1 FOR UPDATE`, scheduleID))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock report schedule: %w", err)
	}
	floor := sc.SendsAfter
	if sc.LastPeriodEnd.After(floor) {
		floor = sc.LastPeriodEnd
	}
	still := sc.Enabled
	if c.carry {
		still = still && sc.CarryKey == c.key && sc.CarryKind == c.kind && sc.CarryTimezone == c.timezone
	} else {
		still = still && sc.CarryKey == "" && c.end.After(floor)
	}
	if !still {
		// Edited, disabled or re-planned meanwhile: look again next tick.
		if _, err := tx.ExecContext(ctx, `UPDATE sla_report_schedules SET next_run_at = $2 WHERE id = $1`, sc.ID, now); err != nil {
			return false, fmt.Errorf("reschedule report schedule: %w", err)
		}
		return false, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM sla_report_deliveries
		WHERE schedule_id = $1 AND period_start <> $2 AND status NOT IN ('pending', 'claimed', 'sending')
	`, sc.ID, c.start.UTC()); err != nil {
		return false, fmt.Errorf("prune report deliveries: %w", err)
	}
	keys := make([]string, len(sc.Recipients))
	for i, r := range sc.Recipients {
		keys[i] = addressKey(sc.ID, r)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sla_report_deliveries (schedule_id, tenant_id, period_start, period_key, address_key, address, status, last_error, next_attempt_at)
		SELECT $1, $2, $3, $4, r.k, r.a,
			CASE WHEN u.address_key IS NULL THEN 'pending' ELSE 'skipped' END,
			CASE WHEN u.address_key IS NULL THEN NULL ELSE 'unsubscribed' END, $5
		FROM unnest($6::text[], $7::text[]) AS r(a, k)
		LEFT JOIN sla_report_unsubscribes u ON u.schedule_id = $1 AND u.address_key = r.k
		ON CONFLICT DO NOTHING
	`, sc.ID, sc.TenantID, c.start.UTC(), c.key, now, pq.Array(sc.Recipients), pq.Array(keys)); err != nil {
		return false, fmt.Errorf("add report deliveries: %w", err)
	}
	lastEnd := c.end
	if sc.LastPeriodEnd.After(lastEnd) {
		lastEnd = sc.LastPeriodEnd
	}
	// next_run_at = now: the next pass sends any further due period or
	// plans the next close.
	if _, err := tx.ExecContext(ctx, `
		UPDATE sla_report_schedules SET last_period_key = $2, last_period_start = $3, last_period_end = $4, last_run_at = $5,
			last_error = NULL, next_run_at = $5, carry_period_kind = NULL, carry_timezone = NULL, carry_period_key = NULL,
			updated_at = NOW()
		WHERE id = $1
	`, sc.ID, c.key, c.start.UTC(), lastEnd.UTC(), now); err != nil {
		return false, fmt.Errorf("claim report schedule period: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit report schedule claim: %w", err)
	}
	return true, nil
}

// claimedDelivery is one delivery a runner batch reserved.
type claimedDelivery struct {
	scheduleID  uuid.UUID
	tenantID    uuid.UUID
	periodStart time.Time
	periodKey   string
	addressKey  string
	address     string
	attempts    int
	claimID     uuid.UUID
}

type groupKey struct {
	scheduleID  uuid.UUID
	periodStart time.Time
}

// deliverBatch reserves up to deliveryBatch due deliveries of enabled
// schedules as 'claimed' under one claim ID, sends them group by group, and
// before returning hands back every row it reserved but did not get to: a
// failure in one group never strands the others.
func (s *Service) deliverBatch(ctx context.Context) (int, error) {
	now := s.now()
	claimID := uuid.New()
	rows, err := s.db.QueryContext(ctx, `
		UPDATE sla_report_deliveries d
		SET status = 'claimed', claim_id = $3, claimed_at = $1, updated_at = NOW()
		FROM (
			SELECT dd.schedule_id, dd.period_start, dd.address_key
			FROM sla_report_deliveries dd
			JOIN sla_report_schedules sc ON sc.id = dd.schedule_id AND sc.enabled
			WHERE dd.status = 'pending' AND dd.next_attempt_at <= $1
			  AND `+tenancy.LiveTenantSQL("sc.tenant_id")+`
			ORDER BY dd.next_attempt_at
			LIMIT $2
			FOR UPDATE OF dd SKIP LOCKED
		) c
		WHERE d.schedule_id = c.schedule_id AND d.period_start = c.period_start AND d.address_key = c.address_key
		RETURNING d.schedule_id, d.tenant_id, d.period_start, d.period_key, d.address_key, d.address, d.attempts
	`, now, deliveryBatch, claimID)
	if err != nil {
		return 0, fmt.Errorf("claim report deliveries: %w", err)
	}
	defer s.releaseBatch(ctx, claimID)
	var claimed []claimedDelivery
	for rows.Next() {
		d := claimedDelivery{claimID: claimID}
		if err := rows.Scan(&d.scheduleID, &d.tenantID, &d.periodStart, &d.periodKey, &d.addressKey, &d.address, &d.attempts); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan report delivery: %w", err)
		}
		claimed = append(claimed, d)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("claim report deliveries: %w", err)
	}

	groups := map[groupKey][]claimedDelivery{}
	var order []groupKey
	for _, d := range claimed {
		k := groupKey{d.scheduleID, d.periodStart}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], d)
	}
	for _, k := range order {
		if ctx.Err() != nil {
			break
		}
		s.sendGroup(ctx, groups[k])
		if s.afterGroup != nil {
			s.afterGroup()
		}
	}
	return len(claimed), nil
}

// releaseBatch hands every row of the batch still only reserved back to
// pending, uncounted. Rows that started sending or finished are untouched.
func (s *Service) releaseBatch(ctx context.Context, claimID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE sla_report_deliveries SET status = 'pending', claim_id = NULL, claimed_at = NULL, updated_at = NOW()
		WHERE claim_id = $1 AND status = 'claimed'
	`, claimID); err != nil && s.log != nil {
		s.log.WithError(err).Error("Failed to hand back report deliveries; they return after the claim times out")
	}
}

// sendGroup sends one period's report to its reserved recipients. Errors
// end up on the rows, never returned: each row ends sent, retrying, failed,
// uncertain, skipped or cancelled, or is handed back by releaseBatch.
func (s *Service) sendGroup(ctx context.Context, group []claimedDelivery) {
	first := group[0]
	sc, err := scanRow(s.db.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM sla_report_schedules
		WHERE tenant_id = $1 AND id = $2 AND `+tenancy.LiveTenantSQL("sla_report_schedules.tenant_id"), first.tenantID, first.scheduleID))
	if errors.Is(err, sql.ErrNoRows) {
		// Deleted meanwhile, its deliveries with it, or its workspace was
		// deleted: the claimed rows are handed back by releaseBatch and
		// never claimed again.
		return
	}
	var sla *models.SLA
	var attachments []mail.Attachment
	var report *models.SLAReport
	var opted map[string]bool
	if err == nil {
		sla, err = s.reports.Get(ctx, sc.TenantID, sc.SLAID)
	}
	if err == nil {
		report, err = s.reports.IssuedReportStarting(ctx, sc.TenantID, sc.SLAID, first.periodStart)
	}
	if err == nil {
		attachments, err = renderAttachments(report, sc.IncludeCSV)
	}
	if err == nil {
		opted, err = unsubscribedKeys(ctx, s.db, sc.ID)
	}
	if err != nil {
		if ctx.Err() != nil {
			return // handed back
		}
		// Nothing was sent: retry later, without counting an attempt.
		for _, d := range group {
			s.update(ctx, d, models.SLAReportDeliveryClaimed, `status = 'pending', claim_id = NULL, next_attempt_at = $4, last_error = $5`,
				s.now().Add(planRetryDelay), clip("prepare the report: "+err.Error()))
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
			return // the rest are handed back
		case !sc.Enabled:
			s.update(ctx, d, models.SLAReportDeliveryClaimed, `status = 'cancelled', claim_id = NULL, last_error = 'schedule disabled'`)
			continue
		case opted[d.addressKey]:
			s.update(ctx, d, models.SLAReportDeliveryClaimed, `status = 'skipped', claim_id = NULL, last_error = 'unsubscribed'`)
			continue
		case !listed[d.addressKey]:
			s.update(ctx, d, models.SLAReportDeliveryClaimed, `status = 'cancelled', claim_id = NULL, last_error = 'removed from the recipients'`)
			continue
		}
		msg, err := s.message(sc, sla, report, d.address, attachments)
		if err != nil {
			s.update(ctx, d, models.SLAReportDeliveryClaimed, `status = 'failed', claim_id = NULL, last_error = $4`, clip(err.Error()))
			continue
		}
		var started bool
		if d, started = s.startSending(ctx, d); !started {
			continue
		}
		s.recordOutcome(ctx, d, s.mailer.SendMessage(ctx, msg))
	}
}

// startSending moves a reserved row to 'sending' (counting the attempt)
// immediately before its message goes out, and reports whether this batch
// still held it: another replica may have taken it back after
// staleClaimAfter while this batch was busy.
func (s *Service) startSending(ctx context.Context, d claimedDelivery) (claimedDelivery, bool) {
	err := s.db.QueryRowContext(ctx, `
		UPDATE sla_report_deliveries SET status = 'sending', attempts = attempts + 1, claimed_at = $5, updated_at = NOW()
		WHERE schedule_id = $1 AND period_start = $2 AND address_key = $3 AND claim_id = $4 AND status = 'claimed'
		RETURNING attempts
	`, d.scheduleID, d.periodStart, d.addressKey, d.claimID, s.now()).Scan(&d.attempts)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil && s.log != nil {
			s.log.WithError(err).Error("Failed to start a report delivery")
		}
		return d, false
	}
	return d, true
}

// recordOutcome stores what a started send came to (err nil: sent).
func (s *Service) recordOutcome(ctx context.Context, d claimedDelivery, err error) {
	now := s.now()
	const sending = models.SLAReportDeliverySending
	switch {
	case err == nil:
		s.update(ctx, d, sending, `status = 'sent', claim_id = NULL, sent_at = $4, last_error = NULL`, now)
	case errors.Is(err, mail.ErrMaybeDelivered):
		s.update(ctx, d, sending, `status = 'uncertain', claim_id = NULL, last_error = $4`,
			clip("the connection was lost after the message was sent; it may have arrived, so it is not sent again: "+err.Error()))
	case ctx.Err() != nil:
		// Stopped before the message was through: nothing reached the
		// relay, so it goes back uncounted.
		s.update(ctx, d, sending, `status = 'pending', claim_id = NULL, claimed_at = NULL, attempts = attempts - 1`)
	case permanent(err) || d.attempts >= maxAttempts:
		s.update(ctx, d, sending, `status = 'failed', claim_id = NULL, last_error = $4`, clip(err.Error()))
	default:
		wait := retryBackoff[min(d.attempts, len(retryBackoff))-1]
		s.update(ctx, d, sending, `status = 'pending', claim_id = NULL, next_attempt_at = $4, last_error = $5`, now.Add(wait), clip(err.Error()))
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

// update writes one row's outcome, on a context detached from the run's so
// a run cut short still records what it did, and only while the row is in
// the state this batch left it in (from) under its claim.
func (s *Service) update(ctx context.Context, d claimedDelivery, from, set string, args ...any) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	all := append([]any{d.scheduleID, d.periodStart, d.addressKey}, args...)
	next := len(all) + 1
	all = append(all, d.claimID, from)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`UPDATE sla_report_deliveries SET %s, updated_at = NOW()
		WHERE schedule_id = $1 AND period_start = $2 AND address_key = $3 AND claim_id = $%d AND status = $%d`, set, next, next+1), all...); err != nil && s.log != nil {
		s.log.WithError(err).Error("Failed to record a report delivery")
	}
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

// recordRun audits a claimed period on the SLA (the resource every
// schedule event names): the report was issued (or reused) and queued for
// its recipients.
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
// replica runs one; the lease, the claims committed before sending and the
// per-delivery states keep them from sending a period twice.
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

// Stop ends the loop and waits for a run in progress: sends in flight are
// cut short and recorded, and every reserved row is handed back.
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
	ctx, cancel := context.WithTimeout(context.Background(), tickTimeout)
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

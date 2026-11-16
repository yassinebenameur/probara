package alerter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/incidentrecovery"
)

// Relapse reopen: every alert kind's opener first tries to reopen its
// subject's recently recovered alert (reopenRecentAlertTx) and inserts a new
// alert only when that finds nothing. The rule and what it notifies are
// documented in shared/alertrouting relapse.go; this file is the write side.
//
// Notifications are not sent here. The dispatch loops page a reopened alert
// like any open one: a route last paged in an earlier episode
// (paged_relapse < relapse_count) is due the one relapse notice
// (openAlertEventDue, the EventRelapsed claim), and the escalation delays and
// reminders count from reopened_at.

// errAlertAlreadyOpen reports that a reopen lost a race to another replica's
// new alert for the same subject (the one-open-alert unique index refused
// it). The caller's transaction is aborted; like a lost INSERT race, there is
// nothing left to do.
var errAlertAlreadyOpen = errors.New("an alert for this subject is already open")

// errReopenBusy reports that the reopen gave up waiting for a row lock
// (reopenLockTimeout). The caller's transaction is aborted; the next tick
// tries again.
var errReopenBusy = errors.New("relapse reopen waited too long for a lock")

// reopenSkipped reports whether a reopen attempt ended with nothing to do
// this tick: another replica opened the subject's alert, or the reopen timed
// out on a lock. Either way the opener must return without inserting.
func reopenSkipped(err error) bool {
	return errors.Is(err, errAlertAlreadyOpen) || errors.Is(err, errReopenBusy)
}

// reopenLockTimeout bounds how long a reopen waits for row locks. The alert
// it reopens was just resolved, and its resolve may still be in flight: the
// alerter's claim and the async worker hold the row FOR SHARE through the
// provider call (up to the 15-second delivery timeout), and the reopen's
// UPDATE waits for them so "recovered" is never sent after "relapsed". The
// openers run serially over every tenant's subjects, so one slow webhook
// must not hold up new alerts everywhere: past this the attempt is dropped
// and the next tick retries. It never falls back to inserting a new alert,
// which would turn the relapse into the separate page the setting exists to
// avoid (and race a replica that does get the lock; the one-open-alert
// index would keep whichever committed first).
const reopenLockTimeout = time.Second

// alertIdentity is what reopen matches an alert on: its kind and subject.
// Monitor kinds name the monitor (and, for host_metric, the series key in
// metricName); monitor-less kinds name the location pair (mesh_edge) or the
// location alone, in sourceLocationID (location_offline).
type alertIdentity struct {
	tenantID         uuid.UUID
	kind             string
	monitorID        uuid.UUID
	metricName       string
	sourceLocationID uuid.UUID
	targetLocationID uuid.UUID
	// metricValueNull, when set, restricts the reopen to an alert of the
	// same form: domain_expiry's "expiry unknown" (no value) and dated forms
	// say different things, so one never reopens as the other.
	metricValueNull *bool
}

// alertColumn is one kind-specific column a reopen refreshes with what the
// opener observed now (failure count, last error, measured value...).
type alertColumn struct {
	name  string
	value any
}

// reopenedAlert is what a reopen returns.
type reopenedAlert struct {
	id           uuid.UUID
	triggeredAt  time.Time
	relapseCount int
	reopenedAt   time.Time
	recoveredAt  time.Time
}

// apply copies the reopen onto the record the caller publishes.
func (r *reopenedAlert) apply(record *alertRecord) {
	record.ID = r.id
	record.TriggeredAt = r.triggeredAt
	record.RelapseCount = r.relapseCount
	reopenedAt, recoveredAt := r.reopenedAt, r.recoveredAt
	record.ReopenedAt = &reopenedAt
	record.LastRecoveredAt = &recoveredAt
}

// reopenRecentAlertTx reopens id's latest alert inside tx when the alerter
// resolved it because its condition cleared (auto_resolved) less than the
// tenant's reopen cooldown ago, and
// returns it; nil when there is nothing to reopen (the feature is off, the
// latest alert is open, was resolved by hand or too long ago, or none
// exists), and the caller opens a new alert. refresh lists the kind-specific
// columns to rewrite.
//
// One statement does the reopen, so it is atomic and race-safe across
// replicas: the UPDATE is guarded by status = 'resolved', so a second
// replica blocks on the row, re-checks the guard against the committed
// reopen and matches nothing, and its INSERT then conflicts with the reopened
// alert. If another replica's INSERT committed first, the one-open-alert
// index refuses this UPDATE: errAlertAlreadyOpen.
//
// The statement also clears the earlier episode's acknowledgement and snooze
// (first_acknowledged_at stays, for SLA reports), hands the one-shot
// acknowledgement claims back so acknowledging the relapse reaches paging
// providers again, and records the relapse in alert_relapses.
func reopenRecentAlertTx(ctx context.Context, tx *sql.Tx, id alertIdentity, refresh []alertColumn) (*reopenedAlert, error) {
	args := []any{id.tenantID, id.kind}
	var subject string
	if id.monitorID != uuid.Nil {
		args = append(args, id.monitorID, id.metricName)
		subject = `%[1]s.monitor_id = $3 AND COALESCE(%[1]s.metric_name, '') = $4`
	} else {
		var target any
		if id.targetLocationID != uuid.Nil {
			target = id.targetLocationID
		}
		args = append(args, id.sourceLocationID, target)
		subject = `%[1]s.monitor_id IS NULL AND %[1]s.source_location_id = $3
			AND %[1]s.target_location_id IS NOT DISTINCT FROM $4::uuid`
	}
	matches := func(alias string) string {
		return alias + `.tenant_id = $1 AND ` + alias + `.kind = $2 AND ` + fmt.Sprintf(subject, alias)
	}
	form := ""
	if id.metricValueNull != nil {
		args = append(args, *id.metricValueNull)
		form = fmt.Sprintf(` AND (al.metric_value IS NULL) = $%d`, len(args))
	}

	var set strings.Builder
	for _, c := range refresh {
		args = append(args, c.value)
		fmt.Fprintf(&set, ", %s = $%d", c.name, len(args))
	}

	query := `
		WITH reopened AS (
			UPDATE alerts al
			SET status = 'active', resolved_at = NULL, auto_resolved = FALSE, resolve_reason = NULL,
				relapse_count = al.relapse_count + 1,
				-- Never before the recovery it interrupts (a resolve written
				-- by an older alerter on its own clock, or a transaction
				-- that began a moment before that resolve committed).
				reopened_at = GREATEST(NOW(), prev.resolved_at),
				acknowledged_at = NULL, acknowledged_by_user_id = NULL,
				snoozed_until = NULL, snoozed_at = NULL, snoozed_by_user_id = NULL` + set.String() + `,
				updated_at = NOW()
			FROM tenants te,
				-- The row as this statement found it: RETURNING reads the
				-- updated row, whose resolved_at is already NULL.
				alerts prev
			WHERE ` + matches("al") + `
			  AND te.id = al.tenant_id AND te.alert_reopen_cooldown_minutes > 0
			  AND prev.id = al.id
			  AND al.status = 'resolved' AND al.auto_resolved
			  AND al.resolved_at > NOW() - make_interval(mins => te.alert_reopen_cooldown_minutes)` + form + `
			  -- Only the subject's latest alert: a newer one (open, or
			  -- resolved by hand) means that episode has been superseded.
			  AND NOT EXISTS (
				SELECT 1 FROM alerts later
				WHERE ` + matches("later") + `
				  AND (later.created_at, later.id) > (al.created_at, al.id))
			RETURNING al.id, al.triggered_at, al.relapse_count, al.reopened_at, prev.resolved_at
		), relapse AS (
			INSERT INTO alert_relapses (alert_id, relapse_number, recovered_at, reopened_at)
			SELECT id, relapse_count, resolved_at, reopened_at FROM reopened
		), ack_claims AS (
			UPDATE alert_notification_states s
			SET acknowledged_sent_at = NULL, updated_at = NOW()
			FROM reopened
			WHERE s.alert_id = reopened.id AND s.acknowledged_sent_at IS NOT NULL
		)
		SELECT id, triggered_at, relapse_count, reopened_at, resolved_at FROM reopened`

	// SET LOCAL lasts to the end of the transaction, so it is reset right
	// after the statement: the opener's INSERT and auto-incident steps keep
	// the session's own timeout.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`SET LOCAL lock_timeout = '%dms'`, reopenLockTimeout.Milliseconds())); err != nil {
		return nil, fmt.Errorf("bound relapse reopen lock wait: %w", err)
	}
	var r reopenedAlert
	err := tx.QueryRowContext(ctx, query, args...).Scan(&r.id, &r.triggeredAt, &r.relapseCount, &r.reopenedAt, &r.recoveredAt)
	switch {
	case isLockTimeout(err):
		return nil, errReopenBusy
	case isUniqueViolation(err):
		return nil, errAlertAlreadyOpen
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("reopen %s alert: %w", id.kind, err)
	}
	if _, rerr := tx.ExecContext(ctx, `SET LOCAL lock_timeout TO DEFAULT`); rerr != nil {
		return nil, fmt.Errorf("reset lock wait after relapse reopen: %w", rerr)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &r, nil
}

// alertRelapsedMessage is the incident timeline entry a reopen writes.
const alertRelapsedMessage = "Alert relapsed: it failed again within the reopen cooldown and reopened"

// noteRelapseTx adds an "Alert relapsed" system entry to every open incident
// the reopened alert is linked to (an auto incident or a manual link), so the
// incident's timeline shows the flap next to the recovery entry the earlier
// resolve wrote. Call it after any auto-incident handling, so an incident the
// reopen created gets it too.
func (a *Alerter) noteRelapseTx(ctx context.Context, tx *sql.Tx, tenantID uuid.UUID, r *reopenedAlert) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT i.id
		FROM incidents i
		JOIN incident_alerts ia ON ia.incident_id = i.id
		WHERE ia.alert_id = $1 AND i.tenant_id = $2 AND i.state <> 'resolved'
		ORDER BY i.id
	`, r.id, tenantID)
	if err != nil {
		return fmt.Errorf("load incidents for alert relapse: %w", err)
	}
	var incidentIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan incident for alert relapse: %w", err)
		}
		incidentIDs = append(incidentIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("iterate incidents for alert relapse: %w", err)
	}
	for _, incidentID := range incidentIDs {
		if err := incidentrecovery.InsertTimelineEntryTx(ctx, tx, tenantID, incidentID, incidentrecovery.SystemEntryType,
			alertRelapsedMessage, map[string]any{
				"event":         "relapsed",
				"alert_id":      r.id.String(),
				"relapse_count": r.relapseCount,
			}); err != nil {
			return err
		}
	}
	a.logger.WithFields(map[string]interface{}{
		"alert_id": r.id, "relapse_count": r.relapseCount,
	}).Info("Reopened a recently recovered alert as a relapse")
	return nil
}

// isUniqueViolation reports whether err is a Postgres unique violation.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

// isLockTimeout reports whether err is Postgres giving up on lock_timeout.
func isLockTimeout(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "55P03"
}

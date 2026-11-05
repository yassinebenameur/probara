package alerter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Reopen within cooldown (flap suppression).
//
// A monitor that goes down again less than its tenant's
// alert_reopen_cooldown_seconds after the alerter auto-resolved its last
// availability alert gets that alert back instead of a new one: the alert
// reopens with relapse_count+1 and reopened_at = NOW(). A flapping target
// therefore keeps one alert (and one auto incident) for the whole flapping
// period, and the alert list counts its relapses.
//
// Each reopen is a new paging episode. It clears the previous episode's
// acknowledgement, unacknowledge and snooze (an ack or snooze was for the
// outage that recovered, not for this one), so the relapse pages like a new
// alert: its escalation tiers count from reopened_at
// (alertrouting.DispatchEligibleSinceExpr), a route that heard the previous
// resolve is paged again through the "reopened" claim, and the page goes out
// as a "created" event with Alert.RelapseCount > 0.
//
// Only the alerter's own resolves reopen: a manual resolve (resolved_via set
// by the API) never does, nor does any alert but the monitor's latest
// availability alert. Other alert kinds never reopen.

// reopenedAlert is the row a reopen brought back.
type reopenedAlert struct {
	id           uuid.UUID
	tenantID     uuid.UUID
	triggeredAt  time.Time
	relapseCount int
	reopenedAt   time.Time
}

// reopenOutageAlertTx reopens dm's latest availability alert inside tx when
// the alerter resolved it less than dm.reopenCooldownSeconds ago. ok is false
// when there is nothing to reopen and the caller opens a new alert.
//
// It is race-safe across replicas the same way the INSERT is: the reopen is
// an UPDATE guarded by status = 'resolved', so a second replica blocks on the
// row, re-checks the guard against the committed reopen and matches nothing,
// then its INSERT conflicts with the now-open alert. When another replica's
// INSERT got there first, the partial unique index rejects this UPDATE with a
// unique violation, which the caller treats as having lost the race.
func (a *Alerter) reopenOutageAlertTx(
	ctx context.Context,
	tx *sql.Tx,
	dm downMonitor,
	lastError *string,
	rootCauseID *uuid.UUID,
	rootCauseDownSince *time.Time,
	failingLocations interface{},
) (reopenedAlert, bool, error) {
	var r reopenedAlert
	err := tx.QueryRowContext(ctx, `
		UPDATE alerts al
		SET status = 'active', resolved_at = NULL, resolve_reason = NULL,
			relapse_count = al.relapse_count + 1, reopened_at = NOW(),
			failure_count = $3, last_error = $4,
			root_cause_monitor_id = $5, root_cause_down_since = $6, root_cause_cleared_at = NULL,
			failing_locations = $7,
			acknowledged_at = NULL, acknowledged_by_user_id = NULL, acknowledged_via = NULL,
			unacknowledged_at = NULL, snoozed_until = NULL, snoozed_by_user_id = NULL,
			updated_at = NOW()
		WHERE al.id = (
				SELECT latest.id FROM alerts latest
				WHERE latest.monitor_id = $1 AND latest.kind = 'availability'
				ORDER BY latest.triggered_at DESC, latest.id DESC
				LIMIT 1)
		  AND al.status = 'resolved'
		  AND al.resolved_via IS NULL
		  AND al.resolved_at > NOW() - ($2::double precision * INTERVAL '1 second')
		RETURNING al.id, al.tenant_id, al.triggered_at, al.relapse_count, al.reopened_at
	`, dm.id, float64(dm.reopenCooldownSeconds), dm.failCount, lastError, rootCauseID, rootCauseDownSince, failingLocations).
		Scan(&r.id, &r.tenantID, &r.triggeredAt, &r.relapseCount, &r.reopenedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return reopenedAlert{}, false, nil
	}
	if err != nil {
		return reopenedAlert{}, false, fmt.Errorf("reopen outage alert: %w", err)
	}
	// The previous episode's acknowledgement reached paging providers once
	// per channel (acknowledged_sent_at). Clear it so acknowledging the
	// relapse reaches them again.
	if _, err := tx.ExecContext(ctx, `
		UPDATE alert_notification_states
		SET acknowledged_sent_at = NULL, updated_at = NOW()
		WHERE alert_id = $1 AND acknowledged_sent_at IS NOT NULL
	`, r.id); err != nil {
		return reopenedAlert{}, false, fmt.Errorf("reset acknowledgement claims: %w", err)
	}
	return r, true, nil
}

// finishReopen records the relapse on the alert's incidents, commits tx and
// publishes the reopened alert as a "created" event with its relapse count.
// Notifications are not sent here: dispatchOpenAlerts pages the relapse on
// its escalation tiers like any open alert.
func (a *Alerter) finishReopen(
	ctx context.Context,
	tx *sql.Tx,
	dm downMonitor,
	r reopenedAlert,
	lastError *string,
	rootCauseID *uuid.UUID,
	rootCauseName *string,
	rootCauseDownSince *time.Time,
) error {
	var autoIncident bool
	if err := tx.QueryRowContext(ctx,
		`SELECT auto_create_incident FROM tenants WHERE id = $1`, dm.tenantID).Scan(&autoIncident); err != nil {
		return fmt.Errorf("load incident toggle: %w", err)
	}
	if autoIncident {
		// Usually a no-op that finds the open auto incident the alert is
		// already linked to; if an operator resolved that incident, the
		// relapse gets a new one, as a new alert would.
		record := alertRecord{ID: r.id, TenantID: dm.tenantID, MonitorID: dm.id, FailureCount: dm.failCount, LastError: lastError}
		binding := policyBinding{MonitorID: dm.id, TenantID: dm.tenantID, MonitorName: dm.name, CreateIncidentOnFire: true}
		if err := a.ensureAutoIncidentForAlertTx(ctx, tx, binding, &record); err != nil {
			return err
		}
	}
	if err := a.recordAlertRelapseTx(ctx, tx, dm.tenantID, dm.id, r.id, r.relapseCount); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reopened alert transaction: %w", err)
	}

	a.logger.WithFields(map[string]interface{}{
		"alert_id": r.id, "monitor_id": dm.id, "relapse_count": r.relapseCount,
	}).Info("Reopened a recently resolved alert instead of opening a new one")

	reopenedAt := r.reopenedAt
	record := alertRecord{
		ID: r.id, TenantID: dm.tenantID, MonitorID: dm.id,
		FailureCount: dm.failCount, LastError: lastError, TriggeredAt: r.triggeredAt,
		RootCauseMonitorID: rootCauseID, RootCauseMonitorName: rootCauseName, RootCauseDownSince: rootCauseDownSince,
		FailingLocations: parseFailingLocations(dm.failingLocations),
		RelapseCount:     r.relapseCount, ReopenedAt: &reopenedAt,
	}
	binding := policyBinding{MonitorID: dm.id, TenantID: dm.tenantID, MonitorName: dm.name}
	a.publishAlertEvent(ctx, "created", binding, &record, nil)
	return nil
}

// alertRelapsedMessage is the incident timeline entry a reopen writes.
const alertRelapsedMessage = "Alert relapsed: the monitor went down again within the reopen cooldown"

// recordAlertRelapseTx adds a "relapsed" system entry to every open incident
// the reopened alert is linked to (auto incidents and manual links alike), so
// the incident's timeline shows the flap.
func (a *Alerter) recordAlertRelapseTx(ctx context.Context, tx *sql.Tx, tenantID, monitorID, alertID uuid.UUID, relapseCount int) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT i.id
		FROM incidents i
		JOIN incident_alerts ia ON ia.incident_id = i.id
		WHERE ia.alert_id = $1 AND i.tenant_id = $2 AND i.state <> 'resolved'
		ORDER BY i.id
	`, alertID, tenantID)
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
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate incidents for alert relapse: %w", err)
	}
	rows.Close()

	for _, incidentID := range incidentIDs {
		if err := a.insertIncidentTimelineEntryTx(ctx, tx, tenantID, incidentID, incidentSystemEntryType, alertRelapsedMessage, map[string]interface{}{
			"event":         "relapsed",
			"alert_id":      alertID.String(),
			"monitor_id":    monitorID.String(),
			"relapse_count": relapseCount,
		}); err != nil {
			return err
		}
	}
	return nil
}

// isUniqueViolation reports whether err is a Postgres unique violation.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

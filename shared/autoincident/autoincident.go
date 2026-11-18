// Package autoincident is the one place an automatic incident is found or
// created for a monitor. Two services do it: the alerter, when a firing
// alert opens one (the workspace's "create incident on fire" setting), and
// the API's status-page runner, when it publishes a monitor's outage to a
// page that opted into automatic publishing. Both must land on the same
// incident, so they serialize on the same advisory lock key and use the
// same lookup: one open automatic incident per (tenant, monitor).
package autoincident

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// LockKey is the advisory-lock key that serializes automatic incident
// creation for one monitor. The trailing nil UUID is where a retired alert
// policy ID used to sit; it stays so the key is byte-identical to the one an
// older alerter computes, and the two still exclude each other during a
// rolling deploy. Do not "simplify" it away.
func LockKey(tenantID, monitorID uuid.UUID) string {
	return fmt.Sprintf("%s:%s:%s", tenantID, monitorID, uuid.Nil)
}

// LockTx takes the monitor's transaction-scoped lock.
func LockTx(ctx context.Context, tx *sql.Tx, tenantID, monitorID uuid.UUID) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, LockKey(tenantID, monitorID)); err != nil {
		return fmt.Errorf("lock auto incident key: %w", err)
	}
	return nil
}

// FindOrCreateTx returns the monitor's open automatic incident, creating it
// with title and summary when there is none. Call it under LockTx. created
// reports a new incident.
//
// An open automatic incident left over from the alert-policy era (its
// retired per-policy column is not named here) is reused rather than
// shadowed by a second one: it is the same monitor's incident.
func FindOrCreateTx(ctx context.Context, tx *sql.Tx, tenantID, monitorID uuid.UUID, title, summary string) (uuid.UUID, bool, error) {
	var incidentID uuid.UUID
	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM incidents
		WHERE tenant_id = $1
			AND is_auto_created = TRUE
			AND auto_monitor_id = $2
			AND state <> 'resolved'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
		FOR UPDATE
	`, tenantID, monitorID).Scan(&incidentID)
	if err == nil {
		return incidentID, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, fmt.Errorf("load auto incident: %w", err)
	}

	incidentID = uuid.New()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO incidents (
			id, tenant_id, title, summary, state, is_auto_created, auto_monitor_id, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'investigating', TRUE, $5, NOW(), NOW())
	`, incidentID, tenantID, title, summary, monitorID); err != nil {
		return uuid.Nil, false, fmt.Errorf("create auto incident: %w", err)
	}
	return incidentID, true, nil
}

// LinkAlertTx links an alert to the incident, reporting whether the link is
// new.
func LinkAlertTx(ctx context.Context, tx *sql.Tx, incidentID, alertID uuid.UUID) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO incident_alerts (incident_id, alert_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (incident_id, alert_id) DO NOTHING
	`, incidentID, alertID)
	if err != nil {
		return false, fmt.Errorf("attach incident alert: %w", err)
	}
	return changed(result), nil
}

// LinkMonitorTx links a monitor to the incident, reporting whether the link
// is new.
func LinkMonitorTx(ctx context.Context, tx *sql.Tx, incidentID, monitorID uuid.UUID) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO incident_monitors (incident_id, monitor_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (incident_id, monitor_id) DO NOTHING
	`, incidentID, monitorID)
	if err != nil {
		return false, fmt.Errorf("attach incident monitor: %w", err)
	}
	return changed(result), nil
}

func changed(result sql.Result) bool {
	if result == nil {
		return false
	}
	n, err := result.RowsAffected()
	return err == nil && n > 0
}

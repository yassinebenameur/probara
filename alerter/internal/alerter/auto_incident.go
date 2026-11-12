package alerter

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/incidentrecovery"
)

func (a *Alerter) ensureAutoIncidentForAlertTx(ctx context.Context, tx *sql.Tx, binding alertSubject, alert *alertRecord) error {
	if !binding.CreateIncidentOnFire {
		return nil
	}

	if err := a.lockAutoIncidentKeyTx(ctx, tx, binding.TenantID, binding.MonitorID); err != nil {
		return err
	}

	incidentID, created, err := a.findOrCreateAutoIncidentTx(ctx, tx, binding)
	if err != nil {
		return err
	}

	alertAttached, err := a.ensureIncidentAlertLinkTx(ctx, tx, incidentID, alert.ID)
	if err != nil {
		return err
	}
	monitorAttached, err := a.ensureIncidentMonitorLinkTx(ctx, tx, incidentID, binding.MonitorID)
	if err != nil {
		return err
	}

	if created || alertAttached || monitorAttached {
		message := "Alert auto-linked to existing incident"
		if created {
			message = "Incident auto-created from firing alert"
		}
		metadata := map[string]interface{}{
			"alert_id":   alert.ID.String(),
			"monitor_id": binding.MonitorID.String(),
		}
		if err := incidentrecovery.InsertTimelineEntryTx(ctx, tx, binding.TenantID, incidentID, incidentrecovery.SystemEntryType, message, metadata); err != nil {
			return err
		}
	}

	return nil
}

func (a *Alerter) findOrCreateAutoIncidentTx(ctx context.Context, tx *sql.Tx, binding alertSubject) (uuid.UUID, bool, error) {
	// One open auto incident per (tenant, monitor). The retired per-policy
	// column on incidents is no longer named here (its drop migration follows
	// one release later), so an open auto incident left over from the policy
	// era is reused rather than shadowed by a second one — acceptable: it is
	// the same monitor's incident.
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
	`, binding.TenantID, binding.MonitorID).Scan(&incidentID)
	if err == nil {
		return incidentID, false, nil
	}
	if err != sql.ErrNoRows {
		return uuid.Nil, false, fmt.Errorf("load auto incident: %w", err)
	}

	incidentID = uuid.New()
	title, summary := buildAlerterAutoIncidentNarrative(binding.MonitorName)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO incidents (
			id, tenant_id, title, summary, state, is_auto_created, auto_monitor_id, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'investigating', TRUE, $5, NOW(), NOW())
	`, incidentID, binding.TenantID, title, summary, binding.MonitorID); err != nil {
		return uuid.Nil, false, fmt.Errorf("create auto incident: %w", err)
	}

	return incidentID, true, nil
}

// autoIncidentLockKey is the advisory-lock key that serializes auto-incident
// creation for one monitor. The trailing nil UUID is where the retired alert
// policy id used to sit; it stays so the key is byte-identical to the one an
// older alerter replica computes, and the two still exclude each other during
// a rolling deploy. Do not "simplify" it away.
func autoIncidentLockKey(tenantID, monitorID uuid.UUID) string {
	return fmt.Sprintf("%s:%s:%s", tenantID, monitorID, uuid.Nil)
}

func (a *Alerter) lockAutoIncidentKeyTx(ctx context.Context, tx *sql.Tx, tenantID, monitorID uuid.UUID) error {
	lockKey := autoIncidentLockKey(tenantID, monitorID)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return fmt.Errorf("lock auto incident key: %w", err)
	}
	return nil
}

func (a *Alerter) ensureIncidentAlertLinkTx(ctx context.Context, tx *sql.Tx, incidentID, alertID uuid.UUID) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO incident_alerts (incident_id, alert_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (incident_id, alert_id) DO NOTHING
	`, incidentID, alertID)
	if err != nil {
		return false, fmt.Errorf("attach incident alert: %w", err)
	}
	return alerterMutationChanged(result), nil
}

func (a *Alerter) ensureIncidentMonitorLinkTx(ctx context.Context, tx *sql.Tx, incidentID, monitorID uuid.UUID) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO incident_monitors (incident_id, monitor_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (incident_id, monitor_id) DO NOTHING
	`, incidentID, monitorID)
	if err != nil {
		return false, fmt.Errorf("attach incident monitor: %w", err)
	}
	return alerterMutationChanged(result), nil
}

func buildAlerterAutoIncidentNarrative(monitorName string) (string, string) {
	monitorName = strings.TrimSpace(monitorName)

	title := "Auto-created incident"
	if monitorName != "" {
		title = fmt.Sprintf("%s incident", monitorName)
	}

	return title, "Automatically created from a firing alert."
}

func alerterMutationChanged(result sql.Result) bool {
	if result == nil {
		return false
	}

	rowsAffected, err := result.RowsAffected()
	return err == nil && rowsAffected > 0
}

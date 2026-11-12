// Attaching alerts and monitors to an incident, and reading them back.

package incidents

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/triageview"
)

// AttachAlert links an alert to an incident.
func (s *Service) AttachAlert(ctx context.Context, tenantID, incidentID, alertID uuid.UUID) (*models.IncidentDetail, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin incident transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := s.ensureIncidentBelongsToTenant(ctx, tx, tenantID, incidentID); err != nil {
		return nil, err
	}
	if err := s.ensureAlertBelongsToTenant(ctx, tx, tenantID, alertID); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO incident_alerts (incident_id, alert_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (incident_id, alert_id) DO NOTHING
	`, incidentID, alertID)
	if err != nil {
		return nil, fmt.Errorf("attach incident alert: %w", err)
	}
	if mutationChanged(result) {
		if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, "Alert attached", map[string]interface{}{
			"alert_id": alertID.String(),
		}); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit incident transaction: %w", err)
	}

	return s.GetIncident(ctx, tenantID, incidentID)
}

// DetachAlert removes an alert link from an incident.
func (s *Service) DetachAlert(ctx context.Context, tenantID, incidentID, alertID uuid.UUID) (*models.IncidentDetail, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin incident transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := s.ensureIncidentBelongsToTenant(ctx, tx, tenantID, incidentID); err != nil {
		return nil, err
	}
	if err := s.ensureAlertBelongsToTenant(ctx, tx, tenantID, alertID); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM incident_alerts
		WHERE incident_id = $1 AND alert_id = $2
	`, incidentID, alertID)
	if err != nil {
		return nil, fmt.Errorf("detach incident alert: %w", err)
	}
	if mutationChanged(result) {
		if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, "Alert detached", map[string]interface{}{
			"alert_id": alertID.String(),
		}); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit incident transaction: %w", err)
	}

	return s.GetIncident(ctx, tenantID, incidentID)
}

// AttachMonitor links a monitor to an incident.
func (s *Service) AttachMonitor(ctx context.Context, tenantID, incidentID, monitorID uuid.UUID) (*models.IncidentDetail, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin incident transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := s.ensureIncidentBelongsToTenant(ctx, tx, tenantID, incidentID); err != nil {
		return nil, err
	}
	if err := s.ensureMonitorBelongsToTenant(ctx, tx, tenantID, monitorID); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO incident_monitors (incident_id, monitor_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (incident_id, monitor_id) DO NOTHING
	`, incidentID, monitorID)
	if err != nil {
		return nil, fmt.Errorf("attach incident monitor: %w", err)
	}
	if mutationChanged(result) {
		if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, "Monitor attached", map[string]interface{}{
			"monitor_id": monitorID.String(),
		}); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit incident transaction: %w", err)
	}

	return s.GetIncident(ctx, tenantID, incidentID)
}

// DetachMonitor removes a monitor link from an incident.
func (s *Service) DetachMonitor(ctx context.Context, tenantID, incidentID, monitorID uuid.UUID) (*models.IncidentDetail, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin incident transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := s.ensureIncidentBelongsToTenant(ctx, tx, tenantID, incidentID); err != nil {
		return nil, err
	}
	if err := s.ensureMonitorBelongsToTenant(ctx, tx, tenantID, monitorID); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM incident_monitors
		WHERE incident_id = $1 AND monitor_id = $2
	`, incidentID, monitorID)
	if err != nil {
		return nil, fmt.Errorf("detach incident monitor: %w", err)
	}
	if mutationChanged(result) {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM incident_status_page_monitors
			WHERE incident_id = $1 AND monitor_id = $2
		`, incidentID, monitorID); err != nil {
			return nil, fmt.Errorf("delete incident publication monitor selections: %w", err)
		}
		if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, "Monitor detached", map[string]interface{}{
			"monitor_id": monitorID.String(),
		}); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit incident transaction: %w", err)
	}

	return s.GetIncident(ctx, tenantID, incidentID)
}

func (s *Service) loadIncidentAlerts(ctx context.Context, tenantID, incidentID uuid.UUID) ([]models.IncidentAlertSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.monitor_id, a.status, a.triggered_at,
			a.acknowledged_at, a.resolved_at, a.failure_count, a.last_error, a.created_at, a.updated_at,
			m.name,
			`+triageview.Columns+`
		FROM incident_alerts ia
		JOIN alerts a ON a.id = ia.alert_id
		JOIN monitors m ON m.id = a.monitor_id
		`+triageview.Join+`
		WHERE ia.incident_id = $1 AND a.tenant_id = $2 AND m.deleted_at IS NULL
		ORDER BY a.triggered_at DESC, a.id DESC
	`, incidentID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load incident alerts: %w", err)
	}
	defer rows.Close()

	alerts := make([]models.IncidentAlertSummary, 0)
	for rows.Next() {
		var item models.IncidentAlertSummary
		var acknowledgedAt sql.NullTime
		var resolvedAt sql.NullTime
		var lastError sql.NullString
		var triage triageview.Scan
		if err := rows.Scan(append([]any{
			&item.ID, &item.MonitorID, &item.Status, &item.TriggeredAt,
			&acknowledgedAt, &resolvedAt, &item.FailureCount, &lastError, &item.CreatedAt, &item.UpdatedAt,
			&item.MonitorName,
		}, triage.Dest()...)...); err != nil {
			return nil, fmt.Errorf("scan incident alert: %w", err)
		}
		if acknowledgedAt.Valid {
			value := acknowledgedAt.Time
			item.AcknowledgedAt = &value
		}
		if resolvedAt.Valid {
			value := resolvedAt.Time
			item.ResolvedAt = &value
		}
		if lastError.Valid {
			value := lastError.String
			item.LastError = &value
		}
		item.Triage = triage.Build()
		alerts = append(alerts, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident alerts: %w", err)
	}

	return alerts, nil
}

func (s *Service) loadIncidentMonitors(ctx context.Context, tenantID, incidentID uuid.UUID) ([]models.IncidentMonitorSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.tenant_id, m.name, m.type, m.created_at, m.updated_at
		FROM incident_monitors im
		JOIN monitors m ON m.id = im.monitor_id
		WHERE im.incident_id = $1 AND m.tenant_id = $2 AND m.deleted_at IS NULL
		ORDER BY m.name ASC, m.id ASC
	`, incidentID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load incident monitors: %w", err)
	}
	defer rows.Close()

	monitors := make([]models.IncidentMonitorSummary, 0)
	for rows.Next() {
		var item models.IncidentMonitorSummary
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.Name, &item.Type, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan incident monitor: %w", err)
		}
		monitors = append(monitors, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident monitors: %w", err)
	}

	return monitors, nil
}

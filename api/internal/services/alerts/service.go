package alerts

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
)

// Service handles alert business logic
type Service struct {
	db *db.Client
}

// NewService creates a new alert service
func NewService(db *db.Client) *Service {
	return &Service{db: db}
}

// ListAlerts lists alerts with filtering and pagination
func (s *Service) ListAlerts(ctx context.Context, tenantID uuid.UUID, params *models.AlertListParams) (*models.AlertListResponse, error) {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 {
		params.PageSize = 20
	}
	if params.PageSize > 100 {
		params.PageSize = 100
	}

	offset := (params.Page - 1) * params.PageSize

	// Build WHERE clause
	whereParts := []string{"a.tenant_id = $1"}
	args := []interface{}{tenantID}
	argIndex := 2

	if params.Status != nil {
		whereParts = append(whereParts, fmt.Sprintf("a.status = $%d", argIndex))
		args = append(args, *params.Status)
		argIndex++
	}

	if params.MonitorID != nil {
		whereParts = append(whereParts, fmt.Sprintf("a.monitor_id = $%d", argIndex))
		args = append(args, *params.MonitorID)
		argIndex++
	}

	if params.Since != nil {
		whereParts = append(whereParts, fmt.Sprintf("a.triggered_at >= $%d", argIndex))
		args = append(args, *params.Since)
		argIndex++
	}

	whereClause := strings.Join(whereParts, " AND ")

	// Count total
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM alerts a WHERE %s`, whereClause)
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count alerts: %w", err)
	}

	// Get alerts with details
	query := fmt.Sprintf(`
		SELECT a.id, a.tenant_id, a.monitor_id, a.alert_policy_id, a.status,
			a.triggered_at, a.acknowledged_at, a.resolved_at, a.failure_count,
			a.last_error, a.created_at, a.updated_at,
			m.name as monitor_name, ap.name as policy_name
		FROM alerts a
		JOIN monitors m ON a.monitor_id = m.id
		JOIN alert_policies ap ON a.alert_policy_id = ap.id
		WHERE %s
		ORDER BY a.triggered_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIndex, argIndex+1)

	args = append(args, params.PageSize, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list alerts: %w", err)
	}
	defer rows.Close()

	var alerts []models.AlertWithDetails
	for rows.Next() {
		var alert models.AlertWithDetails
		err := rows.Scan(
			&alert.ID, &alert.TenantID, &alert.MonitorID, &alert.AlertPolicyID,
			&alert.Status, &alert.TriggeredAt, &alert.AcknowledgedAt, &alert.ResolvedAt,
			&alert.FailureCount, &alert.LastError, &alert.CreatedAt, &alert.UpdatedAt,
			&alert.MonitorName, &alert.PolicyName,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan alert: %w", err)
		}
		alerts = append(alerts, alert)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating alerts: %w", err)
	}

	return &models.AlertListResponse{
		Items:    alerts,
		Page:     params.Page,
		PageSize: params.PageSize,
		Total:    total,
	}, nil
}

// GetAlert retrieves an alert by ID
func (s *Service) GetAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
	query := `
		SELECT a.id, a.tenant_id, a.monitor_id, a.alert_policy_id, a.status,
			a.triggered_at, a.acknowledged_at, a.resolved_at, a.failure_count,
			a.last_error, a.created_at, a.updated_at,
			m.name as monitor_name, ap.name as policy_name
		FROM alerts a
		JOIN monitors m ON a.monitor_id = m.id
		JOIN alert_policies ap ON a.alert_policy_id = ap.id
		WHERE a.id = $1 AND a.tenant_id = $2
	`

	var alert models.AlertWithDetails
	err := s.db.QueryRowContext(ctx, query, alertID, tenantID).Scan(
		&alert.ID, &alert.TenantID, &alert.MonitorID, &alert.AlertPolicyID,
		&alert.Status, &alert.TriggeredAt, &alert.AcknowledgedAt, &alert.ResolvedAt,
		&alert.FailureCount, &alert.LastError, &alert.CreatedAt, &alert.UpdatedAt,
		&alert.MonitorName, &alert.PolicyName,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("alert not found")
		}
		return nil, fmt.Errorf("failed to get alert: %w", err)
	}

	return &alert, nil
}

// GetRecentAlerts retrieves the most recent alerts for dashboard
func (s *Service) GetRecentAlerts(ctx context.Context, tenantID uuid.UUID, limit int) ([]models.AlertWithDetails, error) {
	if limit < 1 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	query := `
		SELECT a.id, a.tenant_id, a.monitor_id, a.alert_policy_id, a.status,
			a.triggered_at, a.acknowledged_at, a.resolved_at, a.failure_count,
			a.last_error, a.created_at, a.updated_at,
			m.name as monitor_name, ap.name as policy_name
		FROM alerts a
		JOIN monitors m ON a.monitor_id = m.id
		JOIN alert_policies ap ON a.alert_policy_id = ap.id
		WHERE a.tenant_id = $1
		ORDER BY a.triggered_at DESC
		LIMIT $2
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent alerts: %w", err)
	}
	defer rows.Close()

	var alerts []models.AlertWithDetails
	for rows.Next() {
		var alert models.AlertWithDetails
		err := rows.Scan(
			&alert.ID, &alert.TenantID, &alert.MonitorID, &alert.AlertPolicyID,
			&alert.Status, &alert.TriggeredAt, &alert.AcknowledgedAt, &alert.ResolvedAt,
			&alert.FailureCount, &alert.LastError, &alert.CreatedAt, &alert.UpdatedAt,
			&alert.MonitorName, &alert.PolicyName,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan alert: %w", err)
		}
		alerts = append(alerts, alert)
	}

	return alerts, nil
}

// AcknowledgeAlert marks an alert as acknowledged
func (s *Service) AcknowledgeAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
	now := time.Now()
	query := `
		UPDATE alerts
		SET status = 'acknowledged', acknowledged_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND status = 'active'
		RETURNING id
	`

	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, query, now, alertID, tenantID).Scan(&id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("alert not found or not active")
		}
		return nil, fmt.Errorf("failed to acknowledge alert: %w", err)
	}

	return s.GetAlert(ctx, tenantID, alertID)
}

// ResolveAlert marks an alert as resolved
func (s *Service) ResolveAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
	now := time.Now()
	query := `
		UPDATE alerts
		SET status = 'resolved', resolved_at = $1, updated_at = $1
		WHERE id = $2 AND tenant_id = $3 AND status IN ('active', 'acknowledged')
		RETURNING id
	`

	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, query, now, alertID, tenantID).Scan(&id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("alert not found or already resolved")
		}
		return nil, fmt.Errorf("failed to resolve alert: %w", err)
	}

	return s.GetAlert(ctx, tenantID, alertID)
}

// CreateAlert creates a new alert
func (s *Service) CreateAlert(ctx context.Context, tenantID, monitorID, policyID uuid.UUID, failureCount int, lastError *string) (*models.AlertWithDetails, error) {
	alertID := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO alerts (
			id, tenant_id, monitor_id, alert_policy_id, status,
			triggered_at, failure_count, last_error, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'active', $5, $6, $7, $5, $5)
		RETURNING id
	`

	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, query,
		alertID, tenantID, monitorID, policyID, now, failureCount, lastError,
	).Scan(&id)

	if err != nil {
		return nil, fmt.Errorf("failed to create alert: %w", err)
	}

	return s.GetAlert(ctx, tenantID, alertID)
}

// GetActiveAlertForMonitor gets the active alert for a monitor if one exists
func (s *Service) GetActiveAlertForMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Alert, error) {
	query := `
		SELECT id, tenant_id, monitor_id, alert_policy_id, status,
			triggered_at, acknowledged_at, resolved_at, failure_count,
			last_error, created_at, updated_at
		FROM alerts
		WHERE tenant_id = $1 AND monitor_id = $2 AND status IN ('active', 'acknowledged')
		ORDER BY triggered_at DESC
		LIMIT 1
	`

	var alert models.Alert
	err := s.db.QueryRowContext(ctx, query, tenantID, monitorID).Scan(
		&alert.ID, &alert.TenantID, &alert.MonitorID, &alert.AlertPolicyID,
		&alert.Status, &alert.TriggeredAt, &alert.AcknowledgedAt, &alert.ResolvedAt,
		&alert.FailureCount, &alert.LastError, &alert.CreatedAt, &alert.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No active alert
		}
		return nil, fmt.Errorf("failed to get active alert: %w", err)
	}

	return &alert, nil
}

// GetAlertCountsByPolicy gets active alert counts grouped by policy
func (s *Service) GetAlertCountsByPolicy(ctx context.Context, tenantID uuid.UUID) (map[uuid.UUID]int, error) {
	query := `
		SELECT alert_policy_id, COUNT(*) as count
		FROM alerts
		WHERE tenant_id = $1 AND status = 'active'
		GROUP BY alert_policy_id
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get alert counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[uuid.UUID]int)
	for rows.Next() {
		var policyID uuid.UUID
		var count int
		if err := rows.Scan(&policyID, &count); err != nil {
			return nil, fmt.Errorf("failed to scan count: %w", err)
		}
		counts[policyID] = count
	}

	return counts, nil
}

// GetMonitorCountsByPolicy gets monitor counts grouped by policy
func (s *Service) GetMonitorCountsByPolicy(ctx context.Context, tenantID uuid.UUID) (map[uuid.UUID]int, error) {
	query := `
		SELECT alert_policy_id, COUNT(*) as count
		FROM monitors
		WHERE tenant_id = $1 AND alert_policy_id IS NOT NULL
		GROUP BY alert_policy_id
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get monitor counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[uuid.UUID]int)
	for rows.Next() {
		var policyID uuid.UUID
		var count int
		if err := rows.Scan(&policyID, &count); err != nil {
			return nil, fmt.Errorf("failed to scan count: %w", err)
		}
		counts[policyID] = count
	}

	return counts, nil
}

// GetAlertsByPolicy gets alerts for a specific policy
func (s *Service) GetAlertsByPolicy(ctx context.Context, tenantID, policyID uuid.UUID, limit int) ([]models.AlertWithDetails, error) {
	if limit < 1 {
		limit = 10
	}

	query := `
		SELECT a.id, a.tenant_id, a.monitor_id, a.alert_policy_id, a.status,
			a.triggered_at, a.acknowledged_at, a.resolved_at, a.failure_count,
			a.last_error, a.created_at, a.updated_at,
			m.name as monitor_name, ap.name as policy_name
		FROM alerts a
		JOIN monitors m ON a.monitor_id = m.id
		JOIN alert_policies ap ON a.alert_policy_id = ap.id
		WHERE a.tenant_id = $1 AND a.alert_policy_id = $2
		ORDER BY a.triggered_at DESC
		LIMIT $3
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID, policyID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get alerts by policy: %w", err)
	}
	defer rows.Close()

	var alerts []models.AlertWithDetails
	for rows.Next() {
		var alert models.AlertWithDetails
		err := rows.Scan(
			&alert.ID, &alert.TenantID, &alert.MonitorID, &alert.AlertPolicyID,
			&alert.Status, &alert.TriggeredAt, &alert.AcknowledgedAt, &alert.ResolvedAt,
			&alert.FailureCount, &alert.LastError, &alert.CreatedAt, &alert.UpdatedAt,
			&alert.MonitorName, &alert.PolicyName,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan alert: %w", err)
		}
		alerts = append(alerts, alert)
	}

	return alerts, nil
}

// GetMonitorsByPolicy gets monitors using a specific policy
func (s *Service) GetMonitorsByPolicy(ctx context.Context, tenantID, policyID uuid.UUID) ([]models.Monitor, error) {
	query := `
		SELECT id, tenant_id, name, type, config, interval_seconds, timeout_seconds,
			alert_policy_id, enabled, tags, next_run_at, created_at, updated_at
		FROM monitors
		WHERE tenant_id = $1 AND alert_policy_id = $2
		ORDER BY name
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID, policyID)
	if err != nil {
		return nil, fmt.Errorf("failed to get monitors by policy: %w", err)
	}
	defer rows.Close()

	var monitors []models.Monitor
	for rows.Next() {
		var m models.Monitor
		var tags sql.NullString
		err := rows.Scan(
			&m.ID, &m.TenantID, &m.Name, &m.Type, &m.Config,
			&m.IntervalSeconds, &m.TimeoutSeconds, &m.AlertPolicyID,
			&m.Enabled, &tags, &m.NextRunAt, &m.CreatedAt, &m.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan monitor: %w", err)
		}
		// Parse tags if present
		if tags.Valid && tags.String != "" {
			// Tags stored as comma-separated or JSON array
			m.Tags = strings.Split(strings.Trim(tags.String, "{}"), ",")
		}
		monitors = append(monitors, m)
	}

	return monitors, nil
}

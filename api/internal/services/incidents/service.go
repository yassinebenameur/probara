package incidents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/pagination"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/db"
)

// ErrIncidentNotFound: the incident does not exist in the tenant.
var ErrIncidentNotFound = errors.New("incident not found")

type incidentExecutor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// Service handles incident business logic.
type Service struct {
	db              *db.Client
	statusPublisher statusUpdatePublisher
}

// NewService creates a new incident service.
func NewService(dbClient *db.Client, statusPublisher statusUpdatePublisher) *Service {
	return &Service{db: dbClient, statusPublisher: statusPublisher}
}

// CreateIncident creates a new manual incident.
func (s *Service) CreateIncident(ctx context.Context, tenantID uuid.UUID, req *models.CreateIncidentRequest) (*models.IncidentDetail, error) {
	if err := validation.ValidateCreateIncident(req); err != nil {
		return nil, err
	}

	title := strings.TrimSpace(req.Title)
	summary := strings.TrimSpace(req.Summary)
	severity := models.IncidentSeverityHigh
	if req.Severity != "" {
		severity = req.Severity
	}
	var ownerUserID *uuid.UUID
	if req.OwnerUserID != "" {
		parsedOwnerUserID, err := uuid.Parse(req.OwnerUserID)
		if err != nil {
			return nil, fmt.Errorf("invalid owner user id")
		}
		ownerUserID = &parsedOwnerUserID
	}
	var alertID *uuid.UUID
	if req.AlertID != "" {
		parsedAlertID, err := uuid.Parse(req.AlertID)
		if err != nil {
			return nil, fmt.Errorf("invalid alert id")
		}
		alertID = &parsedAlertID
	}
	var monitorID *uuid.UUID
	if req.MonitorID != "" {
		parsedMonitorID, err := uuid.Parse(req.MonitorID)
		if err != nil {
			return nil, fmt.Errorf("invalid monitor id")
		}
		monitorID = &parsedMonitorID
	}
	incidentID := uuid.New()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin incident transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO incidents (
			id, tenant_id, title, summary, severity, owner_user_id, state, is_auto_created, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'investigating', FALSE, NOW(), NOW())
	`, incidentID, tenantID, title, summary, severity, ownerUserID); err != nil {
		return nil, fmt.Errorf("create incident: %w", err)
	}

	if alertID != nil {
		if err := s.ensureAlertBelongsToTenant(ctx, tx, tenantID, *alertID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO incident_alerts (incident_id, alert_id, created_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (incident_id, alert_id) DO NOTHING
		`, incidentID, *alertID); err != nil {
			return nil, fmt.Errorf("attach incident alert: %w", err)
		}
	}

	if monitorID != nil {
		if err := s.ensureMonitorBelongsToTenant(ctx, tx, tenantID, *monitorID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO incident_monitors (incident_id, monitor_id, created_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (incident_id, monitor_id) DO NOTHING
		`, incidentID, *monitorID); err != nil {
			return nil, fmt.Errorf("attach incident monitor: %w", err)
		}
	}

	if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, "Incident created", nil); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit incident transaction: %w", err)
	}

	return s.GetIncident(ctx, tenantID, incidentID)
}

// GetIncident retrieves an incident by ID.
func (s *Service) GetIncident(ctx context.Context, tenantID, incidentID uuid.UUID) (*models.IncidentDetail, error) {
	query := `
		SELECT i.id, i.tenant_id, i.title, i.summary, i.state, i.severity, i.owner_user_id, au.username,
			i.resolved_at, i.is_auto_created, i.auto_monitor_id, i.created_at, i.updated_at
		FROM incidents i
		LEFT JOIN admin_users au ON au.id = i.owner_user_id
		WHERE i.id = $1 AND i.tenant_id = $2
	`

	var detail models.IncidentDetail
	var summary sql.NullString
	var severity string
	var ownerUserID uuid.NullUUID
	var ownerUsername sql.NullString
	var resolvedAt sql.NullTime
	var autoMonitorID uuid.NullUUID
	if err := s.db.QueryRowContext(ctx, query, incidentID, tenantID).Scan(
		&detail.ID, &detail.TenantID, &detail.Title, &summary, &detail.State, &severity, &ownerUserID, &ownerUsername,
		&resolvedAt, &detail.IsAutoCreated, &autoMonitorID, &detail.CreatedAt, &detail.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrIncidentNotFound
		}
		return nil, fmt.Errorf("get incident: %w", err)
	}

	if summary.Valid {
		detail.Summary = summary.String
	}
	detail.Severity = models.IncidentSeverity(severity)
	if ownerUserID.Valid {
		ownerID := ownerUserID.UUID
		detail.OwnerUserID = &ownerID
	}
	if ownerUsername.Valid {
		detail.OwnerUsername = ownerUsername.String
	}
	if resolvedAt.Valid {
		resolvedAtValue := resolvedAt.Time
		detail.ResolvedAt = &resolvedAtValue
	}
	if autoMonitorID.Valid {
		monitorID := autoMonitorID.UUID
		detail.AutoMonitorID = &monitorID
	}

	timeline, err := s.loadIncidentTimeline(ctx, tenantID, incidentID)
	if err != nil {
		return nil, err
	}
	alerts, err := s.loadIncidentAlerts(ctx, tenantID, incidentID)
	if err != nil {
		return nil, err
	}
	monitors, err := s.loadIncidentMonitors(ctx, tenantID, incidentID)
	if err != nil {
		return nil, err
	}
	publications, err := s.loadIncidentPublications(ctx, tenantID, incidentID)
	if err != nil {
		return nil, err
	}

	analysis, err := s.loadLatestAIAnalysis(ctx, tenantID, incidentID)
	if err != nil {
		return nil, err
	}

	detail.Alerts = alerts
	detail.Monitors = monitors
	detail.Publications = publications
	detail.Timeline = timeline
	detail.AIAnalysis = analysis

	return &detail, nil
}

// ListIncidents lists incidents with pagination.
func (s *Service) ListIncidents(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.IncidentListResponse, error) {
	page, pageSize = pagination.Normalize(page, pageSize, pagination.DefaultSize, pagination.MaxSize)

	offset := pagination.Offset(page, pageSize)

	var total, openTotal int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE state <> 'resolved')
		FROM incidents WHERE tenant_id = $1
	`, tenantID).Scan(&total, &openTotal); err != nil {
		return nil, fmt.Errorf("count incidents: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		WITH alert_counts AS (
			SELECT incident_id, COUNT(*)::int AS linked_alert_count
			FROM incident_alerts
			GROUP BY incident_id
		),
		monitor_counts AS (
			SELECT incident_id, COUNT(*)::int AS linked_monitor_count
			FROM incident_monitors
			GROUP BY incident_id
		),
		publication_counts AS (
			SELECT incident_id, COUNT(*)::int AS publication_count
			FROM incident_status_page_publications
			WHERE tenant_id = $1 AND unpublished_at IS NULL
			GROUP BY incident_id
		)
		SELECT i.id, i.state,
			CASE WHEN i.is_auto_created THEN 'auto' ELSE 'manual' END AS source,
			i.title, i.severity, i.owner_user_id, au.username, i.updated_at, i.resolved_at,
			COALESCE(ac.linked_alert_count, 0),
			COALESCE(mc.linked_monitor_count, 0),
			COALESCE(pc.publication_count, 0)
		FROM incidents i
		LEFT JOIN alert_counts ac ON ac.incident_id = i.id
		LEFT JOIN monitor_counts mc ON mc.incident_id = i.id
		LEFT JOIN publication_counts pc ON pc.incident_id = i.id
		LEFT JOIN admin_users au ON au.id = i.owner_user_id
		WHERE i.tenant_id = $1
		ORDER BY i.updated_at DESC, i.id DESC
		LIMIT $2 OFFSET $3
	`, tenantID, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()

	var items []models.IncidentListItem
	for rows.Next() {
		incident, err := scanIncidentListItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scan incident: %w", err)
		}
		items = append(items, incident)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incidents: %w", err)
	}

	return &models.IncidentListResponse{
		Items:     items,
		Page:      page,
		PageSize:  pageSize,
		Total:     total,
		OpenTotal: openTotal,
	}, nil
}

// UpdateIncident updates incident title and summary.
func (s *Service) UpdateIncident(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.UpdateIncidentRequest) (*models.IncidentDetail, error) {
	if req == nil {
		return s.GetIncident(ctx, tenantID, incidentID)
	}
	if err := validation.ValidateUpdateIncident(req); err != nil {
		return nil, err
	}

	setParts := make([]string, 0, 2)
	args := make([]interface{}, 0, 3)
	argIndex := 1

	if req.Title != nil {
		setParts = append(setParts, fmt.Sprintf("title = $%d", argIndex))
		args = append(args, strings.TrimSpace(*req.Title))
		argIndex++
	}
	if req.Summary != nil {
		setParts = append(setParts, fmt.Sprintf("summary = $%d", argIndex))
		args = append(args, strings.TrimSpace(*req.Summary))
		argIndex++
	}
	if req.Severity != nil {
		setParts = append(setParts, fmt.Sprintf("severity = $%d", argIndex))
		args = append(args, *req.Severity)
		argIndex++
	}
	if req.OwnerUserID != nil {
		if *req.OwnerUserID == "" {
			setParts = append(setParts, "owner_user_id = NULL")
		} else {
			ownerUserID, err := uuid.Parse(*req.OwnerUserID)
			if err != nil {
				return nil, fmt.Errorf("invalid owner user id")
			}
			setParts = append(setParts, fmt.Sprintf("owner_user_id = $%d", argIndex))
			args = append(args, ownerUserID)
			argIndex++
		}
	}

	if len(setParts) == 0 {
		return s.GetIncident(ctx, tenantID, incidentID)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin incident transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	setParts = append(setParts, "updated_at = NOW()")
	args = append(args, incidentID, tenantID)

	query := fmt.Sprintf(`
		UPDATE incidents
		SET %s
		WHERE id = $%d AND tenant_id = $%d
	`, strings.Join(setParts, ", "), argIndex, argIndex+1)

	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("update incident: %w", err)
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return nil, ErrIncidentNotFound
	}

	if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, "Incident updated", nil); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit incident transaction: %w", err)
	}

	return s.GetIncident(ctx, tenantID, incidentID)
}

// TransitionIncidentState moves an incident to another state.
func (s *Service) TransitionIncidentState(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.TransitionIncidentStateRequest) (*models.IncidentDetail, error) {
	if err := validation.ValidateIncidentStateTransition(req); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin incident transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var currentState models.IncidentState
	if err := tx.QueryRowContext(ctx, `
		SELECT state
		FROM incidents
		WHERE id = $1 AND tenant_id = $2
		FOR UPDATE
	`, incidentID, tenantID).Scan(&currentState); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrIncidentNotFound
		}
		return nil, fmt.Errorf("load incident state: %w", err)
	}

	if currentState == req.State {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit incident transaction: %w", err)
		}
		return s.GetIncident(ctx, tenantID, incidentID)
	}
	if currentState == models.IncidentStateResolved && req.State != models.IncidentStateResolved {
		return nil, fmt.Errorf("resolved incidents cannot be reopened")
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE incidents
		SET state = $1,
			resolved_at = CASE WHEN $1::incident_state = 'resolved' THEN NOW() ELSE NULL END,
			updated_at = NOW()
		WHERE id = $2 AND tenant_id = $3
	`, req.State, incidentID, tenantID); err != nil {
		return nil, fmt.Errorf("transition incident state: %w", err)
	}

	message := fmt.Sprintf("Incident moved to %s", req.State)
	if req.State == models.IncidentStateResolved {
		message = "Incident resolved"
	}
	if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, message, nil); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit incident transaction: %w", err)
	}

	return s.GetIncident(ctx, tenantID, incidentID)
}

func mutationChanged(result sql.Result) bool {
	if result == nil {
		return false
	}

	rowsAffected, err := result.RowsAffected()
	return err == nil && rowsAffected > 0
}

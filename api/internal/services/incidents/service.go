package incidents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

type incidentRowScanner interface {
	Scan(dest ...interface{}) error
}

type incidentExecutor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

type statusUpdatePublisher interface {
	Publish(event statusupdates.Event) error
}

const incidentPublicationUpdatedEventType = "incident.publication.updated"
const incidentStatusPageRefreshLookupTimeout = 5 * time.Second

const (
	autoIncidentCreatedMessage = "Incident auto-created from firing alert"
	autoIncidentLinkedMessage  = "Alert auto-linked to existing incident"
	alertsRecoveredMessage     = "All linked alerts recovered"
)

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
			i.resolved_at, i.is_auto_created, i.auto_monitor_id, i.auto_alert_policy_id, i.created_at, i.updated_at
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
	var autoAlertPolicyID uuid.NullUUID
	if err := s.db.QueryRowContext(ctx, query, incidentID, tenantID).Scan(
		&detail.ID, &detail.TenantID, &detail.Title, &summary, &detail.State, &severity, &ownerUserID, &ownerUsername,
		&resolvedAt, &detail.IsAutoCreated, &autoMonitorID, &autoAlertPolicyID, &detail.CreatedAt, &detail.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("incident not found")
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
	if autoAlertPolicyID.Valid {
		policyID := autoAlertPolicyID.UUID
		detail.AutoAlertPolicyID = &policyID
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

	detail.Alerts = alerts
	detail.Monitors = monitors
	detail.Publications = publications
	detail.Timeline = timeline

	return &detail, nil
}

// ListIncidents lists incidents with pagination.
func (s *Service) ListIncidents(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.IncidentListResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := (page - 1) * pageSize

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE tenant_id = $1`, tenantID).Scan(&total); err != nil {
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
		Items:    items,
		Page:     page,
		PageSize: pageSize,
		Total:    total,
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
		return nil, fmt.Errorf("incident not found")
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
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("incident not found")
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

// CreateIncidentTimelineEntry appends a timeline entry to an incident.
func (s *Service) CreateIncidentTimelineEntry(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.CreateIncidentTimelineEntryRequest) (*models.IncidentDetail, error) {
	if err := validation.ValidateCreateIncidentTimelineEntry(req); err != nil {
		return nil, err
	}

	if _, err := s.GetIncident(ctx, tenantID, incidentID); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin incident transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, req.EntryType, strings.TrimSpace(req.Message), req.Metadata); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit incident transaction: %w", err)
	}

	if req.EntryType == models.IncidentTimelineEntryTypePublicUpdate {
		s.publishIncidentStatusPageRefreshes(ctx, tenantID, incidentID)
	}

	return s.GetIncident(ctx, tenantID, incidentID)
}

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

// PublishIncidentToStatusPage publishes an incident to a status page.
func (s *Service) PublishIncidentToStatusPage(ctx context.Context, tenantID, incidentID, statusPageID uuid.UUID, req *models.UpsertIncidentPublicationRequest) (*models.IncidentDetail, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}

	monitorIDs, err := parseIncidentPublicationMonitorIDs(req.MonitorIDs)
	if err != nil {
		return nil, err
	}

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
	if err := s.ensureStatusPageBelongsToTenant(ctx, tx, tenantID, statusPageID); err != nil {
		return nil, err
	}
	if err := s.validatePublicationMonitorSelection(ctx, tx, tenantID, incidentID, statusPageID, monitorIDs); err != nil {
		return nil, err
	}
	if isNoOp, err := s.isNoOpIncidentPublicationUpdate(ctx, tx, tenantID, incidentID, statusPageID, monitorIDs); err != nil {
		return nil, err
	} else if isNoOp {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit incident transaction: %w", err)
		}
		return s.GetIncident(ctx, tenantID, incidentID)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO incident_status_page_publications (
			incident_id, status_page_id, tenant_id, published_at, unpublished_at, created_at, updated_at
		) VALUES ($1, $2, $3, NOW(), NULL, NOW(), NOW())
		ON CONFLICT (incident_id, status_page_id) DO UPDATE
		SET tenant_id = EXCLUDED.tenant_id,
			published_at = NOW(),
			unpublished_at = NULL,
			updated_at = NOW()
	`, incidentID, statusPageID, tenantID); err != nil {
		return nil, fmt.Errorf("upsert incident publication: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM incident_status_page_monitors
		WHERE incident_id = $1 AND status_page_id = $2
	`, incidentID, statusPageID); err != nil {
		return nil, fmt.Errorf("replace incident publication monitors: %w", err)
	}

	for _, monitorID := range monitorIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO incident_status_page_monitors (incident_id, status_page_id, monitor_id, created_at)
			VALUES ($1, $2, $3, NOW())
		`, incidentID, statusPageID, monitorID); err != nil {
			return nil, fmt.Errorf("insert incident publication monitor: %w", err)
		}
	}

	if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, "Incident published to status page", map[string]interface{}{
		"status_page_id": statusPageID.String(),
		"monitor_ids":    uuidStrings(monitorIDs),
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit incident transaction: %w", err)
	}

	s.publishStatusPageRefresh(tenantID, statusPageID)

	return s.GetIncident(ctx, tenantID, incidentID)
}

// UnpublishIncidentFromStatusPage removes an incident publication from a status page.
func (s *Service) UnpublishIncidentFromStatusPage(ctx context.Context, tenantID, incidentID, statusPageID uuid.UUID) (*models.IncidentDetail, error) {
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
	if err := s.ensureStatusPageBelongsToTenant(ctx, tx, tenantID, statusPageID); err != nil {
		return nil, err
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE incident_status_page_publications
		SET unpublished_at = NOW(), updated_at = NOW()
		WHERE incident_id = $1 AND status_page_id = $2 AND tenant_id = $3 AND unpublished_at IS NULL
	`, incidentID, statusPageID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("unpublish incident from status page: %w", err)
	}
	changed := mutationChanged(result)
	if changed {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM incident_status_page_monitors
			WHERE incident_id = $1 AND status_page_id = $2
		`, incidentID, statusPageID); err != nil {
			return nil, fmt.Errorf("delete incident publication monitors: %w", err)
		}
		if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, "Incident unpublished from status page", map[string]interface{}{
			"status_page_id": statusPageID.String(),
		}); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit incident transaction: %w", err)
	}

	if changed {
		s.publishStatusPageRefresh(tenantID, statusPageID)
	}

	return s.GetIncident(ctx, tenantID, incidentID)
}

// EnsureIncidentForAlert auto-creates or reuses an incident for a firing alert when policy settings allow it.
func (s *Service) EnsureIncidentForAlert(ctx context.Context, tenantID uuid.UUID, alert *models.AlertWithDetails) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin incident transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := s.EnsureIncidentForAlertTx(ctx, tx, tenantID, alert); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit incident transaction: %w", err)
	}

	return nil
}

// EnsureIncidentForAlertTx auto-creates or reuses an incident for a firing alert inside an existing transaction.
func (s *Service) EnsureIncidentForAlertTx(ctx context.Context, tx *sql.Tx, tenantID uuid.UUID, alert *models.AlertWithDetails) error {
	if alert == nil {
		return fmt.Errorf("alert is required")
	}
	if tenantID == uuid.Nil {
		return fmt.Errorf("tenant id is required")
	}
	if alert.TenantID == uuid.Nil || alert.ID == uuid.Nil || alert.MonitorID == uuid.Nil || alert.AlertPolicyID == uuid.Nil {
		return fmt.Errorf("alert automation requires non-nil tenant, alert, monitor, and policy ids")
	}
	if alert.TenantID != tenantID {
		return fmt.Errorf("alert tenant mismatch")
	}

	enabled, err := s.loadCreateIncidentOnFireFlagTx(ctx, tx, tenantID, alert.AlertPolicyID)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}

	if err := s.lockAutoIncidentKeyTx(ctx, tx, tenantID, alert.MonitorID, alert.AlertPolicyID); err != nil {
		return err
	}

	incidentID, created, err := s.findOrCreateAutoIncidentTx(ctx, tx, tenantID, alert)
	if err != nil {
		return err
	}

	alertAttached, err := s.ensureIncidentAlertLinkTx(ctx, tx, incidentID, alert.ID)
	if err != nil {
		return err
	}
	monitorAttached, err := s.ensureIncidentMonitorLinkTx(ctx, tx, incidentID, alert.MonitorID)
	if err != nil {
		return err
	}

	if created || alertAttached || monitorAttached {
		message := autoIncidentLinkedMessage
		if created {
			message = autoIncidentCreatedMessage
		}
		if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, message, map[string]interface{}{
			"alert_id":        alert.ID.String(),
			"monitor_id":      alert.MonitorID.String(),
			"alert_policy_id": alert.AlertPolicyID.String(),
		}); err != nil {
			return err
		}
	}

	return nil
}

// RecordAlertRecoveryIfNeeded appends a timeline entry when all alerts linked to an incident are resolved.
func (s *Service) RecordAlertRecoveryIfNeeded(ctx context.Context, tenantID, alertID uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin incident transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := s.RecordAlertRecoveryIfNeededTx(ctx, tx, tenantID, alertID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit incident transaction: %w", err)
	}

	return nil
}

// RecordAlertRecoveryIfNeededTx appends a timeline entry when all linked alerts are resolved inside an existing transaction.
func (s *Service) RecordAlertRecoveryIfNeededTx(ctx context.Context, tx *sql.Tx, tenantID, alertID uuid.UUID) error {
	incidentIDs, err := s.loadIncidentIDsForAlertTx(ctx, tx, tenantID, alertID)
	if err != nil {
		return err
	}
	sort.Slice(incidentIDs, func(i, j int) bool {
		return incidentIDs[i].String() < incidentIDs[j].String()
	})
	for _, incidentID := range incidentIDs {
		if err := s.lockIncidentRecoveryKeyTx(ctx, tx, incidentID); err != nil {
			return err
		}
		allResolved, resolvedAfter, err := s.loadLinkedAlertRecoveryStateTx(ctx, tx, incidentID)
		if err != nil {
			return err
		}
		if !allResolved {
			continue
		}
		resolvedAfterMarker := recoveryResolutionMarker(resolvedAfter)
		alreadyRecorded, err := s.recoveryTimelineExistsTx(ctx, tx, incidentID, models.IncidentTimelineEntryTypeSystem, alertsRecoveredMessage, resolvedAfterMarker)
		if err != nil {
			return err
		}
		if alreadyRecorded {
			continue
		}
		if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, alertsRecoveredMessage, map[string]interface{}{
			"alert_id":       alertID.String(),
			"resolved_after": resolvedAfterMarker,
		}); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) publishStatusPageRefresh(tenantID, statusPageID uuid.UUID) {
	if s.statusPublisher == nil || tenantID == uuid.Nil || statusPageID == uuid.Nil {
		return
	}

	_ = s.statusPublisher.Publish(statusupdates.Event{
		Type:         incidentPublicationUpdatedEventType,
		StatusPageID: statusPageID.String(),
		TenantID:     tenantID.String(),
		Timestamp:    time.Now().UTC(),
	})
}

func (s *Service) publishIncidentStatusPageRefreshes(_ context.Context, tenantID, incidentID uuid.UUID) {
	if s.statusPublisher == nil {
		return
	}

	refreshCtx, cancel := context.WithTimeout(context.Background(), incidentStatusPageRefreshLookupTimeout)
	defer cancel()

	statusPageIDs, err := s.loadActiveStatusPageIDsForIncident(refreshCtx, tenantID, incidentID)
	if err != nil {
		return
	}
	for _, statusPageID := range statusPageIDs {
		s.publishStatusPageRefresh(tenantID, statusPageID)
	}
}

func (s *Service) loadActiveStatusPageIDsForIncident(ctx context.Context, tenantID, incidentID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT status_page_id
		FROM incident_status_page_publications
		WHERE tenant_id = $1 AND incident_id = $2 AND unpublished_at IS NULL
	`, tenantID, incidentID)
	if err != nil {
		return nil, fmt.Errorf("load incident status page publications: %w", err)
	}
	defer rows.Close()

	statusPageIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var statusPageID uuid.UUID
		if err := rows.Scan(&statusPageID); err != nil {
			return nil, fmt.Errorf("scan incident status page publication: %w", err)
		}
		statusPageIDs = append(statusPageIDs, statusPageID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident status page publications: %w", err)
	}

	return statusPageIDs, nil
}

func (s *Service) loadCreateIncidentOnFireFlagTx(ctx context.Context, tx *sql.Tx, tenantID, policyID uuid.UUID) (bool, error) {
	var enabled bool
	if err := tx.QueryRowContext(ctx, `
		SELECT create_incident_on_fire
		FROM alert_policies
		WHERE id = $1 AND tenant_id = $2
	`, policyID, tenantID).Scan(&enabled); err != nil {
		if err == sql.ErrNoRows {
			return false, fmt.Errorf("alert policy not found")
		}
		return false, fmt.Errorf("load alert policy: %w", err)
	}
	return enabled, nil
}

func (s *Service) lockAutoIncidentKeyTx(ctx context.Context, tx *sql.Tx, tenantID, monitorID, policyID uuid.UUID) error {
	lockKey := fmt.Sprintf("%s:%s:%s", tenantID, monitorID, policyID)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return fmt.Errorf("lock auto incident key: %w", err)
	}
	return nil
}

func (s *Service) lockIncidentRecoveryKeyTx(ctx context.Context, tx *sql.Tx, incidentID uuid.UUID) error {
	lockKey := fmt.Sprintf("incident-recovery:%s", incidentID)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return fmt.Errorf("lock incident recovery key: %w", err)
	}
	return nil
}

func (s *Service) findOrCreateAutoIncidentTx(ctx context.Context, tx *sql.Tx, tenantID uuid.UUID, alert *models.AlertWithDetails) (uuid.UUID, bool, error) {
	var incidentID uuid.UUID
	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM incidents
		WHERE tenant_id = $1
			AND is_auto_created = TRUE
			AND auto_monitor_id = $2
			AND auto_alert_policy_id = $3
			AND state <> 'resolved'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
		FOR UPDATE
	`, tenantID, alert.MonitorID, alert.AlertPolicyID).Scan(&incidentID)
	if err == nil {
		return incidentID, false, nil
	}
	if err != sql.ErrNoRows {
		return uuid.Nil, false, fmt.Errorf("load auto incident: %w", err)
	}

	incidentID = uuid.New()
	title, summary := buildAutoIncidentNarrative(alert.MonitorName, alert.PolicyName)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO incidents (
			id, tenant_id, title, summary, state, is_auto_created, auto_monitor_id, auto_alert_policy_id, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'investigating', TRUE, $5, $6, NOW(), NOW())
	`, incidentID, tenantID, title, summary, alert.MonitorID, alert.AlertPolicyID); err != nil {
		return uuid.Nil, false, fmt.Errorf("create auto incident: %w", err)
	}

	return incidentID, true, nil
}

func (s *Service) ensureIncidentAlertLinkTx(ctx context.Context, tx *sql.Tx, incidentID, alertID uuid.UUID) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO incident_alerts (incident_id, alert_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (incident_id, alert_id) DO NOTHING
	`, incidentID, alertID)
	if err != nil {
		return false, fmt.Errorf("attach incident alert: %w", err)
	}
	return mutationChanged(result), nil
}

func (s *Service) ensureIncidentMonitorLinkTx(ctx context.Context, tx *sql.Tx, incidentID, monitorID uuid.UUID) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO incident_monitors (incident_id, monitor_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (incident_id, monitor_id) DO NOTHING
	`, incidentID, monitorID)
	if err != nil {
		return false, fmt.Errorf("attach incident monitor: %w", err)
	}
	return mutationChanged(result), nil
}

func (s *Service) loadIncidentIDsForAlertTx(ctx context.Context, tx *sql.Tx, tenantID, alertID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT i.id
		FROM incidents i
		JOIN incident_alerts ia ON ia.incident_id = i.id
		JOIN alerts a ON a.id = ia.alert_id AND a.tenant_id = i.tenant_id
		WHERE i.tenant_id = $1 AND ia.alert_id = $2
	`, tenantID, alertID)
	if err != nil {
		return nil, fmt.Errorf("load incidents for alert: %w", err)
	}
	defer rows.Close()

	incidentIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var incidentID uuid.UUID
		if err := rows.Scan(&incidentID); err != nil {
			return nil, fmt.Errorf("scan incident for alert: %w", err)
		}
		incidentIDs = append(incidentIDs, incidentID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incidents for alert: %w", err)
	}

	return incidentIDs, nil
}

func (s *Service) loadLinkedAlertRecoveryStateTx(ctx context.Context, tx *sql.Tx, incidentID uuid.UUID) (bool, time.Time, error) {
	var totalCount int
	var unresolvedCount int
	var resolvedAfter sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*),
			COUNT(*) FILTER (WHERE a.status IN ('active', 'acknowledged')),
			MAX(COALESCE(a.resolved_at, a.updated_at))
		FROM incident_alerts ia
		JOIN alerts a ON a.id = ia.alert_id
		WHERE ia.incident_id = $1
	`, incidentID).Scan(&totalCount, &unresolvedCount, &resolvedAfter); err != nil {
		return false, time.Time{}, fmt.Errorf("load linked alert statuses: %w", err)
	}

	if totalCount == 0 || unresolvedCount != 0 || !resolvedAfter.Valid {
		return false, time.Time{}, nil
	}

	return true, resolvedAfter.Time.UTC(), nil
}

func (s *Service) recoveryTimelineExistsTx(ctx context.Context, tx *sql.Tx, incidentID uuid.UUID, entryType models.IncidentTimelineEntryType, message, resolvedAfter string) (bool, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM incident_timeline_entries
			WHERE incident_id = $1
				AND entry_type = $2
				AND message = $3
				AND metadata->>'resolved_after' = $4
		)
	`, incidentID, entryType, message, resolvedAfter).Scan(&exists); err != nil {
		return false, fmt.Errorf("check incident recovery timeline entry: %w", err)
	}

	return exists, nil
}

func recoveryResolutionMarker(resolvedAfter time.Time) string {
	return resolvedAfter.UTC().Format(time.RFC3339Nano)
}

func (s *Service) insertTimelineEntryTx(ctx context.Context, exec incidentExecutor, tenantID, incidentID uuid.UUID, entryType models.IncidentTimelineEntryType, message string, metadata map[string]interface{}) error {
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal incident timeline metadata: %w", err)
	}

	if _, err := exec.ExecContext(ctx, `
		INSERT INTO incident_timeline_entries (
			id, tenant_id, incident_id, entry_type, message, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, clock_timestamp())
	`, uuid.New(), tenantID, incidentID, entryType, message, metadataJSON); err != nil {
		return fmt.Errorf("insert incident timeline entry: %w", err)
	}

	if _, err := exec.ExecContext(ctx, `
		UPDATE incidents
		SET updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2
	`, incidentID, tenantID); err != nil {
		return fmt.Errorf("touch incident updated_at: %w", err)
	}

	return nil
}

func (s *Service) loadIncidentTimeline(ctx context.Context, tenantID, incidentID uuid.UUID) ([]models.IncidentTimelineEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, incident_id, entry_type, message, metadata, created_at
		FROM incident_timeline_entries
		WHERE tenant_id = $1 AND incident_id = $2
		ORDER BY created_at DESC, id DESC
	`, tenantID, incidentID)
	if err != nil {
		return nil, fmt.Errorf("load incident timeline: %w", err)
	}
	defer rows.Close()

	var timeline []models.IncidentTimelineEntry
	for rows.Next() {
		var entry models.IncidentTimelineEntry
		var metadata []byte
		if err := rows.Scan(
			&entry.ID, &entry.TenantID, &entry.IncidentID, &entry.EntryType,
			&entry.Message, &metadata, &entry.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan incident timeline entry: %w", err)
		}
		if len(metadata) > 0 {
			entry.Metadata = append(json.RawMessage(nil), metadata...)
		} else {
			entry.Metadata = json.RawMessage(`{}`)
		}
		timeline = append(timeline, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident timeline entries: %w", err)
	}

	return timeline, nil
}

func (s *Service) loadIncidentAlerts(ctx context.Context, tenantID, incidentID uuid.UUID) ([]models.IncidentAlertSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.monitor_id, a.alert_policy_id, a.status, a.triggered_at,
			a.acknowledged_at, a.resolved_at, a.failure_count, a.last_error, a.created_at, a.updated_at,
			m.name, ap.name
		FROM incident_alerts ia
		JOIN alerts a ON a.id = ia.alert_id
		JOIN monitors m ON m.id = a.monitor_id
		JOIN alert_policies ap ON ap.id = a.alert_policy_id
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
		if err := rows.Scan(
			&item.ID, &item.MonitorID, &item.AlertPolicyID, &item.Status, &item.TriggeredAt,
			&acknowledgedAt, &resolvedAt, &item.FailureCount, &lastError, &item.CreatedAt, &item.UpdatedAt,
			&item.MonitorName, &item.PolicyName,
		); err != nil {
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

func (s *Service) loadIncidentPublications(ctx context.Context, tenantID, incidentID uuid.UUID) ([]models.IncidentPublication, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT isp.status_page_id, sp.slug, sp.title, isp.published_at, isp.unpublished_at,
			COALESCE(
				array_agg(ispm.monitor_id ORDER BY ispm.monitor_id) FILTER (WHERE ispm.monitor_id IS NOT NULL),
				'{}'
			) AS monitor_ids
		FROM incident_status_page_publications isp
		JOIN status_pages sp ON sp.id = isp.status_page_id AND sp.tenant_id = isp.tenant_id
		LEFT JOIN incident_status_page_monitors ispm
			ON ispm.incident_id = isp.incident_id AND ispm.status_page_id = isp.status_page_id
		WHERE isp.incident_id = $1 AND isp.tenant_id = $2 AND isp.unpublished_at IS NULL
		GROUP BY isp.status_page_id, sp.slug, sp.title, isp.published_at, isp.unpublished_at
		ORDER BY isp.published_at DESC, isp.status_page_id
	`, incidentID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load incident publications: %w", err)
	}
	defer rows.Close()

	publications := make([]models.IncidentPublication, 0)
	for rows.Next() {
		var item models.IncidentPublication
		var unpublishedAt sql.NullTime
		monitorIDs := make([]uuid.UUID, 0)
		if err := rows.Scan(
			&item.StatusPageID, &item.StatusPageSlug, &item.StatusPageTitle, &item.PublishedAt, &unpublishedAt, pq.Array(&monitorIDs),
		); err != nil {
			return nil, fmt.Errorf("scan incident publication: %w", err)
		}
		if unpublishedAt.Valid {
			value := unpublishedAt.Time
			item.UnpublishedAt = &value
		}
		item.MonitorIDs = monitorIDs
		publications = append(publications, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident publications: %w", err)
	}

	return publications, nil
}

func (s *Service) ensureIncidentBelongsToTenant(ctx context.Context, tx *sql.Tx, tenantID, incidentID uuid.UUID) error {
	if err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM incidents
		WHERE id = $1 AND tenant_id = $2
		FOR UPDATE
	`, incidentID, tenantID).Scan(new(int)); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("incident not found")
		}
		return fmt.Errorf("load incident: %w", err)
	}

	return nil
}

func (s *Service) ensureAlertBelongsToTenant(ctx context.Context, tx *sql.Tx, tenantID, alertID uuid.UUID) error {
	if err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM alerts
		WHERE id = $1 AND tenant_id = $2
	`, alertID, tenantID).Scan(new(int)); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("alert not found")
		}
		return fmt.Errorf("load alert: %w", err)
	}

	return nil
}

func (s *Service) ensureMonitorBelongsToTenant(ctx context.Context, tx *sql.Tx, tenantID, monitorID uuid.UUID) error {
	if err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM monitors
		WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	`, monitorID, tenantID).Scan(new(int)); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("monitor not found")
		}
		return fmt.Errorf("load monitor: %w", err)
	}

	return nil
}

func (s *Service) ensureStatusPageBelongsToTenant(ctx context.Context, tx *sql.Tx, tenantID, statusPageID uuid.UUID) error {
	if err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM status_pages
		WHERE id = $1 AND tenant_id = $2
	`, statusPageID, tenantID).Scan(new(int)); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("status page not found")
		}
		return fmt.Errorf("load status page: %w", err)
	}

	return nil
}

func (s *Service) validatePublicationMonitorSelection(ctx context.Context, tx *sql.Tx, tenantID, incidentID, statusPageID uuid.UUID, monitorIDs []uuid.UUID) error {
	if len(monitorIDs) == 0 {
		return nil
	}

	tenantMonitorIDs, err := loadUUIDSet(ctx, tx, `
		SELECT id
		FROM monitors
		WHERE tenant_id = $1 AND id = ANY($2) AND deleted_at IS NULL
	`, tenantID, pq.Array(monitorIDs))
	if err != nil {
		return fmt.Errorf("validate publication monitors: %w", err)
	}

	incidentMonitorIDs, err := loadUUIDSet(ctx, tx, `
		SELECT monitor_id
		FROM incident_monitors
		WHERE incident_id = $1 AND monitor_id = ANY($2)
	`, incidentID, pq.Array(monitorIDs))
	if err != nil {
		return fmt.Errorf("validate incident publication monitors: %w", err)
	}

	statusPageMonitorIDs, err := loadUUIDSet(ctx, tx, `
		SELECT monitor_id
		FROM status_page_monitors
		WHERE status_page_id = $1 AND monitor_id = ANY($2)
	`, statusPageID, pq.Array(monitorIDs))
	if err != nil {
		return fmt.Errorf("validate status page publication monitors: %w", err)
	}

	for _, monitorID := range monitorIDs {
		if _, ok := tenantMonitorIDs[monitorID]; !ok {
			return fmt.Errorf("selected monitors must be linked to the incident and status page")
		}
		if _, ok := incidentMonitorIDs[monitorID]; !ok {
			return fmt.Errorf("selected monitors must be linked to the incident and status page")
		}
		if _, ok := statusPageMonitorIDs[monitorID]; !ok {
			return fmt.Errorf("selected monitors must be linked to the incident and status page")
		}
	}

	return nil
}

func loadUUIDSet(ctx context.Context, tx *sql.Tx, query string, args ...interface{}) (map[uuid.UUID]struct{}, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make(map[uuid.UUID]struct{})
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return ids, nil
}

func parseIncidentPublicationMonitorIDs(rawMonitorIDs []string) ([]uuid.UUID, error) {
	if len(rawMonitorIDs) == 0 {
		return []uuid.UUID{}, nil
	}

	monitorIDs := make([]uuid.UUID, 0, len(rawMonitorIDs))
	seen := make(map[uuid.UUID]struct{}, len(rawMonitorIDs))
	for _, rawMonitorID := range rawMonitorIDs {
		trimmedMonitorID := strings.TrimSpace(rawMonitorID)
		if trimmedMonitorID == "" {
			return nil, fmt.Errorf("monitor ID is required")
		}

		monitorID, err := uuid.Parse(trimmedMonitorID)
		if err != nil {
			return nil, fmt.Errorf("invalid monitor ID")
		}
		if _, ok := seen[monitorID]; ok {
			continue
		}
		seen[monitorID] = struct{}{}
		monitorIDs = append(monitorIDs, monitorID)
	}

	return monitorIDs, nil
}

func uuidStrings(ids []uuid.UUID) []string {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, id.String())
	}
	return values
}

func mutationChanged(result sql.Result) bool {
	if result == nil {
		return false
	}

	rowsAffected, err := result.RowsAffected()
	return err == nil && rowsAffected > 0
}

func (s *Service) isNoOpIncidentPublicationUpdate(ctx context.Context, tx *sql.Tx, tenantID, incidentID, statusPageID uuid.UUID, requestedMonitorIDs []uuid.UUID) (bool, error) {
	var activePublicationExists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM incident_status_page_publications
			WHERE incident_id = $1 AND status_page_id = $2 AND tenant_id = $3 AND unpublished_at IS NULL
		)
	`, incidentID, statusPageID, tenantID).Scan(&activePublicationExists); err != nil {
		return false, fmt.Errorf("load incident publication: %w", err)
	}
	if !activePublicationExists {
		return false, nil
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT monitor_id
		FROM incident_status_page_monitors
		WHERE incident_id = $1 AND status_page_id = $2
	`, incidentID, statusPageID)
	if err != nil {
		return false, fmt.Errorf("load incident publication monitors: %w", err)
	}
	defer rows.Close()

	currentMonitorIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var monitorID uuid.UUID
		if err := rows.Scan(&monitorID); err != nil {
			return false, fmt.Errorf("scan incident publication monitor: %w", err)
		}
		currentMonitorIDs = append(currentMonitorIDs, monitorID)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate incident publication monitors: %w", err)
	}

	return sameUUIDSet(currentMonitorIDs, requestedMonitorIDs), nil
}

func sameUUIDSet(left, right []uuid.UUID) bool {
	if len(left) != len(right) {
		return false
	}

	leftSet := make(map[uuid.UUID]struct{}, len(left))
	for _, id := range left {
		leftSet[id] = struct{}{}
	}
	for _, id := range right {
		if _, ok := leftSet[id]; !ok {
			return false
		}
	}

	return true
}

func buildAutoIncidentNarrative(monitorName, policyName string) (string, string) {
	monitorName = strings.TrimSpace(monitorName)
	policyName = strings.TrimSpace(policyName)

	title := "Auto-created incident"
	if monitorName != "" {
		title = fmt.Sprintf("%s incident", monitorName)
	}

	summary := "Automatically created from a firing alert policy."
	if policyName != "" {
		summary = fmt.Sprintf("Automatically created from alert policy %s.", policyName)
	}

	return title, summary
}

func scanIncident(scanner incidentRowScanner) (models.Incident, error) {
	var incident models.Incident
	var summary sql.NullString
	var severity string
	var ownerUserID uuid.NullUUID
	var ownerUsername sql.NullString
	var resolvedAt sql.NullTime
	var autoMonitorID uuid.NullUUID
	var autoAlertPolicyID uuid.NullUUID

	if err := scanner.Scan(
		&incident.ID, &incident.TenantID, &incident.Title, &summary, &incident.State, &severity, &ownerUserID, &ownerUsername, &resolvedAt,
		&incident.IsAutoCreated, &autoMonitorID, &autoAlertPolicyID, &incident.CreatedAt, &incident.UpdatedAt,
	); err != nil {
		return models.Incident{}, err
	}

	if summary.Valid {
		incident.Summary = summary.String
	}
	incident.Severity = models.IncidentSeverity(severity)
	if ownerUserID.Valid {
		ownerID := ownerUserID.UUID
		incident.OwnerUserID = &ownerID
	}
	if ownerUsername.Valid {
		incident.OwnerUsername = ownerUsername.String
	}
	if resolvedAt.Valid {
		resolvedAtValue := resolvedAt.Time
		incident.ResolvedAt = &resolvedAtValue
	}
	if autoMonitorID.Valid {
		monitorID := autoMonitorID.UUID
		incident.AutoMonitorID = &monitorID
	}
	if autoAlertPolicyID.Valid {
		policyID := autoAlertPolicyID.UUID
		incident.AutoAlertPolicyID = &policyID
	}

	return incident, nil
}

func scanIncidentListItem(scanner incidentRowScanner) (models.IncidentListItem, error) {
	var item models.IncidentListItem
	var severity string
	var ownerUserID uuid.NullUUID
	var ownerUsername sql.NullString
	var resolvedAt sql.NullTime

	if err := scanner.Scan(
		&item.ID, &item.State, &item.Source, &item.Title, &severity, &ownerUserID, &ownerUsername, &item.UpdatedAt, &resolvedAt,
		&item.LinkedAlertCount, &item.LinkedMonitorCount, &item.PublicationCount,
	); err != nil {
		return models.IncidentListItem{}, err
	}
	item.Severity = models.IncidentSeverity(severity)
	if ownerUserID.Valid {
		ownerID := ownerUserID.UUID
		item.OwnerUserID = &ownerID
	}
	if ownerUsername.Valid {
		item.OwnerUsername = ownerUsername.String
	}
	if resolvedAt.Valid {
		resolvedAtValue := resolvedAt.Time
		item.ResolvedAt = &resolvedAtValue
	}

	return item, nil
}

package incidents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/db"
)

type incidentRowScanner interface {
	Scan(dest ...interface{}) error
}

type incidentExecutor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// Service handles incident business logic.
type Service struct {
	db              *db.Client
	statusPublisher statusPublisher
}

// NewService creates a new incident service.
func NewService(dbClient *db.Client, publisher statusPublisher) *Service {
	return &Service{db: dbClient, statusPublisher: publisher}
}

// CreateIncident creates a new manual incident.
func (s *Service) CreateIncident(ctx context.Context, tenantID uuid.UUID, req *models.CreateIncidentRequest) (*models.IncidentDetail, error) {
	if err := validation.ValidateCreateIncident(req); err != nil {
		return nil, err
	}

	title := strings.TrimSpace(req.Title)
	summary := strings.TrimSpace(req.Summary)
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
			id, tenant_id, title, summary, state, is_auto_created, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'investigating', FALSE, NOW(), NOW())
	`, incidentID, tenantID, title, summary); err != nil {
		return nil, fmt.Errorf("create incident: %w", err)
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
		SELECT id, tenant_id, title, summary, state, resolved_at, is_auto_created,
			auto_monitor_id, auto_alert_policy_id, created_at, updated_at
		FROM incidents
		WHERE id = $1 AND tenant_id = $2
	`

	var detail models.IncidentDetail
	var summary sql.NullString
	var resolvedAt sql.NullTime
	var autoMonitorID uuid.NullUUID
	var autoAlertPolicyID uuid.NullUUID
	if err := s.db.QueryRowContext(ctx, query, incidentID, tenantID).Scan(
		&detail.ID, &detail.TenantID, &detail.Title, &summary, &detail.State, &resolvedAt,
		&detail.IsAutoCreated, &autoMonitorID, &autoAlertPolicyID, &detail.CreatedAt, &detail.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("incident not found")
		}
		return nil, fmt.Errorf("get incident: %w", err)
	}

	if summary.Valid {
		detail.Summary = summary.String
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
		SELECT id, tenant_id, title, summary, state, resolved_at, is_auto_created,
			auto_monitor_id, auto_alert_policy_id, created_at, updated_at
		FROM incidents
		WHERE tenant_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`, tenantID, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()

	var items []models.Incident
	for rows.Next() {
		incident, err := scanIncident(rows)
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

	current, err := s.GetIncident(ctx, tenantID, incidentID)
	if err != nil {
		return nil, err
	}
	if current.State == models.IncidentStateResolved && req.State != models.IncidentStateResolved {
		return nil, fmt.Errorf("resolved incidents cannot be reopened")
	}
	if current.State == req.State {
		return current, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin incident transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

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

func (s *Service) insertTimelineEntry(ctx context.Context, tenantID, incidentID uuid.UUID, entryType models.IncidentTimelineEntryType, message string, metadata map[string]interface{}) error {
	return s.insertTimelineEntryTx(ctx, s.db, tenantID, incidentID, entryType, message, metadata)
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
		) VALUES ($1, $2, $3, $4, $5, $6, NOW())
	`, uuid.New(), tenantID, incidentID, entryType, message, metadataJSON); err != nil {
		return fmt.Errorf("insert incident timeline entry: %w", err)
	}

	return nil
}

func (s *Service) loadIncidentTimeline(ctx context.Context, tenantID, incidentID uuid.UUID) ([]models.IncidentTimelineEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, incident_id, entry_type, message, metadata, created_at
		FROM incident_timeline_entries
		WHERE tenant_id = $1 AND incident_id = $2
		ORDER BY created_at ASC, id ASC
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

func scanIncident(scanner incidentRowScanner) (models.Incident, error) {
	var incident models.Incident
	var summary sql.NullString
	var resolvedAt sql.NullTime
	var autoMonitorID uuid.NullUUID
	var autoAlertPolicyID uuid.NullUUID

	if err := scanner.Scan(
		&incident.ID, &incident.TenantID, &incident.Title, &summary, &incident.State, &resolvedAt,
		&incident.IsAutoCreated, &autoMonitorID, &autoAlertPolicyID, &incident.CreatedAt, &incident.UpdatedAt,
	); err != nil {
		return models.Incident{}, err
	}

	if summary.Valid {
		incident.Summary = summary.String
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

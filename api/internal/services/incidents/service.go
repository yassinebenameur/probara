package incidents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

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
	db *db.Client
}

// NewService creates a new incident service.
func NewService(dbClient *db.Client) *Service {
	return &Service{db: dbClient}
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
			i.title, i.updated_at, i.resolved_at,
			COALESCE(ac.linked_alert_count, 0),
			COALESCE(mc.linked_monitor_count, 0),
			COALESCE(pc.publication_count, 0)
		FROM incidents i
		LEFT JOIN alert_counts ac ON ac.incident_id = i.id
		LEFT JOIN monitor_counts mc ON mc.incident_id = i.id
		LEFT JOIN publication_counts pc ON pc.incident_id = i.id
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
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO incident_alerts (incident_id, alert_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (incident_id, alert_id) DO NOTHING
	`, incidentID, alertID); err != nil {
		return nil, fmt.Errorf("attach incident alert: %w", err)
	}
	if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, "Alert attached", map[string]interface{}{
		"alert_id": alertID.String(),
	}); err != nil {
		return nil, err
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
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM incident_alerts
		WHERE incident_id = $1 AND alert_id = $2
	`, incidentID, alertID); err != nil {
		return nil, fmt.Errorf("detach incident alert: %w", err)
	}
	if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, "Alert detached", map[string]interface{}{
		"alert_id": alertID.String(),
	}); err != nil {
		return nil, err
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
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO incident_monitors (incident_id, monitor_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (incident_id, monitor_id) DO NOTHING
	`, incidentID, monitorID); err != nil {
		return nil, fmt.Errorf("attach incident monitor: %w", err)
	}
	if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, "Monitor attached", map[string]interface{}{
		"monitor_id": monitorID.String(),
	}); err != nil {
		return nil, err
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
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM incident_monitors
		WHERE incident_id = $1 AND monitor_id = $2
	`, incidentID, monitorID); err != nil {
		return nil, fmt.Errorf("detach incident monitor: %w", err)
	}
	if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, "Monitor detached", map[string]interface{}{
		"monitor_id": monitorID.String(),
	}); err != nil {
		return nil, err
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

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM incident_status_page_monitors
		WHERE incident_id = $1 AND status_page_id = $2
	`, incidentID, statusPageID); err != nil {
		return nil, fmt.Errorf("delete incident publication monitors: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE incident_status_page_publications
		SET unpublished_at = NOW(), updated_at = NOW()
		WHERE incident_id = $1 AND status_page_id = $2 AND tenant_id = $3 AND unpublished_at IS NULL
	`, incidentID, statusPageID, tenantID); err != nil {
		return nil, fmt.Errorf("unpublish incident from status page: %w", err)
	}
	if err := s.insertTimelineEntryTx(ctx, tx, tenantID, incidentID, models.IncidentTimelineEntryTypeSystem, "Incident unpublished from status page", map[string]interface{}{
		"status_page_id": statusPageID.String(),
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit incident transaction: %w", err)
	}

	return s.GetIncident(ctx, tenantID, incidentID)
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
		WHERE id = $1 AND tenant_id = $2
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
		WHERE tenant_id = $1 AND id = ANY($2)
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

func scanIncidentListItem(scanner incidentRowScanner) (models.IncidentListItem, error) {
	var item models.IncidentListItem
	var resolvedAt sql.NullTime

	if err := scanner.Scan(
		&item.ID, &item.State, &item.Source, &item.Title, &item.UpdatedAt, &resolvedAt,
		&item.LinkedAlertCount, &item.LinkedMonitorCount, &item.PublicationCount,
	); err != nil {
		return models.IncidentListItem{}, err
	}
	if resolvedAt.Valid {
		resolvedAtValue := resolvedAt.Time
		item.ResolvedAt = &resolvedAtValue
	}

	return item, nil
}

// Publishing an incident to status pages, and the refresh fan-out that tells the pages to re-render.

package incidents

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

type statusUpdatePublisher interface {
	Publish(event statusupdates.Event) error
}

const incidentPublicationUpdatedEventType = "incident.publication.updated"

const incidentStatusPageRefreshLookupTimeout = 5 * time.Second

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

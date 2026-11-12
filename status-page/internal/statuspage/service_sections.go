// Sections and the monitors a page renders, including the legacy
// status_page_monitors layout.

package statuspage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// GetStatusPageSections loads the page's sections with their monitors fully populated
// (status, uptime, history and analytics). Loading happens in two phases: cheap section
// "skeletons" first, then a single batched metric pass across every monitor on the page.
func (s *Service) GetStatusPageSections(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]StatusPageSectionData, error) {
	sections, err := s.loadStatusPageSectionSkeletons(ctx, statusPageID, tenantID)
	if err != nil {
		return nil, err
	}
	if err := s.populatePageMonitors(ctx, tenantID, sections); err != nil {
		return nil, err
	}
	return sections, nil
}

func (s *Service) loadStatusPageSectionSkeletons(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]StatusPageSectionData, error) {
	type sectionRow struct {
		ID       uuid.UUID
		Title    string
		Position int
	}

	sectionRows, err := s.db.QueryContext(ctx, `
		SELECT id, title, position
		FROM status_page_sections
		WHERE status_page_id = $1
		ORDER BY position ASC, created_at ASC, id ASC
	`, statusPageID)
	if err != nil {
		if !isUndefinedTableError(err) {
			return nil, fmt.Errorf("failed to query status page sections: %w", err)
		}
		return s.loadLegacyStatusPageSections(ctx, statusPageID, tenantID)
	}
	defer sectionRows.Close()

	sections := make([]StatusPageSectionData, 0)
	for sectionRows.Next() {
		var row sectionRow
		if err := sectionRows.Scan(&row.ID, &row.Title, &row.Position); err != nil {
			return nil, fmt.Errorf("failed to scan status page section: %w", err)
		}
		monitors, err := s.getStatusPageSectionMonitors(ctx, row.ID, tenantID)
		if err != nil {
			if isUndefinedTableError(err) {
				return s.loadLegacyStatusPageSections(ctx, statusPageID, tenantID)
			}
			return nil, err
		}
		sections = append(sections, StatusPageSectionData{
			ID:       row.ID.String(),
			Title:    strings.TrimSpace(row.Title),
			Position: row.Position,
			Monitors: monitors,
		})
	}
	if err := sectionRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating status page sections: %w", err)
	}
	if len(sections) == 0 {
		return s.loadLegacyStatusPageSections(ctx, statusPageID, tenantID)
	}
	return sections, nil
}

// GetStatusPageMonitors retrieves monitors for a status page with their status.
func (s *Service) GetStatusPageMonitors(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]MonitorStatus, error) {
	sections, err := s.GetStatusPageSections(ctx, statusPageID, tenantID)
	if err != nil {
		return nil, err
	}
	return flattenStatusPageSections(sections), nil
}

func (s *Service) getStatusPageSectionMonitors(ctx context.Context, sectionID, tenantID uuid.UUID) ([]MonitorStatus, error) {
	query := `
		SELECT m.id, m.name, spsm.display_name, m.type, m.config, m.tags
		FROM monitors m
		INNER JOIN status_page_section_monitors spsm ON m.id = spsm.monitor_id
		WHERE spsm.section_id = $1 AND m.tenant_id = $2 AND m.deleted_at IS NULL
		ORDER BY spsm.position ASC, m.name
	`
	rows, err := s.db.QueryContext(ctx, query, sectionID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query section monitors: %w", err)
	}
	defer rows.Close()

	return s.scanMonitorStatuses(ctx, rows, tenantID)
}

func (s *Service) loadLegacyStatusPageSections(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]StatusPageSectionData, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.name, spm.display_name, m.type, m.config, m.tags
		FROM monitors m
		INNER JOIN status_page_monitors spm ON m.id = spm.monitor_id
		WHERE spm.status_page_id = $1 AND m.tenant_id = $2 AND m.deleted_at IS NULL
		ORDER BY spm.position ASC, m.name
	`, statusPageID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query legacy monitors: %w", err)
	}
	defer rows.Close()

	monitors, err := s.scanMonitorStatuses(ctx, rows, tenantID)
	if err != nil {
		return nil, err
	}
	if len(monitors) == 0 {
		return nil, nil
	}
	return []StatusPageSectionData{{
		ID:       "",
		Title:    "Services",
		Position: 0,
		Monitors: monitors,
	}}, nil
}

func (s *Service) scanMonitorStatuses(ctx context.Context, rows *sql.Rows, tenantID uuid.UUID) ([]MonitorStatus, error) {
	monitors := make([]MonitorStatus, 0)
	for rows.Next() {
		var monitor MonitorStatus
		var monitorID uuid.UUID
		var displayName sql.NullString
		var monitorType string
		var configJSON []byte
		var tags pq.StringArray
		if err := rows.Scan(&monitorID, &monitor.Name, &displayName, &monitorType, &configJSON, &tags); err != nil {
			return nil, fmt.Errorf("failed to scan monitor: %w", err)
		}
		monitor.ID = monitorID.String()
		monitor.MonitorType = monitorType
		monitor.Tags = []string(tags)
		presenter := s.monitorPresenter(monitorType)

		if displayName.Valid && strings.TrimSpace(displayName.String) != "" {
			monitor.Name = strings.TrimSpace(displayName.String)
		}

		presenter.Configure(&monitor, configJSON)
		// Metrics (status, uptime, history, analytics) are populated in a single batched
		// pass by populatePageMonitors after all section skeletons are loaded. Doing it here,
		// per monitor, was the source of the N+1 query storm that timed out large pages.
		monitors = append(monitors, monitor)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitors: %w", err)
	}
	return monitors, nil
}

func flattenStatusPageSections(sections []StatusPageSectionData) []MonitorStatus {
	monitors := make([]MonitorStatus, 0)
	for _, section := range sections {
		monitors = append(monitors, section.Monitors...)
	}
	return monitors
}

func (s *Service) listStatusPageMonitorIDs(ctx context.Context, statusPageID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT spsm.monitor_id
		FROM status_page_sections sps
		JOIN status_page_section_monitors spsm ON spsm.section_id = sps.id
		WHERE sps.status_page_id = $1
		ORDER BY sps.position ASC, spsm.position ASC, spsm.monitor_id ASC
	`, statusPageID)
	if err != nil {
		if !isUndefinedTableError(err) {
			return nil, fmt.Errorf("failed to list section monitor ids: %w", err)
		}
		return s.listLegacyStatusPageMonitorIDs(ctx, statusPageID)
	}
	defer rows.Close()

	monitorIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var monitorID uuid.UUID
		if err := rows.Scan(&monitorID); err != nil {
			return nil, fmt.Errorf("failed to scan section monitor id: %w", err)
		}
		monitorIDs = append(monitorIDs, monitorID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating section monitor ids: %w", err)
	}
	if len(monitorIDs) == 0 {
		return s.listLegacyStatusPageMonitorIDs(ctx, statusPageID)
	}
	return dedupeMonitorIDs(monitorIDs), nil
}

func (s *Service) listLegacyStatusPageMonitorIDs(ctx context.Context, statusPageID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT monitor_id
		FROM status_page_monitors
		WHERE status_page_id = $1
		ORDER BY position ASC, monitor_id ASC
	`, statusPageID)
	if err != nil {
		return nil, fmt.Errorf("failed to list legacy status page monitor ids: %w", err)
	}
	defer rows.Close()

	monitorIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var monitorID uuid.UUID
		if err := rows.Scan(&monitorID); err != nil {
			return nil, fmt.Errorf("failed to scan legacy status page monitor id: %w", err)
		}
		monitorIDs = append(monitorIDs, monitorID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating legacy status page monitor ids: %w", err)
	}
	return dedupeMonitorIDs(monitorIDs), nil
}

func isUndefinedTableError(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	return pqErr.Code == "42P01"
}

func (s *Service) monitorPresenter(monitorType string) monitorPresenter {
	presenter, ok := s.presenters[monitorType]
	if ok {
		return presenter
	}
	return regularMonitorPresenter{}
}

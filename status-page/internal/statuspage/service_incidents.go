// Published incidents and maintenance windows shown on a status page.

package statuspage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/maintenance"
)

// loadMaintenanceWindows returns the maintenance windows with an occurrence
// in progress or starting in the next 14 days that covers at least one
// monitor on this status page, directly or via a targeted group. Coverage is
// the shared occurrence fragment (S-M5), never the window's own times. Each
// window is listed once, at its earliest such occurrence; a repeating window
// also carries the starts of up to maxListedNextStarts later ones. Affected
// monitor names are page-scoped.
func (s *Service) loadMaintenanceWindows(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]StatusPageMaintenanceWindow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT mw.id, mw.title, mw.description, mo.occurrence_starts_at, MAX(mo.ends_at),
			NOW() >= mo.occurrence_starts_at AS is_active,
			mw.recurrence_weekdays IS NOT NULL AS repeating,
			array_agg(DISTINCT pm.name) AS affected,
			mw.updated_at
		FROM monitors pm
		CROSS JOIN LATERAL `+maintenance.OccurrenceRangesSQL("$2", "pm.id", "NOW()", "NOW() + INTERVAL '14 days'")+` mo
		JOIN maintenance_windows mw ON mw.id = mo.window_id AND mw.tenant_id = $2
		WHERE pm.tenant_id = $2 AND pm.deleted_at IS NULL
		  AND pm.id IN (
			SELECT spsm.monitor_id
			FROM status_page_section_monitors spsm
			JOIN status_page_sections sps ON sps.id = spsm.section_id
			WHERE sps.status_page_id = $1
			UNION
			SELECT spm.monitor_id FROM status_page_monitors spm WHERE spm.status_page_id = $1
		  )
		GROUP BY mw.id, mo.occurrence_starts_at
		ORDER BY mo.occurrence_starts_at, mw.id
	`, statusPageID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query maintenance windows: %w", err)
	}
	defer rows.Close()

	windows := make([]StatusPageMaintenanceWindow, 0)
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var w StatusPageMaintenanceWindow
		var affected []string
		if err := rows.Scan(&id, &w.Title, &w.Description, &w.StartsAt, &w.EndsAt, &w.IsActive, &w.Repeating, pq.Array(&affected), &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan maintenance window: %w", err)
		}
		w.ID = id
		if i, seen := index[id]; seen {
			// A later occurrence of a window already listed.
			if len(windows[i].NextStartsAt) < maxListedNextStarts {
				windows[i].NextStartsAt = append(windows[i].NextStartsAt, w.StartsAt)
			}
			continue
		}
		w.AffectedMonitors = affected
		index[id] = len(windows)
		windows = append(windows, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate maintenance windows: %w", err)
	}
	return windows, nil
}

// maxListedNextStarts caps the later starts a repeating window lists.
const maxListedNextStarts = 3

func (s *Service) loadPublishedIncidents(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]StatusPageIncident, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.title, COALESCE(i.summary, ''), i.state, isp.published_at, i.resolved_at
		FROM incident_status_page_publications isp
		JOIN incidents i ON i.id = isp.incident_id AND i.tenant_id = isp.tenant_id
		WHERE isp.status_page_id = $1 AND isp.tenant_id = $2 AND isp.unpublished_at IS NULL
		ORDER BY CASE WHEN i.state = 'resolved' THEN 1 ELSE 0 END, i.updated_at DESC, isp.published_at DESC
	`, statusPageID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query published incidents: %w", err)
	}
	defer rows.Close()

	incidents := make([]StatusPageIncident, 0)
	incidentIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var incident StatusPageIncident
		var incidentID uuid.UUID
		var resolvedAt sql.NullTime
		if err := rows.Scan(&incidentID, &incident.Title, &incident.Summary, &incident.State, &incident.PublishedAt, &resolvedAt); err != nil {
			return nil, fmt.Errorf("scan published incident: %w", err)
		}
		incident.ID = incidentID.String()
		if resolvedAt.Valid {
			resolvedTime := resolvedAt.Time.UTC()
			incident.ResolvedAt = &resolvedTime
		}

		incidents = append(incidents, incident)
		incidentIDs = append(incidentIDs, incidentID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate published incidents: %w", err)
	}

	componentsByIncident, err := s.batchIncidentAffectedComponents(ctx, statusPageID, incidentIDs)
	if err != nil {
		return nil, err
	}
	updatesByIncident, err := s.batchIncidentPublicUpdates(ctx, tenantID, incidentIDs)
	if err != nil {
		return nil, err
	}

	for i := range incidents {
		incidentID := incidentIDs[i]
		if components, ok := componentsByIncident[incidentID]; ok {
			incidents[i].AffectedComponents = components
		} else {
			incidents[i].AffectedComponents = make([]string, 0)
		}
		if updates, ok := updatesByIncident[incidentID]; ok {
			incidents[i].Updates = updates
		} else {
			incidents[i].Updates = make([]StatusPageIncidentUpdate, 0)
		}
	}

	return incidents, nil
}

// batchIncidentAffectedComponents fetches the affected component names for all
// given incidents in a single query, keyed by incident ID. Per-incident
// ordering (monitor name ASC) matches the previous per-incident query.
func (s *Service) batchIncidentAffectedComponents(ctx context.Context, statusPageID uuid.UUID, incidentIDs []uuid.UUID) (map[uuid.UUID][]string, error) {
	result := make(map[uuid.UUID][]string, len(incidentIDs))
	if len(incidentIDs) == 0 {
		return result, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT ispm.incident_id, m.name
		FROM incident_status_page_monitors ispm
		JOIN monitors m ON m.id = ispm.monitor_id
		WHERE ispm.incident_id = ANY($1) AND ispm.status_page_id = $2 AND m.deleted_at IS NULL
		ORDER BY m.name ASC
	`, pq.Array(incidentIDs), statusPageID)
	if err != nil {
		return nil, fmt.Errorf("query incident affected components: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var incidentID uuid.UUID
		var name string
		if err := rows.Scan(&incidentID, &name); err != nil {
			return nil, fmt.Errorf("scan incident affected component: %w", err)
		}
		if strings.TrimSpace(name) != "" {
			result[incidentID] = append(result[incidentID], strings.TrimSpace(name))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident affected components: %w", err)
	}

	return result, nil
}

// batchIncidentPublicUpdates fetches the public timeline updates for all given
// incidents in a single query, keyed by incident ID. Per-incident ordering
// (created_at DESC, id DESC) matches the previous per-incident query.
func (s *Service) batchIncidentPublicUpdates(ctx context.Context, tenantID uuid.UUID, incidentIDs []uuid.UUID) (map[uuid.UUID][]StatusPageIncidentUpdate, error) {
	result := make(map[uuid.UUID][]StatusPageIncidentUpdate, len(incidentIDs))
	if len(incidentIDs) == 0 {
		return result, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT incident_id, message, created_at
		FROM incident_timeline_entries
		WHERE tenant_id = $1 AND incident_id = ANY($2) AND entry_type = 'public_update'
		ORDER BY created_at DESC, id DESC
	`, tenantID, pq.Array(incidentIDs))
	if err != nil {
		return nil, fmt.Errorf("query incident public updates: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var incidentID uuid.UUID
		var update StatusPageIncidentUpdate
		if err := rows.Scan(&incidentID, &update.Message, &update.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan incident public update: %w", err)
		}
		update.Message = strings.TrimSpace(update.Message)
		if update.Message != "" {
			result[incidentID] = append(result[incidentID], update)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident public updates: %w", err)
	}

	return result, nil
}

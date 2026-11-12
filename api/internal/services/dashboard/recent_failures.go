// Recent failures, the monitor set they are drawn from, and the tenant's tag list.

package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func (s *Service) getRecentFailures(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEnd time.Time, limit int, tags []string) ([]models.DashboardFailureEvent, error) {
	// The LIMIT is applied inside the CTE so the next-success LATERAL probe
	// only runs for the <= limit emitted rows, not every failure in the window.
	query := `
		WITH recent AS (
			SELECT
				cr.id,
				cr.tenant_id,
				cr.monitor_id,
				m.name AS monitor_name,
				cr.status,
				cr.result_source,
				cr.error_message,
				cr.latency_ms,
				cr.created_at
			FROM check_results cr
			JOIN monitors m ON m.id = cr.monitor_id AND m.tenant_id = cr.tenant_id
			WHERE cr.tenant_id = $1
			  AND cr.status IN ('failure', 'error')
			  AND cr.created_at >= $2
			  AND cr.created_at < $3
			  AND m.deleted_at IS NULL
			  %s
			ORDER BY cr.created_at DESC
			LIMIT $%d
		)
		SELECT
			r.id,
			r.monitor_id,
			r.monitor_name,
			r.status,
			r.result_source,
			r.error_message,
			r.latency_ms,
			r.created_at,
			rs.created_at AS resolved_at
		FROM recent r
		LEFT JOIN LATERAL (
			SELECT succ.created_at
			FROM check_results succ
			WHERE succ.tenant_id = r.tenant_id
			  AND succ.monitor_id = r.monitor_id
			  AND succ.status = 'success'
			  AND succ.created_at > r.created_at
			ORDER BY succ.created_at ASC
			LIMIT 1
		) rs ON TRUE
		ORDER BY r.created_at DESC
	`

	tagClause := ""
	limitPlaceholder := 4
	args := []interface{}{tenantID, rangeStart, rangeEnd, limit}
	if len(tags) > 0 {
		tagClause = "AND m.tags && $5::text[]"
		args = []interface{}{tenantID, rangeStart, rangeEnd, limit, pq.Array(tags)}
	}

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(query, tagClause, limitPlaceholder), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query recent failures: %w", err)
	}
	defer rows.Close()

	failures := make([]models.DashboardFailureEvent, 0)
	for rows.Next() {
		var event models.DashboardFailureEvent
		var errorMessage sql.NullString
		var latency sql.NullInt64
		var resolvedAt sql.NullTime

		if err := rows.Scan(
			&event.CheckResultID,
			&event.MonitorID,
			&event.MonitorName,
			&event.Status,
			&event.ResultSource,
			&errorMessage,
			&latency,
			&event.OccurredAt,
			&resolvedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan recent failure: %w", err)
		}

		if errorMessage.Valid {
			event.ErrorMessage = &errorMessage.String
		}
		if latency.Valid {
			v := int(latency.Int64)
			event.LatencyMS = &v
		}
		if resolvedAt.Valid {
			ts := resolvedAt.Time
			event.ResolvedAt = &ts
		}
		event.State = resolveFailureState(event.ResolvedAt)
		failures = append(failures, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating recent failures: %w", err)
	}

	return failures, nil
}

func (s *Service) listEnabledOperationalMonitorIDs(ctx context.Context, tenantID uuid.UUID, tags []string) ([]uuid.UUID, error) {
	query := `
		SELECT id
		FROM monitors
		WHERE tenant_id = $1
		  AND enabled = TRUE
		  AND type <> 'group'
		  AND deleted_at IS NULL
		ORDER BY id
	`
	args := []interface{}{tenantID}
	if len(tags) > 0 {
		query = `
			SELECT id
			FROM monitors
			WHERE tenant_id = $1
			  AND enabled = TRUE
			  AND type <> 'group'
			  AND deleted_at IS NULL
			  AND tags && $2::text[]
			ORDER BY id
		`
		args = append(args, pq.Array(tags))
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list enabled dashboard monitors: %w", err)
	}
	defer rows.Close()

	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan dashboard monitor id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating dashboard monitor ids: %w", err)
	}
	return ids, nil
}

func (s *Service) getAvailableTags(ctx context.Context, tenantID uuid.UUID) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT unnest(tags) AS tag
		FROM monitors
		WHERE tenant_id = $1
		  AND tags IS NOT NULL
		  AND deleted_at IS NULL
		ORDER BY tag
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list dashboard tags: %w", err)
	}
	defer rows.Close()

	tags := make([]string, 0)
	for rows.Next() {
		var tag sql.NullString
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("failed to scan dashboard tag: %w", err)
		}
		if tag.Valid && tag.String != "" {
			tags = append(tags, tag.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating dashboard tags: %w", err)
	}

	return tags, nil
}

func resolveFailureState(resolvedAt *time.Time) models.DashboardFailureState {
	if resolvedAt != nil {
		return models.DashboardFailureStateResolved
	}
	return models.DashboardFailureStateFiring
}

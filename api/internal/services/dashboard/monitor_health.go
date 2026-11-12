// Monitor health list: current state per monitor, read from live state or from rollups depending on the range.

package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/maintenance"
)

// getMonitorHealth returns the per-monitor health grid. LatestStatus is derived
// from the persisted state machine (monitors.current_state) for EVERY range via
// monitorStateToStatus — the same source the status page and problem-monitors
// use — so all surfaces agree even when a monitor's last in-window check status
// differs from its current state (D1). LatestCheckAt remains range-scoped:
//   - 24h: from the shared rolling-24h totals (no extra query),
//   - 1h: one batched MAX(created_at) aggregate over the small raw window,
//   - 7d+: the latest daily-rollup row per monitor (cheap rollup LATERAL).
func (s *Service) getMonitorHealth(ctx context.Context, tenantID uuid.UUID, dashboardRange models.DashboardRange, rangeStart, rangeEndExclusive time.Time, tags []string, precomp *rolling24hData) ([]models.DashboardMonitorHealth, error) {
	if isDashboardRollupRange(dashboardRange) {
		return s.getMonitorHealthRollupRange(ctx, tenantID, rangeStart, rangeEndExclusive, tags)
	}

	healthRows, err := s.listMonitorHealthFromState(ctx, tenantID, tags)
	if err != nil {
		return nil, err
	}
	if len(healthRows) == 0 {
		return healthRows, nil
	}

	if dashboardRange == models.DashboardRange24h {
		var totals map[uuid.UUID]MonitorRolling24hTotals
		if precomp != nil {
			totals = precomp.totals
		}
		if totals == nil {
			data, _, err := s.loadRolling24hData(ctx, tenantID, tags)
			if err != nil {
				return nil, err
			}
			totals = data.totals
		}
		for i := range healthRows {
			if t, ok := totals[healthRows[i].MonitorID]; ok && t.LatestCheckAt != nil {
				ts := *t.LatestCheckAt
				healthRows[i].LatestCheckAt = &ts
			}
		}
		return healthRows, nil
	}

	latest, err := s.batchLatestCheckAt(ctx, tenantID, rangeStart, rangeEndExclusive, tags)
	if err != nil {
		return nil, err
	}
	for i := range healthRows {
		if ts, ok := latest[healthRows[i].MonitorID]; ok {
			t := ts
			healthRows[i].LatestCheckAt = &t
		}
	}
	return healthRows, nil
}

// listMonitorHealthFromState lists the tenant's monitors with LatestStatus
// derived from monitors.current_state. No check_results access at all: the
// previous per-monitor LATERAL probe is gone. LatestCheckAt is left nil for the
// caller to fill from range-scoped data.
func (s *Service) listMonitorHealthFromState(ctx context.Context, tenantID uuid.UUID, tags []string) ([]models.DashboardMonitorHealth, error) {
	query := `
		SELECT m.id, m.name, m.enabled, m.current_state, ` + maintenance.InMaintenancePredicate("m") + ` AS in_maintenance
		FROM monitors m
		WHERE m.tenant_id = $1 AND m.deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	if len(tags) > 0 {
		query += ` AND m.tags && $2::text[]`
		args = append(args, pq.Array(tags))
	}
	query += ` ORDER BY m.name`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query monitor health: %w", err)
	}
	defer rows.Close()

	healthRows := make([]models.DashboardMonitorHealth, 0)
	for rows.Next() {
		var row models.DashboardMonitorHealth
		var currentState string
		if err := rows.Scan(&row.MonitorID, &row.MonitorName, &row.Enabled, &currentState, &row.InMaintenance); err != nil {
			return nil, fmt.Errorf("failed to scan monitor health row: %w", err)
		}
		row.LatestStatus = monitorStateToStatus(currentState)
		healthRows = append(healthRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitor health rows: %w", err)
	}
	return healthRows, nil
}

// batchLatestCheckAt returns each monitor's most recent check timestamp inside
// [rangeStart, rangeEndExclusive) as ONE bounded aggregate over the (small) raw
// window, replacing the per-monitor descending LATERAL probe on check_results.
func (s *Service) batchLatestCheckAt(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEndExclusive time.Time, tags []string) (map[uuid.UUID]time.Time, error) {
	tagClause := ""
	args := []interface{}{tenantID, rangeStart, rangeEndExclusive}
	if len(tags) > 0 {
		tagClause = "AND m.tags && $4::text[]"
		args = append(args, pq.Array(tags))
	}

	query := fmt.Sprintf(`
		SELECT cr.monitor_id, MAX(cr.created_at) AS latest_check_at
		FROM check_results cr
		JOIN monitors m ON m.id = cr.monitor_id AND m.tenant_id = cr.tenant_id
		WHERE cr.tenant_id = $1
		  AND m.deleted_at IS NULL
		  AND cr.result_source <> 'platform'
		  AND cr.created_at >= $2
		  AND cr.created_at < $3
		  %s
		GROUP BY cr.monitor_id
	`, tagClause)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query latest check times: %w", err)
	}
	defer rows.Close()

	latest := make(map[uuid.UUID]time.Time)
	for rows.Next() {
		var monitorID uuid.UUID
		var ts time.Time
		if err := rows.Scan(&monitorID, &ts); err != nil {
			return nil, fmt.Errorf("failed to scan latest check time: %w", err)
		}
		latest[monitorID] = ts
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating latest check times: %w", err)
	}
	return latest, nil
}

// getMonitorHealthRollupRange serves 7d+ ranges: LatestCheckAt still comes from
// the newest daily-rollup row in range (a cheap per-monitor LATERAL on the
// small rollup table), but LatestStatus is derived from monitors.current_state,
// NOT the rollup's latest_status, so status agrees with every other surface.
func (s *Service) getMonitorHealthRollupRange(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEndExclusive time.Time, tags []string) ([]models.DashboardMonitorHealth, error) {
	args := []interface{}{tenantID, rangeStart, rangeEndExclusive}
	whereClause := "WHERE m.tenant_id = $1 AND m.deleted_at IS NULL"
	if len(tags) > 0 {
		whereClause += " AND m.tags && $4::text[]"
		args = append(args, pq.Array(tags))
	}

	query := fmt.Sprintf(`
		SELECT
			m.id,
			m.name,
			m.enabled,
			m.current_state,
			lr.latest_check_at,
			%s AS in_maintenance
		FROM monitors m
		LEFT JOIN LATERAL (
			SELECT mdr.latest_check_at
			FROM monitor_daily_rollups mdr
			WHERE mdr.monitor_id = m.id
			  AND mdr.tenant_id = m.tenant_id
			  AND mdr.bucket_day >= $2::date
			  AND mdr.bucket_day < $3::date
			ORDER BY mdr.bucket_day DESC
			LIMIT 1
		) lr ON TRUE
		%s
		ORDER BY m.name
	`, maintenance.InMaintenancePredicate("m"), whereClause)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query monitor health: %w", err)
	}
	defer rows.Close()

	healthRows := make([]models.DashboardMonitorHealth, 0)
	for rows.Next() {
		var row models.DashboardMonitorHealth
		var currentState string
		var latestCheckAt sql.NullTime

		if err := rows.Scan(&row.MonitorID, &row.MonitorName, &row.Enabled, &currentState, &latestCheckAt, &row.InMaintenance); err != nil {
			return nil, fmt.Errorf("failed to scan monitor health row: %w", err)
		}
		row.LatestStatus = monitorStateToStatus(currentState)
		if latestCheckAt.Valid {
			ts := latestCheckAt.Time
			row.LatestCheckAt = &ts
		}
		healthRows = append(healthRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitor health rows: %w", err)
	}

	return healthRows, nil
}

// monitorStateToStatus maps the persisted state-machine value to the result-status vocabulary
// used in CurrentStatus, keeping the existing API surface while deriving status from state.
// down → "failure" (confirmed outage); suspect/up/degraded → "success" (degraded is below
// quorum, so not counted as an outage here — the raw state is exposed separately);
// unknown → nil (no data yet).
func monitorStateToStatus(state string) *string {
	switch state {
	case "down":
		s := "failure"
		return &s
	case "up", "suspect", "degraded":
		s := "success"
		return &s
	default:
		return nil
	}
}

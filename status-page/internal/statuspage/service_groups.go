// Group monitors: member resolution, aggregated status and history.

package statuspage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/yassinebenameur/probara/shared/monitorstate"
)

// populateGroupMonitorShortRange fills a group monitor's status, uptime, latency, hourly
// strip and recent history by aggregating over its (pre-resolved) leaf member IDs. Long-range
// analytics and the 1h strip are applied separately by the batched passes in
// populatePageMonitors. Per-monitor widgets that the public page never renders (raw latency
// series, short-range downtime) are intentionally not populated.
func (s *Service) populateGroupMonitorShortRange(ctx context.Context, monitor *MonitorStatus, memberIDs []uuid.UUID, tenantID uuid.UUID) {
	if len(memberIDs) == 0 {
		monitor.Status = "unknown"
		monitor.Uptime24hFormatted = "N/A"
		monitor.Uptime1hFormatted = "N/A"
		return
	}

	groupStatus, err := s.GetGroupAggregatedStatus(ctx, monitor.ID, memberIDs, tenantID)
	if err != nil {
		monitor.Status = "unknown"
	} else {
		monitor.Status = groupStatus.Status
		monitor.LastCheckTime = groupStatus.LastCheckTime
		monitor.LastLatency = groupStatus.LastLatency
	}

	uptime24h, err := s.CalculateGroupUptime24h(ctx, memberIDs, tenantID)
	monitor.Uptime24h = uptime24h
	monitor.Uptime24hFormatted = formatUptime(uptime24h, err)

	uptime1h, err := s.CalculateGroupUptime1h(ctx, memberIDs, tenantID)
	monitor.Uptime1h = uptime1h
	monitor.Uptime1hFormatted = formatUptime(uptime1h, err)

	avgLatency1h, _ := s.CalculateGroupAvgLatency(ctx, memberIDs, tenantID, "1 hour")
	monitor.AvgLatency1h = avgLatency1h

	avgLatency24h, _ := s.CalculateGroupAvgLatency(ctx, memberIDs, tenantID, "24 hours")
	monitor.AvgLatency24h = avgLatency24h

	hourlyUptime, err := s.GetGroupHourlyUptime(ctx, memberIDs, tenantID)
	if err == nil {
		monitor.HourlyUptime = hourlyUptime
		monitor.UptimeHistory24h = hourlyUptime
	}

	history, err := s.GetGroupHistory(ctx, memberIDs, tenantID, 50)
	if err == nil {
		monitor.History = history
	}
}

// mapResultStatus maps database status to public status
func mapResultStatus(status string) string {
	switch status {
	case "success":
		return "up"
	case "failure":
		return "down"
	case "error":
		return "error"
	default:
		return "unknown"
	}
}

// getGroupMemberIDs retrieves non-group member monitor IDs for a group recursively, deduplicated.
func (s *Service) getGroupMemberIDs(ctx context.Context, groupID, tenantID uuid.UUID) ([]uuid.UUID, error) {
	query := `
		WITH RECURSIVE member_tree AS (
			SELECT mg.monitor_id
			FROM monitor_groups mg
			JOIN monitors m ON m.id = mg.monitor_id
			WHERE mg.group_id = $1 AND m.tenant_id = $2 AND m.deleted_at IS NULL
			UNION
			SELECT mg.monitor_id
			FROM member_tree mt
			JOIN monitors parent ON parent.id = mt.monitor_id AND parent.tenant_id = $2
			JOIN monitor_groups mg ON mg.group_id = parent.id
			JOIN monitors child ON child.id = mg.monitor_id AND child.tenant_id = $2
			WHERE parent.type = 'group'
			  AND parent.deleted_at IS NULL
			  AND child.deleted_at IS NULL
		)
		SELECT m.id
		FROM monitors m
		JOIN member_tree mt ON mt.monitor_id = m.id
		WHERE m.tenant_id = $2
		  AND m.type <> 'group'
		  AND m.deleted_at IS NULL
		ORDER BY m.name
	`

	rows, err := s.db.QueryContext(ctx, query, groupID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query group members: %w", err)
	}
	defer rows.Close()

	var memberIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan member ID: %w", err)
		}
		memberIDs = append(memberIDs, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating group members: %w", err)
	}

	return memberIDs, nil
}

// GetGroupAggregatedStatus returns a group's displayed status plus the latest
// check time and average latency across its members.
//
// The status is monitorstate's display view of the group (S-G3) over the
// members' monitor-level states, so it honours location quorum, failure
// thresholds and paused members exactly as the operator UI and the alerter
// do. It is never derived from raw check rows: the newest row of a
// multi-location monitor is one location's opinion, not the monitor's state.
// memberIDs (the pre-resolved leaf members) only scope the latency and
// last-check figures.
func (s *Service) GetGroupAggregatedStatus(ctx context.Context, groupID string, memberIDs []uuid.UUID, tenantID uuid.UUID) (*CurrentStatus, error) {
	id, err := uuid.Parse(groupID)
	if err != nil {
		return nil, fmt.Errorf("invalid group ID %q: %w", groupID, err)
	}
	summaries, err := monitorstate.LoadGroupSummaries(ctx, s.db, tenantID, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	result := &CurrentStatus{Status: mapMonitorState(string(summaries[id].DisplayState()))}
	if len(memberIDs) == 0 {
		return result, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT ON (monitor_id) latency_ms, created_at
		FROM check_results
		WHERE monitor_id = ANY($1) AND tenant_id = $2
		ORDER BY monitor_id, created_at DESC
	`, pq.Array(memberIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query member checks: %w", err)
	}
	defer rows.Close()

	var totalLatency int64
	var latencyCount int
	for rows.Next() {
		var latencyMS sql.NullInt64
		var createdAt time.Time
		if err := rows.Scan(&latencyMS, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan member check: %w", err)
		}
		if result.LastCheckTime == nil || createdAt.After(*result.LastCheckTime) {
			checkedAt := createdAt
			result.LastCheckTime = &checkedAt
		}
		if latencyMS.Valid {
			totalLatency += latencyMS.Int64
			latencyCount++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating member checks: %w", err)
	}
	if latencyCount > 0 {
		avgLatency := int(totalLatency / int64(latencyCount))
		result.LastLatency = &avgLatency
	}
	return result, nil
}

// GetGroupHistory gets aggregated check results history for a group
func (s *Service) GetGroupHistory(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID, limit int) ([]CheckResultHistory, error) {
	if len(memberIDs) == 0 {
		return nil, nil
	}

	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	// Aggregate results by minute - if any member fails, mark that minute as failed
	query := `
		WITH minute_results AS (
			SELECT 
				date_trunc('minute', created_at) as minute,
				CASE WHEN COUNT(*) FILTER (WHERE status != 'success') > 0 THEN 'failure' ELSE 'success' END as status,
				ROUND(AVG(latency_ms))::int as avg_latency
			FROM check_results
			WHERE monitor_id = ANY($1) AND tenant_id = $2 AND created_at >= NOW() - INTERVAL '24 hours'
			GROUP BY date_trunc('minute', created_at)
			ORDER BY minute DESC
			LIMIT $3
		)
		SELECT status, avg_latency, minute
		FROM minute_results
		ORDER BY minute ASC
	`

	rows, err := s.db.QueryContext(ctx, query, pq.Array(memberIDs), tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query group history: %w", err)
	}
	defer rows.Close()

	var history []CheckResultHistory
	for rows.Next() {
		var result CheckResultHistory
		var status string
		var latencyMS sql.NullInt64

		err := rows.Scan(&status, &latencyMS, &result.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("failed to scan group history: %w", err)
		}

		result.Status = mapResultStatus(status)
		if latencyMS.Valid {
			latencyInt := int(latencyMS.Int64)
			result.LatencyMS = &latencyInt
		}

		history = append(history, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating group history: %w", err)
	}

	return history, nil
}

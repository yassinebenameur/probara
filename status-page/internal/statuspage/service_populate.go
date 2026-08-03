package statuspage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/maintenance"
)

// populatePageMonitors fills in status, uptime, latency, history and long-range analytics for
// every monitor on the page using a constant number of database queries, regardless of how
// many monitors the page contains. This replaces the previous per-monitor population that
// issued ~12 queries per monitor (an N+1 storm that timed out large pages).
//
// Regular (leaf) monitors are resolved entirely from a handful of batched, page-wide queries.
// Group monitors aggregate over their member sets via the existing group helpers (bounded by
// the number of groups, not total monitors). Long-range analytics for both kinds are resolved
// through a single batched analytics call when available.
func (s *Service) populatePageMonitors(ctx context.Context, tenantID uuid.UUID, sections []StatusPageSectionData) error {
	type pageMon struct {
		ptr     *MonitorStatus
		id      uuid.UUID
		isGroup bool
		members []uuid.UUID
	}

	mons := make([]pageMon, 0)
	regularIDSet := make(map[uuid.UUID]struct{})
	for si := range sections {
		for mi := range sections[si].Monitors {
			ptr := &sections[si].Monitors[mi]
			id, err := uuid.Parse(ptr.ID)
			if err != nil {
				ptr.Status = "unknown"
				continue
			}
			if ptr.MonitorType == "group" {
				members, err := s.getGroupMemberIDs(ctx, id, tenantID)
				if err != nil {
					members = nil
				}
				mons = append(mons, pageMon{ptr: ptr, id: id, isGroup: true, members: dedupeMonitorIDs(members)})
				continue
			}
			regularIDSet[id] = struct{}{}
			mons = append(mons, pageMon{ptr: ptr, id: id})
		}
	}

	if len(mons) == 0 {
		return nil
	}

	regularIDs := make([]uuid.UUID, 0, len(regularIDSet))
	for id := range regularIDSet {
		regularIDs = append(regularIDs, id)
	}

	var (
		statusByID  map[uuid.UUID]*CurrentStatus
		summaryByID map[uuid.UUID]*uptimeSummary
		hourlyByID  map[uuid.UUID][]HourlyUptime
		historyByID map[uuid.UUID][]CheckResultHistory
	)
	if len(regularIDs) > 0 {
		var err error
		if statusByID, err = s.batchCurrentStatus(ctx, regularIDs, tenantID); err != nil {
			return err
		}
		if summaryByID, err = s.batchUptimeSummary(ctx, regularIDs, tenantID); err != nil {
			return err
		}
		if hourlyByID, err = s.batchHourlyUptime(ctx, regularIDs, tenantID); err != nil {
			return err
		}
		if historyByID, err = s.batchHistory(ctx, regularIDs, tenantID); err != nil {
			return err
		}
	}

	// Apply short-range data per monitor.
	for _, m := range mons {
		if m.isGroup {
			s.populateGroupMonitorShortRange(ctx, m.ptr, m.members, tenantID)
			continue
		}
		assignRegularShortRange(m.ptr, m.id, statusByID, summaryByID, hourlyByID, historyByID)
	}

	// Flag components whose certificate is inside its expiry warning window
	// (open tls_expiry alert) — shown as a small note, not a status change.
	if len(regularIDs) > 0 {
		expiring, err := s.batchOpenTLSExpiryAlerts(ctx, regularIDs, tenantID)
		if err != nil {
			return err
		}
		for _, m := range mons {
			if !m.isGroup && expiring[m.id] {
				m.ptr.CertExpiresSoon = true
			}
		}
	}

	// Build long-range scopes (one per distinct monitor on the page) and apply in a batch.
	scopes := make([]sharedanalytics.ScopeAnalyticsBatchRequest, 0, len(mons))
	scopeMonitors := make(map[uuid.UUID][]*MonitorStatus)
	for _, m := range mons {
		var leafIDs []uuid.UUID
		if m.isGroup {
			leafIDs = m.members
		} else {
			leafIDs = []uuid.UUID{m.id}
		}
		if len(leafIDs) == 0 {
			// Empty groups carry no long-range history (matches the prior per-monitor behaviour).
			continue
		}
		if _, ok := scopeMonitors[m.id]; !ok {
			scopes = append(scopes, sharedanalytics.ScopeAnalyticsBatchRequest{Key: m.id, MonitorIDs: leafIDs})
		}
		scopeMonitors[m.id] = append(scopeMonitors[m.id], m.ptr)
	}
	s.applyLongRangeBatch(ctx, tenantID, scopes, scopeMonitors)

	return nil
}

func assignRegularShortRange(monitor *MonitorStatus, id uuid.UUID, statusByID map[uuid.UUID]*CurrentStatus, summaryByID map[uuid.UUID]*uptimeSummary, hourlyByID map[uuid.UUID][]HourlyUptime, historyByID map[uuid.UUID][]CheckResultHistory) {
	if cs := statusByID[id]; cs != nil {
		monitor.Status = cs.Status
		monitor.LastCheckTime = cs.LastCheckTime
		monitor.LastHTTPStatus = cs.LastHTTPStatus
		monitor.LastLatency = cs.LastLatency
		monitor.TLSDaysUntilExpiry = cs.TLSDaysUntilExpiry
		monitor.TLSNotAfter = cs.TLSNotAfter
	} else {
		monitor.Status = "unknown"
	}

	var uptime24h, uptime1h *float64
	if summary := summaryByID[id]; summary != nil {
		uptime24h = summary.Uptime24h
		uptime1h = summary.Uptime1h
		monitor.AvgLatency1h = summary.AvgLatency1h
		monitor.AvgLatency24h = summary.AvgLatency24h
	}
	monitor.Uptime24h = uptime24h
	monitor.Uptime24hFormatted = formatUptime(uptime24h, nil)
	monitor.Uptime1h = uptime1h
	monitor.Uptime1hFormatted = formatUptime(uptime1h, nil)

	if hourly, ok := hourlyByID[id]; ok {
		monitor.HourlyUptime = hourly
		monitor.UptimeHistory24h = hourly
	}
	if history, ok := historyByID[id]; ok {
		monitor.History = history
	}
}

func (s *Service) applyLongRangeBatch(ctx context.Context, tenantID uuid.UUID, scopes []sharedanalytics.ScopeAnalyticsBatchRequest, scopeMonitors map[uuid.UUID][]*MonitorStatus) {
	if len(scopes) == 0 {
		return
	}

	if s.analyticsBatch != nil {
		results, err := s.analyticsBatch.GetScopeAnalyticsBatch(ctx, tenantID, scopes, longRangeRanges, s.now())
		if err == nil {
			for key, ptrs := range scopeMonitors {
				byRange := results[key]
				for _, ptr := range ptrs {
					for _, rangeValue := range longRangeRanges {
						assignLongRangeResult(ptr, rangeValue, byRange[rangeValue])
					}
				}
			}
			return
		}
	}

	// Fallback: per-scope analytics (used when the reader is not batch-capable or batching failed).
	for _, scope := range scopes {
		for _, ptr := range scopeMonitors[scope.Key] {
			s.applyMonitorLongRangeAnalytics(ctx, ptr, tenantID, scope.MonitorIDs)
		}
	}
}

// uptimeSummary holds the batched short-range uptime and latency figures for one monitor.
type uptimeSummary struct {
	Uptime24h     *float64
	Uptime1h      *float64
	AvgLatency1h  *float64
	AvgLatency24h *float64
}

// batchCurrentStatus loads the persisted state and latest check result for each monitor in one
// query. The public status is derived from monitors.current_state (the authoritative state
// machine), while the lateral check_result columns (http_status, latency_ms, metrics_data) are
// retained for display purposes. Monitors with no check results still produce a row (via LEFT
// JOIN LATERAL) whose result columns are all NULL.
func (s *Service) batchCurrentStatus(ctx context.Context, monitorIDs []uuid.UUID, tenantID uuid.UUID) (map[uuid.UUID]*CurrentStatus, error) {
	query := `
		SELECT m.monitor_id, mon.current_state, cr.status, cr.http_status, cr.latency_ms, cr.created_at, cr.metrics_data,
			` + maintenance.InMaintenancePredicate("mon") + ` AS in_maintenance
		FROM unnest($1::uuid[]) AS m(monitor_id)
		JOIN monitors mon ON mon.id = m.monitor_id
		LEFT JOIN LATERAL (
			SELECT cr.status, cr.http_status, cr.latency_ms, cr.created_at, cr.metrics_data
			FROM check_results cr
			WHERE cr.monitor_id = m.monitor_id
			  AND cr.tenant_id = $2
			ORDER BY cr.created_at DESC
			LIMIT 1
		) cr ON TRUE
		ORDER BY m.monitor_id
	`
	rows, err := s.db.QueryContext(ctx, query, pq.Array(monitorIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to batch current status: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]*CurrentStatus)
	for rows.Next() {
		var monitorID uuid.UUID
		var currentState string
		var resultStatus sql.NullString
		var httpStatus, latencyMS sql.NullInt64
		var createdAt sql.NullTime
		var metricsJSON []byte
		var inMaintenance bool
		if err := rows.Scan(&monitorID, &currentState, &resultStatus, &httpStatus, &latencyMS, &createdAt, &metricsJSON, &inMaintenance); err != nil {
			return nil, fmt.Errorf("failed to scan batch current status: %w", err)
		}

		cs := &CurrentStatus{
			Status: mapMonitorState(currentState),
		}
		if inMaintenance {
			cs.Status = "maintenance"
		}
		if createdAt.Valid {
			t := createdAt.Time
			cs.LastCheckTime = &t
		}
		if httpStatus.Valid {
			v := int(httpStatus.Int64)
			cs.LastHTTPStatus = &v
		}
		if latencyMS.Valid {
			v := int(latencyMS.Int64)
			cs.LastLatency = &v
		}
		if len(metricsJSON) > 0 {
			var env httpMetricsEnvelope
			if err := json.Unmarshal(metricsJSON, &env); err == nil && env.HTTP != nil && env.HTTP.TLS != nil {
				if env.HTTP.TLS.DaysUntilExpiry != nil {
					cs.TLSDaysUntilExpiry = env.HTTP.TLS.DaysUntilExpiry
				}
				if env.HTTP.TLS.NotAfter != "" {
					cs.TLSNotAfter = env.HTTP.TLS.NotAfter
				}
			}
		}
		result[monitorID] = cs
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating batch current status: %w", err)
	}
	return result, nil
}

// batchOpenTLSExpiryAlerts returns the set of monitors with an open tls_expiry
// alert, i.e. whose certificate has fewer remaining validity days than the
// monitor's tls_min_days_valid threshold.
func (s *Service) batchOpenTLSExpiryAlerts(ctx context.Context, monitorIDs []uuid.UUID, tenantID uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT monitor_id FROM alerts
		WHERE tenant_id = $2 AND monitor_id = ANY($1)
		  AND kind = 'tls_expiry' AND status IN ('active', 'acknowledged')
	`, pq.Array(monitorIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("batch open tls expiry alerts: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]bool)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan tls expiry alert monitor: %w", err)
		}
		result[id] = true
	}
	return result, rows.Err()
}

// mapMonitorState maps the persisted state-machine value to the public status-page vocabulary.
// "down" is the only confirmed-outage state and maps to "down".
// "suspect" must NOT show as down (unconfirmed blip) — it maps to "up" to avoid public flapping.
// "degraded" (some locations of a multi-location monitor down, below quorum) maps to the page's
// existing "degraded" bucket. "up" maps to "up" and "unknown" (no data yet) maps to "unknown".
func mapMonitorState(state string) string {
	switch state {
	case "down":
		return "down"
	case "degraded":
		return "degraded"
	case "up", "suspect":
		return "up"
	default: // "unknown" or anything unexpected
		return "unknown"
	}
}

// batchUptimeSummary computes 24h/1h uptime and 1h/24h average latency for each monitor in one
// query. Equivalent to CalculateUptime24h + CalculateUptime1h + CalculateAvgLatency(1h/24h).
//
// The rollup cursor is read first and all window bounds are computed in Go and passed as
// parameters: bounds derived from a CTE join on rollup_job_state cannot be pushed into index
// conditions on check_results.created_at and degraded the raw branches to full sequential scans.
// The raw 24h complement is the window minus the rollup region [leading_edge_end, rollup_end),
// selected as two disjoint indexed ranges: [w_start, leading_edge_end) and
// [GREATEST(rollup_end, leading_edge_end), w_end).
func (s *Service) batchUptimeSummary(ctx context.Context, monitorIDs []uuid.UUID, tenantID uuid.UUID) (map[uuid.UUID]*uptimeSummary, error) {
	cursor, err := sharedanalytics.LoadRollupCursor(ctx, s.db)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	wStart := now.Add(-24 * time.Hour)
	wEnd := now
	oneHourStart := now.Add(-1 * time.Hour)
	leadingEdgeEnd := wStart.Truncate(time.Hour).Add(time.Hour)
	trailingEdgeStart := wEnd.Truncate(time.Hour)
	rollupEnd := cursor.RollupEnd(leadingEdgeEnd, trailingEdgeStart)
	rawTailStart := cursor.RawTailStart(leadingEdgeEnd, trailingEdgeStart)

	query := `
		WITH mons AS (
			SELECT unnest($2::uuid[]) AS monitor_id
		),
		rollup_24h AS (
			SELECT
				mhr.monitor_id,
				SUM(mhr.total_checks)::bigint AS total_checks,
				SUM(mhr.success_checks)::bigint AS success_checks,
				SUM(mhr.latency_success_sum_ms) AS latency_sum_ms,
				SUM(mhr.latency_success_count)::bigint AS latency_count
			FROM monitor_hourly_rollups mhr
			WHERE mhr.tenant_id = $1
			  AND mhr.monitor_id = ANY($2)
			  AND mhr.bucket_hour >= $6
			  AND mhr.bucket_hour < $7
			GROUP BY mhr.monitor_id
		),
		raw_24h AS (
			SELECT
				er.monitor_id,
				COUNT(*)::bigint AS total_checks,
				COUNT(*) FILTER (WHERE er.status = 'success')::bigint AS success_checks,
				COALESCE(SUM(er.latency_ms) FILTER (WHERE er.status = 'success' AND er.latency_ms IS NOT NULL), 0)::double precision AS latency_sum_ms,
				COUNT(er.latency_ms) FILTER (WHERE er.status = 'success')::bigint AS latency_count
			FROM (
				SELECT cr.monitor_id, cr.status, cr.latency_ms
				FROM check_results cr
				WHERE cr.tenant_id = $1
				  AND cr.monitor_id = ANY($2)
				  AND cr.result_source <> 'platform'
				  AND cr.created_at >= $3
				  AND cr.created_at < $6

				UNION ALL

				SELECT cr.monitor_id, cr.status, cr.latency_ms
				FROM check_results cr
				WHERE cr.tenant_id = $1
				  AND cr.monitor_id = ANY($2)
				  AND cr.result_source <> 'platform'
				  AND cr.created_at >= $8
				  AND cr.created_at < $4
			) er
			GROUP BY er.monitor_id
		),
		raw_1h AS (
			SELECT
				cr.monitor_id,
				COUNT(*)::bigint AS total_checks,
				COUNT(*) FILTER (WHERE cr.status = 'success')::bigint AS success_checks,
				AVG(cr.latency_ms) FILTER (WHERE cr.status = 'success' AND cr.latency_ms IS NOT NULL) AS avg_latency
			FROM check_results cr
			WHERE cr.tenant_id = $1
			  AND cr.monitor_id = ANY($2)
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $5
			  AND cr.created_at < $4
			GROUP BY cr.monitor_id
		)
		SELECT
			m.monitor_id,
			COALESCE(r24.total_checks, 0) + COALESCE(raw24.total_checks, 0) AS total_24h,
			COALESCE(r24.success_checks, 0) + COALESCE(raw24.success_checks, 0) AS success_24h,
			COALESCE(r1.total_checks, 0) AS total_1h,
			COALESCE(r1.success_checks, 0) AS success_1h,
			r1.avg_latency AS avg_latency_1h,
			CASE
				WHEN COALESCE(r24.latency_count, 0) + COALESCE(raw24.latency_count, 0) > 0
					THEN (COALESCE(r24.latency_sum_ms, 0) + COALESCE(raw24.latency_sum_ms, 0))
						/ (COALESCE(r24.latency_count, 0) + COALESCE(raw24.latency_count, 0))::double precision
				ELSE NULL
			END AS avg_latency_24h
		FROM mons m
		LEFT JOIN rollup_24h r24 ON r24.monitor_id = m.monitor_id
		LEFT JOIN raw_24h raw24 ON raw24.monitor_id = m.monitor_id
		LEFT JOIN raw_1h r1 ON r1.monitor_id = m.monitor_id
		ORDER BY m.monitor_id
	`
	rows, err := s.db.QueryContext(ctx, query, tenantID, pq.Array(monitorIDs), wStart, wEnd, oneHourStart, leadingEdgeEnd, rollupEnd, rawTailStart)
	if err != nil {
		return nil, fmt.Errorf("failed to batch uptime summary: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]*uptimeSummary)
	for rows.Next() {
		var monitorID uuid.UUID
		var total24, success24, total1, success1 int
		var avgLatency1h, avgLatency24h sql.NullFloat64
		if err := rows.Scan(&monitorID, &total24, &success24, &total1, &success1, &avgLatency1h, &avgLatency24h); err != nil {
			return nil, fmt.Errorf("failed to scan batch uptime summary: %w", err)
		}
		summary := &uptimeSummary{}
		if total24 > 0 {
			summary.Uptime24h = ptrFloat(float64(success24) / float64(total24) * 100.0)
		}
		if total1 > 0 {
			summary.Uptime1h = ptrFloat(float64(success1) / float64(total1) * 100.0)
		}
		if avgLatency1h.Valid {
			summary.AvgLatency1h = ptrFloat(avgLatency1h.Float64)
		}
		if avgLatency24h.Valid {
			summary.AvgLatency24h = ptrFloat(avgLatency24h.Float64)
		}
		result[monitorID] = summary
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating batch uptime summary: %w", err)
	}
	return result, nil
}

// batchHourlyUptime computes the 24-hour hourly uptime strip for each monitor in one query.
// Each monitor gets a full 24-bucket series (empty hours carry -1), matching GetMonitorHourlyUptime.
//
// The rollup cursor is read first and the hour-strip bounds are passed as parameters so the
// raw past-cursor scan starts at an index-friendly constant ($5) instead of a CTE-join-derived
// bound the planner could not push down.
func (s *Service) batchHourlyUptime(ctx context.Context, monitorIDs []uuid.UUID, tenantID uuid.UUID) (map[uuid.UUID][]HourlyUptime, error) {
	cursor, err := sharedanalytics.LoadRollupCursor(ctx, s.db)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	startHour := now.Truncate(time.Hour).Add(-23 * time.Hour)
	endExclusive := now.Truncate(time.Hour).Add(time.Hour)
	rawStart := cursor.RawStart(startHour)

	query := `
		WITH mons AS (
			SELECT unnest($2::uuid[]) AS monitor_id
		),
		rollup_stats AS (
			SELECT
				mhr.monitor_id,
				mhr.bucket_hour AS hour,
				SUM(mhr.total_checks)::bigint AS total,
				SUM(mhr.success_checks)::bigint AS successful
			FROM monitor_hourly_rollups mhr
			WHERE mhr.tenant_id = $1
			  AND mhr.monitor_id = ANY($2)
			  AND mhr.bucket_hour >= $3
			  AND mhr.bucket_hour < $4
			GROUP BY mhr.monitor_id, mhr.bucket_hour
		),
		raw_stats AS (
			SELECT
				cr.monitor_id,
				date_trunc('hour', cr.created_at) AS hour,
				COUNT(*)::bigint AS total,
				COUNT(*) FILTER (WHERE cr.status = 'success')::bigint AS successful
			FROM check_results cr
			WHERE cr.tenant_id = $1
			  AND cr.monitor_id = ANY($2)
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $5
			  AND cr.created_at < $4
			  AND (
				$6::timestamptz IS NULL
				OR (cr.created_at, cr.id) > ($6::timestamptz, $7::uuid)
			  )
			GROUP BY cr.monitor_id, date_trunc('hour', cr.created_at)
		)
		SELECT
			m.monitor_id,
			h.hour,
			COALESCE(rs.total, 0) + COALESCE(raw.total, 0) AS total,
			COALESCE(rs.successful, 0) + COALESCE(raw.successful, 0) AS successful
		FROM mons m
		CROSS JOIN generate_series($3::timestamptz, $4::timestamptz - INTERVAL '1 hour', INTERVAL '1 hour') AS h(hour)
		LEFT JOIN rollup_stats rs ON rs.monitor_id = m.monitor_id AND rs.hour = h.hour
		LEFT JOIN raw_stats raw ON raw.monitor_id = m.monitor_id AND raw.hour = h.hour
		ORDER BY m.monitor_id, h.hour
	`
	rows, err := s.db.QueryContext(ctx, query, tenantID, pq.Array(monitorIDs), startHour, endExclusive, rawStart, cursor.LastCreatedAt, cursor.LastCheckResultID)
	if err != nil {
		return nil, fmt.Errorf("failed to batch hourly uptime: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID][]HourlyUptime)
	for rows.Next() {
		var monitorID uuid.UUID
		var hour time.Time
		var total, successful int
		if err := rows.Scan(&monitorID, &hour, &total, &successful); err != nil {
			return nil, fmt.Errorf("failed to scan batch hourly uptime: %w", err)
		}
		uptime := -1.0
		if total > 0 {
			uptime = float64(successful) / float64(total) * 100.0
		}
		result[monitorID] = append(result[monitorID], HourlyUptime{
			Hour:   hour.Format("15:04"),
			Uptime: uptime,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating batch hourly uptime: %w", err)
	}
	return result, nil
}

// batchHistory loads the most recent 50 check results (last 24h, chronological) for each monitor
// in one query. Equivalent to GetMonitorHistory(monitorID, 50, nil) per monitor.
func (s *Service) batchHistory(ctx context.Context, monitorIDs []uuid.UUID, tenantID uuid.UUID) (map[uuid.UUID][]CheckResultHistory, error) {
	query := `
		SELECT m.monitor_id, cr.status, cr.latency_ms, cr.created_at
		FROM unnest($1::uuid[]) AS m(monitor_id)
		CROSS JOIN LATERAL (
			SELECT cr.status, cr.latency_ms, cr.created_at
			FROM check_results cr
			WHERE cr.monitor_id = m.monitor_id
			  AND cr.tenant_id = $2
			  AND cr.created_at >= NOW() - INTERVAL '24 hours'
			ORDER BY cr.created_at DESC
			LIMIT 50
		) cr
		ORDER BY m.monitor_id, cr.created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, pq.Array(monitorIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to batch history: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID][]CheckResultHistory)
	for rows.Next() {
		var monitorID uuid.UUID
		var status string
		var latencyMS sql.NullInt64
		var createdAt time.Time
		if err := rows.Scan(&monitorID, &status, &latencyMS, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan batch history: %w", err)
		}
		entry := CheckResultHistory{
			Timestamp: createdAt,
			Status:    mapResultStatus(status),
		}
		if latencyMS.Valid {
			v := int(latencyMS.Int64)
			entry.LatencyMS = &v
		}
		result[monitorID] = append(result[monitorID], entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating batch history: %w", err)
	}
	return result, nil
}

func ptrFloat(v float64) *float64 {
	return &v
}

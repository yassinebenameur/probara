package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/db"
)

const (
	defaultFailuresLimit = 10
	defaultAlertsLimit   = 10
	maxListLimit         = 50
	problemMonitorLimit  = 5
)

// Service handles dashboard aggregation logic.
type Service struct {
	db           db.DB
	alertService alertservice.AlertService
	analytics    *sharedanalytics.Repository
}

// NewService creates a new dashboard service.
func NewService(database db.DB, alerts alertservice.AlertService) *Service {
	return &Service{
		db:           database,
		alertService: alerts,
		analytics:    sharedanalytics.NewRepository(database),
	}
}

// GetOverview returns dashboard overview data for a tenant.
func (s *Service) GetOverview(ctx context.Context, tenantID uuid.UUID, params *models.DashboardOverviewQuery) (*models.DashboardOverviewResponse, error) {
	normalized := normalizeOverviewParams(params)
	rangeStart, rangeEnd, bucketDuration, err := rangeBounds(normalized.Range)
	if err != nil {
		return nil, err
	}
	rangeEndExclusive := rangeEnd.Add(bucketDuration)

	availableTags, err := s.getAvailableTags(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	stats, err := s.getStats(ctx, tenantID, normalized.Range, rangeStart, rangeEndExclusive, normalized.Tags)
	if err != nil {
		return nil, err
	}

	trend, err := s.getTrend(ctx, tenantID, normalized.Range, rangeStart, rangeEnd, normalized.Tags)
	if err != nil {
		return nil, err
	}

	activity := []models.DashboardActivityHour{}
	if normalized.Range == models.DashboardRange24h {
		activity, err = s.getActivity24h(ctx, tenantID, normalized.Tags)
		if err != nil {
			return nil, err
		}
	}

	monitorHealth, err := s.getMonitorHealth(ctx, tenantID, normalized.Range, rangeStart, rangeEndExclusive, normalized.Tags)
	if err != nil {
		return nil, err
	}

	opsSummary, err := s.getOpsSummary(ctx, tenantID, monitorHealth, normalized.Tags)
	if err != nil {
		return nil, err
	}

	problemMonitors, err := s.getProblemMonitors(ctx, tenantID, normalized.Range, rangeStart, rangeEndExclusive, problemMonitorLimit, normalized.Tags)
	if err != nil {
		return nil, err
	}

	recentFailures, err := s.getRecentFailures(ctx, tenantID, rangeStart, rangeEndExclusive, normalized.FailuresLimit, normalized.Tags)
	if err != nil {
		return nil, err
	}

	recentAlerts := []models.AlertWithDetails{}
	if s.alertService != nil {
		recentAlerts, err = s.alertService.GetRecentAlertsForTags(ctx, tenantID, normalized.Tags, normalized.AlertsLimit)
		if err != nil {
			return nil, fmt.Errorf("failed to get recent alerts: %w", err)
		}
	}

	return &models.DashboardOverviewResponse{
		Range:           normalized.Range,
		GeneratedAt:     time.Now().UTC(),
		AvailableTags:   availableTags,
		Stats:           stats,
		Trend:           trend,
		Activity24h:     activity,
		OpsSummary:      opsSummary,
		MonitorHealth:   monitorHealth,
		ProblemMonitors: problemMonitors,
		RecentFailures:  recentFailures,
		RecentAlerts:    recentAlerts,
	}, nil
}

func (s *Service) getStats(ctx context.Context, tenantID uuid.UUID, dashboardRange models.DashboardRange, rangeStart, rangeEnd time.Time, tags []string) (models.DashboardStats, error) {
	stats := models.DashboardStats{}

	countQuery := `
		SELECT
			COUNT(*) AS total_monitors,
			COUNT(*) FILTER (WHERE enabled) AS active_monitors,
			COUNT(*) FILTER (WHERE type = 'http') AS http_monitors,
			COUNT(*) FILTER (WHERE type = 'agent') AS agent_monitors
		FROM monitors
		WHERE tenant_id = $1
	`
	countArgs := []interface{}{tenantID}
	if len(tags) > 0 {
		countQuery += ` AND tags @> $2::text[]`
		countArgs = append(countArgs, pq.Array(tags))
	}
	if err := s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(
		&stats.TotalMonitors,
		&stats.ActiveMonitors,
		&stats.HTTPMonitors,
		&stats.AgentMonitors,
	); err != nil {
		return stats, fmt.Errorf("failed to query dashboard stats: %w", err)
	}

	if dashboardRange != models.DashboardRange24h {
		monitorIDs, err := s.listEnabledOperationalMonitorIDs(ctx, tenantID, tags)
		if err != nil {
			return stats, err
		}
		analyticsResult, err := s.analytics.GetScopeAnalytics(ctx, tenantID, monitorIDs, sharedanalytics.Range(dashboardRange), time.Now().UTC())
		if err != nil {
			return stats, fmt.Errorf("failed to query rollup-backed dashboard stats: %w", err)
		}
		stats.OverallUptime = analyticsResult.Summary.SLAPct
		if analyticsResult.Summary.AvgLatencyMS != nil {
			stats.AvgResponseMS = *analyticsResult.Summary.AvgLatencyMS
		}
		return stats, nil
	}

	uptimeQuery := `
		WITH per_monitor AS (
			SELECT
				cr.monitor_id,
				COUNT(*) AS total_checks,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS success_checks
			FROM check_results cr
			JOIN monitors m ON m.id = cr.monitor_id
			WHERE cr.tenant_id = $1
			  AND m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $2
			  AND cr.created_at < $3
			  %s
			GROUP BY cr.monitor_id
		)
		SELECT COALESCE(AVG((success_checks::float / NULLIF(total_checks, 0)) * 100.0), 0)
		FROM per_monitor
		WHERE total_checks > 0
	`
	uptimeTagClause := ""
	uptimeArgs := []interface{}{tenantID, rangeStart, rangeEnd}
	if len(tags) > 0 {
		uptimeTagClause = "AND m.tags @> $4::text[]"
		uptimeArgs = append(uptimeArgs, pq.Array(tags))
	}
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(uptimeQuery, uptimeTagClause), uptimeArgs...).Scan(&stats.OverallUptime); err != nil {
		return stats, fmt.Errorf("failed to query monitor-weighted uptime: %w", err)
	}

	latencyQuery := `
		WITH per_monitor AS (
			SELECT
				cr.monitor_id,
				AVG(cr.latency_ms::float) AS avg_latency
			FROM check_results cr
			JOIN monitors m ON m.id = cr.monitor_id
			WHERE cr.tenant_id = $1
			  AND m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $2
			  AND cr.created_at < $3
			  AND cr.status = 'success'
			  AND cr.latency_ms IS NOT NULL
			  %s
			GROUP BY cr.monitor_id
		)
		SELECT COALESCE(AVG(avg_latency), 0)
		FROM per_monitor
	`
	latencyTagClause := ""
	latencyArgs := []interface{}{tenantID, rangeStart, rangeEnd}
	if len(tags) > 0 {
		latencyTagClause = "AND m.tags @> $4::text[]"
		latencyArgs = append(latencyArgs, pq.Array(tags))
	}
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(latencyQuery, latencyTagClause), latencyArgs...).Scan(&stats.AvgResponseMS); err != nil {
		return stats, fmt.Errorf("failed to query monitor-weighted response time: %w", err)
	}

	return stats, nil
}

func (s *Service) getTrend(ctx context.Context, tenantID uuid.UUID, dashboardRange models.DashboardRange, rangeStart, rangeEnd time.Time, tags []string) ([]models.DashboardTrendPoint, error) {
	if dashboardRange != models.DashboardRange24h {
		monitorIDs, err := s.listEnabledOperationalMonitorIDs(ctx, tenantID, tags)
		if err != nil {
			return nil, err
		}
		analyticsResult, err := s.analytics.GetScopeAnalytics(ctx, tenantID, monitorIDs, sharedanalytics.Range(dashboardRange), time.Now().UTC())
		if err != nil {
			return nil, fmt.Errorf("failed to query rollup-backed dashboard trend: %w", err)
		}
		trend := make([]models.DashboardTrendPoint, 0, len(analyticsResult.Series))
		for _, point := range analyticsResult.Series {
			responseTime := 0.0
			if point.AvgLatencyMS != nil {
				responseTime = *point.AvgLatencyMS
			}
			trend = append(trend, models.DashboardTrendPoint{
				BucketStart:  point.BucketStart,
				Label:        formatTrendLabel(point.BucketStart, dashboardRange),
				Uptime:       point.UptimePct,
				ResponseTime: responseTime,
				TotalChecks:  point.TotalChecks,
			})
		}
		return trend, nil
	}

	unit := "day"
	interval := "1 day"
	if dashboardRange == models.DashboardRange24h {
		unit = "hour"
		interval = "1 hour"
	}

	endExclusive := rangeEnd.Add(24 * time.Hour)
	if dashboardRange == models.DashboardRange24h {
		endExclusive = rangeEnd.Add(time.Hour)
	}

	tagClause := ""
	args := []interface{}{tenantID, rangeStart, rangeEnd, endExclusive}
	if len(tags) > 0 {
		tagClause = "AND m.tags @> $5::text[]"
		args = append(args, pq.Array(tags))
	}

	query := fmt.Sprintf(`
		WITH buckets AS (
			SELECT generate_series($2::timestamptz, $3::timestamptz, INTERVAL '%s') AS bucket_start
		),
		per_monitor_bucket AS (
			SELECT
				date_trunc('%s', cr.created_at) AS bucket_start,
				cr.monitor_id,
				COUNT(*) AS total_checks,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS success_checks,
				AVG(cr.latency_ms::float) FILTER (WHERE cr.status = 'success' AND cr.latency_ms IS NOT NULL) AS avg_latency
			FROM check_results cr
			JOIN monitors m ON m.id = cr.monitor_id
			WHERE cr.tenant_id = $1
			  AND m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $2
			  AND cr.created_at < $4
			  %s
			GROUP BY 1, cr.monitor_id
		),
		bucket_agg AS (
			SELECT
				bucket_start,
				AVG((success_checks::float / NULLIF(total_checks, 0)) * 100.0) AS uptime,
				AVG(avg_latency) AS response_time,
				SUM(total_checks) AS total_checks
			FROM per_monitor_bucket
			GROUP BY bucket_start
		)
		SELECT
			b.bucket_start,
			COALESCE(ba.uptime, 0),
			COALESCE(ba.response_time, 0),
			COALESCE(ba.total_checks, 0)
		FROM buckets b
		LEFT JOIN bucket_agg ba ON ba.bucket_start = b.bucket_start
		ORDER BY b.bucket_start
	`, interval, unit, tagClause)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query trend data: %w", err)
	}
	defer rows.Close()

	trend := make([]models.DashboardTrendPoint, 0)
	for rows.Next() {
		var point models.DashboardTrendPoint
		if err := rows.Scan(&point.BucketStart, &point.Uptime, &point.ResponseTime, &point.TotalChecks); err != nil {
			return nil, fmt.Errorf("failed to scan trend point: %w", err)
		}
		point.Label = formatTrendLabel(point.BucketStart, dashboardRange)
		trend = append(trend, point)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating trend rows: %w", err)
	}

	return trend, nil
}

func (s *Service) getActivity24h(ctx context.Context, tenantID uuid.UUID, tags []string) ([]models.DashboardActivityHour, error) {
	now := time.Now().UTC()
	end := now.Truncate(time.Hour)
	start := end.Add(-23 * time.Hour)
	endExclusive := end.Add(time.Hour)

	tagClause := ""
	args := []interface{}{tenantID, start, end, endExclusive}
	if len(tags) > 0 {
		tagClause = "AND m.tags @> $5::text[]"
		args = append(args, pq.Array(tags))
	}

	query := fmt.Sprintf(`
		WITH buckets AS (
			SELECT generate_series($2::timestamptz, $3::timestamptz, INTERVAL '1 hour') AS bucket_start
		),
		hourly_stats AS (
			SELECT
				date_trunc('hour', cr.created_at) AS bucket_start,
				COUNT(*) AS checks,
				COUNT(*) FILTER (WHERE cr.status IN ('failure', 'error')) AS failures
			FROM check_results cr
			JOIN monitors m ON m.id = cr.monitor_id
			WHERE cr.tenant_id = $1
			  AND m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND cr.created_at >= $2
			  AND cr.created_at < $4
			  %s
			GROUP BY 1
		)
		SELECT
			b.bucket_start,
			COALESCE(hs.checks, 0) AS checks,
			COALESCE(hs.failures, 0) AS failures
		FROM buckets b
		LEFT JOIN hourly_stats hs ON hs.bucket_start = b.bucket_start
		ORDER BY b.bucket_start
	`, tagClause)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query 24-hour activity: %w", err)
	}
	defer rows.Close()

	activity := make([]models.DashboardActivityHour, 0)
	for rows.Next() {
		var point models.DashboardActivityHour
		if err := rows.Scan(&point.BucketStart, &point.Checks, &point.Failures); err != nil {
			return nil, fmt.Errorf("failed to scan 24-hour activity point: %w", err)
		}
		point.Label = point.BucketStart.UTC().Format("15")
		activity = append(activity, point)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating 24-hour activity rows: %w", err)
	}

	return activity, nil
}

func (s *Service) getMonitorHealth(ctx context.Context, tenantID uuid.UUID, dashboardRange models.DashboardRange, rangeStart, rangeEndExclusive time.Time, tags []string) ([]models.DashboardMonitorHealth, error) {
	args := []interface{}{tenantID, rangeStart, rangeEndExclusive}
	whereClause := "WHERE m.tenant_id = $1"
	if len(tags) > 0 {
		whereClause += " AND m.tags @> $4::text[]"
		args = append(args, pq.Array(tags))
	}

	query := fmt.Sprintf(`
		SELECT
			m.id,
			m.name,
			m.enabled,
			lr.status,
			lr.created_at
		FROM monitors m
		LEFT JOIN LATERAL (
			SELECT cr.status, cr.created_at
			FROM check_results cr
			WHERE cr.monitor_id = m.id
			  AND cr.tenant_id = m.tenant_id
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $2
			  AND cr.created_at < $3
			ORDER BY cr.created_at DESC
			LIMIT 1
		) lr ON TRUE
		%s
		ORDER BY m.name
	`, whereClause)
	if dashboardRange != models.DashboardRange24h {
		query = fmt.Sprintf(`
			SELECT
				m.id,
				m.name,
				m.enabled,
				lr.latest_status,
				lr.latest_check_at
			FROM monitors m
			LEFT JOIN LATERAL (
				SELECT mdr.latest_status, mdr.latest_check_at
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
		`, whereClause)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query monitor health: %w", err)
	}
	defer rows.Close()

	healthRows := make([]models.DashboardMonitorHealth, 0)
	for rows.Next() {
		var row models.DashboardMonitorHealth
		var latestStatus sql.NullString
		var latestCheckAt sql.NullTime

		if err := rows.Scan(&row.MonitorID, &row.MonitorName, &row.Enabled, &latestStatus, &latestCheckAt); err != nil {
			return nil, fmt.Errorf("failed to scan monitor health row: %w", err)
		}
		if latestStatus.Valid {
			row.LatestStatus = &latestStatus.String
		}
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

func (s *Service) getOpsSummary(ctx context.Context, tenantID uuid.UUID, monitorHealth []models.DashboardMonitorHealth, tags []string) (models.DashboardOpsSummary, error) {
	summary := models.DashboardOpsSummary{}
	for _, row := range monitorHealth {
		switch {
		case !row.Enabled:
			summary.PausedMonitors++
		case row.LatestStatus == nil:
			continue
		case *row.LatestStatus == "success":
			summary.UpMonitors++
		default:
			summary.DownMonitors++
		}
	}

	query := `
		SELECT
			COUNT(*) FILTER (WHERE a.status = 'active') AS active_alerts,
			COUNT(*) FILTER (WHERE a.status = 'acknowledged') AS acknowledged_alerts
		FROM alerts a
		JOIN monitors m ON m.id = a.monitor_id AND m.tenant_id = a.tenant_id
		WHERE a.tenant_id = $1
		  AND a.status IN ('active', 'acknowledged')
	`
	args := []interface{}{tenantID}
	if len(tags) > 0 {
		query += ` AND m.tags @> $2::text[]`
		args = append(args, pq.Array(tags))
	}
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&summary.ActiveAlerts, &summary.AcknowledgedAlerts); err != nil {
		return summary, fmt.Errorf("failed to query ops summary alerts: %w", err)
	}

	return summary, nil
}

func (s *Service) getProblemMonitors(ctx context.Context, tenantID uuid.UUID, dashboardRange models.DashboardRange, rangeStart, rangeEndExclusive time.Time, limit int, tags []string) ([]models.DashboardProblemMonitor, error) {
	if limit < 1 {
		limit = problemMonitorLimit
	}

	tagClause := ""
	limitPlaceholder := 4
	args := []interface{}{tenantID, rangeStart, rangeEndExclusive, limit}
	if len(tags) > 0 {
		tagClause = "AND m.tags @> $5::text[]"
		args = []interface{}{tenantID, rangeStart, rangeEndExclusive, limit, pq.Array(tags)}
	}

	monitorStatusJoin := `
		LEFT JOIN LATERAL (
			SELECT cr.status AS current_status
			FROM check_results cr
			WHERE cr.monitor_id = m.id
			  AND cr.tenant_id = m.tenant_id
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $2
			  AND cr.created_at < $3
			ORDER BY cr.created_at DESC
			LIMIT 1
		) cs ON TRUE
	`
	uptimeStats := `
		uptime_stats AS (
			SELECT
				cr.monitor_id,
				COUNT(*) AS total_checks,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS success_checks
			FROM check_results cr
			JOIN monitors m ON m.id = cr.monitor_id
			WHERE cr.tenant_id = $1
			  AND m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND m.type <> 'group'
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $2
			  AND cr.created_at < $3
			  %s
			GROUP BY cr.monitor_id
		),
	`
	if dashboardRange != models.DashboardRange24h {
		monitorStatusJoin = `
		LEFT JOIN LATERAL (
			SELECT mdr.latest_status AS current_status
			FROM monitor_daily_rollups mdr
			WHERE mdr.monitor_id = m.id
			  AND mdr.tenant_id = m.tenant_id
			  AND mdr.bucket_day >= $2::date
			  AND mdr.bucket_day < $3::date
			ORDER BY mdr.bucket_day DESC
			LIMIT 1
		) cs ON TRUE
	`
		uptimeStats = `
		uptime_stats AS (
			SELECT
				mdr.monitor_id,
				SUM(mdr.total_checks) AS total_checks,
				SUM(mdr.success_checks) AS success_checks
			FROM monitor_daily_rollups mdr
			JOIN monitors m ON m.id = mdr.monitor_id
			WHERE mdr.tenant_id = $1
			  AND m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND m.type <> 'group'
			  AND mdr.bucket_day >= $2::date
			  AND mdr.bucket_day < $3::date
			  %s
			GROUP BY mdr.monitor_id
		),
	`
	}

	query := fmt.Sprintf(`
		WITH
		failure_stats AS (
			SELECT
				cr.monitor_id,
				COUNT(*) FILTER (WHERE cr.status = 'failure') AS failure_count,
				COUNT(*) FILTER (WHERE cr.status = 'error') AS error_count,
				MAX(cr.created_at) FILTER (WHERE cr.status IN ('failure', 'error')) AS latest_failure_at
			FROM check_results cr
			JOIN monitors m ON m.id = cr.monitor_id
			WHERE cr.tenant_id = $1
			  AND m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND m.type <> 'group'
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $2
			  AND cr.created_at < $3
			  %s
			GROUP BY cr.monitor_id
		),
		%s
		ranked_monitors AS (
			SELECT
				m.id,
				m.name,
				cs.current_status,
				COALESCE(fs.failure_count, 0) AS failure_count,
				COALESCE(fs.error_count, 0) AS error_count,
				CASE
					WHEN COALESCE(us.total_checks, 0) > 0 THEN (us.success_checks::float / us.total_checks::float) * 100.0
					ELSE 0
				END AS uptime,
				fs.latest_failure_at
			FROM monitors m
			LEFT JOIN failure_stats fs ON fs.monitor_id = m.id
			LEFT JOIN uptime_stats us ON us.monitor_id = m.id
			%s
			WHERE m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND m.type <> 'group'
			  %s
			  AND (COALESCE(fs.failure_count, 0) + COALESCE(fs.error_count, 0)) > 0
		)
		SELECT id, name, current_status, failure_count, error_count, uptime, latest_failure_at
		FROM ranked_monitors
		ORDER BY (failure_count + error_count) DESC, latest_failure_at DESC NULLS LAST, name ASC
		LIMIT $%d
	`, tagClause, fmt.Sprintf(uptimeStats, tagClause), monitorStatusJoin, tagClause, limitPlaceholder)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query problem monitors: %w", err)
	}
	defer rows.Close()

	monitors := make([]models.DashboardProblemMonitor, 0)
	for rows.Next() {
		var row models.DashboardProblemMonitor
		var currentStatus sql.NullString
		var latestFailureAt sql.NullTime

		if err := rows.Scan(
			&row.MonitorID,
			&row.MonitorName,
			&currentStatus,
			&row.FailureCount,
			&row.ErrorCount,
			&row.Uptime,
			&latestFailureAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan problem monitor row: %w", err)
		}

		if currentStatus.Valid {
			row.CurrentStatus = &currentStatus.String
		}
		if latestFailureAt.Valid {
			ts := latestFailureAt.Time
			row.LatestFailureAt = &ts
		}
		monitors = append(monitors, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating problem monitor rows: %w", err)
	}

	return monitors, nil
}

func (s *Service) getRecentFailures(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEnd time.Time, limit int, tags []string) ([]models.DashboardFailureEvent, error) {
	query := `
		SELECT
			cr.id,
			cr.monitor_id,
			m.name,
			cr.status,
			cr.result_source,
			cr.error_message,
			cr.latency_ms,
			cr.created_at,
			rs.created_at AS resolved_at
		FROM check_results cr
		JOIN monitors m ON m.id = cr.monitor_id AND m.tenant_id = cr.tenant_id
		LEFT JOIN LATERAL (
			SELECT succ.created_at
			FROM check_results succ
			WHERE succ.tenant_id = cr.tenant_id
			  AND succ.monitor_id = cr.monitor_id
			  AND succ.status = 'success'
			  AND succ.created_at > cr.created_at
			ORDER BY succ.created_at ASC
			LIMIT 1
		) rs ON TRUE
		WHERE cr.tenant_id = $1
		  AND cr.status IN ('failure', 'error')
		  AND cr.created_at >= $2
		  AND cr.created_at < $3
		  %s
		ORDER BY cr.created_at DESC
		LIMIT $%d
	`

	tagClause := ""
	limitPlaceholder := 4
	args := []interface{}{tenantID, rangeStart, rangeEnd, limit}
	if len(tags) > 0 {
		tagClause = "AND m.tags @> $5::text[]"
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
			  AND tags @> $2::text[]
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

func normalizeOverviewParams(params *models.DashboardOverviewQuery) models.DashboardOverviewQuery {
	normalized := models.DashboardOverviewQuery{
		Range:         models.DashboardRange24h,
		FailuresLimit: defaultFailuresLimit,
		AlertsLimit:   defaultAlertsLimit,
	}
	if params == nil {
		return normalized
	}

	switch params.Range {
	case models.DashboardRange24h, models.DashboardRange7d, models.DashboardRange30d, models.DashboardRange90d, models.DashboardRange365d:
		normalized.Range = params.Range
	}

	if params.FailuresLimit > 0 {
		normalized.FailuresLimit = params.FailuresLimit
	}
	if normalized.FailuresLimit > maxListLimit {
		normalized.FailuresLimit = maxListLimit
	}

	if params.AlertsLimit > 0 {
		normalized.AlertsLimit = params.AlertsLimit
	}
	if normalized.AlertsLimit > maxListLimit {
		normalized.AlertsLimit = maxListLimit
	}

	if len(params.Tags) > 0 {
		tagSet := make(map[string]struct{}, len(params.Tags))
		for _, tag := range params.Tags {
			trimmed := strings.TrimSpace(tag)
			if trimmed == "" {
				continue
			}
			tagSet[trimmed] = struct{}{}
		}
		if len(tagSet) > 0 {
			normalized.Tags = make([]string, 0, len(tagSet))
			for tag := range tagSet {
				normalized.Tags = append(normalized.Tags, tag)
			}
			sort.Strings(normalized.Tags)
		}
	}

	return normalized
}

func resolveFailureState(resolvedAt *time.Time) models.DashboardFailureState {
	if resolvedAt != nil {
		return models.DashboardFailureStateResolved
	}
	return models.DashboardFailureStateFiring
}

func rangeBounds(rangeValue models.DashboardRange) (time.Time, time.Time, time.Duration, error) {
	now := time.Now().UTC()
	switch rangeValue {
	case models.DashboardRange24h:
		end := now.Truncate(time.Hour)
		return end.Add(-23 * time.Hour), end, time.Hour, nil
	case models.DashboardRange7d:
		end := truncateDayUTC(now)
		return end.AddDate(0, 0, -6), end, 24 * time.Hour, nil
	case models.DashboardRange30d:
		end := truncateDayUTC(now)
		return end.AddDate(0, 0, -29), end, 24 * time.Hour, nil
	case models.DashboardRange90d:
		end := truncateDayUTC(now)
		return end.AddDate(0, 0, -89), end, 24 * time.Hour, nil
	case models.DashboardRange365d:
		end := truncateDayUTC(now)
		return end.AddDate(0, 0, -364), end, 24 * time.Hour, nil
	default:
		return time.Time{}, time.Time{}, 0, fmt.Errorf("invalid dashboard range: %s", rangeValue)
	}
}

func truncateDayUTC(t time.Time) time.Time {
	return time.Date(t.UTC().Year(), t.UTC().Month(), t.UTC().Day(), 0, 0, 0, 0, time.UTC)
}

func formatTrendLabel(bucketStart time.Time, rangeValue models.DashboardRange) string {
	switch rangeValue {
	case models.DashboardRange24h:
		return bucketStart.UTC().Format("15")
	case models.DashboardRange7d:
		return bucketStart.UTC().Format("Mon")
	case models.DashboardRange30d, models.DashboardRange90d, models.DashboardRange365d:
		return bucketStart.UTC().Format("Jan 2")
	default:
		return bucketStart.UTC().Format("Jan 2")
	}
}

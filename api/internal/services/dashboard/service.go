package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/shared/db"
)

const (
	defaultFailuresLimit = 10
	defaultAlertsLimit   = 10
	maxListLimit         = 50
)

// Service handles dashboard aggregation logic.
type Service struct {
	db           db.DB
	alertService alertservice.AlertService
}

// NewService creates a new dashboard service.
func NewService(database db.DB, alerts alertservice.AlertService) *Service {
	return &Service{
		db:           database,
		alertService: alerts,
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

	stats, err := s.getStats(ctx, tenantID, rangeStart, rangeEndExclusive)
	if err != nil {
		return nil, err
	}

	trend, err := s.getTrend(ctx, tenantID, normalized.Range, rangeStart, rangeEnd)
	if err != nil {
		return nil, err
	}

	activity, err := s.getActivity24h(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	monitorHealth, err := s.getMonitorHealth(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	recentFailures, err := s.getRecentFailures(ctx, tenantID, rangeStart, rangeEndExclusive, normalized.FailuresLimit)
	if err != nil {
		return nil, err
	}

	recentAlerts := []models.AlertWithDetails{}
	if s.alertService != nil {
		recentAlerts, err = s.alertService.GetRecentAlerts(ctx, tenantID, normalized.AlertsLimit)
		if err != nil {
			return nil, fmt.Errorf("failed to get recent alerts: %w", err)
		}
	}

	return &models.DashboardOverviewResponse{
		Range:          normalized.Range,
		GeneratedAt:    time.Now().UTC(),
		Stats:          stats,
		Trend:          trend,
		Activity24h:    activity,
		MonitorHealth:  monitorHealth,
		RecentFailures: recentFailures,
		RecentAlerts:   recentAlerts,
	}, nil
}

func (s *Service) getStats(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEnd time.Time) (models.DashboardStats, error) {
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
	if err := s.db.QueryRowContext(ctx, countQuery, tenantID).Scan(
		&stats.TotalMonitors,
		&stats.ActiveMonitors,
		&stats.HTTPMonitors,
		&stats.AgentMonitors,
	); err != nil {
		return stats, fmt.Errorf("failed to query dashboard stats: %w", err)
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
			GROUP BY cr.monitor_id
		)
		SELECT COALESCE(AVG((success_checks::float / NULLIF(total_checks, 0)) * 100.0), 0)
		FROM per_monitor
		WHERE total_checks > 0
	`
	if err := s.db.QueryRowContext(ctx, uptimeQuery, tenantID, rangeStart, rangeEnd).Scan(&stats.OverallUptime); err != nil {
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
			GROUP BY cr.monitor_id
		)
		SELECT COALESCE(AVG(avg_latency), 0)
		FROM per_monitor
	`
	if err := s.db.QueryRowContext(ctx, latencyQuery, tenantID, rangeStart, rangeEnd).Scan(&stats.AvgResponseMS); err != nil {
		return stats, fmt.Errorf("failed to query monitor-weighted response time: %w", err)
	}

	return stats, nil
}

func (s *Service) getTrend(ctx context.Context, tenantID uuid.UUID, dashboardRange models.DashboardRange, rangeStart, rangeEnd time.Time) ([]models.DashboardTrendPoint, error) {
	unit := "day"
	interval := "1 day"
	if dashboardRange == models.DashboardRange24h {
		unit = "hour"
		interval = "1 hour"
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
	`, interval, unit)

	endExclusive := rangeEnd.Add(24 * time.Hour)
	if dashboardRange == models.DashboardRange24h {
		endExclusive = rangeEnd.Add(time.Hour)
	}

	rows, err := s.db.QueryContext(ctx, query, tenantID, rangeStart, rangeEnd, endExclusive)
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

func (s *Service) getActivity24h(ctx context.Context, tenantID uuid.UUID) ([]models.DashboardActivityHour, error) {
	now := time.Now().UTC()
	end := now.Truncate(time.Hour)
	start := end.Add(-23 * time.Hour)
	endExclusive := end.Add(time.Hour)

	query := `
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
			GROUP BY 1
		)
		SELECT
			b.bucket_start,
			COALESCE(hs.checks, 0) AS checks,
			COALESCE(hs.failures, 0) AS failures
		FROM buckets b
		LEFT JOIN hourly_stats hs ON hs.bucket_start = b.bucket_start
		ORDER BY b.bucket_start
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID, start, end, endExclusive)
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

func (s *Service) getMonitorHealth(ctx context.Context, tenantID uuid.UUID) ([]models.DashboardMonitorHealth, error) {
	query := `
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
			ORDER BY cr.created_at DESC
			LIMIT 1
		) lr ON TRUE
		WHERE m.tenant_id = $1
		ORDER BY m.name
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID)
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

func (s *Service) getRecentFailures(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEnd time.Time, limit int) ([]models.DashboardFailureEvent, error) {
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
		ORDER BY cr.created_at DESC
		LIMIT $4
	`

	rows, err := s.db.QueryContext(ctx, query, tenantID, rangeStart, rangeEnd, limit)
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

func normalizeOverviewParams(params *models.DashboardOverviewQuery) models.DashboardOverviewQuery {
	normalized := models.DashboardOverviewQuery{
		Range:         models.DashboardRange7d,
		FailuresLimit: defaultFailuresLimit,
		AlertsLimit:   defaultAlertsLimit,
	}
	if params == nil {
		return normalized
	}

	switch params.Range {
	case models.DashboardRange24h, models.DashboardRange7d, models.DashboardRange30d:
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
	default:
		return bucketStart.UTC().Format("Jan 2")
	}
}

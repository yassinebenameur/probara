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
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/maintenance"
)

const (
	defaultFailuresLimit = 10
	defaultAlertsLimit   = 10
	maxListLimit         = 50
	problemMonitorLimit  = 5
	problemCandidateMult = 4
)

// rollupCursorLagWarnThreshold is how far the hourly-rollup cursor may trail
// "now" before the dashboard logs a warning: past this point every rolling-24h
// computation is scanning raw check_results for the whole lag tail, which is
// exactly the load the rollups exist to avoid. The window itself is never
// truncated — results stay correct, only slower.
const rollupCursorLagWarnThreshold = 2 * time.Hour

// tenantSettingsReader is the minimal slice of the tenant service used by the dashboard
// service for resolving the curated dashboard_group_tags list.
type tenantSettingsReader interface {
	GetTenantSettings(ctx context.Context, tenantID uuid.UUID) (*models.TenantSettings, error)
}

// Service handles dashboard aggregation logic.
type Service struct {
	db           db.DB
	alertService alertservice.AlertService
	analytics    sharedanalytics.Reader
	tenants      tenantSettingsReader
	log          *logger.Logger // optional; nil disables service-level logging
	rolling24h   *rolling24hCache
}

// NewService creates a new dashboard service. log may be nil (e.g. in tests);
// it is only used for operational warnings such as rollup-cursor lag.
func NewService(database db.DB, alerts alertservice.AlertService, analytics sharedanalytics.Reader, tenants tenantSettingsReader, log *logger.Logger) *Service {
	return &Service{
		db:           database,
		alertService: alerts,
		analytics:    analytics,
		tenants:      tenants,
		log:          log,
		rolling24h:   newRolling24hCache(rolling24hCacheTTL),
	}
}

// GetOverview returns dashboard overview data for a tenant.
func (s *Service) GetOverview(ctx context.Context, tenantID uuid.UUID, params *models.DashboardOverviewQuery) (*models.DashboardOverviewResponse, error) {
	normalized := normalizeOverviewParams(params)

	summary, err := s.GetSummary(ctx, tenantID, &models.DashboardOverviewQuery{
		Range: normalized.Range,
		Tags:  normalized.Tags,
	})
	if err != nil {
		return nil, err
	}

	problemMonitors, err := s.GetProblemMonitors(ctx, tenantID, &models.DashboardListQuery{
		Range: normalized.Range,
		Limit: problemMonitorLimit,
		Tags:  normalized.Tags,
	})
	if err != nil {
		return nil, err
	}

	recentFailures, err := s.GetRecentFailures(ctx, tenantID, &models.DashboardListQuery{
		Range: normalized.Range,
		Limit: normalized.FailuresLimit,
		Tags:  normalized.Tags,
	})
	if err != nil {
		return nil, err
	}

	recentAlerts, err := s.GetRecentAlerts(ctx, tenantID, &models.DashboardListQuery{
		Range: normalized.Range,
		Limit: normalized.AlertsLimit,
		Tags:  normalized.Tags,
	})
	if err != nil {
		return nil, err
	}

	return &models.DashboardOverviewResponse{
		Range:           summary.Range,
		GeneratedAt:     summary.GeneratedAt,
		AvailableTags:   summary.AvailableTags,
		Stats:           summary.Stats,
		Trend:           summary.Trend,
		Activity24h:     summary.Activity24h,
		OpsSummary:      summary.OpsSummary,
		MonitorHealth:   summary.MonitorHealth,
		ProblemMonitors: problemMonitors.ProblemMonitors,
		RecentFailures:  recentFailures.RecentFailures,
		RecentAlerts:    recentAlerts.RecentAlerts,
	}, nil
}

// rolling24hData holds the DB-heavy exact-rolling-24h aggregates that several
// dashboard sections (stats, trend, activity, groups) all derive from. For the
// 24h range GetSummary computes these once and shares them across sections so
// the same expensive queries don't run multiple times per request. It is nil
// for every other range.
type rolling24hData struct {
	totals       map[uuid.UUID]MonitorRolling24hTotals
	hourlySeries []HourlyBucketPoint
}

// GetSummary returns lightweight dashboard data for first paint.
func (s *Service) GetSummary(ctx context.Context, tenantID uuid.UUID, params *models.DashboardOverviewQuery) (*models.DashboardSummaryResponse, error) {
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

	// For the 24h range, compute the shared exact-rolling totals and the hourly
	// bucket series once at a single "now" and reuse them across stats, trend,
	// activity, and groups instead of re-running each heavy query per section.
	var precomp *rolling24hData
	if normalized.Range == models.DashboardRange24h {
		precomp, _, err = s.loadRolling24hData(ctx, tenantID, normalized.Tags)
		if err != nil {
			return nil, err
		}
	}

	stats, err := s.getStats(ctx, tenantID, normalized.Range, rangeStart, rangeEndExclusive, normalized.Tags, precomp)
	if err != nil {
		return nil, err
	}

	trend, err := s.getTrend(ctx, tenantID, normalized.Range, rangeStart, rangeEnd, normalized.Tags, precomp)
	if err != nil {
		return nil, err
	}

	activity := []models.DashboardActivityHour{}
	if normalized.Range == models.DashboardRange24h {
		activity, err = s.getActivity24h(ctx, tenantID, normalized.Tags, precomp)
		if err != nil {
			return nil, err
		}
	}

	monitorHealth, err := s.getMonitorHealth(ctx, tenantID, normalized.Range, rangeStart, rangeEndExclusive, normalized.Tags, precomp)
	if err != nil {
		return nil, err
	}

	opsSummary, err := s.getOpsSummary(ctx, tenantID, monitorHealth, normalized.Tags)
	if err != nil {
		return nil, err
	}

	settings, err := s.tenants.GetTenantSettings(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to load tenant settings for groups: %w", err)
	}
	groupTags := settings.DashboardGroupTags
	if groupTags == nil {
		groupTags = []string{}
	}

	groups, err := s.loadGroups(ctx, tenantID, normalized.Range, rangeStart, rangeEndExclusive, normalized.Tags, groupTags, precomp)
	if err != nil {
		return nil, err
	}

	return &models.DashboardSummaryResponse{
		Range:         normalized.Range,
		GeneratedAt:   time.Now().UTC(),
		AvailableTags: availableTags,
		GroupTags:     groupTags,
		Stats:         stats,
		Trend:         trend,
		Activity24h:   activity,
		OpsSummary:    opsSummary,
		MonitorHealth: monitorHealth,
		Groups:        groups,
	}, nil
}

// GetProblemMonitors returns the problem monitors dashboard section.
func (s *Service) GetProblemMonitors(ctx context.Context, tenantID uuid.UUID, params *models.DashboardListQuery) (*models.DashboardProblemMonitorsResponse, error) {
	normalized := normalizeListParams(params, problemMonitorLimit)
	rangeStart, rangeEnd, bucketDuration, err := rangeBounds(normalized.Range)
	if err != nil {
		return nil, err
	}
	rangeEndExclusive := rangeEnd.Add(bucketDuration)

	problemMonitors, err := s.getProblemMonitors(ctx, tenantID, normalized.Range, rangeStart, rangeEndExclusive, normalized.Limit, normalized.Tags)
	if err != nil {
		return nil, err
	}

	return &models.DashboardProblemMonitorsResponse{
		Range:           normalized.Range,
		GeneratedAt:     time.Now().UTC(),
		ProblemMonitors: problemMonitors,
	}, nil
}

// GetRecentFailures returns the recent failures dashboard section.
func (s *Service) GetRecentFailures(ctx context.Context, tenantID uuid.UUID, params *models.DashboardListQuery) (*models.DashboardRecentFailuresResponse, error) {
	normalized := normalizeListParams(params, defaultFailuresLimit)
	rangeStart, rangeEnd, bucketDuration, err := rangeBounds(normalized.Range)
	if err != nil {
		return nil, err
	}
	rangeEndExclusive := rangeEnd.Add(bucketDuration)

	recentFailures, err := s.getRecentFailures(ctx, tenantID, rangeStart, rangeEndExclusive, normalized.Limit, normalized.Tags)
	if err != nil {
		return nil, err
	}

	return &models.DashboardRecentFailuresResponse{
		Range:          normalized.Range,
		GeneratedAt:    time.Now().UTC(),
		RecentFailures: recentFailures,
	}, nil
}

// GetRecentAlerts returns the recent alerts dashboard section.
func (s *Service) GetRecentAlerts(ctx context.Context, tenantID uuid.UUID, params *models.DashboardListQuery) (*models.DashboardRecentAlertsResponse, error) {
	normalized := normalizeListParams(params, defaultAlertsLimit)

	recentAlerts := []models.AlertWithDetails{}
	if s.alertService != nil {
		alerts, err := s.alertService.GetRecentAlertsForTags(ctx, tenantID, normalized.Tags, normalized.Limit)
		if err != nil {
			return nil, fmt.Errorf("failed to get recent alerts: %w", err)
		}
		recentAlerts = alerts
	}

	return &models.DashboardRecentAlertsResponse{
		Range:        normalized.Range,
		GeneratedAt:  time.Now().UTC(),
		RecentAlerts: recentAlerts,
	}, nil
}

// loadRolling24hData returns the shared 24h aggregates — the enabled
// operational monitor set, the exact-rolling-24h totals, and the hour-aligned
// bucket series, all anchored to a single "now" — through the short-TTL
// rolling24h cache. The summary, problem-monitors, and sparkline requests of
// one dashboard page load therefore cost a single computation per (tenant,
// tag-filter); the returned data and monitorIDs are shared and must be
// treated as read-only.
func (s *Service) loadRolling24hData(ctx context.Context, tenantID uuid.UUID, tags []string) (*rolling24hData, []uuid.UUID, error) {
	return s.rolling24h.Get(rolling24hCacheKey(tenantID, tags), func() (*rolling24hData, []uuid.UUID, error) {
		// Detach from the triggering request's cancellation (see
		// rolling24hBuildTimeout): concurrent waiters share this build.
		buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rolling24hBuildTimeout)
		defer cancel()

		monitorIDs, err := s.listEnabledOperationalMonitorIDs(buildCtx, tenantID, tags)
		if err != nil {
			return nil, nil, err
		}
		now := time.Now().UTC()
		// Cheap one-row cursor read purely for the lag warning: a failure here
		// is not fatal (the stitched queries below load the cursor themselves
		// and will surface any real error).
		if cursor, err := sharedanalytics.LoadRollupCursor(buildCtx, s.db); err == nil {
			s.warnIfRollupCursorLagging(now, cursor)
		}
		totals, err := loadExactRolling24hSummary(buildCtx, s.db, tenantID, monitorIDs, now)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load 24h rolling summary: %w", err)
		}
		series, err := loadHourlyBucketSeries24h(buildCtx, s.db, tenantID, monitorIDs, now)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load 24h hourly bucket series: %w", err)
		}
		return &rolling24hData{
			totals:       totals,
			hourlySeries: series,
		}, monitorIDs, nil
	})
}

// warnIfRollupCursorLagging logs a structured warning when the hourly-rollup
// cursor trails now by more than rollupCursorLagWarnThreshold: every rolling-24h
// dashboard query is then scanning raw check_results for the whole lag tail
// past the cursor. The 24h window is never truncated; this is observability only.
func (s *Service) warnIfRollupCursorLagging(now time.Time, cursor sharedanalytics.RollupCursor) {
	if s.log == nil || cursor.LastCreatedAt == nil {
		return
	}
	lag := now.Sub(cursor.LastCreatedAt.UTC())
	if lag <= rollupCursorLagWarnThreshold {
		return
	}
	s.log.WithFields(map[string]interface{}{
		"cursor_last_created_at": cursor.LastCreatedAt.UTC().Format(time.RFC3339),
		"lag":                    lag.Round(time.Second).String(),
		"threshold":              rollupCursorLagWarnThreshold.String(),
	}).Warn("rollup cursor is lagging; dashboard 24h queries are scanning raw check_results past the cursor")
}

func (s *Service) getStats(ctx context.Context, tenantID uuid.UUID, dashboardRange models.DashboardRange, rangeStart, rangeEnd time.Time, tags []string, precomp *rolling24hData) (models.DashboardStats, error) {
	stats := models.DashboardStats{}

	countQuery := `
		SELECT
			COUNT(*) AS total_monitors,
			COUNT(*) FILTER (WHERE enabled) AS active_monitors,
			COUNT(*) FILTER (WHERE type = 'http') AS http_monitors,
			COUNT(*) FILTER (WHERE type = 'agent') AS agent_monitors
		FROM monitors
		WHERE tenant_id = $1 AND deleted_at IS NULL
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

	if dashboardRange == models.DashboardRange24h {
		var totals map[uuid.UUID]MonitorRolling24hTotals
		if precomp != nil {
			totals = precomp.totals
		}
		if totals == nil {
			data, _, err := s.loadRolling24hData(ctx, tenantID, tags)
			if err != nil {
				return stats, err
			}
			totals = data.totals
		}
		stats.OverallUptime = computeMonitorWeightedUptime(totals)
		stats.AvgResponseMS = computeMonitorWeightedLatency(totals)
		return stats, nil
	}

	if isDashboardRollupRange(dashboardRange) {
		monitorIDs, err := s.listEnabledOperationalMonitorIDs(ctx, tenantID, tags)
		if err != nil {
			return stats, err
		}
		analyticsResult, err := s.analytics.GetScopeAnalytics(ctx, tenantID, monitorIDs, sharedanalytics.Range(dashboardRange), time.Now().UTC())
		if err != nil {
			return stats, fmt.Errorf("failed to query rollup-backed dashboard stats: %w", err)
		}
		if analyticsResult.Summary.HasData {
			// Interval-based availability when the timeline covers the
			// window (S-U1); the sampled rate otherwise (S-U5).
			sla := analyticsResult.Summary.AvailabilityPct
			stats.OverallUptime = &sla
		}
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
			  AND m.deleted_at IS NULL
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $2
			  AND cr.created_at < $3
			  %s
			GROUP BY cr.monitor_id
		)
		SELECT AVG((success_checks::float / NULLIF(total_checks, 0)) * 100.0)
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
			  AND m.deleted_at IS NULL
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

func (s *Service) getTrend(ctx context.Context, tenantID uuid.UUID, dashboardRange models.DashboardRange, rangeStart, rangeEnd time.Time, tags []string, precomp *rolling24hData) ([]models.DashboardTrendPoint, error) {
	if isDashboardRollupRange(dashboardRange) {
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

	if dashboardRange == models.DashboardRange24h {
		// 24h trend uses pooled (sum-of-success / sum-of-total) bucket uptime.
		// This intentionally differs from getStats' monitor-weighted mean: see
		// plan Decision D4 (chart sums are not required to equal scalar stats).
		var series []HourlyBucketPoint
		if precomp != nil {
			series = precomp.hourlySeries
		}
		if series == nil {
			data, _, err := s.loadRolling24hData(ctx, tenantID, tags)
			if err != nil {
				return nil, err
			}
			series = data.hourlySeries
		}
		trend := make([]models.DashboardTrendPoint, 0, len(series))
		for _, p := range series {
			uptime := 0.0
			if p.TotalChecks > 0 {
				uptime = (float64(p.SuccessChecks) / float64(p.TotalChecks)) * 100.0
			}
			responseTime := 0.0
			if p.LatencyCount > 0 {
				responseTime = p.LatencySumMS / float64(p.LatencyCount)
			}
			trend = append(trend, models.DashboardTrendPoint{
				BucketStart:  p.BucketStart,
				Label:        formatTrendLabel(p.BucketStart, dashboardRange),
				Uptime:       uptime,
				ResponseTime: responseTime,
				TotalChecks:  p.TotalChecks,
			})
		}
		return trend, nil
	}

	if dashboardRange != models.DashboardRange1h {
		return nil, fmt.Errorf("getTrend: unexpected dashboard range for raw fallback: %s", dashboardRange)
	}

	interval := "5 minutes"
	endExclusive := rangeEnd.Add(5 * time.Minute)

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
				date_bin(INTERVAL '%s', cr.created_at, $2::timestamptz) AS bucket_start,
				cr.monitor_id,
				COUNT(*) AS total_checks,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS success_checks,
				AVG(cr.latency_ms::float) FILTER (WHERE cr.status = 'success' AND cr.latency_ms IS NOT NULL) AS avg_latency
			FROM check_results cr
			JOIN monitors m ON m.id = cr.monitor_id
			WHERE cr.tenant_id = $1
			  AND m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND m.deleted_at IS NULL
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
	`, interval, interval, tagClause)

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

func (s *Service) getActivity24h(ctx context.Context, tenantID uuid.UUID, tags []string, precomp *rolling24hData) ([]models.DashboardActivityHour, error) {
	var series []HourlyBucketPoint
	if precomp != nil {
		series = precomp.hourlySeries
	}
	if series == nil {
		data, _, err := s.loadRolling24hData(ctx, tenantID, tags)
		if err != nil {
			return nil, err
		}
		series = data.hourlySeries
	}
	activity := make([]models.DashboardActivityHour, 0, len(series))
	for _, p := range series {
		failures := p.TotalChecks - p.SuccessChecks
		if failures < 0 {
			failures = 0
		}
		activity = append(activity, models.DashboardActivityHour{
			BucketStart: p.BucketStart,
			Label:       p.BucketStart.Format("15"),
			Checks:      p.TotalChecks,
			Failures:    failures,
		})
	}
	return activity, nil
}

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
		query += ` AND m.tags @> $2::text[]`
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
		tagClause = "AND m.tags @> $4::text[]"
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
		whereClause += " AND m.tags @> $4::text[]"
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

// getOpsSummary derives the up/down/paused counts from the monitor-health rows,
// whose LatestStatus comes from the persisted state machine via
// monitorStateToStatus: down → "failure" (counted down), up/suspect → "success"
// (counted up), unknown → nil (skipped) — consistent with problem-monitors and
// the public status page.
func (s *Service) getOpsSummary(ctx context.Context, tenantID uuid.UUID, monitorHealth []models.DashboardMonitorHealth, tags []string) (models.DashboardOpsSummary, error) {
	summary := models.DashboardOpsSummary{}
	for _, row := range monitorHealth {
		switch {
		case !row.Enabled:
			summary.PausedMonitors++
		case row.InMaintenance:
			summary.MaintenanceMonitors++
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
		  AND m.deleted_at IS NULL
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

type problemMonitorCandidate struct {
	MonitorID     uuid.UUID
	MonitorName   string
	CurrentState  string
	TotalChecks   int
	SuccessChecks int
	ProblemChecks int
	LatestCheckAt *time.Time
}

type problemMonitorFailureStats struct {
	FailureCount    int
	ErrorCount      int
	LatestFailureAt *time.Time
}

func (s *Service) getProblemMonitors(ctx context.Context, tenantID uuid.UUID, dashboardRange models.DashboardRange, rangeStart, rangeEndExclusive time.Time, limit int, tags []string) ([]models.DashboardProblemMonitor, error) {
	if limit < 1 {
		limit = problemMonitorLimit
	}

	var monitors []models.DashboardProblemMonitor
	var err error
	switch {
	case isDashboardRollupRange(dashboardRange):
		monitors, err = s.getProblemMonitorsLongRange(ctx, tenantID, rangeStart, rangeEndExclusive, limit, tags)
	case dashboardRange == models.DashboardRange24h:
		monitors, err = s.getProblemMonitors24h(ctx, tenantID, rangeStart, rangeEndExclusive, limit, tags)
	default:
		monitors, err = s.getProblemMonitors1h(ctx, tenantID, rangeStart, rangeEndExclusive, limit, tags)
	}
	if err != nil {
		return nil, err
	}

	// Rollups carry no error messages, so the latest failure reason is fetched
	// from raw check_results in one batched probe over the <= limit winners,
	// keeping the three range variants untouched.
	if err := s.attachLatestProblemErrors(ctx, tenantID, rangeStart, rangeEndExclusive, monitors); err != nil {
		return nil, err
	}
	return monitors, nil
}

// attachLatestProblemErrors populates LatestErrorMessage on each problem
// monitor from its most recent failing check inside the range. Monitors whose
// failures only exist in rollups (raw rows already purged) keep a nil message.
func (s *Service) attachLatestProblemErrors(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEndExclusive time.Time, monitors []models.DashboardProblemMonitor) error {
	if len(monitors) == 0 {
		return nil
	}
	monitorIDs := make([]uuid.UUID, 0, len(monitors))
	for _, m := range monitors {
		monitorIDs = append(monitorIDs, m.MonitorID)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT ON (cr.monitor_id) cr.monitor_id, cr.error_message
		FROM check_results cr
		WHERE cr.tenant_id = $1
		  AND cr.monitor_id = ANY($2)
		  AND cr.status IN ('failure', 'error')
		  AND cr.result_source <> 'platform'
		  AND cr.created_at >= $3
		  AND cr.created_at < $4
		ORDER BY cr.monitor_id, cr.created_at DESC
	`, tenantID, pq.Array(monitorIDs), rangeStart, rangeEndExclusive)
	if err != nil {
		return fmt.Errorf("failed to query latest problem errors: %w", err)
	}
	defer rows.Close()

	latest := make(map[uuid.UUID]*string, len(monitors))
	for rows.Next() {
		var monitorID uuid.UUID
		var message sql.NullString
		if err := rows.Scan(&monitorID, &message); err != nil {
			return fmt.Errorf("failed to scan latest problem error: %w", err)
		}
		if message.Valid && message.String != "" {
			v := message.String
			latest[monitorID] = &v
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating latest problem errors: %w", err)
	}

	for i := range monitors {
		monitors[i].LatestErrorMessage = latest[monitors[i].MonitorID]
	}
	return nil
}

func (s *Service) getProblemMonitors1h(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEndExclusive time.Time, limit int, tags []string) ([]models.DashboardProblemMonitor, error) {
	tagClause := ""
	limitPlaceholder := 4
	args := []interface{}{tenantID, rangeStart, rangeEndExclusive, limit}
	if len(tags) > 0 {
		tagClause = "AND m.tags @> $5::text[]"
		args = []interface{}{tenantID, rangeStart, rangeEndExclusive, limit, pq.Array(tags)}
	}

	query := fmt.Sprintf(`
		WITH problem_stats AS (
			SELECT
				cr.monitor_id,
				COUNT(*) AS total_checks,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS success_checks,
				COUNT(*) FILTER (WHERE cr.status = 'failure') AS failure_count,
				COUNT(*) FILTER (WHERE cr.status = 'error') AS error_count,
				MAX(cr.created_at) FILTER (WHERE cr.status IN ('failure', 'error')) AS latest_failure_at
			FROM check_results cr
			JOIN monitors m ON m.id = cr.monitor_id
			WHERE cr.tenant_id = $1
			  AND m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND m.type <> 'group'
			  AND m.deleted_at IS NULL
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= $2
			  AND cr.created_at < $3
			  %s
			GROUP BY cr.monitor_id
		)
		SELECT
			m.id,
			m.name,
			m.current_state,
			ps.failure_count,
			ps.error_count,
			CASE
				WHEN ps.total_checks > 0 THEN (ps.success_checks::float / ps.total_checks::float) * 100.0
				ELSE 0
			END AS uptime,
			ps.latest_failure_at
		FROM problem_stats ps
		JOIN monitors m ON m.id = ps.monitor_id AND m.tenant_id = $1 AND m.deleted_at IS NULL
		WHERE (ps.failure_count + ps.error_count) > 0
		ORDER BY (ps.failure_count + ps.error_count) DESC, ps.latest_failure_at DESC NULLS LAST, m.name ASC
		LIMIT $%d
	`, tagClause, limitPlaceholder)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query 1h problem monitors: %w", err)
	}
	defer rows.Close()

	return scanProblemMonitorRows(rows)
}

func (s *Service) getProblemMonitors24h(ctx context.Context, tenantID uuid.UUID, _rangeStart, _rangeEndExclusive time.Time, limit int, tags []string) ([]models.DashboardProblemMonitor, error) {
	// The monitor-set predicate (enabled, type<>'group', deleted_at IS NULL,
	// tags @>) and the totals computation are exactly the ones cached by
	// loadRolling24hData, so the parallel summary request's work is reused.
	data, monitorIDs, err := s.loadRolling24hData(ctx, tenantID, tags)
	if err != nil {
		return nil, err
	}
	if len(monitorIDs) == 0 {
		return []models.DashboardProblemMonitor{}, nil
	}
	totals := data.totals

	// Materialise candidates (any monitor with at least one bad check).
	type candidate struct {
		monitorID     uuid.UUID
		failureCount  int
		errorCount    int
		uptime        float64
		latestFailure *time.Time
	}
	cands := make([]candidate, 0, len(monitorIDs))
	for _, id := range monitorIDs {
		t := totals[id]
		bad := t.FailureChecks + t.ErrorChecks
		if bad == 0 {
			continue
		}
		uptime := 0.0
		if t.TotalChecks > 0 {
			uptime = (float64(t.SuccessChecks) / float64(t.TotalChecks)) * 100.0
		}
		var latest *time.Time
		if t.LatestCheckAt != nil && t.LatestStatus != nil && (*t.LatestStatus == "failure" || *t.LatestStatus == "error") {
			ts := *t.LatestCheckAt
			latest = &ts
		}
		cands = append(cands, candidate{
			monitorID:     id,
			failureCount:  t.FailureChecks,
			errorCount:    t.ErrorChecks,
			uptime:        uptime,
			latestFailure: latest,
		})
	}
	if len(cands) == 0 {
		return []models.DashboardProblemMonitor{}, nil
	}

	sort.Slice(cands, func(i, j int) bool {
		li := cands[i].failureCount + cands[i].errorCount
		lj := cands[j].failureCount + cands[j].errorCount
		if li != lj {
			return li > lj
		}
		if cands[i].latestFailure == nil && cands[j].latestFailure != nil {
			return false
		}
		if cands[i].latestFailure != nil && cands[j].latestFailure == nil {
			return true
		}
		if cands[i].latestFailure != nil && cands[j].latestFailure != nil && !cands[i].latestFailure.Equal(*cands[j].latestFailure) {
			return cands[i].latestFailure.After(*cands[j].latestFailure)
		}
		return cands[i].monitorID.String() < cands[j].monitorID.String()
	})
	if len(cands) > limit {
		cands = cands[:limit]
	}

	// Fetch names + current_state for the top-N candidates.
	topIDs := make([]uuid.UUID, 0, len(cands))
	for _, c := range cands {
		topIDs = append(topIDs, c.monitorID)
	}
	nameRows, err := s.db.QueryContext(ctx, `
		SELECT id, name, current_state
		FROM monitors
		WHERE tenant_id = $1 AND id = ANY($2) AND deleted_at IS NULL
	`, tenantID, pq.Array(topIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to load problem-monitor names: %w", err)
	}
	defer nameRows.Close()
	type monitorMeta struct {
		name         string
		currentState string
	}
	meta := make(map[uuid.UUID]monitorMeta, len(topIDs))
	for nameRows.Next() {
		var id uuid.UUID
		var m monitorMeta
		if err := nameRows.Scan(&id, &m.name, &m.currentState); err != nil {
			return nil, fmt.Errorf("failed to scan problem-monitor name: %w", err)
		}
		meta[id] = m
	}
	if err := nameRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating problem-monitor names: %w", err)
	}

	out := make([]models.DashboardProblemMonitor, 0, len(cands))
	for _, c := range cands {
		m := meta[c.monitorID]
		out = append(out, models.DashboardProblemMonitor{
			MonitorID:       c.monitorID,
			MonitorName:     m.name,
			CurrentState:    m.currentState,
			CurrentStatus:   monitorStateToStatus(m.currentState),
			FailureCount:    c.failureCount,
			ErrorCount:      c.errorCount,
			Uptime:          c.uptime,
			LatestFailureAt: c.latestFailure,
		})
	}
	return out, nil
}

func (s *Service) getProblemMonitorsLongRange(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEndExclusive time.Time, limit int, tags []string) ([]models.DashboardProblemMonitor, error) {
	candidateLimit := limit * problemCandidateMult
	if candidateLimit < limit {
		candidateLimit = limit
	}

	candidates, err := s.listProblemMonitorCandidates(ctx, tenantID, rangeStart, rangeEndExclusive, candidateLimit, tags)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return []models.DashboardProblemMonitor{}, nil
	}

	monitorIDs := make([]uuid.UUID, 0, len(candidates))
	for _, candidate := range candidates {
		monitorIDs = append(monitorIDs, candidate.MonitorID)
	}

	rawStats, err := s.getProblemMonitorFailureStats(ctx, tenantID, rangeStart, rangeEndExclusive, monitorIDs)
	if err != nil {
		return nil, err
	}

	monitors := make([]models.DashboardProblemMonitor, 0, len(candidates))
	for _, candidate := range candidates {
		stats := rawStats[candidate.MonitorID]
		failureCount := stats.FailureCount
		errorCount := stats.ErrorCount
		latestFailureAt := stats.LatestFailureAt

		if failureCount+errorCount == 0 && candidate.ProblemChecks > 0 {
			failureCount = candidate.ProblemChecks
			latestFailureAt = candidate.LatestCheckAt
		}
		if failureCount+errorCount == 0 {
			continue
		}

		uptime := 0.0
		if candidate.TotalChecks > 0 {
			uptime = (float64(candidate.SuccessChecks) / float64(candidate.TotalChecks)) * 100.0
		}

		monitors = append(monitors, models.DashboardProblemMonitor{
			MonitorID:       candidate.MonitorID,
			MonitorName:     candidate.MonitorName,
			CurrentState:    candidate.CurrentState,
			CurrentStatus:   monitorStateToStatus(candidate.CurrentState),
			FailureCount:    failureCount,
			ErrorCount:      errorCount,
			Uptime:          uptime,
			LatestFailureAt: latestFailureAt,
		})
	}

	sort.Slice(monitors, func(i, j int) bool {
		leftCount := monitors[i].FailureCount + monitors[i].ErrorCount
		rightCount := monitors[j].FailureCount + monitors[j].ErrorCount
		if leftCount != rightCount {
			return leftCount > rightCount
		}
		if monitors[i].LatestFailureAt == nil && monitors[j].LatestFailureAt != nil {
			return false
		}
		if monitors[i].LatestFailureAt != nil && monitors[j].LatestFailureAt == nil {
			return true
		}
		if monitors[i].LatestFailureAt != nil && monitors[j].LatestFailureAt != nil && !monitors[i].LatestFailureAt.Equal(*monitors[j].LatestFailureAt) {
			return monitors[i].LatestFailureAt.After(*monitors[j].LatestFailureAt)
		}
		return monitors[i].MonitorName < monitors[j].MonitorName
	})

	if len(monitors) > limit {
		monitors = monitors[:limit]
	}
	return monitors, nil
}

func (s *Service) listProblemMonitorCandidates(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEndExclusive time.Time, limit int, tags []string) ([]problemMonitorCandidate, error) {
	tagClause := ""
	args := []interface{}{tenantID, rangeStart, rangeEndExclusive, limit}
	if len(tags) > 0 {
		tagClause = "AND m.tags @> $5::text[]"
		args = append(args, pq.Array(tags))
	}

	query := fmt.Sprintf(`
		WITH rollup_candidates AS (
			SELECT
				m.id,
				m.name,
				m.current_state,
				COALESCE(SUM(mdr.total_checks), 0) AS total_checks,
				COALESCE(SUM(mdr.success_checks), 0) AS success_checks,
				COALESCE(SUM(mdr.total_checks - mdr.success_checks), 0) AS problem_checks,
				latest.latest_check_at
			FROM monitors m
			LEFT JOIN monitor_daily_rollups mdr
				ON mdr.monitor_id = m.id
				AND mdr.tenant_id = m.tenant_id
				AND mdr.bucket_day >= $2::date
				AND mdr.bucket_day < $3::date
			LEFT JOIN LATERAL (
				SELECT mdr_latest.latest_check_at
				FROM monitor_daily_rollups mdr_latest
				WHERE mdr_latest.monitor_id = m.id
				  AND mdr_latest.tenant_id = m.tenant_id
				  AND mdr_latest.bucket_day >= $2::date
				  AND mdr_latest.bucket_day < $3::date
				ORDER BY mdr_latest.bucket_day DESC
				LIMIT 1
			) latest ON TRUE
			WHERE m.tenant_id = $1
			  AND m.enabled = TRUE
			  AND m.type <> 'group'
			  AND m.deleted_at IS NULL
			  %s
			GROUP BY m.id, m.name, m.current_state, latest.latest_check_at
			HAVING COALESCE(SUM(mdr.total_checks - mdr.success_checks), 0) > 0
			    OR m.current_state IN ('down', 'degraded')
			ORDER BY problem_checks DESC, name ASC
			LIMIT $4
		)
		SELECT id, name, current_state, total_checks, success_checks, problem_checks, latest_check_at
		FROM rollup_candidates
		ORDER BY problem_checks DESC, name ASC
	`, tagClause)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query problem monitor rollup candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]problemMonitorCandidate, 0)
	for rows.Next() {
		var candidate problemMonitorCandidate
		var latestCheckAt sql.NullTime

		if err := rows.Scan(
			&candidate.MonitorID,
			&candidate.MonitorName,
			&candidate.CurrentState,
			&candidate.TotalChecks,
			&candidate.SuccessChecks,
			&candidate.ProblemChecks,
			&latestCheckAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan problem monitor rollup candidate: %w", err)
		}
		if latestCheckAt.Valid {
			ts := latestCheckAt.Time
			candidate.LatestCheckAt = &ts
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating problem monitor rollup candidates: %w", err)
	}

	return candidates, nil
}

func (s *Service) getProblemMonitorFailureStats(ctx context.Context, tenantID uuid.UUID, rangeStart, rangeEndExclusive time.Time, monitorIDs []uuid.UUID) (map[uuid.UUID]problemMonitorFailureStats, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			cr.monitor_id,
			COUNT(*) FILTER (WHERE cr.status = 'failure') AS failure_count,
			COUNT(*) FILTER (WHERE cr.status = 'error') AS error_count,
			MAX(cr.created_at) FILTER (WHERE cr.status IN ('failure', 'error')) AS latest_failure_at
		FROM check_results cr
		WHERE cr.tenant_id = $1
		  AND cr.result_source <> 'platform'
		  AND cr.created_at >= $2
		  AND cr.created_at < $3
		  AND cr.monitor_id = ANY($4)
		GROUP BY cr.monitor_id
	`, tenantID, rangeStart, rangeEndExclusive, pq.Array(monitorIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to query problem monitor raw stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[uuid.UUID]problemMonitorFailureStats, len(monitorIDs))
	for rows.Next() {
		var monitorID uuid.UUID
		var row problemMonitorFailureStats
		var latestFailureAt sql.NullTime

		if err := rows.Scan(&monitorID, &row.FailureCount, &row.ErrorCount, &latestFailureAt); err != nil {
			return nil, fmt.Errorf("failed to scan problem monitor raw stats: %w", err)
		}
		if latestFailureAt.Valid {
			ts := latestFailureAt.Time
			row.LatestFailureAt = &ts
		}
		stats[monitorID] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating problem monitor raw stats: %w", err)
	}

	return stats, nil
}

func scanProblemMonitorRows(rows *sql.Rows) ([]models.DashboardProblemMonitor, error) {
	monitors := make([]models.DashboardProblemMonitor, 0)
	for rows.Next() {
		var row models.DashboardProblemMonitor
		var latestFailureAt sql.NullTime

		if err := rows.Scan(
			&row.MonitorID,
			&row.MonitorName,
			&row.CurrentState,
			&row.FailureCount,
			&row.ErrorCount,
			&row.Uptime,
			&latestFailureAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan problem monitor row: %w", err)
		}

		row.CurrentStatus = monitorStateToStatus(row.CurrentState)
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

func normalizeOverviewParams(params *models.DashboardOverviewQuery) models.DashboardOverviewQuery {
	normalized := models.DashboardOverviewQuery{
		Range:         normalizeDashboardRange(""),
		FailuresLimit: defaultFailuresLimit,
		AlertsLimit:   defaultAlertsLimit,
	}
	if params == nil {
		return normalized
	}

	normalized.Range = normalizeDashboardRange(params.Range)

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

	normalized.Tags = normalizeDashboardTags(params.Tags)

	return normalized
}

func normalizeListParams(params *models.DashboardListQuery, defaultLimit int) models.DashboardListQuery {
	normalized := models.DashboardListQuery{
		Range: normalizeDashboardRange(""),
		Limit: defaultLimit,
	}
	if params == nil {
		return normalized
	}

	normalized.Range = normalizeDashboardRange(params.Range)
	if params.Limit > 0 {
		normalized.Limit = params.Limit
	}
	if normalized.Limit > maxListLimit {
		normalized.Limit = maxListLimit
	}
	normalized.Tags = normalizeDashboardTags(params.Tags)

	return normalized
}

func normalizeDashboardRange(rangeValue models.DashboardRange) models.DashboardRange {
	switch rangeValue {
	case models.DashboardRange1h, models.DashboardRange24h, models.DashboardRange7d, models.DashboardRange30d, models.DashboardRange90d, models.DashboardRange365d:
		return rangeValue
	default:
		return models.DashboardRange24h
	}
}

func isDashboardRollupRange(rangeValue models.DashboardRange) bool {
	switch rangeValue {
	case models.DashboardRange7d, models.DashboardRange30d, models.DashboardRange90d, models.DashboardRange365d:
		return true
	default:
		return false
	}
}

func normalizeDashboardTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}

	tagSet := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			continue
		}
		tagSet[trimmed] = struct{}{}
	}
	if len(tagSet) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(tagSet))
	for tag := range tagSet {
		normalized = append(normalized, tag)
	}
	sort.Strings(normalized)
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
	case models.DashboardRange1h:
		end := now.Truncate(5 * time.Minute)
		return end.Add(-55 * time.Minute), end, 5 * time.Minute, nil
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
	case models.DashboardRange1h:
		return bucketStart.UTC().Format("15:04")
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

// computeMonitorWeightedUptime returns the monitor-weighted mean uptime % over the
// given totals: each monitor contributes one data point (its per-monitor success
// rate); monitors with no data are skipped. Returns nil when nothing was
// checked — no data must not read as 0% (S-D1, docs/state-semantics.md).
func computeMonitorWeightedUptime(totals map[uuid.UUID]MonitorRolling24hTotals) *float64 {
	sum := 0.0
	n := 0
	for _, t := range totals {
		if t.TotalChecks == 0 {
			continue
		}
		sum += (float64(t.SuccessChecks) / float64(t.TotalChecks)) * 100.0
		n++
	}
	if n == 0 {
		return nil
	}
	u := sum / float64(n)
	return &u
}

// computeMonitorWeightedLatency returns the monitor-weighted mean success-latency
// in milliseconds: each monitor contributes one data point (its per-monitor avg
// latency); monitors with no successful checks are skipped. Returns 0 on empty input.
func computeMonitorWeightedLatency(totals map[uuid.UUID]MonitorRolling24hTotals) float64 {
	if len(totals) == 0 {
		return 0
	}
	sum := 0.0
	n := 0
	for _, t := range totals {
		if t.LatencyCount == 0 {
			continue
		}
		sum += t.LatencySumMS / float64(t.LatencyCount)
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

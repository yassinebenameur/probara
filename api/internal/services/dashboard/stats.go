// Headline stats, the uptime/latency trend, 24h activity and the ops summary, with the weighting math they share.

package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/alertrouting"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
)

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
		countQuery += ` AND tags && $2::text[]`
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
		uptimeTagClause = "AND m.tags && $4::text[]"
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
		latencyTagClause = "AND m.tags && $4::text[]"
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
		tagClause = "AND m.tags && $5::text[]"
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
			COUNT(*) FILTER (WHERE a.status = 'acknowledged') AS acknowledged_alerts,
			COUNT(*) FILTER (WHERE ` + alertrouting.SuppressedByDependencyPredicate("a", "m", "te") + `) AS suppressed_alerts
		FROM alerts a
		JOIN monitors m ON m.id = a.monitor_id AND m.tenant_id = a.tenant_id
		JOIN tenants te ON te.id = a.tenant_id
		WHERE a.tenant_id = $1
		  AND a.status IN ('active', 'acknowledged')
		  AND m.deleted_at IS NULL
	`
	args := []interface{}{tenantID}
	if len(tags) > 0 {
		query += ` AND m.tags && $2::text[]`
		args = append(args, pq.Array(tags))
	}
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&summary.ActiveAlerts, &summary.AcknowledgedAlerts, &summary.SuppressedAlerts); err != nil {
		return summary, fmt.Errorf("failed to query ops summary alerts: %w", err)
	}

	unroutedQuery := `
		SELECT COUNT(*)
		FROM monitors m
		WHERE m.tenant_id = $1
		  AND m.deleted_at IS NULL
		  AND m.enabled = TRUE
		  AND ` + alertrouting.UnreachablePredicate("m")
	unroutedArgs := []interface{}{tenantID}
	if len(tags) > 0 {
		unroutedQuery += ` AND m.tags && $2::text[]`
		unroutedArgs = append(unroutedArgs, pq.Array(tags))
	}
	if err := s.db.QueryRowContext(ctx, unroutedQuery, unroutedArgs...).Scan(&summary.UnroutedMonitors); err != nil {
		return summary, fmt.Errorf("failed to query unrouted monitors: %w", err)
	}

	return summary, nil
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

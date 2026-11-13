// Long-range (7d-365d) analytics for monitors and the page as a whole.

package statuspage

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
)

func formatUptime(uptime *float64, err error) string {
	if err != nil || uptime == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.2f%%", *uptime)
}

// longRangeRanges are the rollup-backed ranges rendered per monitor on the status page.
var longRangeRanges = []sharedanalytics.Range{sharedanalytics.Range7d, sharedanalytics.Range30d, sharedanalytics.Range90d, sharedanalytics.Range365d}

// applyMonitorLongRangeAnalytics is the per-monitor (unbatched) long-range path, retained as
// a fallback for analytics readers that do not implement batched access.
func (s *Service) applyMonitorLongRangeAnalytics(ctx context.Context, monitor *MonitorStatus, tenantID uuid.UUID, monitorIDs []uuid.UUID) {
	now := s.now()
	for _, rangeValue := range longRangeRanges {
		result, err := s.analytics.GetScopeAnalytics(ctx, tenantID, monitorIDs, rangeValue, now)
		if err != nil {
			continue
		}
		assignLongRangeResult(monitor, rangeValue, result)
	}
}

// assignLongRangeResult maps one analytics Result onto the monitor's range-specific fields.
// Shared by the batched and unbatched long-range paths so they produce identical output.
func assignLongRangeResult(monitor *MonitorStatus, rangeValue sharedanalytics.Range, result *sharedanalytics.Result) {
	if result == nil {
		return
	}
	uptimeHistory := mapDailySeries(result.Series)
	latencyHistory := mapLatencySeries(result.Series, rangeValue)
	downtime := mapDowntimePeriods(result.Downtime)
	switch rangeValue {
	case sharedanalytics.Range7d:
		monitor.UptimeHistory7d = uptimeHistory
	case sharedanalytics.Range30d:
		monitor.UptimeHistory30d = uptimeHistory
		monitor.LatencyHistory30d = latencyHistory
		monitor.DowntimePeriods30d = downtime
	case sharedanalytics.Range90d:
		monitor.UptimeHistory90d = uptimeHistory
		monitor.LatencyHistory90d = latencyHistory
		monitor.DowntimePeriods90d = downtime
	case sharedanalytics.Range365d:
		monitor.UptimeHistory365d = uptimeHistory
		monitor.LatencyHistory365d = latencyHistory
		monitor.DowntimePeriods365d = downtime
	}
}

func (s *Service) applyGlobalLongRangeAnalytics(ctx context.Context, page *StatusPageData, tenantID uuid.UUID, monitorIDs []uuid.UUID) {
	ranges := []sharedanalytics.Range{sharedanalytics.Range7d, sharedanalytics.Range30d, sharedanalytics.Range90d, sharedanalytics.Range365d}
	now := s.now()
	for _, rangeValue := range ranges {
		result, err := s.analytics.GetScopeAnalytics(ctx, tenantID, monitorIDs, rangeValue, now)
		if err != nil {
			continue
		}
		uptimeHistory := mapDailySeries(result.Series)
		switch rangeValue {
		case sharedanalytics.Range7d:
			page.UptimeHistory7 = uptimeHistory
		case sharedanalytics.Range30d:
			page.UptimeHistory30 = uptimeHistory
		case sharedanalytics.Range90d:
			page.UptimeHistory90 = uptimeHistory
		case sharedanalytics.Range365d:
			page.UptimeHistory365 = uptimeHistory
		}
	}
}

func (s *Service) resolveStatusPageOperationalMonitorIDs(ctx context.Context, statusPageID, tenantID uuid.UUID) ([]uuid.UUID, error) {
	monitorIDs, err := s.listStatusPageMonitorIDs(ctx, statusPageID)
	if err != nil {
		return nil, err
	}
	if len(monitorIDs) == 0 {
		return []uuid.UUID{}, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.type
		FROM monitors m
		WHERE m.id = ANY($1) AND m.tenant_id = $2 AND m.deleted_at IS NULL
	`, pq.Array(monitorIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list status page monitor ids: %w", err)
	}
	defer rows.Close()

	resolved := make([]uuid.UUID, 0)
	for rows.Next() {
		var monitorID uuid.UUID
		var monitorType string
		if err := rows.Scan(&monitorID, &monitorType); err != nil {
			return nil, fmt.Errorf("failed to scan status page monitor id: %w", err)
		}
		operationalIDs, err := s.monitorPresenter(monitorType).ResolveOperationalIDs(ctx, s, monitorID, tenantID)
		if err != nil {
			continue
		}
		resolved = append(resolved, operationalIDs...)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating status page monitor ids: %w", err)
	}
	return dedupeMonitorIDs(resolved), nil
}

func mapDailySeries(series []sharedanalytics.SeriesPoint) []DailyUptime {
	result := make([]DailyUptime, 0, len(series))
	for _, point := range series {
		uptime := point.UptimePct
		if !point.HasData {
			uptime = -1
		}
		result = append(result, DailyUptime{
			Date:   point.BucketStart.UTC().Format("2006-01-02"),
			Uptime: uptime,
		})
	}
	return result
}

func mapLatencySeries(series []sharedanalytics.SeriesPoint, rangeValue sharedanalytics.Range) []LatencyPoint {
	result := make([]LatencyPoint, 0, len(series))
	timeFormat := "Jan 2"
	if rangeValue == sharedanalytics.Range30d {
		timeFormat = "Jan 2"
	}
	for _, point := range series {
		if point.AvgLatencyMS == nil || !point.HasData {
			continue
		}
		result = append(result, LatencyPoint{
			Timestamp: point.BucketStart.UTC(),
			LatencyMS: int(*point.AvgLatencyMS),
			Time:      point.BucketStart.UTC().Format(timeFormat),
			Unix:      point.BucketStart.UTC().Unix(),
		})
	}
	return result
}

func mapDowntimePeriods(periods []sharedanalytics.DowntimePeriod) []DowntimePeriod {
	result := make([]DowntimePeriod, 0, len(periods))
	for _, period := range periods {
		result = append(result, DowntimePeriod{
			StartTime: period.Start.UTC().Format("Jan 2 15:04"),
			EndTime:   period.End.UTC().Format("Jan 2 15:04"),
			StartUnix: period.Start.UTC().Unix(),
			EndUnix:   period.End.UTC().Unix(),
		})
	}
	return result
}

func dedupeMonitorIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	result := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

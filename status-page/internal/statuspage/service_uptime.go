package statuspage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// bucketUptime converts one time bucket's (total, successful) check counts into
// the uptime percentage the page renders. -1 means "no data": the template
// draws that bucket as empty rather than as 0% uptime.
func bucketUptime(total, successful int) float64 {
	if total <= 0 {
		return -1
	}
	return float64(successful) / float64(total) * 100.0
}

// The per-monitor and per-group uptime queries are the same SQL over
// `monitor_id = ANY($1)`: a single monitor is a one-element set. The public
// methods keep both call shapes; each query is written once below. Every
// query excludes result_source 'platform' (freshness failures the platform
// records for silent agents/push monitors), as the batched page queries and
// shared analytics do, so a group row and a leaf row over the same checks
// show the same uptime.

// CalculateUptime24h calculates uptime percentage over the last 24 hours.
func (s *Service) CalculateUptime24h(ctx context.Context, monitorID, tenantID uuid.UUID) (*float64, error) {
	return s.uptimeForMonitors(ctx, []uuid.UUID{monitorID}, tenantID, "24 hours")
}

// GetMonitorHourlyUptime calculates hourly uptime for a single monitor over the last 24 hours.
func (s *Service) GetMonitorHourlyUptime(ctx context.Context, monitorID, tenantID uuid.UUID) ([]HourlyUptime, error) {
	return s.hourlyUptimeForMonitors(ctx, []uuid.UUID{monitorID}, tenantID)
}

// CalculateGroupUptime24h calculates aggregated uptime for a group over the last 24 hours.
func (s *Service) CalculateGroupUptime24h(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID) (*float64, error) {
	return s.uptimeForMonitors(ctx, memberIDs, tenantID, "24 hours")
}

// CalculateGroupUptime1h calculates aggregated uptime for a group over the last 1 hour.
func (s *Service) CalculateGroupUptime1h(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID) (*float64, error) {
	return s.uptimeForMonitors(ctx, memberIDs, tenantID, "1 hour")
}

// CalculateGroupAvgLatency calculates average latency across a group's members
// over a Postgres interval literal such as "24 hours".
func (s *Service) CalculateGroupAvgLatency(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID, interval string) (*float64, error) {
	return s.avgLatencyForMonitors(ctx, memberIDs, tenantID, interval)
}

// GetGroupHourlyUptime calculates hourly uptime for a group over the last 24 hours.
func (s *Service) GetGroupHourlyUptime(ctx context.Context, memberIDs []uuid.UUID, tenantID uuid.UUID) ([]HourlyUptime, error) {
	return s.hourlyUptimeForMonitors(ctx, memberIDs, tenantID)
}

// uptimeForMonitors returns the success ratio over the trailing window, or nil
// when no checks were recorded. window is a Postgres interval literal.
func (s *Service) uptimeForMonitors(ctx context.Context, monitorIDs []uuid.UUID, tenantID uuid.UUID, window string) (*float64, error) {
	if len(monitorIDs) == 0 {
		return nil, nil
	}

	var total, successful int
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE status = 'success') AS successful
		FROM check_results
		WHERE monitor_id = ANY($1) AND tenant_id = $2 AND created_at >= NOW() - $3::interval
		  AND result_source <> 'platform'
	`, pq.Array(monitorIDs), tenantID, window).Scan(&total, &successful)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate %s uptime: %w", window, err)
	}
	if total == 0 {
		return nil, nil
	}

	uptime := float64(successful) / float64(total) * 100.0
	return &uptime, nil
}

// avgLatencyForMonitors returns the mean successful-check latency over the
// trailing interval, or nil when there is none.
func (s *Service) avgLatencyForMonitors(ctx context.Context, monitorIDs []uuid.UUID, tenantID uuid.UUID, interval string) (*float64, error) {
	if len(monitorIDs) == 0 {
		return nil, nil
	}

	var avgLatency sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `
		SELECT AVG(latency_ms)
		FROM check_results
		WHERE monitor_id = ANY($1) AND tenant_id = $2
		  AND created_at >= NOW() - $3::interval
		  AND latency_ms IS NOT NULL
		  AND status = 'success'
		  AND result_source <> 'platform'
	`, pq.Array(monitorIDs), tenantID, interval).Scan(&avgLatency)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate avg latency: %w", err)
	}
	if !avgLatency.Valid {
		return nil, nil
	}

	val := avgLatency.Float64
	return &val, nil
}

// hourlyUptimeForMonitors returns one HourlyUptime per hour for the last 24
// hours, including hours with no checks (uptime -1).
func (s *Service) hourlyUptimeForMonitors(ctx context.Context, monitorIDs []uuid.UUID, tenantID uuid.UUID) ([]HourlyUptime, error) {
	if len(monitorIDs) == 0 {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		WITH hours AS (
			SELECT generate_series(
				date_trunc('hour', NOW() - INTERVAL '23 hours'),
				date_trunc('hour', NOW()),
				INTERVAL '1 hour'
			) AS hour
		),
		hourly_stats AS (
			SELECT
				date_trunc('hour', cr.created_at) AS hour,
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS successful
			FROM check_results cr
			WHERE cr.monitor_id = ANY($1)
			  AND cr.tenant_id = $2
			  AND cr.created_at >= NOW() - INTERVAL '24 hours'
			  AND cr.result_source <> 'platform'
			GROUP BY date_trunc('hour', cr.created_at)
		)
		SELECT
			h.hour,
			COALESCE(hs.total, 0) AS total,
			COALESCE(hs.successful, 0) AS successful
		FROM hours h
		LEFT JOIN hourly_stats hs ON h.hour = hs.hour
		ORDER BY h.hour
	`, pq.Array(monitorIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query hourly uptime: %w", err)
	}
	defer rows.Close()

	var result []HourlyUptime
	for rows.Next() {
		var hour time.Time
		var total, successful int
		if err := rows.Scan(&hour, &total, &successful); err != nil {
			return nil, fmt.Errorf("failed to scan hourly uptime: %w", err)
		}
		result = append(result, HourlyUptime{
			Hour:   hour.Format("15:04"),
			Uptime: bucketUptime(total, successful),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating hourly uptime: %w", err)
	}
	return result, nil
}

// minuteBucketCount is one minute's raw check counts for the 1h strip.
type minuteBucketCount struct {
	Total      int
	Successful int
}

// oneHourBuckets is the number of one-minute buckets in the 1h strip.
const oneHourBuckets = 60

// oneHourWindow returns the 1h strip's bounds: 60 clock-aligned one-minute
// buckets ending with the current (partial) minute, the same shape as the 24h
// strip's hourly buckets. Computed in Go and bound as parameters so the
// created_at range stays an index condition.
func oneHourWindow(now time.Time) (start, end time.Time) {
	end = now.UTC().Truncate(time.Minute).Add(time.Minute)
	return end.Add(-oneHourBuckets * time.Minute), end
}

// minuteBucketCounts loads per-minute check counts over [start, end) for each
// monitor in one query. The 1h window is short enough to read raw
// check_results (rollups are hourly). Monitors without checks are absent from
// the result; minuteUptime turns a missing or zero bucket into "no data".
func (s *Service) minuteBucketCounts(ctx context.Context, monitorIDs []uuid.UUID, tenantID uuid.UUID, start, end time.Time) (map[uuid.UUID][]minuteBucketCount, error) {
	result := make(map[uuid.UUID][]minuteBucketCount)
	if len(monitorIDs) == 0 {
		return result, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			cr.monitor_id,
			date_trunc('minute', cr.created_at) AS bucket,
			COUNT(*)::bigint AS total,
			COUNT(*) FILTER (WHERE cr.status = 'success')::bigint AS successful
		FROM check_results cr
		WHERE cr.tenant_id = $1
		  AND cr.monitor_id = ANY($2)
		  AND cr.result_source <> 'platform'
		  AND cr.created_at >= $3
		  AND cr.created_at < $4
		GROUP BY 1, 2
	`, tenantID, pq.Array(monitorIDs), start, end)
	if err != nil {
		return nil, fmt.Errorf("failed to query minute uptime: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var monitorID uuid.UUID
		var bucket time.Time
		var total, successful int
		if err := rows.Scan(&monitorID, &bucket, &total, &successful); err != nil {
			return nil, fmt.Errorf("failed to scan minute uptime: %w", err)
		}
		idx := int(bucket.Sub(start) / time.Minute)
		if idx < 0 || idx >= oneHourBuckets {
			continue
		}
		counts, ok := result[monitorID]
		if !ok {
			counts = make([]minuteBucketCount, oneHourBuckets)
			result[monitorID] = counts
		}
		counts[idx].Total += total
		counts[idx].Successful += successful
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating minute uptime: %w", err)
	}
	return result, nil
}

// sumMinuteBuckets adds the per-minute counts of the given monitors, so a
// group or the page-level strip weighs every check equally (the same
// aggregation as the hourly group and global strips).
func sumMinuteBuckets(byID map[uuid.UUID][]minuteBucketCount, monitorIDs []uuid.UUID) []minuteBucketCount {
	sum := make([]minuteBucketCount, oneHourBuckets)
	for _, id := range dedupeMonitorIDs(monitorIDs) {
		for i, c := range byID[id] {
			sum[i].Total += c.Total
			sum[i].Successful += c.Successful
		}
	}
	return sum
}

// minuteUptime renders a dense 60-bucket series; a nil counts slice (no
// checks in the window) yields 60 "no data" buckets rather than nothing, so
// the strip still spans the full hour.
func minuteUptime(counts []minuteBucketCount, start time.Time) []MinuteUptime {
	result := make([]MinuteUptime, oneHourBuckets)
	for i := range result {
		var c minuteBucketCount
		if i < len(counts) {
			c = counts[i]
		}
		result[i] = MinuteUptime{
			Time:   start.Add(time.Duration(i) * time.Minute).Format("15:04"),
			Uptime: bucketUptime(c.Total, c.Successful),
		}
	}
	return result
}

// GetGlobalMinuteUptime returns the page-level 1h strip: one bucket per
// minute over the last hour, summed across the page's operational monitors.
func (s *Service) GetGlobalMinuteUptime(ctx context.Context, monitorIDs []uuid.UUID, tenantID uuid.UUID) ([]MinuteUptime, error) {
	start, end := oneHourWindow(s.now())
	byID, err := s.minuteBucketCounts(ctx, monitorIDs, tenantID, start, end)
	if err != nil {
		return nil, err
	}
	return minuteUptime(sumMinuteBuckets(byID, monitorIDs), start), nil
}

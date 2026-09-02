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
// methods keep both call shapes; each query is written once below.

// CalculateUptime24h calculates uptime percentage over the last 24 hours.
func (s *Service) CalculateUptime24h(ctx context.Context, monitorID, tenantID uuid.UUID) (*float64, error) {
	return s.uptimeForMonitors(ctx, []uuid.UUID{monitorID}, tenantID, "24 hours")
}

// GetMonitorHourlyUptime calculates hourly uptime for a single monitor over the last 24 hours.
func (s *Service) GetMonitorHourlyUptime(ctx context.Context, monitorID, tenantID uuid.UUID) ([]HourlyUptime, error) {
	return s.hourlyUptimeForMonitors(ctx, []uuid.UUID{monitorID}, tenantID)
}

// GetMonitor5MinuteUptime calculates 5-minute bucket uptime for a single monitor over the last 1 hour.
func (s *Service) GetMonitor5MinuteUptime(ctx context.Context, monitorID, tenantID uuid.UUID) ([]MinuteUptime, error) {
	return s.fiveMinuteUptimeForMonitors(ctx, []uuid.UUID{monitorID}, tenantID)
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

// fiveMinuteUptimeForMonitors returns one MinuteUptime per 5-minute bucket for
// the last hour, including buckets with no checks (uptime -1).
func (s *Service) fiveMinuteUptimeForMonitors(ctx context.Context, monitorIDs []uuid.UUID, tenantID uuid.UUID) ([]MinuteUptime, error) {
	if len(monitorIDs) == 0 {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		WITH buckets AS (
			SELECT generate_series(
				date_trunc('minute', NOW() - INTERVAL '55 minutes') - (EXTRACT(minute FROM NOW())::int % 5) * INTERVAL '1 minute',
				date_trunc('minute', NOW()),
				INTERVAL '5 minutes'
			) AS bucket
		),
		bucket_stats AS (
			SELECT
				date_trunc('minute', cr.created_at) - (EXTRACT(minute FROM cr.created_at)::int % 5) * INTERVAL '1 minute' AS bucket,
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE cr.status = 'success') AS successful
			FROM check_results cr
			WHERE cr.monitor_id = ANY($1)
			  AND cr.tenant_id = $2
			  AND cr.created_at >= NOW() - INTERVAL '1 hour'
			GROUP BY 1
		)
		SELECT
			b.bucket,
			COALESCE(bs.total, 0) AS total,
			COALESCE(bs.successful, 0) AS successful
		FROM buckets b
		LEFT JOIN bucket_stats bs ON b.bucket = bs.bucket
		ORDER BY b.bucket
	`, pq.Array(monitorIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query 5-minute uptime: %w", err)
	}
	defer rows.Close()

	var result []MinuteUptime
	for rows.Next() {
		var bucket time.Time
		var total, successful int
		if err := rows.Scan(&bucket, &total, &successful); err != nil {
			return nil, fmt.Errorf("failed to scan 5-minute uptime: %w", err)
		}
		result = append(result, MinuteUptime{
			Time:   bucket.Format("15:04"),
			Uptime: bucketUptime(total, successful),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating 5-minute uptime: %w", err)
	}
	return result, nil
}

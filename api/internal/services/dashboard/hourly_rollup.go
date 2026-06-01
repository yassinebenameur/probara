package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/db"
)

// MonitorRolling24hTotals is one monitor's exact-rolling-24h aggregate, built by
// stitching together monitor_hourly_rollups (full clock-hours strictly inside the
// window AND fully covered by the rollup cursor) with check_results for the
// partial leading hour, the partial trailing hour, and anything after the cursor.
//
// FailureChecks includes rollup-era bad checks (total − success) because
// monitor_hourly_rollups has no failure/error breakdown. ErrorChecks is therefore
// an undercount for the rollup region and reflects only raw-edge errors.
type MonitorRolling24hTotals struct {
	TotalChecks   int
	SuccessChecks int
	FailureChecks int
	ErrorChecks   int
	LatencySumMS  float64
	LatencyCount  int
	LatestStatus  *string
	LatestCheckAt *time.Time
}

// loadExactRolling24hSummary returns per-monitor totals over the exact rolling
// window (now-24h, now]. Empty input returns an empty map.
//
// The window is split into:
//   - Leading partial hour:  [w_start, leading_edge_end)              → raw check_results
//   - Rollup region:         [leading_edge_end, rollup_end)           → monitor_hourly_rollups
//   - Trailing-or-past-cursor: [rollup_end, w_end)                    → raw check_results
//
// where leading_edge_end  = date_trunc('hour', w_start) + 1h
//
//	trailing_edge_start = date_trunc('hour', w_end)
//	rollup_end        = LEAST(trailing_edge_start, date_trunc('hour', cursor))
//
// rollup_end == leading_edge_end means the rollup contributes nothing (e.g. the
// cursor lags by more than a day) and the entire window comes from raw.
func loadExactRolling24hSummary(ctx context.Context, dbClient db.DB, tenantID uuid.UUID, monitorIDs []uuid.UUID, now time.Time) (map[uuid.UUID]MonitorRolling24hTotals, error) {
	if len(monitorIDs) == 0 {
		return map[uuid.UUID]MonitorRolling24hTotals{}, nil
	}

	now = now.UTC()
	wStart := now.Add(-24 * time.Hour)
	wEnd := now

	query := `
		WITH rollup_state AS (
			SELECT last_created_at, last_check_result_id
			FROM rollup_job_state
			WHERE job_name = 'monitor_daily_rollups'
		),
		bounds AS (
			SELECT
				$2::timestamptz AS w_start,
				$3::timestamptz AS w_end,
				date_trunc('hour', $2::timestamptz) + INTERVAL '1 hour' AS leading_edge_end,
				date_trunc('hour', $3::timestamptz) AS trailing_edge_start
		),
		rollup_window AS (
			SELECT
				b.w_start,
				b.w_end,
				b.leading_edge_end,
				b.trailing_edge_start,
				LEAST(
					b.trailing_edge_start,
					COALESCE(date_trunc('hour', rs.last_created_at), b.leading_edge_end)
				) AS rollup_end
			FROM bounds b
			LEFT JOIN rollup_state rs ON TRUE
		),
		rollup_totals AS (
			SELECT
				mhr.monitor_id,
				SUM(mhr.total_checks)::bigint AS total_checks,
				SUM(mhr.success_checks)::bigint AS success_checks,
				SUM(mhr.latency_success_sum_ms) AS latency_sum_ms,
				SUM(mhr.latency_success_count)::bigint AS latency_count
			FROM monitor_hourly_rollups mhr
			CROSS JOIN rollup_window rw
			WHERE mhr.tenant_id = $1
			  AND mhr.monitor_id = ANY($4)
			  AND mhr.bucket_hour >= rw.leading_edge_end
			  AND mhr.bucket_hour < rw.rollup_end
			GROUP BY mhr.monitor_id
		),
		-- Raw edge rows are the partial leading hour and the trailing/past-cursor
		-- tail: everything in the exact window NOT covered by the rollup region
		-- [leading_edge_end, rollup_end). Selecting them as two direct indexed
		-- ranges (instead of scanning the full 24h and discarding the rollup
		-- region with a filter) lets Postgres use the (tenant_id, monitor_id,
		-- created_at) index to touch only the ~edge rows. The two ranges are
		-- disjoint: range 1 ends at leading_edge_end and range 2 starts at
		-- GREATEST(rollup_end, leading_edge_end) >= leading_edge_end. When the
		-- rollup contributes nothing (rollup_end <= leading_edge_end) the ranges
		-- meet at leading_edge_end and together cover the whole window. This is
		-- exactly equivalent to the previous full-scan + edge filter.
		raw_edge_rows AS MATERIALIZED (
			SELECT cr.monitor_id, cr.status, cr.latency_ms, cr.created_at
			FROM check_results cr
			CROSS JOIN rollup_window rw
			WHERE cr.tenant_id = $1
			  AND cr.monitor_id = ANY($4)
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= rw.w_start
			  AND cr.created_at < rw.leading_edge_end

			UNION ALL

			SELECT cr.monitor_id, cr.status, cr.latency_ms, cr.created_at
			FROM check_results cr
			CROSS JOIN rollup_window rw
			WHERE cr.tenant_id = $1
			  AND cr.monitor_id = ANY($4)
			  AND cr.result_source <> 'platform'
			  AND cr.created_at >= GREATEST(rw.rollup_end, rw.leading_edge_end)
			  AND cr.created_at < rw.w_end
		),
		raw_complement AS (
			SELECT
				er.monitor_id,
				COUNT(*)::bigint AS total_checks,
				COUNT(*) FILTER (WHERE er.status = 'success')::bigint AS success_checks,
				COUNT(*) FILTER (WHERE er.status = 'failure')::bigint AS failure_checks,
				COUNT(*) FILTER (WHERE er.status = 'error')::bigint AS error_checks,
				COALESCE(SUM(er.latency_ms) FILTER (WHERE er.status = 'success' AND er.latency_ms IS NOT NULL), 0)::double precision AS latency_sum_ms,
				COUNT(er.latency_ms) FILTER (WHERE er.status = 'success')::bigint AS latency_count
			FROM raw_edge_rows er
			GROUP BY er.monitor_id
		),
		latest_rollup AS (
			SELECT DISTINCT ON (mhr.monitor_id)
				mhr.monitor_id,
				mhr.latest_status,
				mhr.latest_check_at
			FROM monitor_hourly_rollups mhr
			CROSS JOIN rollup_window rw
			WHERE mhr.tenant_id = $1
			  AND mhr.monitor_id = ANY($4)
			  AND mhr.bucket_hour >= rw.leading_edge_end
			  AND mhr.bucket_hour < rw.rollup_end
			ORDER BY mhr.monitor_id, mhr.bucket_hour DESC
		),
		latest_raw AS (
			SELECT DISTINCT ON (er.monitor_id)
				er.monitor_id,
				er.status AS latest_status,
				er.created_at AS latest_check_at
			FROM raw_edge_rows er
			ORDER BY er.monitor_id, er.created_at DESC
		)
		SELECT
			m.id AS monitor_id,
			COALESCE(rt.total_checks, 0) + COALESCE(rc.total_checks, 0) AS total_checks,
			COALESCE(rt.success_checks, 0) + COALESCE(rc.success_checks, 0) AS success_checks,
			COALESCE(rc.failure_checks, 0) AS failure_checks_raw,
			COALESCE(rc.error_checks, 0) AS error_checks_raw,
			(COALESCE(rt.total_checks, 0) - COALESCE(rt.success_checks, 0)) AS bad_checks_rollup,
			COALESCE(rt.latency_sum_ms, 0) + COALESCE(rc.latency_sum_ms, 0) AS latency_sum_ms,
			COALESCE(rt.latency_count, 0) + COALESCE(rc.latency_count, 0) AS latency_count,
			CASE
				WHEN lr.latest_check_at IS NOT NULL
				 AND (lru.latest_check_at IS NULL OR lr.latest_check_at >= lru.latest_check_at)
					THEN lr.latest_status
				ELSE lru.latest_status
			END AS latest_status,
			GREATEST(lr.latest_check_at, lru.latest_check_at) AS latest_check_at
		FROM monitors m
		LEFT JOIN rollup_totals rt ON rt.monitor_id = m.id
		LEFT JOIN raw_complement rc ON rc.monitor_id = m.id
		LEFT JOIN latest_rollup lru ON lru.monitor_id = m.id
		LEFT JOIN latest_raw lr ON lr.monitor_id = m.id
		WHERE m.id = ANY($4) AND m.tenant_id = $1
	`

	rows, err := dbClient.QueryContext(ctx, query, tenantID, wStart, wEnd, pq.Array(monitorIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to query exact-rolling 24h summary: %w", err)
	}
	defer rows.Close()

	out := make(map[uuid.UUID]MonitorRolling24hTotals, len(monitorIDs))
	for rows.Next() {
		var (
			monitorID       uuid.UUID
			totalChecks     int64
			successChecks   int64
			failureCountRaw int64
			errorCountRaw   int64
			badChecksRollup int64
			latencySumMS    float64
			latencyCount    int64
			latestStatus    sql.NullString
			latestCheckAt   sql.NullTime
		)
		if err := rows.Scan(
			&monitorID,
			&totalChecks,
			&successChecks,
			&failureCountRaw,
			&errorCountRaw,
			&badChecksRollup,
			&latencySumMS,
			&latencyCount,
			&latestStatus,
			&latestCheckAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan rolling 24h summary row: %w", err)
		}

		// Rollup region does not break out failure vs error, so we attribute the
		// rollup-era bad checks to FailureChecks. The raw-edge counts still
		// preserve the breakdown for the partial leading/trailing/past-cursor portion.
		row := MonitorRolling24hTotals{
			TotalChecks:   int(totalChecks),
			SuccessChecks: int(successChecks),
			FailureChecks: int(failureCountRaw) + int(badChecksRollup),
			ErrorChecks:   int(errorCountRaw),
			LatencySumMS:  latencySumMS,
			LatencyCount:  int(latencyCount),
		}
		if latestStatus.Valid {
			s := latestStatus.String
			row.LatestStatus = &s
		}
		if latestCheckAt.Valid {
			ts := latestCheckAt.Time.UTC()
			row.LatestCheckAt = &ts
		}
		out[monitorID] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rolling 24h summary rows: %w", err)
	}
	return out, nil
}

// HourlyBucketPoint is one hour-aligned bucket [BucketStart, BucketStart+1h).
// The most recent bucket may cover the current incomplete hour and is built
// by adding the per-hour rollup row (if any) to raw check_results past the
// rollup cursor inside the same hour.
type HourlyBucketPoint struct {
	BucketStart   time.Time
	TotalChecks   int
	SuccessChecks int
	LatencySumMS  float64
	LatencyCount  int
}

// loadHourlyBucketSeries24h returns 24 hour-aligned bucket points ending at
// date_trunc('hour', now). Empty input returns 24 empty buckets.
func loadHourlyBucketSeries24h(ctx context.Context, dbClient db.DB, tenantID uuid.UUID, monitorIDs []uuid.UUID, now time.Time) ([]HourlyBucketPoint, error) {
	now = now.UTC()
	endHour := now.Truncate(time.Hour)
	startHour := endHour.Add(-23 * time.Hour)
	endExclusive := endHour.Add(time.Hour)

	series := make([]HourlyBucketPoint, 24)
	for i := 0; i < 24; i++ {
		series[i] = HourlyBucketPoint{BucketStart: startHour.Add(time.Duration(i) * time.Hour)}
	}
	if len(monitorIDs) == 0 {
		return series, nil
	}

	query := `
			WITH rollup_state AS (
				SELECT last_created_at, last_check_result_id
				FROM rollup_job_state
				WHERE job_name = 'monitor_daily_rollups'
			),
			raw_bounds AS MATERIALIZED (
				SELECT
					$2::timestamptz AS start_hour,
					$3::timestamptz AS end_exclusive,
					COALESCE(GREATEST($2::timestamptz, rs.last_created_at), $2::timestamptz) AS raw_start,
					rs.last_created_at,
					COALESCE(rs.last_check_result_id, '00000000-0000-0000-0000-000000000000'::uuid) AS last_check_result_id
				FROM (SELECT 1) anchor
				LEFT JOIN rollup_state rs ON TRUE
			),
			rollup_per_hour AS (
				SELECT
					mhr.bucket_hour,
					SUM(mhr.total_checks)::bigint AS total_checks,
					SUM(mhr.success_checks)::bigint AS success_checks,
				SUM(mhr.latency_success_sum_ms) AS latency_sum_ms,
				SUM(mhr.latency_success_count)::bigint AS latency_count
			FROM monitor_hourly_rollups mhr
			WHERE mhr.tenant_id = $1
			  AND mhr.monitor_id = ANY($4)
			  AND mhr.bucket_hour >= $2
			  AND mhr.bucket_hour < $3
			GROUP BY mhr.bucket_hour
		),
		raw_per_hour AS (
			SELECT
				date_trunc('hour', cr.created_at) AS bucket_hour,
				COUNT(*)::bigint AS total_checks,
				COUNT(*) FILTER (WHERE cr.status = 'success')::bigint AS success_checks,
					COALESCE(SUM(cr.latency_ms) FILTER (WHERE cr.status = 'success' AND cr.latency_ms IS NOT NULL), 0)::double precision AS latency_sum_ms,
					COUNT(cr.latency_ms) FILTER (WHERE cr.status = 'success')::bigint AS latency_count
				FROM check_results cr
				CROSS JOIN raw_bounds rb
				WHERE cr.tenant_id = $1
				  AND cr.monitor_id = ANY($4)
				  AND cr.result_source <> 'platform'
				  AND cr.created_at >= rb.raw_start
				  AND cr.created_at < rb.end_exclusive
				  AND (
					rb.last_created_at IS NULL
					OR (cr.created_at, cr.id) > (
						rb.last_created_at,
						rb.last_check_result_id
					)
				  )
				GROUP BY 1
			)
		SELECT
			b.bucket_hour,
			COALESCE(rh.total_checks, 0) + COALESCE(rwh.total_checks, 0) AS total_checks,
			COALESCE(rh.success_checks, 0) + COALESCE(rwh.success_checks, 0) AS success_checks,
			COALESCE(rh.latency_sum_ms, 0) + COALESCE(rwh.latency_sum_ms, 0) AS latency_sum_ms,
			COALESCE(rh.latency_count, 0) + COALESCE(rwh.latency_count, 0) AS latency_count
		FROM generate_series($2::timestamptz, ($3::timestamptz - INTERVAL '1 hour'), INTERVAL '1 hour') AS b(bucket_hour)
		LEFT JOIN rollup_per_hour rh ON rh.bucket_hour = b.bucket_hour
		LEFT JOIN raw_per_hour rwh ON rwh.bucket_hour = b.bucket_hour
		ORDER BY b.bucket_hour
	`

	rows, err := dbClient.QueryContext(ctx, query, tenantID, startHour, endExclusive, pq.Array(monitorIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to query hourly bucket series: %w", err)
	}
	defer rows.Close()

	idx := 0
	for rows.Next() {
		var (
			bucket       time.Time
			total        int64
			success      int64
			latencySum   float64
			latencyCount int64
		)
		if err := rows.Scan(&bucket, &total, &success, &latencySum, &latencyCount); err != nil {
			return nil, fmt.Errorf("failed to scan hourly bucket row: %w", err)
		}
		if idx >= len(series) {
			break
		}
		bucketUTC := bucket.UTC()
		if !bucketUTC.Equal(series[idx].BucketStart) {
			return nil, fmt.Errorf("hourly bucket series misaligned at index %d: got %s, want %s", idx, bucketUTC, series[idx].BucketStart)
		}
		series[idx] = HourlyBucketPoint{
			BucketStart:   bucketUTC,
			TotalChecks:   int(total),
			SuccessChecks: int(success),
			LatencySumMS:  latencySum,
			LatencyCount:  int(latencyCount),
		}
		idx++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating hourly bucket rows: %w", err)
	}
	return series, nil
}

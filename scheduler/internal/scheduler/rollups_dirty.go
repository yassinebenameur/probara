package scheduler

// Dirty-ledger rollup consumer (Stage 7, see docs/correctness-notes.md):
// result ingestion marks each inserted row's (monitor, hour) in rollup_dirty
// within the same transaction, and this consumer rebuilds marked hourly
// buckets WHOLESALE from raw rows (REPLACE semantics, as
// scripts/backfill_hourly_rollups.sql does), then re-derives the affected
// daily buckets from the hourly table. Exact by construction: whenever a row
// commits — late, redelivered, behind any clock — its bucket is marked, so
// nothing can be permanently skipped (the retired cursor scanned worker-clock
// created_at and silently lost late-visible rows). A failed batch simply
// leaves its marks in the ledger for the next run; there is no poison-skip.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type dirtyBucket struct {
	MonitorID  uuid.UUID
	BucketHour time.Time
	// MarkedAt is captured at batch load; the consumed-marks delete is
	// conditional on it, so a producer re-marking the bucket after the load
	// (its row invisible to this rebuild) keeps its mark for the next run.
	MarkedAt time.Time
}

// consumeDirtyBuckets drains the ledger in batches. It reports how many
// buckets were rebuilt and whether the ledger was fully drained (only then
// may the cursor advance).
func (s *Scheduler) consumeDirtyBuckets(ctx context.Context) (int, bool, error) {
	processed := 0
	for {
		if ctx.Err() != nil {
			return processed, false, ctx.Err()
		}
		batch, err := s.loadDirtyBatch(ctx)
		if err != nil {
			return processed, false, err
		}
		if len(batch) == 0 {
			return processed, true, nil
		}
		if err := s.rebuildDirtyBatchTx(ctx, batch); err != nil {
			return processed, false, err
		}
		processed += len(batch)
		if len(batch) < rollupMaintenanceBatchSize {
			return processed, true, nil
		}
	}
}

func (s *Scheduler) loadDirtyBatch(ctx context.Context) ([]dirtyBucket, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT monitor_id, bucket_hour, marked_at FROM rollup_dirty
		ORDER BY bucket_hour, monitor_id
		LIMIT $1
	`, rollupMaintenanceBatchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dirtyBucket
	for rows.Next() {
		var b dirtyBucket
		if err := rows.Scan(&b.MonitorID, &b.BucketHour, &b.MarkedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// rebuildDirtyBatchTx rebuilds every bucket in the batch, re-derives the
// affected daily rows, and deletes the consumed marks — one transaction, so
// a failure leaves the ledger intact for the next run.
func (s *Scheduler) rebuildDirtyBatchTx(ctx context.Context, batch []dirtyBucket) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	monitorIDs := make([]uuid.UUID, 0, len(batch))
	hours := make([]time.Time, 0, len(batch))
	markedAts := make([]time.Time, 0, len(batch))
	type dayKey struct {
		monitor uuid.UUID
		day     time.Time
	}
	days := make(map[dayKey]struct{}, len(batch))
	for _, b := range batch {
		monitorIDs = append(monitorIDs, b.MonitorID)
		hours = append(hours, b.BucketHour)
		markedAts = append(markedAts, b.MarkedAt)
		day := time.Date(b.BucketHour.UTC().Year(), b.BucketHour.UTC().Month(), b.BucketHour.UTC().Day(), 0, 0, 0, 0, time.UTC)
		days[dayKey{b.MonitorID, day}] = struct{}{}
	}

	for _, b := range batch {
		// Wholesale hourly rebuild (REPLACE): column math mirrors
		// scripts/backfill_hourly_rollups.sql.
		res, err := tx.ExecContext(ctx, `
			INSERT INTO monitor_hourly_rollups (
				tenant_id, monitor_id, bucket_hour, total_checks, success_checks, error_checks,
				latency_success_sum_ms, latency_success_count, latest_status, latest_check_at,
				created_at, updated_at
			)
			SELECT
				cr.tenant_id, cr.monitor_id, $2::timestamptz,
				COUNT(*),
				COUNT(*) FILTER (WHERE cr.status = 'success'),
				COUNT(*) FILTER (WHERE cr.status = 'error'),
				COALESCE(SUM(cr.latency_ms) FILTER (WHERE cr.status = 'success' AND cr.latency_ms IS NOT NULL), 0)::DOUBLE PRECISION,
				COUNT(cr.latency_ms) FILTER (WHERE cr.status = 'success'),
				(ARRAY_AGG(cr.status ORDER BY cr.created_at DESC, cr.id DESC))[1],
				MAX(cr.created_at),
				NOW(), NOW()
			FROM check_results cr
			WHERE cr.result_source = 'monitor'
			  AND cr.monitor_id = $1
			  AND cr.created_at >= $2::timestamptz
			  AND cr.created_at < $2::timestamptz + INTERVAL '1 hour'
			GROUP BY cr.tenant_id, cr.monitor_id
			ON CONFLICT (monitor_id, bucket_hour) DO UPDATE SET
				tenant_id = EXCLUDED.tenant_id,
				total_checks = EXCLUDED.total_checks,
				success_checks = EXCLUDED.success_checks,
				error_checks = EXCLUDED.error_checks,
				latency_success_sum_ms = EXCLUDED.latency_success_sum_ms,
				latency_success_count = EXCLUDED.latency_success_count,
				latest_status = EXCLUDED.latest_status,
				latest_check_at = EXCLUDED.latest_check_at,
				updated_at = NOW()
		`, b.MonitorID, b.BucketHour)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// No surviving raw rows (retention/purge): the bucket is empty.
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM monitor_hourly_rollups WHERE monitor_id = $1 AND bucket_hour = $2
			`, b.MonitorID, b.BucketHour); err != nil {
				return err
			}
		}
	}

	for k := range days {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO monitor_daily_rollups (
				tenant_id, monitor_id, bucket_day, total_checks, success_checks, error_checks,
				latency_success_sum_ms, latency_success_count, latest_status, latest_check_at,
				created_at, updated_at
			)
			SELECT
				hr.tenant_id, hr.monitor_id, $2::date,
				SUM(hr.total_checks), SUM(hr.success_checks), SUM(hr.error_checks),
				SUM(hr.latency_success_sum_ms), SUM(hr.latency_success_count),
				(ARRAY_AGG(hr.latest_status ORDER BY hr.latest_check_at DESC NULLS LAST))[1],
				MAX(hr.latest_check_at),
				NOW(), NOW()
			FROM monitor_hourly_rollups hr
			WHERE hr.monitor_id = $1
			  AND hr.bucket_hour >= $2::timestamptz
			  AND hr.bucket_hour < $2::timestamptz + INTERVAL '1 day'
			GROUP BY hr.tenant_id, hr.monitor_id
			ON CONFLICT (monitor_id, bucket_day) DO UPDATE SET
				tenant_id = EXCLUDED.tenant_id,
				total_checks = EXCLUDED.total_checks,
				success_checks = EXCLUDED.success_checks,
				error_checks = EXCLUDED.error_checks,
				latency_success_sum_ms = EXCLUDED.latency_success_sum_ms,
				latency_success_count = EXCLUDED.latency_success_count,
				latest_status = EXCLUDED.latest_status,
				latest_check_at = EXCLUDED.latest_check_at,
				updated_at = NOW()
		`, k.monitor, k.day)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM monitor_daily_rollups WHERE monitor_id = $1 AND bucket_day = $2
			`, k.monitor, k.day); err != nil {
				return err
			}
		}
	}

	// Conditional consume: a mark re-touched (marked_at refreshed) after this
	// batch was loaded belongs to a row this rebuild could not see — leave it
	// for the next run. The producer's raw insert and mark refresh commit
	// atomically, so a mark visible at load time implies its rows are visible
	// to the rebuild above.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM rollup_dirty d
		USING unnest($1::uuid[], $2::timestamptz[], $3::timestamptz[]) AS u(monitor_id, bucket_hour, marked_at)
		WHERE d.monitor_id = u.monitor_id
		  AND d.bucket_hour = u.bucket_hour
		  AND d.marked_at <= u.marked_at
	`, pq.Array(monitorIDs), pq.Array(hours), pq.Array(markedAts)); err != nil {
		return err
	}
	return tx.Commit()
}

// advanceRollupCursor moves the completeness watermark the dashboard 24h
// stitcher reads (shared/analytics/rollup_cursor.go). Called only after a
// run that DRAINED the ledger: at that moment every committed row has been
// folded, so the newest row is a valid high-water mark. Never moves
// backwards. A row committing mid-advance is at most one tick behind — its
// mark is already in the ledger.
func (s *Scheduler) advanceRollupCursor(ctx context.Context) error {
	var lastCreatedAt time.Time
	var lastID uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		SELECT created_at, id FROM check_results
		WHERE result_source = 'monitor'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`).Scan(&lastCreatedAt, &lastID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// Full-tuple comparison: a tie on created_at with a larger id must still
	// advance, or tuple-based raw branches double-count the already-folded
	// newest row.
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO rollup_job_state (job_name, last_created_at, last_check_result_id, last_run_at, updated_at)
		VALUES ($1, $2, $3, NOW(), NOW())
		ON CONFLICT (job_name) DO UPDATE SET
			last_created_at = CASE
				WHEN rollup_job_state.last_created_at IS NULL
					OR (EXCLUDED.last_created_at, EXCLUDED.last_check_result_id) > (rollup_job_state.last_created_at, rollup_job_state.last_check_result_id)
				THEN EXCLUDED.last_created_at
				ELSE rollup_job_state.last_created_at
			END,
			last_check_result_id = CASE
				WHEN rollup_job_state.last_created_at IS NULL
					OR (EXCLUDED.last_created_at, EXCLUDED.last_check_result_id) > (rollup_job_state.last_created_at, rollup_job_state.last_check_result_id)
				THEN EXCLUDED.last_check_result_id
				ELSE rollup_job_state.last_check_result_id
			END,
			last_run_at = NOW(),
			updated_at = NOW()
	`, rollupJobName, lastCreatedAt, lastID)
	return err
}

// syncDowntimeFromIntervals projects the state timeline onto the downtime
// tables: closed 'down' intervals upsert into monitor_downtime_periods, and
// monitor_downtime_open mirrors the currently open 'down' interval. Upsert-
// only — historical (pre-timeline) periods are never touched. Group monitors
// have no timeline and keep their frozen rows until their state derivation
// is unified.
func (s *Scheduler) syncDowntimeFromIntervals(ctx context.Context, since time.Time) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT monitor_id FROM monitor_state_intervals
		WHERE created_at > $1 OR ended_at > $1
	`, since)
	if err != nil {
		return err
	}
	ids, err := scanUUIDs(rows)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.syncMonitorDowntime(ctx, id); err != nil {
			s.logger.WithError(err).WithField("monitor_id", id).Error("Failed to sync downtime from intervals")
		}
	}
	return nil
}

func (s *Scheduler) syncMonitorDowntime(ctx context.Context, monitorID uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO monitor_downtime_periods (tenant_id, monitor_id, start_time, end_time, created_at, updated_at)
		SELECT tenant_id, monitor_id, started_at, ended_at, NOW(), NOW()
		FROM monitor_state_intervals
		WHERE monitor_id = $1 AND state = 'down' AND ended_at IS NOT NULL
		ON CONFLICT (monitor_id, start_time) DO UPDATE SET
			end_time = EXCLUDED.end_time,
			updated_at = NOW()
	`, monitorID); err != nil {
		return err
	}

	var tenantID uuid.UUID
	var openStart time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT tenant_id, started_at FROM monitor_state_intervals
		WHERE monitor_id = $1 AND ended_at IS NULL AND state = 'down'
	`, monitorID).Scan(&tenantID, &openStart)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM monitor_downtime_open WHERE monitor_id = $1
		`, monitorID); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		// monitor_downtime_open keys the interval start on started_at (000035);
		// last_seen_at is NOT NULL and, under interval projection, best
		// approximated by the sync time (the interval is open right now).
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO monitor_downtime_open (tenant_id, monitor_id, started_at, last_seen_at, updated_at)
			VALUES ($1, $2, $3, NOW(), NOW())
			ON CONFLICT (monitor_id) DO UPDATE SET
				started_at = EXCLUDED.started_at,
				last_seen_at = EXCLUDED.last_seen_at,
				updated_at = NOW()
		`, tenantID, monitorID, openStart); err != nil {
			return err
		}
	}
	return tx.Commit()
}

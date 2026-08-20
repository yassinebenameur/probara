package scheduler

// Metric-store maintenance, run inside the rollup maintenance cycle (same
// advisory lock and cadence as the check-result rollups):
//
//   - partition upkeep: create daily metric_samples partitions ahead, drop
//     partitions past METRIC_RAW_RETENTION_DAYS (retention by partition DROP,
//     never row deletes);
//   - dirty-ledger consumption: OTLP ingest marks (monitor, hour) in
//     metric_rollup_dirty in the same transaction as its sample inserts; this
//     consumer rebuilds every series of a marked monitor-hour WHOLESALE into
//     metric_rollups_hourly. Same exactness contract as rollups_dirty.go: a
//     failed batch leaves its marks, re-marks mid-rebuild survive the
//     conditional delete.

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/metricstore"
)

// metricPartitionAheadDays: daily partitions are pre-created this far ahead.
// The rebuild's one-hour lookback (INTERVAL '1 hour' in the CTE) exists so
// each first-in-bucket sample has its predecessor for the reset-aware
// increase; series reporting slower than hourly lose only the boundary delta.
const metricPartitionAheadDays = 3

// maintainMetricPartitions creates upcoming daily partitions and drops the
// ones fully past the raw retention window.
func (s *Scheduler) maintainMetricPartitions(ctx context.Context) error {
	now := time.Now().UTC()
	if err := metricstore.EnsurePartitions(ctx, s.db, now, now.AddDate(0, 0, metricPartitionAheadDays)); err != nil {
		return err
	}

	retentionDays := s.config.MetricRawRetentionDays
	if retentionDays <= 0 {
		return nil
	}
	// A partition covering [day, day+1) is droppable once day+1 <= cutoff.
	cutoff := now.AddDate(0, 0, -retentionDays).Truncate(24 * time.Hour)

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.relname
		FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = 'metric_samples'
	`)
	if err != nil {
		return fmt.Errorf("list metric_samples partitions: %w", err)
	}
	defer rows.Close()
	var drop []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan partition name: %w", err)
		}
		const prefix = "metric_samples_"
		if len(name) != len(prefix)+8 || name[:len(prefix)] != prefix {
			continue
		}
		day, err := time.ParseInLocation("20060102", name[len(prefix):], time.UTC)
		if err != nil {
			continue
		}
		if !day.Add(24 * time.Hour).After(cutoff) {
			drop = append(drop, name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range drop {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, pq.QuoteIdentifier(name))); err != nil {
			return fmt.Errorf("drop expired partition %s: %w", name, err)
		}
		s.logger.WithField("partition", name).Info("Dropped expired metric_samples partition")
	}
	return nil
}

// consumeMetricDirtyBuckets drains metric_rollup_dirty in batches, rebuilding
// each marked monitor-hour wholesale. Reports rebuilt bucket count and
// whether the ledger drained.
func (s *Scheduler) consumeMetricDirtyBuckets(ctx context.Context) (int, bool, error) {
	processed := 0
	for {
		if ctx.Err() != nil {
			return processed, false, ctx.Err()
		}
		batch, err := s.loadMetricDirtyBatch(ctx)
		if err != nil {
			return processed, false, err
		}
		if len(batch) == 0 {
			return processed, true, nil
		}
		if err := s.rebuildMetricDirtyBatchTx(ctx, batch); err != nil {
			return processed, false, err
		}
		processed += len(batch)
		if len(batch) < rollupMaintenanceBatchSize {
			return processed, true, nil
		}
	}
}

func (s *Scheduler) loadMetricDirtyBatch(ctx context.Context) ([]dirtyBucket, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT monitor_id, bucket_hour, marked_at FROM metric_rollup_dirty
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

func (s *Scheduler) rebuildMetricDirtyBatchTx(ctx context.Context, batch []dirtyBucket) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	monitorIDs := make([]uuid.UUID, 0, len(batch))
	hours := make([]time.Time, 0, len(batch))
	markedAts := make([]time.Time, 0, len(batch))
	for _, b := range batch {
		monitorIDs = append(monitorIDs, b.MonitorID)
		hours = append(hours, b.BucketHour)
		markedAts = append(markedAts, b.MarkedAt)
	}

	for _, b := range batch {
		// Wholesale REPLACE: clear the monitor-hour, then rebuild every series
		// from raw samples. `increase` is the reset-aware sum of positive
		// deltas whose later sample falls in the bucket (a drop = counter
		// reset contributes the post-reset value); NULL for gauges. The
		// window extends one hour back so the first in-bucket delta has its
		// predecessor.
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM metric_rollups_hourly r
			USING metric_series s
			WHERE r.series_id = s.id AND s.monitor_id = $1 AND r.bucket = $2
		`, b.MonitorID, b.BucketHour); err != nil {
			return fmt.Errorf("clear metric rollup bucket: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			WITH w AS (
				SELECT ms.series_id, ms.ts, ms.value,
				       s.metric_type = 'sum' AND s.is_monotonic AS is_counter,
				       lag(ms.value) OVER (PARTITION BY ms.series_id ORDER BY ms.ts) AS prev
				FROM metric_samples ms
				JOIN metric_series s ON s.id = ms.series_id
				WHERE s.monitor_id = $1
				  AND ms.ts >= $2::timestamptz - INTERVAL '1 hour'
				  AND ms.ts < $2::timestamptz + INTERVAL '1 hour'
			)
			INSERT INTO metric_rollups_hourly (
				series_id, bucket, sample_count, min_value, max_value,
				sum_value, first_value, last_value, increase
			)
			SELECT series_id, $2::timestamptz,
			       COUNT(*) FILTER (WHERE ts >= $2::timestamptz),
			       MIN(value) FILTER (WHERE ts >= $2::timestamptz),
			       MAX(value) FILTER (WHERE ts >= $2::timestamptz),
			       SUM(value) FILTER (WHERE ts >= $2::timestamptz),
			       (ARRAY_AGG(value ORDER BY ts) FILTER (WHERE ts >= $2::timestamptz))[1],
			       (ARRAY_AGG(value ORDER BY ts DESC) FILTER (WHERE ts >= $2::timestamptz))[1],
			       CASE WHEN BOOL_AND(is_counter) THEN
			           COALESCE(SUM(
			               CASE WHEN prev IS NULL THEN NULL
			                    WHEN value >= prev THEN value - prev
			                    ELSE value END
			           ) FILTER (WHERE ts >= $2::timestamptz), 0)
			       ELSE NULL END
			FROM w
			GROUP BY series_id
			HAVING COUNT(*) FILTER (WHERE ts >= $2::timestamptz) > 0
		`, b.MonitorID, b.BucketHour); err != nil {
			return fmt.Errorf("rebuild metric rollup bucket: %w", err)
		}
	}

	// Conditional consume — identical contract to rollup_dirty (see
	// rollups_dirty.go): only marks unchanged since load are deleted.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM metric_rollup_dirty d
		USING unnest($1::uuid[], $2::timestamptz[], $3::timestamptz[]) AS u(monitor_id, bucket_hour, marked_at)
		WHERE d.monitor_id = u.monitor_id
		  AND d.bucket_hour = u.bucket_hour
		  AND d.marked_at <= u.marked_at
	`, pq.Array(monitorIDs), pq.Array(hours), pq.Array(markedAts)); err != nil {
		return err
	}
	return tx.Commit()
}

// pruneTenantMetricSamples applies a tenant's tighter data_retention_days to
// raw metric samples (the global cap is enforced by partition drop). Batched
// like check-result retention; hourly metric rollups survive to the 400-day
// prune like every other rollup.
func (s *Scheduler) pruneTenantMetricSamples(ctx context.Context, tenantID uuid.UUID, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	batchSize := s.config.RetentionCleanupBatchSize
	if batchSize <= 0 {
		batchSize = 5000
	}
	maxRows := s.config.RetentionCleanupMaxRowsPerRun
	if maxRows <= 0 {
		maxRows = 200000
	}

	var totalDeleted int64
	for totalDeleted < int64(maxRows) {
		limit := batchSize
		if remaining := int(int64(maxRows) - totalDeleted); remaining < limit {
			limit = remaining
		}
		res, err := s.db.ExecContext(ctx, `
			DELETE FROM metric_samples
			WHERE (series_id, ts) IN (
				SELECT ms.series_id, ms.ts
				FROM metric_samples ms
				JOIN metric_series s ON s.id = ms.series_id
				WHERE s.tenant_id = $1 AND ms.ts < $2
				LIMIT $3
			)
		`, tenantID, cutoff, limit)
		if err != nil {
			return totalDeleted, fmt.Errorf("prune tenant metric samples: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return totalDeleted, err
		}
		totalDeleted += n
		if n < int64(limit) {
			break
		}
	}
	return totalDeleted, nil
}

package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

const (
	rollupMaintenanceAdvisoryLock = int64(901_337_402)
	rollupMaintenanceTimeout      = 30 * time.Minute
	rollupMaintenanceBatchSize    = 5000
	rollupRetentionDays           = 400
	rollupJobName                 = "monitor_daily_rollups"
)

func (s *Scheduler) triggerRollupMaintenance() {
	s.rollupMu.Lock()
	if s.rollupRunning {
		s.rollupMu.Unlock()
		return
	}
	s.rollupRunning = true
	s.rollupMu.Unlock()

	go func() {
		defer func() {
			s.rollupMu.Lock()
			s.rollupRunning = false
			s.rollupMu.Unlock()
		}()

		processed, cursorUnix, err := s.runRollupMaintenance()
		if err != nil {
			s.rollupErrors.With(prometheus.Labels{}).Inc()
			s.logger.WithError(err).Error("Rollup maintenance run failed")
			return
		}

		s.rollupRuns.With(prometheus.Labels{}).Inc()
		if processed > 0 {
			s.rollupRows.With(prometheus.Labels{}).Add(float64(processed))
		}
		if cursorUnix > 0 {
			s.rollupCursor.With(prometheus.Labels{}).Set(float64(cursorUnix))
		}
	}()
}

func (s *Scheduler) runRollupMaintenance() (int, int64, error) {
	started := time.Now()
	defer func() {
		s.rollupDuration.With(prometheus.Labels{}).Observe(time.Since(started).Seconds())
	}()

	ctx, cancel := context.WithTimeout(s.ctx, rollupMaintenanceTimeout)
	defer cancel()

	lock, err := acquireMaintenanceLock(ctx, s.db.DB, rollupMaintenanceAdvisoryLock)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to acquire rollup advisory lock: %w", err)
	}
	if lock == nil {
		return 0, 0, nil
	}
	defer func() {
		if err := lock.release(); err != nil {
			s.logger.WithError(err).Warn("Failed to release rollup advisory lock")
		}
	}()

	s.logger.WithField("timeout", rollupMaintenanceTimeout.String()).Info("Rollup maintenance run started")

	// Dirty-ledger consumption (rollups_dirty.go): rebuild marked buckets
	// wholesale. A failed batch leaves its marks for the next run — errors
	// here never skip data, transient or not (the old cursor's poison-skip
	// path is retired).
	totalProcessed, drained, err := s.consumeDirtyBuckets(ctx)
	if err != nil {
		return totalProcessed, 0, err
	}

	// The completeness watermark may only advance when the ledger is empty:
	// at that moment every committed row has been folded.
	if drained {
		if err := s.advanceRollupCursor(ctx); err != nil {
			return totalProcessed, 0, err
		}
	}

	// Downtime tables are a projection of the state timeline. The 10-minute
	// overlap makes the sync window generously idempotent across runs.
	if err := s.syncDowntimeFromIntervals(ctx, started.Add(-10*time.Minute)); err != nil {
		return totalProcessed, 0, err
	}

	if err := s.pruneRollupTables(ctx); err != nil {
		return totalProcessed, 0, err
	}

	// Metric store maintenance (same lock/cadence): partition upkeep, then
	// the metric dirty-ledger consumer. Failures leave marks in place — next
	// run retries, nothing is skipped.
	if err := s.maintainMetricPartitions(ctx); err != nil {
		return totalProcessed, 0, err
	}
	metricProcessed, _, err := s.consumeMetricDirtyBuckets(ctx)
	if err != nil {
		return totalProcessed + metricProcessed, 0, err
	}
	totalProcessed += metricProcessed

	var lastCursorUnix int64
	var lastCreatedAt sql.NullTime
	if err := s.db.QueryRowContext(ctx, `
		SELECT last_created_at FROM rollup_job_state WHERE job_name = $1
	`, rollupJobName).Scan(&lastCreatedAt); err == nil && lastCreatedAt.Valid {
		lastCursorUnix = lastCreatedAt.Time.UTC().Unix()
	}

	s.logger.WithFields(logrus.Fields{
		"rebuilt_buckets": totalProcessed,
		"drained":         drained,
		"elapsed":         time.Since(started).String(),
	}).Info("Rollup maintenance run completed")

	return totalProcessed, lastCursorUnix, nil
}

func (s *Scheduler) pruneRollupTables(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin rollup prune transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := pruneRollupTablesTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit rollup prune transaction: %w", err)
	}
	return nil
}

func pruneRollupTablesTx(ctx context.Context, tx *sql.Tx) error {
	cutoff := time.Now().UTC().AddDate(0, 0, -rollupRetentionDays)
	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_daily_rollups WHERE bucket_day < $1::date`, cutoff); err != nil {
		return fmt.Errorf("failed to prune daily rollups: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_hourly_rollups WHERE bucket_hour < $1`, cutoff); err != nil {
		return fmt.Errorf("failed to prune hourly rollups: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_downtime_periods WHERE end_time < $1`, cutoff); err != nil {
		return fmt.Errorf("failed to prune downtime periods: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE monitor_downtime_open SET started_at = $1, updated_at = NOW() WHERE started_at < $1`, cutoff); err != nil {
		return fmt.Errorf("failed to clamp open downtime periods: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM metric_rollups_hourly WHERE bucket < $1`, cutoff); err != nil {
		return fmt.Errorf("failed to prune metric rollups: %w", err)
	}
	return nil
}

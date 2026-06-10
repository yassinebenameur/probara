package scheduler

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

const (
	rollupMaintenanceAdvisoryLock = int64(901_337_402)
	rollupMaintenanceTimeout      = 30 * time.Minute
	rollupMaintenanceBatchSize    = 5000
	rollupProgressLogInterval     = 15 * time.Second
	rollupRetentionDays           = 400
	rollupJobName                 = "monitor_daily_rollups"
)

type rollupState struct {
	LastCreatedAt *time.Time
	LastResultID  *uuid.UUID
}

type rollupCheckResult struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	MonitorID    uuid.UUID
	Status       string
	LatencyMS    sql.NullInt64
	ResultSource string
	CreatedAt    time.Time
}

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

	var locked bool
	if err := s.db.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", rollupMaintenanceAdvisoryLock).Scan(&locked); err != nil {
		return 0, 0, fmt.Errorf("failed to acquire rollup advisory lock: %w", err)
	}
	if !locked {
		return 0, 0, nil
	}
	defer func() {
		if _, err := s.db.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", rollupMaintenanceAdvisoryLock); err != nil {
			s.logger.WithError(err).Warn("Failed to release rollup advisory lock")
		}
	}()

	state, err := s.loadRollupState(ctx)
	if err != nil {
		return 0, 0, err
	}

	totalProcessed := 0
	lastCursorUnix := int64(0)
	batches := 0
	lastProgressLog := started

	s.logger.WithFields(logrus.Fields{
		"cursor_time": state.LastCreatedAt,
		"cursor_id":   state.LastResultID,
		"timeout":     rollupMaintenanceTimeout.String(),
	}).Info("Rollup maintenance run started")

	for {
		rows, err := s.loadRollupBatch(ctx, state)
		if err != nil {
			return totalProcessed, lastCursorUnix, err
		}
		if len(rows) == 0 {
			break
		}

		// Fast path: the whole batch in one transaction.
		if err := s.applyRollupBatchTx(ctx, rows, state); err != nil {
			if isTransientRollupError(err) {
				// Transient infrastructure failure: abort the run; the cursor
				// was not advanced and the next run retries from here.
				return totalProcessed, lastCursorUnix, err
			}
			// Non-transient batch failure: likely a poisoned row. Replay the
			// batch row-by-row so a single bad row cannot wedge the cursor.
			s.logger.WithError(err).Warn("Rollup batch failed; replaying batch row-by-row")
			applied, replayErr := s.replayRollupBatchRowByRow(ctx, rows, state)
			totalProcessed += applied
			if state.LastCreatedAt != nil {
				lastCursorUnix = state.LastCreatedAt.UTC().Unix()
			}
			if replayErr != nil {
				return totalProcessed, lastCursorUnix, replayErr
			}
		} else {
			totalProcessed += len(rows)
		}
		batches++
		if state.LastCreatedAt != nil {
			lastCursorUnix = state.LastCreatedAt.UTC().Unix()
		}

		now := time.Now()
		if now.Sub(lastProgressLog) >= rollupProgressLogInterval {
			s.logger.WithFields(logrus.Fields{
				"processed_rows": totalProcessed,
				"batches":        batches,
				"elapsed":        now.Sub(started).String(),
				"cursor_time":    state.LastCreatedAt,
				"cursor_id":      state.LastResultID,
			}).Info("Rollup maintenance progress")
			lastProgressLog = now
		}

		if len(rows) < rollupMaintenanceBatchSize {
			break
		}
	}

	if err := s.pruneRollupTables(ctx); err != nil {
		return totalProcessed, lastCursorUnix, err
	}
	if totalProcessed == 0 && state.LastCreatedAt != nil {
		lastCursorUnix = state.LastCreatedAt.UTC().Unix()
	}

	s.logger.WithFields(logrus.Fields{
		"processed_rows": totalProcessed,
		"batches":        batches,
		"elapsed":        time.Since(started).String(),
		"cursor_time":    state.LastCreatedAt,
		"cursor_id":      state.LastResultID,
	}).Info("Rollup maintenance run completed")

	return totalProcessed, lastCursorUnix, nil
}

// applyRollupBatchTx applies a whole batch of check results plus the cursor
// advance in a single transaction. On success the caller's state is advanced
// past the last row of the batch; on failure the transaction is rolled back
// and the state is left untouched.
func (s *Scheduler) applyRollupBatchTx(ctx context.Context, rows []rollupCheckResult, state *rollupState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin rollup transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	pending := *state
	for _, row := range rows {
		if err := s.applyRow(ctx, tx, row); err != nil {
			return err
		}
		advanceRollupCursor(&pending, row)
	}
	if err := upsertRollupState(ctx, tx, &pending); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit rollup transaction: %w", err)
	}
	committed = true
	*state = pending
	return nil
}

// replayRollupBatchRowByRow retries a failed batch one row per transaction.
// A row that fails with a non-transient error is SKIPPED: it is logged at
// ERROR level, counted in rollup_rows_skipped_total, and the cursor is
// committed past it so the run keeps making progress (the raw check_results
// row is untouched and remains recoverable later via backfill). A transient
// failure aborts the run; the cursor has already been committed through the
// last successfully processed row. Returns the number of rows applied
// (skipped rows excluded).
func (s *Scheduler) replayRollupBatchRowByRow(ctx context.Context, rows []rollupCheckResult, state *rollupState) (int, error) {
	applied := 0
	for _, row := range rows {
		err := s.applyRollupRowTx(ctx, row, state)
		if err == nil {
			applied++
			continue
		}
		if isTransientRollupError(err) {
			return applied, fmt.Errorf("transient error during rollup row replay: %w", err)
		}
		s.logger.WithError(err).WithFields(logrus.Fields{
			"check_result_id": row.ID,
			"tenant_id":       row.TenantID,
			"monitor_id":      row.MonitorID,
			"created_at":      row.CreatedAt,
		}).Error("Skipping poisoned rollup row")
		s.rollupRowsSkipped.With(prometheus.Labels{}).Inc()
		if err := s.commitRollupCursorPast(ctx, row, state); err != nil {
			return applied, err
		}
	}
	return applied, nil
}

// applyRollupRowTx applies a single check result and the matching cursor
// advance in its own short transaction. The caller's state is advanced only
// after a successful commit.
func (s *Scheduler) applyRollupRowTx(ctx context.Context, row rollupCheckResult, state *rollupState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin rollup row transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := s.applyRow(ctx, tx, row); err != nil {
		return err
	}
	pending := *state
	advanceRollupCursor(&pending, row)
	if err := upsertRollupState(ctx, tx, &pending); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit rollup row transaction: %w", err)
	}
	committed = true
	*state = pending
	return nil
}

// commitRollupCursorPast persists a cursor advance past a skipped row in its
// own transaction, without applying the row.
func (s *Scheduler) commitRollupCursorPast(ctx context.Context, row rollupCheckResult, state *rollupState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin rollup cursor transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	pending := *state
	advanceRollupCursor(&pending, row)
	if err := upsertRollupState(ctx, tx, &pending); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit rollup cursor transaction: %w", err)
	}
	committed = true
	*state = pending
	return nil
}

func advanceRollupCursor(state *rollupState, row rollupCheckResult) {
	createdAt := row.CreatedAt.UTC()
	id := row.ID
	state.LastCreatedAt = &createdAt
	state.LastResultID = &id
}

// isTransientRollupError reports whether err looks like a transient
// infrastructure failure (worth aborting the run and retrying later) rather
// than a data problem with a specific row. Classification is deliberately
// conservative and simple: context cancellation/deadline, bad driver
// connections, and net errors are transient; ANYTHING else is treated as a
// row-level error and makes the row eligible for skipping during replay.
func isTransientRollupError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	if errors.Is(err, driver.ErrBadConn) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func (s *Scheduler) loadRollupState(ctx context.Context) (*rollupState, error) {
	state := &rollupState{}
	var lastCreatedAt sql.NullTime
	var lastResultID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT last_created_at, last_check_result_id::text
		FROM rollup_job_state
		WHERE job_name = $1
	`, rollupJobName).Scan(&lastCreatedAt, &lastResultID)
	if err != nil {
		if err == sql.ErrNoRows {
			return state, nil
		}
		return nil, fmt.Errorf("failed to load rollup state: %w", err)
	}
	if lastCreatedAt.Valid {
		ts := lastCreatedAt.Time.UTC()
		state.LastCreatedAt = &ts
	}
	if lastResultID.Valid {
		parsed, parseErr := uuid.Parse(lastResultID.String)
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse rollup cursor id: %w", parseErr)
		}
		state.LastResultID = &parsed
	}
	return state, nil
}

func (s *Scheduler) loadRollupBatch(ctx context.Context, state *rollupState) ([]rollupCheckResult, error) {
	query := `
		SELECT id, tenant_id, monitor_id, status, latency_ms, result_source, created_at
		FROM check_results
		WHERE result_source = $4
		  AND ($1::timestamptz IS NULL OR (created_at, id) > ($1::timestamptz, $2::uuid))
		ORDER BY created_at ASC, id ASC
		LIMIT $3
	`
	var lastCreatedAt interface{}
	var lastResultID interface{}
	if state.LastCreatedAt != nil {
		lastCreatedAt = *state.LastCreatedAt
	} else {
		lastCreatedAt = nil
	}
	if state.LastResultID != nil {
		lastResultID = *state.LastResultID
	} else {
		lastResultID = uuid.Nil
	}
	rows, err := s.db.QueryContext(
		ctx,
		query,
		lastCreatedAt,
		lastResultID,
		rollupMaintenanceBatchSize,
		string(sharedmodels.ResultSourceMonitor),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load rollup batch: %w", err)
	}
	defer rows.Close()

	batch := make([]rollupCheckResult, 0, rollupMaintenanceBatchSize)
	for rows.Next() {
		var row rollupCheckResult
		if err := rows.Scan(&row.ID, &row.TenantID, &row.MonitorID, &row.Status, &row.LatencyMS, &row.ResultSource, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan rollup row: %w", err)
		}
		row.CreatedAt = row.CreatedAt.UTC()
		batch = append(batch, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rollup batch: %w", err)
	}
	return batch, nil
}

func applyRollupRow(ctx context.Context, tx *sql.Tx, row rollupCheckResult) error {
	if row.ResultSource != string(sharedmodels.ResultSourceMonitor) {
		return nil
	}
	day := time.Date(row.CreatedAt.Year(), row.CreatedAt.Month(), row.CreatedAt.Day(), 0, 0, 0, 0, time.UTC)
	hour := row.CreatedAt.Truncate(time.Hour)
	latencySum := 0.0
	latencyCount := 0
	if row.Status == string(sharedmodels.ResultStatusSuccess) && row.LatencyMS.Valid {
		latencySum = float64(row.LatencyMS.Int64)
		latencyCount = 1
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO monitor_daily_rollups (
			tenant_id, monitor_id, bucket_day, total_checks, success_checks, error_checks,
			latency_success_sum_ms, latency_success_count, latest_status, latest_check_at,
			created_at, updated_at
		) VALUES ($1, $2, $3, 1, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		ON CONFLICT (monitor_id, bucket_day) DO UPDATE SET
			tenant_id = EXCLUDED.tenant_id,
			total_checks = monitor_daily_rollups.total_checks + 1,
			success_checks = monitor_daily_rollups.success_checks + EXCLUDED.success_checks,
			error_checks = monitor_daily_rollups.error_checks + EXCLUDED.error_checks,
			latency_success_sum_ms = monitor_daily_rollups.latency_success_sum_ms + EXCLUDED.latency_success_sum_ms,
			latency_success_count = monitor_daily_rollups.latency_success_count + EXCLUDED.latency_success_count,
			latest_status = CASE
				WHEN monitor_daily_rollups.latest_check_at IS NULL OR EXCLUDED.latest_check_at >= monitor_daily_rollups.latest_check_at
				THEN EXCLUDED.latest_status
				ELSE monitor_daily_rollups.latest_status
			END,
			latest_check_at = GREATEST(COALESCE(monitor_daily_rollups.latest_check_at, EXCLUDED.latest_check_at), EXCLUDED.latest_check_at),
			updated_at = NOW()
	`, row.TenantID, row.MonitorID, day, boolToInt(row.Status == string(sharedmodels.ResultStatusSuccess)), boolToInt(row.Status == string(sharedmodels.ResultStatusError)), latencySum, latencyCount, row.Status, row.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to upsert daily rollup: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO monitor_hourly_rollups (
			tenant_id, monitor_id, bucket_hour, total_checks, success_checks, error_checks,
			latency_success_sum_ms, latency_success_count, latest_status, latest_check_at,
			created_at, updated_at
		) VALUES ($1, $2, $3, 1, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		ON CONFLICT (monitor_id, bucket_hour) DO UPDATE SET
			tenant_id = EXCLUDED.tenant_id,
			total_checks = monitor_hourly_rollups.total_checks + 1,
			success_checks = monitor_hourly_rollups.success_checks + EXCLUDED.success_checks,
			error_checks = monitor_hourly_rollups.error_checks + EXCLUDED.error_checks,
			latency_success_sum_ms = monitor_hourly_rollups.latency_success_sum_ms + EXCLUDED.latency_success_sum_ms,
			latency_success_count = monitor_hourly_rollups.latency_success_count + EXCLUDED.latency_success_count,
			latest_status = CASE
				WHEN monitor_hourly_rollups.latest_check_at IS NULL OR EXCLUDED.latest_check_at >= monitor_hourly_rollups.latest_check_at
				THEN EXCLUDED.latest_status
				ELSE monitor_hourly_rollups.latest_status
			END,
			latest_check_at = GREATEST(COALESCE(monitor_hourly_rollups.latest_check_at, EXCLUDED.latest_check_at), EXCLUDED.latest_check_at),
			updated_at = NOW()
	`, row.TenantID, row.MonitorID, hour, boolToInt(row.Status == string(sharedmodels.ResultStatusSuccess)), boolToInt(row.Status == string(sharedmodels.ResultStatusError)), latencySum, latencyCount, row.Status, row.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to upsert hourly rollup: %w", err)
	}

	if row.Status == string(sharedmodels.ResultStatusSuccess) {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO monitor_downtime_periods (tenant_id, monitor_id, start_time, end_time, created_at, updated_at)
			SELECT tenant_id, monitor_id, started_at, $3, NOW(), NOW()
			FROM monitor_downtime_open
			WHERE monitor_id = $1 AND tenant_id = $2
			ON CONFLICT (monitor_id, start_time) DO UPDATE SET
				end_time = EXCLUDED.end_time,
				updated_at = NOW()
		`, row.MonitorID, row.TenantID, row.CreatedAt); err != nil {
			return fmt.Errorf("failed to close downtime period: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_downtime_open WHERE monitor_id = $1 AND tenant_id = $2`, row.MonitorID, row.TenantID); err != nil {
			return fmt.Errorf("failed to delete open downtime row: %w", err)
		}
		return nil
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO monitor_downtime_open (tenant_id, monitor_id, started_at, last_seen_at, created_at, updated_at)
		VALUES ($1, $2, $3, $3, NOW(), NOW())
		ON CONFLICT (monitor_id) DO UPDATE SET
			last_seen_at = GREATEST(monitor_downtime_open.last_seen_at, EXCLUDED.last_seen_at),
			updated_at = NOW()
	`, row.TenantID, row.MonitorID, row.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to upsert open downtime row: %w", err)
	}
	return nil
}

func upsertRollupState(ctx context.Context, tx *sql.Tx, state *rollupState) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO rollup_job_state (job_name, last_created_at, last_check_result_id, last_run_at, updated_at)
		VALUES ($1, $2, $3, NOW(), NOW())
		ON CONFLICT (job_name) DO UPDATE SET
			last_created_at = EXCLUDED.last_created_at,
			last_check_result_id = EXCLUDED.last_check_result_id,
			last_run_at = NOW(),
			updated_at = NOW()
	`, rollupJobName, state.LastCreatedAt, state.LastResultID)
	if err != nil {
		return fmt.Errorf("failed to upsert rollup state: %w", err)
	}
	return nil
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
	return nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

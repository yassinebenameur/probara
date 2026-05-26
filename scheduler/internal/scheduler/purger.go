package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
)

const (
	monitorPurgeAdvisoryLock = int64(901_337_402)
	monitorPurgeRunTimeout   = 30 * time.Minute
)

type purgerOptions struct {
	BatchSize     int
	MaxRowsPerRun int
}

type purgerMetrics struct {
	runs        *prometheus.CounterVec
	rows        *prometheus.CounterVec
	monitors    *prometheus.CounterVec
	errors      *prometheus.CounterVec
	runDuration *prometheus.HistogramVec
}

type purger struct {
	db      *db.Client
	logger  *logger.Logger
	metrics *purgerMetrics
	opts    purgerOptions
}

func newPurger(dbClient *db.Client, log *logger.Logger, metrics *purgerMetrics, opts purgerOptions) *purger {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 5000
	}
	if opts.MaxRowsPerRun <= 0 {
		opts.MaxRowsPerRun = 200000
	}
	return &purger{db: dbClient, logger: log, metrics: metrics, opts: opts}
}

// runOnce acquires the advisory lock, drains as many tombstoned monitors as it
// can within MaxRowsPerRun of total child-row deletions, and returns the count
// of monitor rows fully purged.
func (p *purger) runOnce(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, monitorPurgeRunTimeout)
	defer cancel()

	var locked bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT pg_try_advisory_lock($1)`, monitorPurgeAdvisoryLock,
	).Scan(&locked); err != nil {
		return 0, fmt.Errorf("acquire monitor purge advisory lock: %w", err)
	}
	if !locked {
		return 0, nil
	}
	defer func() {
		if _, err := p.db.ExecContext(context.Background(),
			`SELECT pg_advisory_unlock($1)`, monitorPurgeAdvisoryLock,
		); err != nil {
			p.logger.WithError(err).Warn("failed to release monitor purge advisory lock")
		}
	}()

	purged := 0
	rowsBudget := p.opts.MaxRowsPerRun

	for rowsBudget > 0 {
		mon, err := p.claimNextTombstonedMonitor(ctx)
		if err != nil {
			return purged, err
		}
		if mon == nil {
			return purged, nil
		}

		spent, done, err := p.purgeMonitor(ctx, *mon, rowsBudget)
		if err != nil {
			if p.metrics != nil {
				p.metrics.errors.With(prometheus.Labels{}).Inc()
			}
			p.logger.WithError(err).WithFields(logrus.Fields{
				"monitor_id": *mon,
			}).Error("purge monitor failed; will retry on next run")
			return purged, err
		}
		rowsBudget -= spent
		if !done {
			// Budget exhausted partway through this monitor; the next tick will
			// pick it up again because deleted_at is still set. This is a benign
			// stop, not an error — don't bump error metrics, don't claim it as
			// purged.
			p.logger.WithFields(logrus.Fields{
				"monitor_id":  *mon,
				"rows_purged": spent,
			}).Debug("monitor purge paused at row budget; resuming next tick")
			return purged, nil
		}
		purged++

		if p.metrics != nil {
			p.metrics.monitors.With(prometheus.Labels{}).Inc()
		}
		p.logger.WithFields(logrus.Fields{
			"monitor_id":  *mon,
			"rows_purged": spent,
		}).Info("monitor fully purged")
	}

	return purged, nil
}

// claimNextTombstonedMonitor returns the oldest tombstoned monitor (FIFO by deleted_at)
// or nil if there are none.
func (p *purger) claimNextTombstonedMonitor(ctx context.Context) (*uuid.UUID, error) {
	var id uuid.UUID
	err := p.db.QueryRowContext(ctx, `
		SELECT id FROM monitors
		WHERE deleted_at IS NOT NULL
		ORDER BY deleted_at ASC
		LIMIT 1
	`).Scan(&id)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("select next tombstoned monitor: %w", err)
	}
	return &id, nil
}

// purgeMonitor drains child rows in small batches and, if everything fits
// inside rowsBudget, deletes the monitor row itself. Returns:
//   - spent: total child rows deleted in this call (for cross-monitor budgeting)
//   - done:  true iff the monitor row was hard-deleted (i.e. fully purged)
//   - err:   only non-nil for actual SQL errors; budget exhaustion is benign
//
// When budget is exhausted partway, the monitor row is left in place
// (still tombstoned) and the next purger tick resumes it.
func (p *purger) purgeMonitor(ctx context.Context, monitorID uuid.UUID, rowsBudget int) (spent int, done bool, err error) {
	ids := []uuid.UUID{monitorID}

	// Each entry is a child table that should be drained before the monitor row
	// itself. Order matters only when a downstream FK could block deletion; the
	// cascade chains (alerts -> alert_notification_states, alerts -> incident_alerts)
	// will be cleaned up automatically when we delete the alert row.
	type batchedDelete struct {
		name string
		sql  string
	}
	// Tables ordered safest-first. Each child table is drained before the monitor
	// row itself. Tables with no surrogate `id` column (the rollups and downtime
	// tables — see shared/db/migrations/000035_create_monitor_rollups.up.sql:13,
	// :26, :39 and 000041_create_monitor_hourly_rollups.up.sql) use ctid for
	// batching. Small bounded tables (monitor_downtime_open: at most one row per
	// monitor; monitor_alert_policies; monitor_groups; status_page_monitors;
	// incident_monitors) are drained in one shot via the monitor row's ON DELETE
	// CASCADE when we issue the final `DELETE FROM monitors` below.
	deletes := []batchedDelete{
		{"alerts", `DELETE FROM alerts WHERE id IN (
			SELECT id FROM alerts WHERE monitor_id = ANY($1) LIMIT $2)`},
		{"monitor_downtime_periods", `DELETE FROM monitor_downtime_periods
			WHERE ctid IN (SELECT ctid FROM monitor_downtime_periods
			               WHERE monitor_id = ANY($1) LIMIT $2)`},
		{"monitor_hourly_rollups", `DELETE FROM monitor_hourly_rollups
			WHERE ctid IN (SELECT ctid FROM monitor_hourly_rollups
			               WHERE monitor_id = ANY($1) LIMIT $2)`},
		{"monitor_daily_rollups", `DELETE FROM monitor_daily_rollups
			WHERE ctid IN (SELECT ctid FROM monitor_daily_rollups
			               WHERE monitor_id = ANY($1) LIMIT $2)`},
		{"check_results", `DELETE FROM check_results
			WHERE id IN (SELECT id FROM check_results
			             WHERE monitor_id = ANY($1) LIMIT $2)`},
	}

	for _, d := range deletes {
		for {
			limit := p.opts.BatchSize
			if limit > rowsBudget {
				limit = rowsBudget
			}
			if limit <= 0 {
				// Budget exhausted exactly at a batch boundary; we don't know
				// whether this table is drained without an extra round-trip,
				// and the monitor row hasn't been deleted yet. Return done=false
				// so runOnce defers the rest to the next tick — this is NOT
				// an error.
				return spent, false, nil
			}

			result, execErr := p.db.ExecContext(ctx, d.sql, pq.Array(ids), limit)
			if execErr != nil {
				return spent, false, fmt.Errorf("delete %s: %w", d.name, execErr)
			}
			rowsAffected, raErr := result.RowsAffected()
			if raErr != nil {
				return spent, false, fmt.Errorf("rows affected for %s: %w", d.name, raErr)
			}
			spent += int(rowsAffected)
			rowsBudget -= int(rowsAffected)
			if p.metrics != nil {
				p.metrics.rows.With(prometheus.Labels{}).Add(float64(rowsAffected))
			}

			if rowsAffected < int64(limit) {
				break // table is drained for this monitor
			}
		}
	}

	// Child tables fully drained. Drop the monitor row itself. Remaining
	// children with small bounded fan-out (monitor_downtime_open: 1 row per
	// monitor; monitor_groups, monitor_alert_policies, status_page_monitors,
	// status_page_section_monitors, incident_monitors) are cleaned up via
	// their existing ON DELETE CASCADE FKs.
	if _, execErr := p.db.ExecContext(ctx,
		`DELETE FROM monitors WHERE id = $1`, monitorID,
	); execErr != nil {
		return spent, false, fmt.Errorf("delete monitor row: %w", execErr)
	}
	return spent, true, nil
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/monitorstate"
)

// checkResultsPruneCutoff reads monitorstate.RawPruneCutoffSQL on the database
// clock, the clock Record's mark horizon uses. Rollups are kept
// rollupRetentionDays, independent of this cutoff.
func (s *Scheduler) checkResultsPruneCutoff(ctx context.Context) (time.Time, error) {
	var cutoff time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT `+monitorstate.RawPruneCutoffSQL).Scan(&cutoff); err != nil {
		return time.Time{}, fmt.Errorf("read check_results prune cutoff: %w", err)
	}
	return cutoff.UTC(), nil
}

// tenantDaysCutoff is the prune boundary for stores that follow the tenant's
// data_retention_days; zero (keep forever) when the tenant sets 0.
func tenantDaysCutoff(_ context.Context, tenantDays int) (time.Time, error) {
	if tenantDays <= 0 {
		return time.Time{}, nil
	}
	return time.Now().UTC().AddDate(0, 0, -tenantDays), nil
}

// checkResultNotPendingRollup keeps retention off raw rows whose rollup bucket
// still has a rollup_dirty mark. The rebuild REPLACES the bucket from the raw
// rows it finds (and deletes it when none remain), so pruning first would
// shrink or erase that history. A mark is consumed only in the transaction
// that rebuilt the bucket from the full raw set; once it is gone the rows are
// safe to prune on a later pass.
const checkResultNotPendingRollup = `NOT EXISTS (
	SELECT 1 FROM rollup_dirty d
	WHERE d.monitor_id = check_results.monitor_id
	  AND d.bucket_hour = date_trunc('hour', check_results.created_at)
)`

func (s *Scheduler) retentionMaxRows() int {
	if s.config.RetentionCleanupMaxRowsPerRun > 0 {
		return s.config.RetentionCleanupMaxRowsPerRun
	}
	return 200000
}

// oldestExpiredRetentionRow is called only after a store used its entire
// deletion budget. Check/mesh probes use their tenant-time indexes; metric
// probes inspect only the oldest indexed sample of each tenant series.
func (s *Scheduler) oldestExpiredRetentionRow(ctx context.Context, store string, tenantID uuid.UUID, cutoff time.Time) (time.Time, error) {
	var query string
	switch store {
	case "check_results":
		query = `SELECT created_at FROM check_results
			WHERE tenant_id = $1 AND created_at < $2 AND ` + checkResultNotPendingRollup + `
			ORDER BY created_at LIMIT 1`
	case "mesh_probe_results":
		query = `SELECT created_at FROM mesh_probe_results
			WHERE tenant_id = $1 AND created_at < $2 ORDER BY created_at LIMIT 1`
	case "metric_samples":
		query = `SELECT MIN(sample.ts) FROM metric_series s
			JOIN LATERAL (
				SELECT ts FROM metric_samples WHERE series_id = s.id AND ts < $2
				ORDER BY ts LIMIT 1
			) sample ON TRUE
			WHERE s.tenant_id = $1`
	default:
		return time.Time{}, fmt.Errorf("unknown retention store %q", store)
	}
	var oldest sql.NullTime
	err := s.db.QueryRowContext(ctx, query, tenantID, cutoff).Scan(&oldest)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("probe expired %s for tenant %s: %w", store, tenantID, err)
	}
	return oldest.Time, nil
}

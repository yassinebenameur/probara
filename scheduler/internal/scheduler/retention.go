package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

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
			WHERE tenant_id = $1 AND created_at < $2 ORDER BY created_at LIMIT 1`
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

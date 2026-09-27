package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/shared/metricstore"
	sharetest "github.com/yassinebenameur/probara/shared/testutil"
)

func TestRetentionCleanup_CatchesUpAllStoresWithinBudgets(t *testing.T) {
	ctx := context.Background()
	database, cleanup := sharetest.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	// The migration helper retains a connection. Give the scheduler exactly
	// two additional slots: one reserved lock session and one work session.
	database.SetMaxOpenConns(database.Stats().InUse + 2)
	s := newTestScheduler(database)
	jobCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancel)
	s.ctx = jobCtx
	s.config.RetentionCleanupEnabled = true
	s.config.RetentionCleanupHourUTC = 2
	s.config.RetentionCleanupBatchSize = 1
	s.config.RetentionCleanupMaxRowsPerRun = 2
	tenant := sharetest.InsertTenant(ctx, t, database, "retention-catchup")
	_, err := database.ExecContext(ctx, `UPDATE tenants SET data_retention_days = 30 WHERE id = $1`, tenant)
	require.NoError(t, err)
	monitor := sharetest.InsertHTTPMonitor(ctx, t, database, tenant, "retention-check")
	source := insertMeshLocation(ctx, t, database, tenant, "source", "10.0.0.1:8080")
	target := insertMeshLocation(ctx, t, database, tenant, "target", "10.0.0.2:8080")
	old := time.Now().UTC().AddDate(0, 0, -40).Truncate(time.Second)
	require.NoError(t, metricstore.EnsurePartitions(ctx, database, old, old))
	for i := 0; i < 4; i++ {
		insertCheckResult(ctx, t, database, tenant, monitor, old, "success", "monitor", 12)
		insertMeshResult(ctx, t, database, tenant, source, target, old)
		var seriesID int64
		require.NoError(t, database.QueryRowContext(ctx, `
			INSERT INTO metric_series (tenant_id, monitor_id, metric_name, metric_type, attr_hash)
			VALUES ($1, $2, 'test.retention', 'gauge', $3) RETURNING id
		`, tenant, monitor, []byte{byte(i)}).Scan(&seriesID))
		_, err := database.ExecContext(ctx, `INSERT INTO metric_samples (series_id, ts, value) VALUES ($1, $2, 1)`, seriesID, old)
		require.NoError(t, err)
	}
	// Fresh and retention-disabled tenant rows must survive catch-up passes.
	insertCheckResult(ctx, t, database, tenant, monitor, time.Now().UTC(), "success", "monitor", 12)
	otherTenant := sharetest.InsertTenant(ctx, t, database, "retention-disabled")
	otherMonitor := sharetest.InsertHTTPMonitor(ctx, t, database, otherTenant, "retention-disabled-check")
	insertCheckResult(ctx, t, database, otherTenant, otherMonitor, old, "success", "monitor", 12)

	now := time.Now().UTC().Truncate(24 * time.Hour).Add(3 * time.Hour)
	for pass := 0; pass < 2; pass++ {
		require.True(t, s.beginRetentionCleanup(now.Add(time.Duration(pass)*time.Minute)))
		executed, deleted, pending, err := s.runRetentionCleanup()
		require.NoError(t, err)
		require.True(t, executed)
		require.EqualValues(t, 6, deleted, "each pass deletes at most two rows per store")
		require.Equal(t, pass == 0, pending, "an exactly-full final pass must detect that the backlog is gone")
		s.finishRetentionCleanup(now.Format("2006-01-02"), !pending)
		for _, store := range []string{"check_results", "mesh_probe_results", "metric_samples"} {
			wantBacklog, wantOldest := float64(0), float64(0)
			if pending {
				wantBacklog, wantOldest = 1, float64(old.Unix())
			}
			require.Equal(t, wantBacklog, testutil.ToFloat64(s.retentionBacklog.WithLabelValues(store)))
			require.Equal(t, wantOldest, testutil.ToFloat64(s.retentionOldest.WithLabelValues(store)))
		}
	}
	require.False(t, s.beginRetentionCleanup(now.Add(2*time.Minute)))
	require.Equal(t, 2, countRows(ctx, t, database, `SELECT COUNT(*) FROM check_results`))
	require.Zero(t, countRows(ctx, t, database, `SELECT COUNT(*) FROM mesh_probe_results`))
	require.Zero(t, countRows(ctx, t, database, `SELECT COUNT(*) FROM metric_samples`))
}

func TestMaintenanceLock_PinsSessionAcrossPoolUseAndCancellation(t *testing.T) {
	ctx := context.Background()
	database, cleanup := sharetest.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	for _, id := range []int64{retentionCleanupAdvisoryLock, rollupMaintenanceAdvisoryLock} {
		jobCtx, cancel := context.WithCancel(ctx)
		lock, err := acquireMaintenanceLock(jobCtx, database.DB, id)
		require.NoError(t, err)
		require.NotNil(t, lock)
		// Borrowing from the same pool must not hand out the lock-holding
		// session and reentrantly grant another caller this very same lock.
		competitor, err := database.Conn(ctx)
		require.NoError(t, err)
		var locked bool
		require.NoError(t, competitor.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, id).Scan(&locked))
		require.False(t, locked, "another pool user must not enter the protected job")
		cancel()
		require.NoError(t, lock.release(), "cancellation must still release the owning session's lock")
		require.NoError(t, competitor.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, id).Scan(&locked))
		require.True(t, locked, "no leaked lock may survive the job")
		_, err = competitor.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, id)
		require.NoError(t, err)
		require.NoError(t, competitor.Close())
	}
}

func TestMaintenanceJobs_ReleaseLocksAfterWork(t *testing.T) {
	ctx := context.Background()
	database, cleanup := sharetest.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	s := newTestScheduler(database)
	for _, job := range []struct {
		name string
		id   int64
		run  func() error
	}{
		{"retention", retentionCleanupAdvisoryLock, func() error { _, _, _, err := s.runRetentionCleanup(); return err }},
		{"rollup", rollupMaintenanceAdvisoryLock, func() error { _, _, err := s.runRollupMaintenance(); return err }},
	} {
		t.Run(job.name, func(t *testing.T) {
			competitor, err := database.Conn(ctx)
			require.NoError(t, err)
			defer competitor.Close()
			require.NoError(t, job.run())
			var locked bool
			require.NoError(t, competitor.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, job.id).Scan(&locked))
			require.True(t, locked, "maintenance must release its lock after database work")
			_, err = competitor.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, job.id)
			require.NoError(t, err)
		})
	}
}

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/shared/metricstore"
	"github.com/yassinebenameur/probara/shared/monitorstate"
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
	// Fresh rows survive catch-up passes. A retention-disabled tenant keeps its
	// mesh history, but its raw check results are still capped at
	// monitorstate.CheckResultsRawRetentionDays.
	insertCheckResult(ctx, t, database, tenant, monitor, time.Now().UTC(), "success", "monitor", 12)
	otherTenant := sharetest.InsertTenant(ctx, t, database, "retention-disabled")
	otherMonitor := sharetest.InsertHTTPMonitor(ctx, t, database, otherTenant, "retention-disabled-check")
	insertCheckResult(ctx, t, database, otherTenant, otherMonitor, old, "success", "monitor", 12)
	insertCheckResult(ctx, t, database, otherTenant, otherMonitor, time.Now().UTC().AddDate(0, 0, -20), "success", "monitor", 12)
	otherSource := insertMeshLocation(ctx, t, database, otherTenant, "source", "10.0.1.1:8080")
	otherTarget := insertMeshLocation(ctx, t, database, otherTenant, "target", "10.0.1.2:8080")
	insertMeshResult(ctx, t, database, otherTenant, otherSource, otherTarget, old)

	now := time.Now().UTC().Truncate(24 * time.Hour).Add(3 * time.Hour)
	for pass := 0; pass < 2; pass++ {
		require.True(t, s.beginRetentionCleanup(now.Add(time.Duration(pass)*time.Minute)))
		executed, deleted, pending, err := s.runRetentionCleanup()
		require.NoError(t, err)
		require.True(t, executed)
		// Two rows per store for the capped tenant, plus the retention-disabled
		// tenant's one expired check result on the first pass.
		wantDeleted := 6
		if pass == 0 {
			wantDeleted = 7
		}
		require.EqualValues(t, wantDeleted, deleted, "each pass deletes at most two rows per store and tenant")
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
	require.Equal(t, 1, countRows(ctx, t, database, `SELECT COUNT(*) FROM mesh_probe_results`))
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

// Regression: a raw row older than the retention horizon whose bucket still
// has a rollup_dirty mark must survive cleanup until the rebuild consumes the
// mark. Pruning first let the REPLACE rebuild delete the bucket's rollup.
func TestRetentionCleanup_KeepsRawRowsOfPendingRollupBuckets(t *testing.T) {
	ctx := context.Background()
	database, cleanup := setupRollupTestDB(ctx, t)
	t.Cleanup(cleanup)
	s := newTestScheduler(database)
	s.config.RetentionCleanupEnabled = true

	// data_retention_days defaults to 0 (keep forever): only the raw cap applies.
	tenant := insertTenant(ctx, t, database)
	monitor := insertMonitor(ctx, t, database, tenant, "pending-rollup")
	pendingHour := time.Now().UTC().AddDate(0, 0, -40).Truncate(time.Hour)
	for i := 0; i < 3; i++ {
		insertMarkedCheckResult(ctx, t, database, tenant, monitor, pendingHour.Add(time.Duration(i)*time.Minute), "success", 50)
	}
	// Same age, but its bucket was already rolled up (no mark): prunable now.
	insertCheckResult(ctx, t, database, tenant, monitor, pendingHour.Add(-2*time.Hour), "success", "monitor", 50)

	runCleanup := func() {
		t.Helper()
		executed, _, pending, err := s.runRetentionCleanup()
		require.NoError(t, err)
		require.True(t, executed)
		require.False(t, pending, "rows held for a pending rollup are not a backlog")
	}
	hourlyTotal := func() int {
		t.Helper()
		return countRows(ctx, t, database, `SELECT COALESCE(SUM(total_checks), 0) FROM monitor_hourly_rollups WHERE monitor_id = $1 AND bucket_hour = $2`, monitor, pendingHour)
	}

	runCleanup()
	require.Equal(t, 3, countRows(ctx, t, database, `SELECT COUNT(*) FROM check_results WHERE monitor_id = $1`, monitor),
		"only the unmarked bucket's row is pruned")

	_, _, err := s.runRollupMaintenance()
	require.NoError(t, err)
	require.Equal(t, 3, hourlyTotal(), "the rebuild saw every raw row of the pending bucket")
	require.Zero(t, countDirtyMarks(ctx, t, database))

	// Once the mark is consumed the rows go, and the rollup stays.
	runCleanup()
	require.Zero(t, countRows(ctx, t, database, `SELECT COUNT(*) FROM check_results WHERE monitor_id = $1`, monitor))
	_, _, err = s.runRollupMaintenance()
	require.NoError(t, err)
	require.Equal(t, 3, hourlyTotal())
}

// A result arriving for a bucket past the raw horizon is stored but never
// marked: its bucket's raw rows may already be pruned, and a REPLACE rebuild
// from the survivors would shrink the finished rollup.
func TestRecord_DoesNotMarkBucketsPastRawRetention(t *testing.T) {
	ctx := context.Background()
	database, cleanup := setupRollupTestDB(ctx, t)
	t.Cleanup(cleanup)
	tenant := insertTenant(ctx, t, database)
	monitor := insertMonitor(ctx, t, database, tenant, "stale-result")

	record := func(startedAt time.Time) {
		t.Helper()
		_, err := monitorstate.Record(ctx, database.DB, monitorstate.Result{
			MonitorID: monitor, TenantID: tenant, JobID: uuid.New(),
			Status: "success", ResultSource: "monitor",
			StartedAt: startedAt, CompletedAt: startedAt,
		})
		require.NoError(t, err)
	}
	record(time.Now().UTC().AddDate(0, 0, -(monitorstate.CheckResultsRawRetentionDays + 1)))
	require.Equal(t, 1, countRows(ctx, t, database, `SELECT COUNT(*) FROM check_results WHERE monitor_id = $1`, monitor))
	require.Zero(t, countDirtyMarks(ctx, t, database), "a bucket past the raw horizon is not marked")

	record(time.Now().UTC())
	require.Equal(t, 1, countDirtyMarks(ctx, t, database), "fresh results still mark their bucket")
}

// Regression: cleanup used an instant cutoff (now - 30d) while Record checked
// the row's own timestamp, so the hour straddling the cutoff could lose its
// early rows to cleanup, then get re-marked by a late result in its surviving
// minutes; the REPLACE rebuild then shrank that hour to the survivors. Pruning
// is now whole hours, one hour below the mark horizon.
func TestRetentionCleanup_LateResultNeverShrinksHorizonHour(t *testing.T) {
	ctx := context.Background()
	database, cleanup := setupRollupTestDB(ctx, t)
	t.Cleanup(cleanup)
	s := newTestScheduler(database)
	s.config.RetentionCleanupEnabled = true

	tenant := insertTenant(ctx, t, database)
	monitor := insertMonitor(ctx, t, database, tenant, "horizon-hour")
	var horizon time.Time
	require.NoError(t, database.QueryRowContext(ctx, `SELECT `+monitorstate.RollupMarkHorizonSQL).Scan(&horizon))
	horizon = horizon.UTC()
	slack, expired := horizon.Add(-time.Hour), horizon.Add(-2*time.Hour)

	// The horizon hour's first rows sit before the old instant cutoff.
	for i := 0; i < 3; i++ {
		insertMarkedCheckResult(ctx, t, database, tenant, monitor, horizon.Add(time.Duration(i)*time.Millisecond), "success", 50)
	}
	for i := 0; i < 2; i++ {
		insertMarkedCheckResult(ctx, t, database, tenant, monitor, slack.Add(time.Duration(i)*time.Minute), "success", 50)
	}
	insertMarkedCheckResult(ctx, t, database, tenant, monitor, expired, "success", 50)
	_, _, err := s.runRollupMaintenance()
	require.NoError(t, err)
	require.Zero(t, countDirtyMarks(ctx, t, database))

	hourRows := func(hour time.Time) int {
		t.Helper()
		return countRows(ctx, t, database, `SELECT COUNT(*) FROM check_results WHERE monitor_id = $1 AND date_trunc('hour', created_at) = $2`, monitor, hour)
	}
	hourlyTotal := func(hour time.Time) int {
		t.Helper()
		return countRows(ctx, t, database, `SELECT COALESCE(SUM(total_checks), 0) FROM monitor_hourly_rollups WHERE monitor_id = $1 AND bucket_hour = $2`, monitor, hour)
	}

	_, _, _, err = s.runRetentionCleanup()
	require.NoError(t, err)
	require.Equal(t, 3, hourRows(horizon), "cleanup never prunes the hour the horizon falls in")
	require.Equal(t, 2, hourRows(slack), "cleanup keeps one full hour of slack below the horizon")
	require.Zero(t, hourRows(expired), "whole hours below the slack are pruned")

	// Late results land in the horizon hour's surviving minutes and in the
	// slack hour, through the real ingest path.
	record := func(startedAt time.Time) {
		t.Helper()
		_, err := monitorstate.Record(ctx, database.DB, monitorstate.Result{
			MonitorID: monitor, TenantID: tenant, JobID: uuid.New(),
			Status: "success", ResultSource: "monitor",
			StartedAt: startedAt, CompletedAt: startedAt,
		})
		require.NoError(t, err)
	}
	record(horizon.Add(59 * time.Minute))
	record(slack.Add(30 * time.Minute))
	// The horizon hour is marked unless the clock crossed an hour boundary
	// since it was read; the slack hour is below the horizon and never is.
	markedHorizon := countRows(ctx, t, database, `SELECT COUNT(*) FROM rollup_dirty WHERE monitor_id = $1 AND bucket_hour = $2`, monitor, horizon)
	require.Zero(t, countRows(ctx, t, database, `SELECT COUNT(*) FROM rollup_dirty WHERE monitor_id = $1 AND bucket_hour = $2`, monitor, slack))

	_, _, err = s.runRollupMaintenance()
	require.NoError(t, err)
	require.Equal(t, 3+markedHorizon, hourlyTotal(horizon), "the rebuild saw every earlier row of the horizon hour")
	require.Equal(t, 2, hourlyTotal(slack), "an unmarked hour's rollup is left as built")
	require.Equal(t, 1, hourlyTotal(expired), "pruned hours keep their rollup")
}

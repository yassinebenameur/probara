//go:build !unit

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/shared/maintenance"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A deleted workspace drops out of every scheduler loop at once, before the
// purge removes anything: no dispatch, no watchdog transition, no mesh
// probe, no maintenance occurrences rolled forward.
func TestSchedulerLoopsSkipDeletedWorkspaces(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	live := testutil.InsertTenant(ctx, t, dbClient, "live")
	deleted := testutil.InsertTenant(ctx, t, dbClient, "deleted")
	liveMonitor := testutil.InsertHTTPMonitor(ctx, t, dbClient, live, "live-target")
	deletedMonitor := testutil.InsertHTTPMonitor(ctx, t, dbClient, deleted, "deleted-target")
	for _, tenant := range []uuid.UUID{live, deleted} {
		insertMeshLocation(ctx, t, dbClient, tenant, "mesh-a-"+tenant.String()[:8], "10.0.0.1:8080")
		insertMeshLocation(ctx, t, dbClient, tenant, "mesh-b-"+tenant.String()[:8], "10.0.0.2:8080")
		_, err := dbClient.ExecContext(ctx, `
			INSERT INTO maintenance_windows (tenant_id, title, starts_at, ends_at,
				recurrence_weekdays, recurrence_start_time, recurrence_duration_minutes, recurrence_timezone)
			VALUES ($1, 'weekly', NOW(), NOW() + INTERVAL '1 hour', ARRAY[0,1,2,3,4,5,6], '03:00', 30, 'UTC')`, tenant)
		require.NoError(t, err)
	}
	s, spy := newMeshTestScheduler(t, dbClient)
	s.runMeshBatch(ctx) // edges for both workspaces, while both are live
	spy.published = nil
	markDeleted(ctx, t, dbClient, deleted)

	t.Run("dispatch", func(t *testing.T) {
		tx, err := dbClient.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		due, err := s.fetchDueMonitors(ctx, tx, 10)
		require.NoError(t, err)
		require.Len(t, due, 1)
		require.Equal(t, liveMonitor, due[0].ID)
	})

	t.Run("watchdog", func(t *testing.T) {
		setMonitorEvidence(ctx, t, dbClient, liveMonitor, "down", time.Now().Add(-10*time.Minute))
		setMonitorEvidence(ctx, t, dbClient, deletedMonitor, "down", time.Now().Add(-10*time.Minute))
		require.NoError(t, s.watchdogActivePass(ctx))
		state, _ := monitorStateAndFails(ctx, t, dbClient, liveMonitor)
		require.Equal(t, "unknown", state)
		state, _ = monitorStateAndFails(ctx, t, dbClient, deletedMonitor)
		require.Equal(t, "down", state, "a deleted workspace's monitor is left as it was")
	})

	t.Run("mesh", func(t *testing.T) {
		_, err := dbClient.ExecContext(ctx, `UPDATE location_mesh_state SET next_run_at = NOW() - INTERVAL '1 second'`)
		require.NoError(t, err)
		s.runMeshBatch(ctx)
		require.NotEmpty(t, spy.published)
		for _, p := range spy.published {
			require.Equalf(t, live.String(), p.job.TenantID, "mesh probe published for %s", p.job.TenantID)
		}
	})

	t.Run("maintenance occurrences", func(t *testing.T) {
		_, err := maintenance.RollForward(ctx, dbClient, time.Now())
		require.NoError(t, err)
		var liveOcc, deletedOcc int
		require.NoError(t, dbClient.QueryRowContext(ctx, `
			SELECT COUNT(*) FILTER (WHERE tenant_id = $1), COUNT(*) FILTER (WHERE tenant_id = $2)
			FROM maintenance_window_occurrences`, live, deleted).Scan(&liveOcc, &deletedOcc))
		require.NotZero(t, liveOcc)
		require.Zero(t, deletedOcc)
	})
}

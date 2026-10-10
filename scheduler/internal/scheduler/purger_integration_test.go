package scheduler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestPurger_RemovesChildRowsThenMonitor(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "")

	// Insert a handful of check_results so cascade has real work to do.
	for i := 0; i < 5; i++ {
		_, err := dbClient.ExecContext(ctx, `
			INSERT INTO check_results (id, tenant_id, monitor_id, job_id, status, latency_ms, created_at)
			VALUES (gen_random_uuid(), $1, $2, gen_random_uuid(), 'success', 12, NOW())
		`, tenantID, monitorID)
		require.NoError(t, err)
	}

	// Tombstone the monitor.
	_, err := dbClient.ExecContext(ctx,
		`UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, monitorID,
	)
	require.NoError(t, err)

	p := newPurger(dbClient, testLogger(t), nil, purgerOptions{
		BatchSize:     2,
		MaxRowsPerRun: 100,
	})

	purged, err := p.runOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, purged, "should have purged exactly one monitor")

	// Monitor row must be gone.
	var count int
	require.NoError(t, dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM monitors WHERE id = $1`, monitorID,
	).Scan(&count))
	require.Equal(t, 0, count)

	// check_results must be gone too.
	require.NoError(t, dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_results WHERE monitor_id = $1`, monitorID,
	).Scan(&count))
	require.Equal(t, 0, count)
}

func TestPurger_ResumesAcrossTicksAtExactBudgetBoundary(t *testing.T) {
	// Reproduces the exact-multiple budget exhaustion edge case: when a child
	// table has exactly N=BatchSize rows and MaxRowsPerRun also equals N, the
	// purger must NOT report an error — it should return done=false, get
	// re-invoked next tick, and finish.
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "")

	// Seed exactly batchSize=4 rows so the first run consumes the entire budget
	// without yet knowing the table is drained.
	for i := 0; i < 4; i++ {
		_, err := dbClient.ExecContext(ctx, `
			INSERT INTO check_results (id, tenant_id, monitor_id, job_id, status, latency_ms, created_at)
			VALUES (gen_random_uuid(), $1, $2, gen_random_uuid(), 'success', 12, NOW())
		`, tenantID, monitorID)
		require.NoError(t, err)
	}
	_, err := dbClient.ExecContext(ctx,
		`UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, monitorID)
	require.NoError(t, err)

	p := newPurger(dbClient, testLogger(t), nil, purgerOptions{
		BatchSize:     4,
		MaxRowsPerRun: 4,
	})

	// First tick: hits budget mid-table; must not error, must not yet purge.
	purged, err := p.runOnce(ctx)
	require.NoError(t, err, "exact-multiple budget exhaustion must not surface as an error")
	require.Equal(t, 0, purged, "monitor row should not yet be deleted")

	var count int
	require.NoError(t, dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM monitors WHERE id = $1`, monitorID,
	).Scan(&count))
	require.Equal(t, 1, count, "monitor row should still exist (tombstoned)")

	// Second tick: drains the empty table, deletes the monitor row.
	purged, err = p.runOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, purged)

	require.NoError(t, dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM monitors WHERE id = $1`, monitorID,
	).Scan(&count))
	require.Equal(t, 0, count, "monitor row should be gone after second tick")
}

func TestPurger_NoTombstonedMonitors_NoOp(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	p := newPurger(dbClient, testLogger(t), nil, purgerOptions{BatchSize: 2, MaxRowsPerRun: 100})

	purged, err := p.runOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, purged)
}

func TestPurger_PartialUniqueIndexAllowsReimport(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")

	// Insert a push monitor with a token, soft-delete it.
	id := uuid.New()
	token := "tok-abc"
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitors (id, tenant_id, name, type, config, interval_seconds, timeout_seconds,
		    alert_policy_id, enabled, tags, agent_id, push_token, next_run_at, created_at, updated_at)
		VALUES ($1, $2, 'first', 'push', '{}'::jsonb, 60, 30, NULL, TRUE, ARRAY[]::text[],
		    NULL, $3, NULL, NOW(), NOW())
	`, id, tenantID, token)
	require.NoError(t, err)

	_, err = dbClient.ExecContext(ctx,
		`UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, id,
	)
	require.NoError(t, err)

	// Immediately reinsert with the same push_token — should succeed because the
	// partial unique index excludes tombstoned rows.
	newID := uuid.New()
	_, err = dbClient.ExecContext(ctx, `
		INSERT INTO monitors (id, tenant_id, name, type, config, interval_seconds, timeout_seconds,
		    alert_policy_id, enabled, tags, agent_id, push_token, next_run_at, created_at, updated_at)
		VALUES ($1, $2, 'second', 'push', '{}'::jsonb, 60, 30, NULL, TRUE, ARRAY[]::text[],
		    NULL, $3, NULL, NOW(), NOW())
	`, newID, tenantID, token)
	require.NoError(t, err, "reimporting with the same push_token must succeed while old row is tombstoned but not yet purged")
}

func TestPurger_DetachesAutoIncidentsBeforeDelete(t *testing.T) {
	// Regression: incidents_auto_fields_check (migration 000038) requires
	// auto_monitor_id to stay non-null while is_auto_created is true. The FK
	// has ON DELETE SET NULL, so without an explicit detach the final
	// DELETE FROM monitors trips the CHECK and the monitor stays stuck.
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "")

	policyID := uuid.New()
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_policies (id, tenant_id, name, failure_threshold, failure_window_seconds, created_at, updated_at)
		VALUES ($1, $2, 'p', 1, 60, NOW(), NOW())
	`, policyID, tenantID)
	require.NoError(t, err)

	incidentID := uuid.New()
	_, err = dbClient.ExecContext(ctx, `
		INSERT INTO incidents (id, tenant_id, title, summary, state, is_auto_created, auto_monitor_id, auto_alert_policy_id, created_at, updated_at)
		VALUES ($1, $2, 'auto', '', 'investigating', TRUE, $3, $4, NOW(), NOW())
	`, incidentID, tenantID, monitorID, policyID)
	require.NoError(t, err)

	_, err = dbClient.ExecContext(ctx,
		`UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, monitorID)
	require.NoError(t, err)

	p := newPurger(dbClient, testLogger(t), nil, purgerOptions{BatchSize: 10, MaxRowsPerRun: 100})
	purged, err := p.runOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, purged, "monitor with auto-incident must be purged, not blocked")

	// Incident must still exist, detached.
	var (
		isAuto      bool
		autoMonitor *uuid.UUID
		autoPolicy  *uuid.UUID
	)
	require.NoError(t, dbClient.QueryRowContext(ctx,
		`SELECT is_auto_created, auto_monitor_id, auto_alert_policy_id FROM incidents WHERE id = $1`,
		incidentID,
	).Scan(&isAuto, &autoMonitor, &autoPolicy))
	require.False(t, isAuto, "incident should be demoted to manual")
	require.Nil(t, autoMonitor, "auto_monitor_id should be cleared")
	require.Nil(t, autoPolicy, "auto_alert_policy_id should be cleared")
}

func testLogger(t testing.TB) *logger.Logger {
	t.Helper()
	return logger.New("scheduler_test", "error")
}

//go:build !unit

package scheduler

// Recovery confirmation in the scheduler (docs/state-semantics.md S-T7): the
// watchdog's stale-location reset and progress refresh, and the dispatch-time
// recovering flag behind the fast recheck.

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestWatchdogStaleDownLocationOwesConfirmationAndProgressRefreshes(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "watchdog-recovery")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "recovering")
	locations := map[string]uuid.UUID{"fresh": uuid.New(), "stale": uuid.New()}
	for name, id := range locations {
		if _, err := dbClient.ExecContext(ctx, `INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, $3, $3)`,
			id, tenantID, name); err != nil {
			t.Fatal(err)
		}
		if _, err := dbClient.ExecContext(ctx, `INSERT INTO monitor_locations (monitor_id, location_id) VALUES ($1, $2)`,
			monitorID, id); err != nil {
			t.Fatal(err)
		}
	}
	// Quorum 1, both down and recovering; the stale one is the furthest along,
	// so the monitor-level progress (the slowest location, quorum 1) is 1.
	mustExecSched := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := dbClient.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec: %v", err)
		}
	}
	mustExecSched(`INSERT INTO monitor_location_state (monitor_id, location_id, tenant_id, current_state, consecutive_successes, last_check_at)
		VALUES ($1, $2, $3, 'down', 1, NOW())`, monitorID, locations["fresh"], tenantID)
	mustExecSched(`INSERT INTO monitor_location_state (monitor_id, location_id, tenant_id, current_state, consecutive_successes, last_check_at)
		VALUES ($1, $2, $3, 'down', 2, NOW() - INTERVAL '1 hour')`, monitorID, locations["stale"], tenantID)
	mustExecSched(`UPDATE monitors SET current_state = 'down', consecutive_successes = 2, recovery_threshold = 3,
		location_quorum = 1, interval_seconds = 60 WHERE id = $1`, monitorID)

	s := newTestScheduler(dbClient)
	if err := s.watchdogQuorumPass(ctx); err != nil {
		t.Fatalf("quorum pass: %v", err)
	}

	var state string
	var successes int
	var pending bool
	if err := dbClient.QueryRowContext(ctx, `
		SELECT current_state, consecutive_successes, recovery_pending FROM monitor_location_state
		WHERE monitor_id = $1 AND location_id = $2
	`, monitorID, locations["stale"]).Scan(&state, &successes, &pending); err != nil {
		t.Fatal(err)
	}
	if state != "unknown" || successes != 0 || !pending {
		t.Fatalf("stale down location = %s/CS %d pending %v, want unknown/0 owing the confirmation [S-F2, S-T7]", state, successes, pending)
	}
	// The aggregate is still down (the fresh location), and its progress now
	// counts the fresh location only, though the state did not change.
	if err := dbClient.QueryRowContext(ctx,
		`SELECT current_state, consecutive_successes FROM monitors WHERE id = $1`, monitorID).Scan(&state, &successes); err != nil {
		t.Fatal(err)
	}
	if state != "down" || successes != 1 {
		t.Fatalf("monitor = %s/CS %d, want down/1 recomputed from the fresh location [S-T7]", state, successes)
	}
}

func TestFetchDueMonitorsFlagsRecovering(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	recovering := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "recovering")
	down := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "down")
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE monitors SET current_state = 'down', consecutive_successes = CASE WHEN id = $1 THEN 1 ELSE 0 END
		WHERE id IN ($1, $2)
	`, recovering, down); err != nil {
		t.Fatal(err)
	}

	s := &Scheduler{db: dbClient}
	tx, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	monitors, err := s.fetchDueMonitors(ctx, tx, 10)
	if err != nil {
		t.Fatalf("fetchDueMonitors: %v", err)
	}
	got := map[uuid.UUID]bool{}
	for _, m := range monitors {
		got[m.ID] = m.Recovering
	}
	if !got[recovering] || got[down] {
		t.Fatalf("recovering flags = %v, want only %s recovering [S-T7]", got, recovering)
	}
}

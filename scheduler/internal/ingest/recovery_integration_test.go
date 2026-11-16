//go:build !unit

package ingest

// Recovery confirmation through the results ingest (docs/state-semantics.md
// S-T7): a down stream stays down until recovery_threshold passes in a row,
// the unconfirmed time stays on the down interval, and on location-bound
// monitors the confirmation runs per location before quorum.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func setRecoveryThreshold(ctx context.Context, t testing.TB, dbClient *db.Client, monitorID uuid.UUID, failures, recovery int) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE monitors SET consecutive_failures_threshold = $2, recovery_threshold = $3 WHERE id = $1
	`, monitorID, failures, recovery); err != nil {
		t.Fatalf("set thresholds: %v", err)
	}
}

func monitorCounts(ctx context.Context, t testing.TB, dbClient *db.Client, monitorID uuid.UUID) (string, int, int) {
	t.Helper()
	var state string
	var fails, successes int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT current_state, consecutive_failures, consecutive_successes FROM monitors WHERE id = $1
	`, monitorID).Scan(&state, &fails, &successes); err != nil {
		t.Fatalf("query monitor counts: %v", err)
	}
	return state, fails, successes
}

func TestIngestRecoveryConfirmation(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "recovery")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "flapping")
	setRecoveryThreshold(ctx, t, dbClient, monitorID, 1, 3)
	spy := &spyStatusPublisher{}
	i := newTestIngest(t, dbClient, spy)

	steps := []struct {
		status    string
		state     string
		fails     int
		successes int
		rules     string
	}{
		{"failure", "down", 1, 0, "S-T2"},
		{"success", "down", 0, 1, "S-T7 (1 of 3)"},
		{"success", "down", 0, 2, "S-T7 (2 of 3)"},
		{"failure", "down", 1, 0, "S-T3, S-T7 (a failure resets the count)"},
		{"success", "down", 0, 1, "S-T7"},
		{"success", "down", 0, 2, "S-T7"},
		{"success", "up", 0, 0, "S-T7 (confirmed)"},
	}
	var beforeConfirming time.Time
	for idx, step := range steps {
		if idx == len(steps)-1 {
			if err := dbClient.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&beforeConfirming); err != nil {
				t.Fatalf("read database clock: %v", err)
			}
		}
		if err := i.handleMessage(ctx, testMessage(mustMarshal(t, monitorMessage(tenantID, monitorID, step.status)))); err != nil {
			t.Fatalf("handleMessage %s: %v", step.status, err)
		}
		state, fails, successes := monitorCounts(ctx, t, dbClient, monitorID)
		if state != step.state || fails != step.fails || successes != step.successes {
			t.Fatalf("step %d (%s): %s/CF %d/CS %d, want %s/%d/%d [%s]",
				idx, step.status, state, fails, successes, step.state, step.fails, step.successes, step.rules)
		}
	}

	// The unconfirmed passes moved no state: one down interval from the first
	// failure to the confirming pass, then up (S-U4) — the recovery is not
	// re-dated to the first pass.
	intervals := loadIntervals(ctx, t, dbClient, monitorID)
	if len(intervals) != 2 || intervals[0].State != "down" || intervals[1].State != "up" {
		t.Fatalf("intervals = %+v, want down then up [S-T7, S-U4]", intervals)
	}
	if intervals[0].Ended == nil || intervals[0].Ended.Before(beforeConfirming) {
		t.Fatalf("down interval ended %v, before the confirming pass at %v [S-T7]", intervals[0].Ended, beforeConfirming)
	}
	if len(spy.events) != 2 {
		t.Fatalf("published %d state changes, want 2 (down, up) — unconfirmed passes are not transitions", len(spy.events))
	}
}

func TestQuorumRecoveryConfirmation(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "quorum-recovery")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	locA := insertLocation(ctx, t, dbClient, tenantID, "vpc-a")
	locB := insertLocation(ctx, t, dbClient, tenantID, "vpc-b")
	locC := insertLocation(ctx, t, dbClient, tenantID, "vpc-c")
	selectLocations(ctx, t, dbClient, monitorID, 2, locA, locB, locC)
	setRecoveryThreshold(ctx, t, dbClient, monitorID, 1, 2)
	i := newTestIngest(t, dbClient, nil)

	locationSuccesses := func(locationID uuid.UUID) int {
		t.Helper()
		var n int
		if err := dbClient.QueryRowContext(ctx, `
			SELECT consecutive_successes FROM monitor_location_state WHERE monitor_id = $1 AND location_id = $2
		`, monitorID, locationID).Scan(&n); err != nil {
			t.Fatalf("query location successes: %v", err)
		}
		return n
	}

	steps := []struct {
		loc       uuid.UUID
		status    string
		state     string
		successes int // monitor level (RecoveryProgress)
		rules     string
	}{
		{locA, "failure", "degraded", 0, "S-A2"},
		{locB, "failure", "down", 0, "S-A1"},
		{locC, "failure", "down", 0, "S-A1 (3 down, 2 must confirm)"},
		{locA, "success", "down", 0, "S-T7 per location: a 1 of 2; b and c at 0"},
		{locB, "success", "down", 1, "S-T7: the second-most advanced location has 1"},
		{locA, "success", "down", 1, "S-T7: a confirmed, 2 still down = quorum"},
		{locB, "success", "degraded", 0, "S-T7, S-A2: b confirmed, below quorum"},
		{locC, "success", "degraded", 0, "S-T7: c 1 of 2 (monitor not down)"},
		{locC, "success", "up", 0, "S-T7, S-A4"},
	}
	for idx, step := range steps {
		ingestMessage(t, i, locationMessage(tenantID, monitorID, step.loc, step.status))
		state, _, successes := monitorCounts(ctx, t, dbClient, monitorID)
		if state != step.state || successes != step.successes {
			t.Fatalf("step %d: monitor %s/CS %d, want %s/%d [%s]", idx, state, successes, step.state, step.successes, step.rules)
		}
		if idx == 3 && locationSuccesses(locA) != 1 {
			t.Fatalf("location a successes = %d, want 1 [S-T7]", locationSuccesses(locA))
		}
	}
	for _, loc := range []uuid.UUID{locA, locB, locC} {
		if n := locationSuccesses(loc); n != 0 {
			t.Fatalf("location %s successes = %d after recovering, want 0 [S-T7]", loc, n)
		}
	}
}

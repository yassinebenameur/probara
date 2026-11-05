//go:build !unit

package ingest

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func setRecoveryThreshold(ctx context.Context, t testing.TB, dbClient *db.Client, monitorID uuid.UUID, n int) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE monitors SET recovery_threshold = $2 WHERE id = $1`, monitorID, n); err != nil {
		t.Fatalf("set recovery_threshold: %v", err)
	}
}

func monitorStreaks(ctx context.Context, t testing.TB, dbClient *db.Client, monitorID uuid.UUID) (string, int, int) {
	t.Helper()
	var state string
	var fails, successes int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT current_state, consecutive_failures, consecutive_successes FROM monitors WHERE id = $1
	`, monitorID).Scan(&state, &fails, &successes); err != nil {
		t.Fatalf("query monitor streaks: %v", err)
	}
	return state, fails, successes
}

// S-T7, location-less: with recovery_threshold 3 a down monitor stays down
// through two passes, a failure restarts the count, and only the third
// consecutive pass brings it up. Unconfirmed recovery time stays inside the
// one down interval (it is accounted as down), and only the two real
// transitions publish.
func TestIngestRecoveryConfirmation(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "recovery")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	setRecoveryThreshold(ctx, t, dbClient, monitorID, 3)

	spy := &spyStatusPublisher{}
	i := newTestIngest(t, dbClient, spy)
	handle := func(status string) {
		t.Helper()
		if err := i.handleMessage(ctx, testMessage(mustMarshal(t, monitorMessage(tenantID, monitorID, status)))); err != nil {
			t.Fatalf("handleMessage %s: %v", status, err)
		}
	}
	assert := func(step, wantState string, wantFails, wantSuccesses int) {
		t.Helper()
		state, fails, successes := monitorStreaks(ctx, t, dbClient, monitorID)
		if state != wantState || fails != wantFails || successes != wantSuccesses {
			t.Fatalf("%s: state/CF/CS = %s/%d/%d, want %s/%d/%d [S-T7]",
				step, state, fails, successes, wantState, wantFails, wantSuccesses)
		}
	}

	handle("failure")
	handle("failure")
	assert("confirmed down", "down", 2, 0)
	published := len(spy.events)

	handle("success")
	assert("pass 1 of 3", "down", 0, 1)
	handle("success")
	assert("pass 2 of 3", "down", 0, 2)
	handle("failure")
	assert("failure resets the count", "down", 1, 0)
	handle("success")
	handle("success")
	assert("passes 1-2 of 3 again", "down", 0, 2)
	if len(spy.events) != published {
		t.Fatalf("unconfirmed passes published %d events, want none [S-T7, S-F3]", len(spy.events)-published)
	}

	handle("success")
	assert("confirmed recovery", "up", 0, 0)
	if len(spy.events) != published+1 {
		t.Fatalf("confirmed recovery published %d events, want 1", len(spy.events)-published)
	}

	// Timeline: suspect → down → up; the five unconfirmed results open no
	// interval of their own.
	rows, err := dbClient.QueryContext(ctx, `
		SELECT state FROM monitor_state_intervals WHERE monitor_id = $1 ORDER BY started_at, created_at
	`, monitorID)
	if err != nil {
		t.Fatalf("query intervals: %v", err)
	}
	defer rows.Close()
	var states []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan interval: %v", err)
		}
		if len(states) == 0 && s == "unknown" {
			continue // a creation interval, when the fixture writes one
		}
		states = append(states, s)
	}
	want := []string{"suspect", "down", "up"}
	if len(states) != len(want) {
		t.Fatalf("intervals = %v, want %v [S-T7, S-U4]", states, want)
	}
	for n := range want {
		if states[n] != want[n] {
			t.Fatalf("intervals = %v, want %v [S-T7, S-U4]", states, want)
		}
	}
}

// S-T7 per location under quorum 2 of 2, recovery_threshold 2: a location
// mid-confirmation still counts as down, so the monitor stays down until one
// location confirms (then degraded) and up only once both have. The
// monitor-level count is the furthest-along fresh location's.
func TestQuorumRecoveryConfirmation(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "quorum-recovery")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	locA := insertLocation(ctx, t, dbClient, tenantID, "vpc-a")
	locB := insertLocation(ctx, t, dbClient, tenantID, "vpc-b")
	selectLocations(ctx, t, dbClient, monitorID, 2, locA, locB)
	setRecoveryThreshold(ctx, t, dbClient, monitorID, 2)

	i := newTestIngest(t, dbClient, nil)
	send := func(loc uuid.UUID, status string) {
		t.Helper()
		ingestMessage(t, i, locationMessage(tenantID, monitorID, loc, status))
	}
	assert := func(step, wantState string, wantSuccesses int) {
		t.Helper()
		state, _, successes := monitorStreaks(ctx, t, dbClient, monitorID)
		if state != wantState || successes != wantSuccesses {
			t.Fatalf("%s: monitor = %s CS %d, want %s CS %d [S-T7, S-A1, S-A2]",
				step, state, successes, wantState, wantSuccesses)
		}
	}
	locationSuccesses := func(loc uuid.UUID) int {
		t.Helper()
		var n int
		if err := dbClient.QueryRowContext(ctx, `
			SELECT consecutive_successes FROM monitor_location_state WHERE monitor_id = $1 AND location_id = $2
		`, monitorID, loc).Scan(&n); err != nil {
			t.Fatalf("query location streak: %v", err)
		}
		return n
	}

	for _, loc := range []uuid.UUID{locA, locB} {
		send(loc, "failure")
		send(loc, "failure")
	}
	assert("both down", "down", 0)

	send(locA, "success")
	assert("A pass 1 of 2", "down", 1)
	if got := locationSuccesses(locA); got != 1 {
		t.Fatalf("location A CS = %d, want 1 [S-T7]", got)
	}
	send(locB, "success")
	assert("B pass 1 of 2", "down", 1)

	send(locA, "success")
	assert("A confirmed", "degraded", 1)
	if got := locationSuccesses(locA); got != 0 {
		t.Fatalf("location A CS = %d after confirming, want 0 [S-T7]", got)
	}

	send(locB, "failure")
	assert("B failure resets its count", "degraded", 0)
	send(locB, "success")
	send(locB, "success")
	assert("B confirmed", "up", 0)
}

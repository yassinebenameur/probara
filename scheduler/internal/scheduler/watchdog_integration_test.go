package scheduler

// Absence-watchdog tests (docs/state-semantics.md S-F1..S-F4, S-A10): missing
// evidence becomes explicit state instead of a frozen last value.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func setMonitorEvidence(ctx context.Context, t *testing.T, db *shareddb.Client, monitorID uuid.UUID, state string, lastResultAt time.Time) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		UPDATE monitors SET current_state = $2, last_result_at = $3, interval_seconds = 60 WHERE id = $1
	`, monitorID, state, lastResultAt); err != nil {
		t.Fatalf("set monitor evidence: %v", err)
	}
}

func monitorStateAndFails(ctx context.Context, t *testing.T, db *shareddb.Client, monitorID uuid.UUID) (string, int) {
	t.Helper()
	var state string
	var fails int
	if err := db.QueryRowContext(ctx,
		`SELECT current_state, consecutive_failures FROM monitors WHERE id = $1`, monitorID).Scan(&state, &fails); err != nil {
		t.Fatalf("query monitor state: %v", err)
	}
	return state, fails
}

// S-F2: a stale active monitor transitions to unknown (reason
// watchdog_stale); fresh monitors are untouched.
func TestWatchdogMarksStaleActiveMonitorUnknown(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "watchdog")
	staleID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "stale-target")
	freshID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "fresh-target")
	setMonitorEvidence(ctx, t, dbClient, staleID, "up", time.Now().Add(-10*time.Minute))
	setMonitorEvidence(ctx, t, dbClient, freshID, "up", time.Now())

	s := newTestScheduler(dbClient)
	if err := s.watchdogActivePass(ctx); err != nil {
		t.Fatalf("active pass: %v", err)
	}

	if state, fails := monitorStateAndFails(ctx, t, dbClient, staleID); state != "unknown" || fails != 0 {
		t.Fatalf("stale monitor = %s/%d, want unknown/0 [S-F2]", state, fails)
	}
	if state, _ := monitorStateAndFails(ctx, t, dbClient, freshID); state != "up" {
		t.Fatalf("fresh monitor = %s, want up untouched [S-F1]", state)
	}
	var reason string
	var open bool
	if err := dbClient.QueryRowContext(ctx, `
		SELECT reason, ended_at IS NULL FROM monitor_state_intervals
		WHERE monitor_id = $1 ORDER BY started_at DESC LIMIT 1
	`, staleID).Scan(&reason, &open); err != nil {
		t.Fatalf("query interval: %v", err)
	}
	if reason != "watchdog_stale" || !open {
		t.Fatalf("interval = %s/open=%v, want watchdog_stale/open [S-F3, S-U4]", reason, open)
	}
}

// S-A10: a location whose evidence goes stale stops voting — the watchdog
// re-derives the aggregate without any result arriving.
func TestWatchdogReaggregatesStaleLocations(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "watchdog-quorum")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "quorum-target")
	locationID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, 'wd-loc', 'wd-loc')
	`, locationID, tenantID); err != nil {
		t.Fatalf("insert location: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_locations (monitor_id, location_id) VALUES ($1, $2)
	`, monitorID, locationID); err != nil {
		t.Fatalf("attach location: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_location_state (monitor_id, location_id, tenant_id, current_state, consecutive_failures, last_check_at)
		VALUES ($1, $2, $3, 'up', 0, NOW() - INTERVAL '10 minutes')
	`, monitorID, locationID, tenantID); err != nil {
		t.Fatalf("insert location state: %v", err)
	}
	setMonitorEvidence(ctx, t, dbClient, monitorID, "up", time.Now().Add(-10*time.Minute))

	s := newTestScheduler(dbClient)
	if err := s.watchdogQuorumPass(ctx); err != nil {
		t.Fatalf("quorum pass: %v", err)
	}
	if state, _ := monitorStateAndFails(ctx, t, dbClient, monitorID); state != "unknown" {
		t.Fatalf("monitor = %s, want unknown (zero fresh locations) [S-A10, S-A5]", state)
	}

	// The location comes back reporting down: fresh evidence votes again.
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE monitor_location_state SET current_state = 'down', consecutive_failures = 2, last_check_at = NOW()
		WHERE monitor_id = $1 AND location_id = $2
	`, monitorID, locationID); err != nil {
		t.Fatalf("refresh location state: %v", err)
	}
	if err := s.reaggregateMonitor(ctx, monitorID); err != nil {
		t.Fatalf("reaggregate: %v", err)
	}
	if state, fails := monitorStateAndFails(ctx, t, dbClient, monitorID); state != "down" || fails != 2 {
		t.Fatalf("monitor = %s/%d, want down/2 from fresh evidence [S-A1]", state, fails)
	}
}

// Push heartbeats: a missed ping IS failure evidence (interval + grace), and
// the synthetic result self-debounces via last_result_at.
func TestWatchdogSynthesizesPushFailure(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "watchdog-push")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "push-target")
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE monitors SET type = 'push', config = '{"grace_period_seconds": 30}'::jsonb,
			interval_seconds = 60, last_result_at = NOW() - INTERVAL '5 minutes'
		WHERE id = $1
	`, monitorID); err != nil {
		t.Fatalf("make push monitor: %v", err)
	}

	s := newTestScheduler(dbClient)
	if err := s.watchdogHeartbeatPass(ctx, "push"); err != nil {
		t.Fatalf("push pass: %v", err)
	}

	if state, fails := monitorStateAndFails(ctx, t, dbClient, monitorID); state != "suspect" || fails != 1 {
		t.Fatalf("push monitor = %s/%d, want suspect/1 after one synthetic failure", state, fails)
	}
	var results int
	if err := dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_results WHERE monitor_id = $1 AND status = 'failure'`, monitorID).Scan(&results); err != nil {
		t.Fatalf("count results: %v", err)
	}
	if results != 1 {
		t.Fatalf("synthetic failures = %d, want 1", results)
	}

	// Immediate re-run: the synthetic result refreshed last_result_at, so the
	// next failure fires a full window later, not every tick (S-F4).
	if err := s.watchdogHeartbeatPass(ctx, "push"); err != nil {
		t.Fatalf("push pass repeat: %v", err)
	}
	if err := dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_results WHERE monitor_id = $1 AND status = 'failure'`, monitorID).Scan(&results); err != nil {
		t.Fatalf("count results: %v", err)
	}
	if results != 1 {
		t.Fatalf("synthetic failures after immediate re-run = %d, want 1 (self-debounce)", results)
	}
}

// Agent heartbeats (S-F1/S-F4): an agent monitor whose OTLP export receipts
// stop arriving accumulates synthetic failures on the freshness horizon
// (interval×3, floor 90s), self-debounced exactly like push.
func TestWatchdogSynthesizesAgentFailure(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "watchdog-agent")
	monitorID := testutil.InsertAgentMonitor(ctx, t, dbClient, tenantID, "agent-target", uuid.New().String(), 60)
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE monitors SET current_state = 'up', last_result_at = NOW() - INTERVAL '10 minutes' WHERE id = $1
	`, monitorID); err != nil {
		t.Fatalf("age agent evidence: %v", err)
	}

	s := newTestScheduler(dbClient)
	if err := s.watchdogHeartbeatPass(ctx, "agent"); err != nil {
		t.Fatalf("agent pass: %v", err)
	}

	if state, fails := monitorStateAndFails(ctx, t, dbClient, monitorID); state != "suspect" || fails != 1 {
		t.Fatalf("agent monitor = %s/%d, want suspect/1 after one synthetic failure", state, fails)
	}
	var results int
	if err := dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_results WHERE monitor_id = $1 AND status = 'failure'`, monitorID).Scan(&results); err != nil {
		t.Fatalf("count results: %v", err)
	}
	if results != 1 {
		t.Fatalf("synthetic failures = %d, want 1", results)
	}

	// Self-debounce: the synthetic row advanced last_result_at, so an
	// immediate re-run adds nothing (S-F4).
	if err := s.watchdogHeartbeatPass(ctx, "agent"); err != nil {
		t.Fatalf("agent pass repeat: %v", err)
	}
	if err := dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_results WHERE monitor_id = $1 AND status = 'failure'`, monitorID).Scan(&results); err != nil {
		t.Fatalf("count results: %v", err)
	}
	if results != 1 {
		t.Fatalf("synthetic failures after immediate re-run = %d, want 1 (self-debounce)", results)
	}
}

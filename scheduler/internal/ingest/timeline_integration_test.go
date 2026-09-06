package ingest

// Timeline write-path tests for docs/state-semantics.md: intervals are
// written in the transition's transaction (S-U4), late evidence never moves
// state backwards (S-O2), and location-less results for a location-bound
// monitor are history-only (S-E5).

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type intervalRow struct {
	State   string
	Reason  string
	Started time.Time
	Ended   *time.Time
}

func loadIntervals(ctx context.Context, t *testing.T, dbClient *db.Client, monitorID uuid.UUID) []intervalRow {
	t.Helper()
	rows, err := dbClient.QueryContext(ctx, `
		SELECT state, reason, started_at, ended_at
		FROM monitor_state_intervals
		WHERE monitor_id = $1
		ORDER BY started_at, created_at
	`, monitorID)
	if err != nil {
		t.Fatalf("query intervals: %v", err)
	}
	defer rows.Close()
	var out []intervalRow
	for rows.Next() {
		var r intervalRow
		if err := rows.Scan(&r.State, &r.Reason, &r.Started, &r.Ended); err != nil {
			t.Fatalf("scan interval: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate intervals: %v", err)
	}
	return out
}

// S-U4: every monitor-level transition closes the open interval and opens the
// next one in the same transaction, leaving a gapless timeline with exactly
// one open row.
func TestIngestWritesStateIntervals(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "timeline")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "timeline-target")
	i := newTestIngest(t, dbClient, nil)

	ingestMessage(t, i, monitorMessage(tenantID, monitorID, "failure")) // -> suspect
	ingestMessage(t, i, monitorMessage(tenantID, monitorID, "failure")) // -> down
	ingestMessage(t, i, monitorMessage(tenantID, monitorID, "failure")) // stays down: no interval
	ingestMessage(t, i, monitorMessage(tenantID, monitorID, "success")) // -> up

	intervals := loadIntervals(ctx, t, dbClient, monitorID)
	wantStates := []string{"suspect", "down", "up"}
	if len(intervals) != len(wantStates) {
		t.Fatalf("interval count = %d, want %d: %+v", len(intervals), len(wantStates), intervals)
	}
	for idx, want := range wantStates {
		if intervals[idx].State != want || intervals[idx].Reason != "result" {
			t.Fatalf("interval %d = %s/%s, want %s/result [S-U4]", idx, intervals[idx].State, intervals[idx].Reason, want)
		}
	}
	for idx := 0; idx < len(intervals)-1; idx++ {
		if intervals[idx].Ended == nil {
			t.Fatalf("interval %d should be closed", idx)
		}
		if !intervals[idx].Ended.Equal(intervals[idx+1].Started) {
			t.Fatalf("timeline gap: interval %d ends %v, next starts %v [S-U4]",
				idx, intervals[idx].Ended, intervals[idx+1].Started)
		}
	}
	if intervals[len(intervals)-1].Ended != nil {
		t.Fatal("newest interval must be open")
	}

	// Every monitor-source insert marks its rollup bucket dirty in the same
	// transaction (Stage 7 exactness-by-construction).
	var dirty int
	if err := dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rollup_dirty WHERE monitor_id = $1`, monitorID).Scan(&dirty); err != nil {
		t.Fatalf("count dirty buckets: %v", err)
	}
	if dirty == 0 {
		t.Fatal("expected at least one dirty rollup bucket after ingest")
	}
}

// S-O2: evidence older than the newest applied on the stream is recorded as
// history but never moves state backwards.
func TestIngestLateResultIsHistoryOnly(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "late-result")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "late-target")
	i := newTestIngest(t, dbClient, nil)

	ingestMessage(t, i, monitorMessage(tenantID, monitorID, "success")) // -> up, watermark = now

	late := monitorMessage(tenantID, monitorID, "failure")
	late.StartedAt = time.Now().Add(-time.Hour)
	late.CompletedAt = late.StartedAt.Add(time.Second)
	ingestMessage(t, i, late)

	var state string
	var fails int
	if err := dbClient.QueryRowContext(ctx,
		`SELECT current_state, consecutive_failures FROM monitors WHERE id = $1`,
		monitorID).Scan(&state, &fails); err != nil {
		t.Fatalf("query state: %v", err)
	}
	if state != "up" || fails != 0 {
		t.Fatalf("state = %s/%d after late failure, want up/0 [S-O2]", state, fails)
	}
	var historyRows int
	if err := dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_results WHERE monitor_id = $1`, monitorID).Scan(&historyRows); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if historyRows != 2 {
		t.Fatalf("history rows = %d, want 2 (late result kept as history) [S-O2]", historyRows)
	}
	if intervals := loadIntervals(ctx, t, dbClient, monitorID); len(intervals) != 1 {
		t.Fatalf("intervals = %d, want 1 (late result opens nothing) [S-O2, S-U4]", len(intervals))
	}

	// Fresh evidence still applies normally after a late arrival.
	ingestMessage(t, i, monitorMessage(tenantID, monitorID, "failure"))
	if err := dbClient.QueryRowContext(ctx,
		`SELECT current_state FROM monitors WHERE id = $1`, monitorID).Scan(&state); err != nil {
		t.Fatalf("query state: %v", err)
	}
	if state != "suspect" {
		t.Fatalf("state = %s after fresh failure, want suspect", state)
	}
}

// S-O2 applies per evidence stream: a location's late result keeps its
// history row but never rolls the per-location state or the aggregate back.
func TestIngestLateLocationResultIsHistoryOnly(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "late-loc")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "late-loc-target")
	locationID := insertLocation(ctx, t, dbClient, tenantID, "late-loc-a")
	selectLocations(ctx, t, dbClient, monitorID, 1, locationID)
	i := newTestIngest(t, dbClient, nil)

	ingestMessage(t, i, locationMessage(tenantID, monitorID, locationID, "success")) // loc up -> monitor up

	late := locationMessage(tenantID, monitorID, locationID, "failure")
	late.StartedAt = time.Now().Add(-time.Hour)
	late.CompletedAt = late.StartedAt.Add(time.Second)
	ingestMessage(t, i, late)

	if state := monitorState(ctx, t, dbClient, monitorID); state != "up" {
		t.Fatalf("monitor state = %s after late location failure, want up [S-O2]", state)
	}
	var locState string
	var locFails int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT current_state, consecutive_failures FROM monitor_location_state
		WHERE monitor_id = $1 AND location_id = $2
	`, monitorID, locationID).Scan(&locState, &locFails); err != nil {
		t.Fatalf("query location state: %v", err)
	}
	if locState != "up" || locFails != 0 {
		t.Fatalf("location state = %s/%d, want up/0 [S-O2]", locState, locFails)
	}
	var historyRows int
	if err := dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_results WHERE monitor_id = $1`, monitorID).Scan(&historyRows); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if historyRows != 2 {
		t.Fatalf("history rows = %d, want 2 [S-O2]", historyRows)
	}
	if intervals := loadIntervals(ctx, t, dbClient, monitorID); len(intervals) != 1 {
		t.Fatalf("intervals = %d, want 1 [S-O2, S-U4]", len(intervals))
	}
}

// S-E5: a monitor with selected locations is owned by the quorum aggregate; a
// location-less (default-fleet) result keeps its history row but must not
// clobber the state.
func TestIngestLocationLessResultForLocationBoundMonitorIsHistoryOnly(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "loc-bound")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "loc-bound-target")
	locationID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, 'tl-loc', 'tl-loc')
	`, locationID, tenantID); err != nil {
		t.Fatalf("insert location: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_locations (monitor_id, location_id) VALUES ($1, $2)
	`, monitorID, locationID); err != nil {
		t.Fatalf("attach location: %v", err)
	}

	i := newTestIngest(t, dbClient, nil)
	msg := monitorMessage(tenantID, monitorID, "failure")
	msg.LocationID = "" // default-fleet result despite selected locations
	ingestMessage(t, i, msg)

	var state string
	if err := dbClient.QueryRowContext(ctx,
		`SELECT current_state FROM monitors WHERE id = $1`, monitorID).Scan(&state); err != nil {
		t.Fatalf("query state: %v", err)
	}
	if state != "unknown" {
		t.Fatalf("state = %s, want unknown untouched [S-E5]", state)
	}
	var historyRows int
	if err := dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_results WHERE monitor_id = $1`, monitorID).Scan(&historyRows); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if historyRows != 1 {
		t.Fatalf("history rows = %d, want 1 [S-E5]", historyRows)
	}
	if intervals := loadIntervals(ctx, t, dbClient, monitorID); len(intervals) != 0 {
		t.Fatalf("intervals = %d, want 0 [S-E5]", len(intervals))
	}
}

// S-O1/S-U4: the monitor lock serializes writers, but NOW() is the
// transaction *start* time. A transaction that began first yet acquired the
// lock second used to close an interval opened later than its own NOW() and
// trip the `ended_at >= started_at` check (the flaky failure behind
// TestQuorumConcurrentResultsNoDeadlock in CI). Force that exact interleaving
// deterministically and require a monotonic, gap-free timeline instead.
func TestRecordIntervalTxEarlierTransactionLosingTheLockStaysMonotonic(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "timeline-race")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")

	lockMonitor := func(tx *sql.Tx) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, `SELECT id FROM monitors WHERE id = $1 FOR UPDATE`, monitorID); err != nil {
			t.Fatalf("lock monitor: %v", err)
		}
	}

	// Transaction A starts first: its NOW() is pinned to the earlier instant.
	txA, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin A: %v", err)
	}
	defer txA.Rollback() //nolint:errcheck // rollback after commit is a no-op
	var startA time.Time
	if err := txA.QueryRowContext(ctx, `SELECT NOW()`).Scan(&startA); err != nil {
		t.Fatalf("pin A's transaction timestamp: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Transaction B starts later but wins the lock and commits first, so the
	// open interval now starts *after* A's NOW().
	txB, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin B: %v", err)
	}
	lockMonitor(txB)
	if err := monitorstate.RecordIntervalTx(ctx, txB, tenantID, monitorID, monitorstate.StateDown, monitorstate.IntervalReasonResult); err != nil {
		t.Fatalf("B record interval: %v", err)
	}
	if err := txB.Commit(); err != nil {
		t.Fatalf("commit B: %v", err)
	}

	// A takes the lock second and records its own transition.
	lockMonitor(txA)
	if err := monitorstate.RecordIntervalTx(ctx, txA, tenantID, monitorID, monitorstate.StateUp, monitorstate.IntervalReasonResult); err != nil {
		t.Fatalf("A record interval after losing the lock race: %v", err)
	}
	if err := txA.Commit(); err != nil {
		t.Fatalf("commit A: %v", err)
	}

	intervals := loadIntervals(ctx, t, dbClient, monitorID)
	if len(intervals) != 2 {
		t.Fatalf("intervals = %d, want 2 (B's closed down, A's open up): %+v", len(intervals), intervals)
	}
	// B's interval is zero-length (closed at its own start), so both rows share
	// started_at and position in the timeline is not meaningful; pick by state.
	closed, open := intervals[0], intervals[1]
	if closed.Ended == nil {
		closed, open = open, closed
	}
	if closed.State != "down" || closed.Ended == nil {
		t.Fatalf("expected B's closed down interval, got %+v", closed)
	}
	if closed.Ended.Before(closed.Started) {
		t.Fatalf("closed interval ends before it starts: %+v", closed)
	}
	if !closed.Started.After(startA) {
		t.Fatalf("test precondition: B's interval (%s) must start after A's NOW() (%s)", closed.Started, startA)
	}
	if open.State != "up" || open.Ended != nil {
		t.Fatalf("second interval should be A's open up interval: %+v", open)
	}
	if !open.Started.Equal(*closed.Ended) {
		t.Fatalf("timeline gap: closed ends %s, open starts %s", *closed.Ended, open.Started)
	}
	var openCount int
	if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_state_intervals WHERE monitor_id = $1 AND ended_at IS NULL`, monitorID).Scan(&openCount); err != nil {
		t.Fatalf("count open intervals: %v", err)
	}
	if openCount != 1 {
		t.Fatalf("open intervals = %d, want exactly 1", openCount)
	}
}

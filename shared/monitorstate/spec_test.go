package monitorstate

// Scenario and property tests enforcing docs/state-semantics.md. Each case
// cites the spec rules (S-XX#) it enforces.
//
// Golden scenarios drive the pure state layer the way the ingest pipeline
// does: Apply per evidence stream, then AggregateLocations over the
// per-location snapshots — mirroring the pure parts of persistAtLocation
// (scheduler/internal/ingest/quorum.go). Transactional rules (S-E1, S-E3,
// S-E4, S-O1) are enforced by the testcontainers suites under
// scheduler/internal/ingest, not here.
//
// Property tests use fixed seeds so failures are reproducible.

import (
	"math/rand"
	"testing"
	"time"
)

// specStep is one piece of evidence arriving for a multi-location monitor,
// with the expected monitor-level aggregate after it is applied.
type specStep struct {
	loc     string
	failure bool
	want    State
	rules   string
}

// runLocationScenario replays evidence through the per-location temporal
// machine and re-aggregates after each step, as persistAtLocation does. A
// location has no snapshot until its first report (the LEFT JOIN in the
// counts query), and unreported locations contribute to Selected only.
func runLocationScenario(t *testing.T, selected []string, quorum, threshold int, steps []specStep) {
	t.Helper()
	runLocationScenarioWithRecovery(t, selected, quorum, threshold, 1, steps)
}

// runLocationScenarioWithRecovery is runLocationScenario with a recovery
// threshold (S-T7) applied per location stream, as persistAtLocation does.
func runLocationScenarioWithRecovery(t *testing.T, selected []string, quorum, threshold, recovery int, steps []specStep) {
	t.Helper()
	perLoc := map[string]Snapshot{}
	for i, step := range steps {
		snap, reported := perLoc[step.loc]
		if !reported {
			snap = Snapshot{State: StateUnknown}
		}
		tr := Apply(snap, step.failure, threshold, recovery)
		perLoc[step.loc] = snapshotOf(tr)

		c := LocationCounts{Selected: len(selected)}
		for _, l := range selected {
			s, ok := perLoc[l]
			if !ok {
				continue
			}
			switch s.State {
			case StateDown:
				c.Down++
			case StateSuspect:
				c.Suspect++
			case StateUp:
				c.Up++
			}
		}
		if got := AggregateLocations(c, quorum); got != step.want {
			t.Fatalf("step %d (loc=%s failure=%v, counts=%+v): monitor state = %s, want %s [rules %s]",
				i, step.loc, step.failure, c, got, step.want, step.rules)
		}
	}
}

// Full confirmation/recovery arc across three locations with quorum 2.
func TestSpecScenario_QuorumConfirmationAndRecovery(t *testing.T) {
	runLocationScenario(t, []string{"a", "b", "c"}, 2, 2, []specStep{
		{loc: "a", failure: true, want: StateSuspect, rules: "S-A3, S-T2"},
		{loc: "a", failure: true, want: StateDegraded, rules: "S-A2, S-T2"},
		{loc: "b", failure: true, want: StateDegraded, rules: "S-A2 (down beats suspect)"},
		{loc: "b", failure: true, want: StateDown, rules: "S-A1"},
		{loc: "c", failure: false, want: StateDown, rules: "S-A1 (an up location cannot outvote quorum)"},
		{loc: "b", failure: false, want: StateDegraded, rules: "S-A2, S-T4"},
		{loc: "a", failure: false, want: StateUp, rules: "S-A4, S-T4"},
	})
}

// Locations that never report contribute to no count and can never trip
// quorum; one confirmed-down location holds the monitor at degraded.
func TestSpecScenario_UnreportedLocationsNeverTripQuorum(t *testing.T) {
	runLocationScenario(t, []string{"a", "b", "c"}, 2, 1, []specStep{
		{loc: "a", failure: true, want: StateDegraded, rules: "S-A2, S-A5"},
		{loc: "a", failure: true, want: StateDegraded, rules: "S-A2, S-T3"},
	})
}

// A single fresh up location makes the monitor up while the others have never
// reported (S-A4/S-A5). NOTE: under S-A10 (pending) this holds only while
// that location's evidence is fresh — freshness is time-based and outside
// this pure harness; the enforcing test lands with the watchdog stage.
func TestSpecScenario_SingleUpAmongSilentIsUp(t *testing.T) {
	runLocationScenario(t, []string{"a", "b", "c"}, 2, 2, []specStep{
		{loc: "a", failure: false, want: StateUp, rules: "S-A4, S-A5; S-A10 pending"},
	})
}

// Location-less golden arc through the temporal machine, asserting the full
// transition bookkeeping (S-T1..S-T6).
func TestSpecScenario_LocationlessConfirmation(t *testing.T) {
	steps := []struct {
		failure bool
		want    Transition
		rules   string
	}{
		{true, Transition{From: StateUnknown, To: StateSuspect, ConsecutiveFailures: 1, Changed: true}, "S-T2"},
		{true, Transition{From: StateSuspect, To: StateDown, ConsecutiveFailures: 2, Changed: true, OpenedOutage: true}, "S-T2, S-T6"},
		{true, Transition{From: StateDown, To: StateDown, ConsecutiveFailures: 3}, "S-T3"},
		{false, Transition{From: StateDown, To: StateUp, Changed: true, ClosedOutage: true}, "S-T4, S-T6"},
		{true, Transition{From: StateUp, To: StateSuspect, ConsecutiveFailures: 1, Changed: true}, "S-T2"},
		{false, Transition{From: StateSuspect, To: StateUp, Changed: true}, "S-T1 (blip, no outage)"},
	}
	snap := Snapshot{State: StateUnknown}
	for i, step := range steps {
		got := Apply(snap, step.failure, 2, 1)
		if got != step.want {
			t.Fatalf("step %d: Apply() = %+v, want %+v [rules %s]", i, got, step.want, step.rules)
		}
		snap = snapshotOf(got)
	}
}

// snapshotOf is the snapshot a writer persists after a transition.
func snapshotOf(tr Transition) Snapshot {
	return Snapshot{State: tr.To, ConsecutiveFailures: tr.ConsecutiveFailures, ConsecutiveSuccesses: tr.ConsecutiveSuccesses}
}

// S-T7 golden arc, location-less, recovery threshold 3: passes from down
// count toward confirmation while the stream stays down, a failure resets
// the count, and the outage closes only at confirmation (S-T6).
func TestSpecScenario_RecoveryConfirmation(t *testing.T) {
	steps := []struct {
		failure bool
		want    Transition
		rules   string
	}{
		{true, Transition{From: StateUnknown, To: StateSuspect, ConsecutiveFailures: 1, Changed: true}, "S-T2"},
		{true, Transition{From: StateSuspect, To: StateDown, ConsecutiveFailures: 2, Changed: true, OpenedOutage: true}, "S-T2, S-T6"},
		{false, Transition{From: StateDown, To: StateDown, ConsecutiveSuccesses: 1}, "S-T7 (pass 1 of 3, still down)"},
		{false, Transition{From: StateDown, To: StateDown, ConsecutiveSuccesses: 2}, "S-T7 (pass 2 of 3, still down)"},
		{true, Transition{From: StateDown, To: StateDown, ConsecutiveFailures: 1}, "S-T3, S-T7 (failure resets passes)"},
		{false, Transition{From: StateDown, To: StateDown, ConsecutiveSuccesses: 1}, "S-T7 (count restarts)"},
		{false, Transition{From: StateDown, To: StateDown, ConsecutiveSuccesses: 2}, "S-T7"},
		{false, Transition{From: StateDown, To: StateUp, Changed: true, ClosedOutage: true}, "S-T4, S-T6, S-T7 (confirmed)"},
		{true, Transition{From: StateUp, To: StateSuspect, ConsecutiveFailures: 1, Changed: true}, "S-T2"},
		{false, Transition{From: StateSuspect, To: StateUp, Changed: true}, "S-T1, S-T7 (only leaving down is confirmed)"},
	}
	snap := Snapshot{State: StateUnknown}
	for i, step := range steps {
		got := Apply(snap, step.failure, 2, 3)
		if got != step.want {
			t.Fatalf("step %d: Apply() = %+v, want %+v [rules %s]", i, got, step.want, step.rules)
		}
		snap = snapshotOf(got)
	}
}

// S-T7 per location under quorum: a location confirming its recovery still
// counts as down, so the monitor stays down until enough locations confirm.
func TestSpecScenario_QuorumRecoveryConfirmation(t *testing.T) {
	runLocationScenarioWithRecovery(t, []string{"a", "b", "c"}, 2, 1, 2, []specStep{
		{loc: "a", failure: true, want: StateDegraded, rules: "S-A2, S-T2"},
		{loc: "b", failure: true, want: StateDown, rules: "S-A1"},
		{loc: "a", failure: false, want: StateDown, rules: "S-T7 (a: pass 1 of 2, still down), S-A1"},
		{loc: "b", failure: false, want: StateDown, rules: "S-T7 (b: pass 1 of 2), S-A1"},
		{loc: "a", failure: false, want: StateDegraded, rules: "S-T7 (a confirmed), S-A2"},
		{loc: "b", failure: true, want: StateDegraded, rules: "S-T7 (b: failure resets), S-A2"},
		{loc: "b", failure: false, want: StateDegraded, rules: "S-T7 (b: pass 1 of 2)"},
		{loc: "b", failure: false, want: StateUp, rules: "S-T7 (b confirmed), S-A4"},
	})
}

// S-A7: a single-location monitor behaves identically to a location-less one
// — the aggregate of one location mirrors that location's temporal state.
func TestSpecScenario_SingleLocationParity(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for seq := 0; seq < 100; seq++ {
		threshold := 1 + rng.Intn(3)
		recovery := 1 + rng.Intn(3)
		global := Snapshot{State: StateUnknown}
		loc := Snapshot{State: StateUnknown}
		for i := 0; i < 20; i++ {
			failure := rng.Intn(2) == 0
			gt := Apply(global, failure, threshold, recovery)
			global = snapshotOf(gt)
			lt := Apply(loc, failure, threshold, recovery)
			loc = snapshotOf(lt)

			c := LocationCounts{Selected: 1}
			switch loc.State {
			case StateDown:
				c.Down++
			case StateSuspect:
				c.Suspect++
			case StateUp:
				c.Up++
			}
			if agg := AggregateLocations(c, 1); agg != global.State {
				t.Fatalf("seq %d step %d: aggregate %s != location-less %s [S-A7]", seq, i, agg, global.State)
			}
		}
	}
}

// S-T1..S-T7 invariants over the whole input space of Apply.
func TestSpecProperty_ApplyInvariants(t *testing.T) {
	states := []State{StateUnknown, StateUp, StateSuspect, StateDown, StateDegraded}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 4000; i++ {
		cur := Snapshot{State: states[rng.Intn(len(states))], ConsecutiveFailures: rng.Intn(11)}
		if cur.State == StateDown {
			cur.ConsecutiveSuccesses = rng.Intn(6)
		}
		failure := rng.Intn(2) == 0
		threshold := rng.Intn(7) - 1 // includes 0 and -1 (clamp to 1)
		eff := threshold
		if eff < 1 {
			eff = 1
		}
		recovery := rng.Intn(7) - 1 // includes 0 and -1 (clamp to 1)
		effR := recovery
		if effR < 1 {
			effR = 1
		}
		got := Apply(cur, failure, threshold, recovery)

		if got.To != StateUp && got.To != StateSuspect && got.To != StateDown {
			t.Fatalf("Apply(%+v, %v, %d).To = %s: machine output outside {up,suspect,down} [S-T5]", cur, failure, threshold, got.To)
		}
		if got.Changed != (got.From != got.To) {
			t.Fatalf("Changed=%v but From=%s To=%s", got.Changed, got.From, got.To)
		}
		if got.OpenedOutage != (got.To == StateDown && got.From != StateDown) {
			t.Fatalf("OpenedOutage=%v for %s→%s [S-T6]", got.OpenedOutage, got.From, got.To)
		}
		if got.ClosedOutage != (got.From == StateDown && got.To == StateUp) {
			t.Fatalf("ClosedOutage=%v for %s→%s [S-T6]", got.ClosedOutage, got.From, got.To)
		}
		if !failure && got.ConsecutiveFailures != 0 {
			t.Fatalf("pass ⇒ CF 0, got %d [S-T1]", got.ConsecutiveFailures)
		}
		if !failure && cur.State != StateDown && (got.To != StateUp || got.ConsecutiveSuccesses != 0) {
			t.Fatalf("pass from %s ⇒ up/CS 0, got %s/%d [S-T1, S-T7]", cur.State, got.To, got.ConsecutiveSuccesses)
		}
		if !failure && cur.State == StateDown {
			if confirmed := cur.ConsecutiveSuccesses+1 >= effR; confirmed {
				if got.To != StateUp || got.ConsecutiveSuccesses != 0 {
					t.Fatalf("confirming pass (CS %d+1 >= R %d) ⇒ up/CS 0, got %s/%d [S-T4, S-T7]",
						cur.ConsecutiveSuccesses, effR, got.To, got.ConsecutiveSuccesses)
				}
			} else if got.To != StateDown || got.ConsecutiveSuccesses != cur.ConsecutiveSuccesses+1 || got.Changed {
				t.Fatalf("unconfirmed pass (CS %d+1 < R %d) ⇒ down/CS+1 unchanged, got %+v [S-T7]",
					cur.ConsecutiveSuccesses, effR, got)
			}
		}
		if got.To != StateDown && got.ConsecutiveSuccesses != 0 {
			t.Fatalf("CS=%d on a %s stream: CS > 0 only while down [S-T7]", got.ConsecutiveSuccesses, got.To)
		}
		if failure && got.ConsecutiveSuccesses != 0 {
			t.Fatalf("fail ⇒ CS 0, got %d [S-T7]", got.ConsecutiveSuccesses)
		}
		if effR == 1 && got.ConsecutiveSuccesses != 0 {
			t.Fatalf("R=1 ⇒ single-sample recovery, got CS %d [S-T7]", got.ConsecutiveSuccesses)
		}
		if failure {
			if got.ConsecutiveFailures != cur.ConsecutiveFailures+1 {
				t.Fatalf("fail ⇒ CF+1, got %d from %d [S-T2]", got.ConsecutiveFailures, cur.ConsecutiveFailures)
			}
			if cur.State == StateDown && got.To != StateDown {
				t.Fatalf("down + fail ⇒ down, got %s [S-T3]", got.To)
			}
			if wantDown := cur.State == StateDown || got.ConsecutiveFailures >= eff; wantDown != (got.To == StateDown) {
				t.Fatalf("fail with CF=%d threshold=%d from %s: To=%s [S-T2]", got.ConsecutiveFailures, eff, cur.State, got.To)
			}
		}
	}
}

// The stream-level shape of S-T1/S-T2/S-T3/S-T7: after any result sequence
// from up, consecutive failures equal the trailing failure run, and the state
// follows from the trailing runs: a failure run reaching the threshold is
// down; otherwise, a stream that was down when its trailing pass run began
// stays down until that run reaches the recovery threshold, with CS equal to
// the run; anything else is up, or suspect under a short failure run.
func TestSpecProperty_TrailingFailureRun(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 1000; i++ {
		threshold := 1 + rng.Intn(4)
		recovery := 1 + rng.Intn(4)
		snap := Snapshot{State: StateUp}
		failRun, passRun := 0, 0
		// Whether the stream was down when its trailing pass/failure run began.
		downAtPassRunStart, downAtFailRunStart := false, false
		for j, n := 0, 1+rng.Intn(40); j < n; j++ {
			failure := rng.Intn(2) == 0
			if !failure && passRun == 0 {
				downAtPassRunStart = snap.State == StateDown
			}
			if failure && failRun == 0 {
				downAtFailRunStart = snap.State == StateDown
			}
			tr := Apply(snap, failure, threshold, recovery)
			snap = snapshotOf(tr)
			if failure {
				failRun++
				passRun = 0
			} else {
				failRun = 0
				passRun++
			}
		}
		if snap.ConsecutiveFailures != failRun {
			t.Fatalf("CF=%d, trailing failure run=%d [S-T1, S-T2]", snap.ConsecutiveFailures, failRun)
		}
		want, wantCS := StateUp, 0
		switch {
		case failRun >= threshold:
			want = StateDown
		case failRun > 0:
			// A short failure run is suspect, unless it began on a down
			// stream: down stays down (S-T3).
			want = StateSuspect
			if downAtFailRunStart {
				want = StateDown
			}
		case downAtPassRunStart && passRun < recovery:
			want, wantCS = StateDown, passRun
		}
		if snap.State != want || snap.ConsecutiveSuccesses != wantCS {
			t.Fatalf("state=%s CS=%d, want %s CS=%d (failRun=%d passRun=%d threshold=%d recovery=%d) [S-T2, S-T3, S-T7]",
				snap.State, snap.ConsecutiveSuccesses, want, wantCS, failRun, passRun, threshold, recovery)
		}
	}
}

// S-T7 with the default recovery threshold of 1 is exactly the pre-S-T7
// machine: every pass is up at once and no success is ever counted, so
// existing monitors behave as before.
func TestSpecProperty_RecoveryThresholdOneIsSingleSample(t *testing.T) {
	rng := rand.New(rand.NewSource(1337))
	for i := 0; i < 500; i++ {
		threshold := 1 + rng.Intn(4)
		snap := Snapshot{State: StateUnknown}
		for j := 0; j < 30; j++ {
			failure := rng.Intn(2) == 0
			tr := Apply(snap, failure, threshold, 1)
			if !failure && (tr.To != StateUp || tr.ClosedOutage != (snap.State == StateDown)) {
				t.Fatalf("R=1 pass from %s ⇒ %+v, want up at once [S-T4, S-T7]", snap.State, tr)
			}
			if tr.ConsecutiveSuccesses != 0 {
				t.Fatalf("R=1 counted a success: %+v [S-T7]", tr)
			}
			snap = snapshotOf(tr)
		}
	}
}

// S-A1/S-A2/S-A6 invariants over the aggregation input space, plus severity
// monotonicity: turning an up location into a down one never makes the
// monitor look healthier.
func TestSpecProperty_AggregateInvariants(t *testing.T) {
	severity := map[State]int{StateUnknown: 0, StateUp: 1, StateSuspect: 2, StateDegraded: 3, StateDown: 4}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		c := LocationCounts{Down: rng.Intn(6), Suspect: rng.Intn(6), Up: rng.Intn(6)}
		c.Selected = c.Down + c.Suspect + c.Up + rng.Intn(4)
		quorum := rng.Intn(9) - 1 // includes 0 and -1 (clamp)
		eff := quorum
		if eff < 1 {
			eff = 1
		}
		if c.Selected > 0 && eff > c.Selected {
			eff = c.Selected
		}
		got := AggregateLocations(c, quorum)

		if _, ok := severity[got]; !ok {
			t.Fatalf("AggregateLocations(%+v, %d) = %q: not a State", c, quorum, got)
		}
		if (got == StateDown) != (c.Down >= eff) {
			t.Fatalf("%+v quorum=%d (eff %d): got %s, down iff Down>=eff [S-A1]", c, quorum, eff, got)
		}
		if (got == StateDegraded) != (c.Down > 0 && c.Down < eff) {
			t.Fatalf("%+v quorum=%d (eff %d): got %s, degraded iff 0<Down<eff [S-A2]", c, quorum, eff, got)
		}
		if AggregateLocations(c, eff) != got {
			t.Fatalf("clamping not equivalent: quorum %d vs eff %d on %+v [S-A6]", quorum, eff, c)
		}
		if c.Up > 0 {
			worse := LocationCounts{Down: c.Down + 1, Suspect: c.Suspect, Up: c.Up - 1, Selected: c.Selected}
			if severity[AggregateLocations(worse, quorum)] < severity[got] {
				t.Fatalf("up→down conversion improved state: %+v→%s vs %+v→%s [S-A1, S-A2]",
					c, got, worse, AggregateLocations(worse, quorum))
			}
		}
	}
}

// S-F6: a location is offline when it is enabled, live, was seen at least
// once, and has been silent for the tenant's threshold (floor 60s). Never-seen
// and disabled locations are never offline; the connected badge is a
// separate, shorter window.
func TestSpecLocationOffline(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { ts := now.Add(-d); return &ts }
	cases := []struct {
		name         string
		enabled      bool
		deleted      bool
		lastSeen     *time.Time
		offlineAfter int
		want         bool
	}{
		{"never_seen_is_not_offline", true, false, nil, 180, false},
		{"recent_heartbeat", true, false, ago(30 * time.Second), 180, false},
		{"just_under_threshold", true, false, ago(179 * time.Second), 180, false},
		{"at_threshold", true, false, ago(180 * time.Second), 180, true},
		{"long_silent", true, false, ago(time.Hour), 180, true},
		{"disabled_is_not_offline", false, false, ago(time.Hour), 180, false},
		{"deleted_is_not_offline", true, true, ago(time.Hour), 180, false},
		{"threshold_floor_60s", true, false, ago(45 * time.Second), 10, false},
		{"threshold_floor_reached", true, false, ago(60 * time.Second), 10, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LocationOffline(tc.enabled, tc.deleted, tc.lastSeen, tc.offlineAfter, now); got != tc.want {
				t.Fatalf("LocationOffline = %v, want %v [rules S-F6]", got, tc.want)
			}
		})
	}
	// Connected and offline are distinct windows: between them a location
	// is neither (disconnected badge, no alert yet).
	if LocationConnected(ago(90*time.Second), now) || LocationOffline(true, false, ago(90*time.Second), DefaultLocationOfflineAfterSeconds, now) {
		t.Fatal("90s silent must read disconnected but not yet offline [rules S-F6]")
	}
}

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
	perLoc := map[string]Snapshot{}
	for i, step := range steps {
		snap, reported := perLoc[step.loc]
		if !reported {
			snap = Snapshot{State: StateUnknown}
		}
		tr := Apply(snap, step.failure, threshold)
		perLoc[step.loc] = Snapshot{State: tr.To, ConsecutiveFailures: tr.ConsecutiveFailures}

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
		got := Apply(snap, step.failure, 2)
		if got != step.want {
			t.Fatalf("step %d: Apply() = %+v, want %+v [rules %s]", i, got, step.want, step.rules)
		}
		snap = Snapshot{State: got.To, ConsecutiveFailures: got.ConsecutiveFailures}
	}
}

// S-A7: a single-location monitor behaves identically to a location-less one
// — the aggregate of one location mirrors that location's temporal state.
func TestSpecScenario_SingleLocationParity(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for seq := 0; seq < 100; seq++ {
		threshold := 1 + rng.Intn(3)
		global := Snapshot{State: StateUnknown}
		loc := Snapshot{State: StateUnknown}
		for i := 0; i < 20; i++ {
			failure := rng.Intn(2) == 0
			gt := Apply(global, failure, threshold)
			global = Snapshot{State: gt.To, ConsecutiveFailures: gt.ConsecutiveFailures}
			lt := Apply(loc, failure, threshold)
			loc = Snapshot{State: lt.To, ConsecutiveFailures: lt.ConsecutiveFailures}

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

// S-T1..S-T6 invariants over the whole input space of Apply.
func TestSpecProperty_ApplyInvariants(t *testing.T) {
	states := []State{StateUnknown, StateUp, StateSuspect, StateDown, StateDegraded}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 2000; i++ {
		cur := Snapshot{State: states[rng.Intn(len(states))], ConsecutiveFailures: rng.Intn(11)}
		failure := rng.Intn(2) == 0
		threshold := rng.Intn(7) - 1 // includes 0 and -1 (clamp to 1)
		eff := threshold
		if eff < 1 {
			eff = 1
		}
		got := Apply(cur, failure, threshold)

		if got.To != StateUp && got.To != StateSuspect && got.To != StateDown {
			t.Fatalf("Apply(%+v, %v, %d).To = %s: machine output outside {up,suspect,down} [S-T5]", cur, failure, threshold, got.To)
		}
		if got.Changed != (got.From != got.To) {
			t.Fatalf("Changed=%v but From=%s To=%s", got.Changed, got.From, got.To)
		}
		if got.OpenedOutage != (got.To == StateDown && got.From != StateDown) {
			t.Fatalf("OpenedOutage=%v for %s→%s [S-T6]", got.OpenedOutage, got.From, got.To)
		}
		if got.ClosedOutage != (got.From == StateDown && !failure) {
			t.Fatalf("ClosedOutage=%v for %s failure=%v [S-T6]", got.ClosedOutage, got.From, failure)
		}
		if !failure && (got.To != StateUp || got.ConsecutiveFailures != 0) {
			t.Fatalf("pass ⇒ up/0, got %s/%d [S-T1]", got.To, got.ConsecutiveFailures)
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

// The stream-level shape of S-T1/S-T2/S-T3: after any result sequence from
// up, consecutive failures equal the trailing failure run, and the state is
// determined by that run against the threshold.
func TestSpecProperty_TrailingFailureRun(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 500; i++ {
		threshold := 1 + rng.Intn(4)
		snap := Snapshot{State: StateUp}
		run := 0
		for j, n := 0, 1+rng.Intn(30); j < n; j++ {
			failure := rng.Intn(2) == 0
			tr := Apply(snap, failure, threshold)
			snap = Snapshot{State: tr.To, ConsecutiveFailures: tr.ConsecutiveFailures}
			if failure {
				run++
			} else {
				run = 0
			}
		}
		if snap.ConsecutiveFailures != run {
			t.Fatalf("CF=%d, trailing run=%d [S-T1, S-T2]", snap.ConsecutiveFailures, run)
		}
		want := StateUp
		if run >= threshold {
			want = StateDown
		} else if run > 0 {
			want = StateSuspect
		}
		if snap.State != want {
			t.Fatalf("state=%s, want %s (run=%d threshold=%d) [S-T2, S-T3]", snap.State, want, run, threshold)
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

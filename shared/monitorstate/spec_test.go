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
	runRecoveryLocationScenario(t, selected, quorum, threshold, 1, steps)
}

// runRecoveryLocationScenario is runLocationScenario with a recovery
// threshold (S-T7), which every location's machine applies before quorum.
func runRecoveryLocationScenario(t *testing.T, selected []string, quorum, threshold, recovery int, steps []specStep) {
	t.Helper()
	perLoc := map[string]Snapshot{}
	for i, step := range steps {
		snap, reported := perLoc[step.loc]
		if !reported {
			snap = Snapshot{State: StateUnknown}
		}
		tr := Apply(snap, step.failure, threshold, recovery)
		perLoc[step.loc] = Snapshot{State: tr.To, ConsecutiveFailures: tr.ConsecutiveFailures, ConsecutiveSuccesses: tr.ConsecutiveSuccesses}

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
		snap = Snapshot{State: got.To, ConsecutiveFailures: got.ConsecutiveFailures}
	}
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
			global = Snapshot{State: gt.To, ConsecutiveFailures: gt.ConsecutiveFailures, ConsecutiveSuccesses: gt.ConsecutiveSuccesses}
			lt := Apply(loc, failure, threshold, recovery)
			loc = Snapshot{State: lt.To, ConsecutiveFailures: lt.ConsecutiveFailures, ConsecutiveSuccesses: lt.ConsecutiveSuccesses}

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
			var downSuccesses []int
			if loc.State == StateDown {
				downSuccesses = []int{loc.ConsecutiveSuccesses}
			}
			if p := RecoveryProgress(downSuccesses, 1, 1); p != global.ConsecutiveSuccesses {
				t.Fatalf("seq %d step %d: recovery progress %d != location-less %d [S-A7, S-T7]", seq, i, p, global.ConsecutiveSuccesses)
			}
		}
	}
}

// S-T1..S-T7 invariants over the whole input space of Apply.
func TestSpecProperty_ApplyInvariants(t *testing.T) {
	states := []State{StateUnknown, StateUp, StateSuspect, StateDown, StateDegraded}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 2000; i++ {
		cur := Snapshot{State: states[rng.Intn(len(states))], ConsecutiveFailures: rng.Intn(11)}
		if cur.State == StateDown {
			cur.ConsecutiveSuccesses = rng.Intn(11)
		}
		if cur.State == StateUnknown || cur.State == StateSuspect || cur.State == StateDown {
			cur.RecoveryPending = rng.Intn(2) == 0
		}
		failure := rng.Intn(2) == 0
		threshold := rng.Intn(7) - 1 // includes 0 and -1 (clamp to 1)
		eff := threshold
		if eff < 1 {
			eff = 1
		}
		recovery := rng.Intn(7) - 1 // includes 0 and -1 (clamp to 1)
		effRecovery := recovery
		if effRecovery < 1 {
			effRecovery = 1
		}
		// S-T7: a pass leaves down (or the confirmation a silent down stream
		// owes) only once it completes the confirmation.
		counted := 0
		if cur.State == StateDown {
			counted = cur.ConsecutiveSuccesses
		}
		owes := cur.State == StateDown || cur.RecoveryPending
		confirming := owes && !failure && counted+1 < effRecovery
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
		if got.ClosedOutage != (got.From == StateDown && !failure && !confirming) {
			t.Fatalf("ClosedOutage=%v for %s failure=%v confirming=%v [S-T6, S-T7]", got.ClosedOutage, got.From, failure, confirming)
		}
		if confirming && (got.To != StateDown || got.ConsecutiveFailures != 0 || got.ConsecutiveSuccesses != counted+1) {
			t.Fatalf("unconfirmed pass owing a confirmation ⇒ down/CF 0/CS+1, got %s/%d/%d from %+v [S-T7]",
				got.To, got.ConsecutiveFailures, got.ConsecutiveSuccesses, cur)
		}
		// The owed confirmation is kept by failures short of down and by the
		// passes that count it; a failure that makes the stream down, and the
		// confirmation itself, clear it.
		if want := cur.RecoveryPending && (failure != (got.To == StateDown)); got.RecoveryPending != want {
			t.Fatalf("RecoveryPending=%v from %+v failure=%v to %s, want %v [S-T7]",
				got.RecoveryPending, cur, failure, got.To, want)
		}
		if !failure && !confirming && (got.To != StateUp || got.ConsecutiveFailures != 0 || got.ConsecutiveSuccesses != 0) {
			t.Fatalf("pass ⇒ up/0/0, got %s/%d/%d [S-T1, S-T4, S-T7]", got.To, got.ConsecutiveFailures, got.ConsecutiveSuccesses)
		}
		if got.ConsecutiveSuccesses != 0 && got.To != StateDown {
			t.Fatalf("CS=%d outside down (%s) [S-T7]", got.ConsecutiveSuccesses, got.To)
		}
		if failure {
			if got.ConsecutiveSuccesses != 0 {
				t.Fatalf("fail ⇒ CS reset, got %d [S-T7]", got.ConsecutiveSuccesses)
			}
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
			tr := Apply(snap, failure, threshold, 1)
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

// S-T7: with a recovery threshold of 3, a down stream needs three passes in a
// row to be up again. Until then it stays down (an outage, so downtime), and a
// failure in between restarts the count.
func TestSpecScenario_RecoveryConfirmation(t *testing.T) {
	steps := []struct {
		failure bool
		want    Transition
		rules   string
	}{
		{true, Transition{From: StateUnknown, To: StateSuspect, ConsecutiveFailures: 1, Changed: true}, "S-T2"},
		{true, Transition{From: StateSuspect, To: StateDown, ConsecutiveFailures: 2, Changed: true, OpenedOutage: true}, "S-T2, S-T6"},
		{false, Transition{From: StateDown, To: StateDown, ConsecutiveSuccesses: 1}, "S-T7 (1 of 3, still down)"},
		{false, Transition{From: StateDown, To: StateDown, ConsecutiveSuccesses: 2}, "S-T7 (2 of 3, still down)"},
		{true, Transition{From: StateDown, To: StateDown, ConsecutiveFailures: 1}, "S-T3, S-T7 (a failure resets the count)"},
		{false, Transition{From: StateDown, To: StateDown, ConsecutiveSuccesses: 1}, "S-T7 (counting again from 1)"},
		{false, Transition{From: StateDown, To: StateDown, ConsecutiveSuccesses: 2}, "S-T7"},
		{false, Transition{From: StateDown, To: StateUp, Changed: true, ClosedOutage: true}, "S-T7, S-T6 (confirmed: the outage closes here)"},
		{true, Transition{From: StateUp, To: StateSuspect, ConsecutiveFailures: 1, Changed: true}, "S-T2"},
		{false, Transition{From: StateSuspect, To: StateUp, Changed: true}, "S-T1, S-T7 (only down needs confirming)"},
	}
	snap := Snapshot{State: StateUnknown}
	for i, step := range steps {
		got := Apply(snap, step.failure, 2, 3)
		if got != step.want {
			t.Fatalf("step %d: Apply() = %+v, want %+v [rules %s]", i, got, step.want, step.rules)
		}
		snap = Snapshot{State: got.To, ConsecutiveFailures: got.ConsecutiveFailures, ConsecutiveSuccesses: got.ConsecutiveSuccesses}
	}
}

// S-T7: a threshold of 1 is today's single-sample recovery, whatever count a
// snapshot carries; and only down needs confirming — the unknown a pause,
// a location change or staleness leaves behind (S-P2, S-F2) is up on the
// first pass at any threshold.
func TestSpecScenario_RecoveryThresholdOneAndEntryStates(t *testing.T) {
	for _, recovery := range []int{1, 0, -1} {
		got := Apply(Snapshot{State: StateDown, ConsecutiveFailures: 4}, false, 2, recovery)
		want := Transition{From: StateDown, To: StateUp, Changed: true, ClosedOutage: true}
		if got != want {
			t.Fatalf("recovery %d: Apply() = %+v, want %+v [S-T4, S-T7]", recovery, got, want)
		}
	}
	for _, from := range []State{StateUnknown, StateSuspect, StateUp} {
		got := Apply(Snapshot{State: from}, false, 2, 5)
		if got.To != StateUp || got.ConsecutiveSuccesses != 0 || got.ClosedOutage {
			t.Fatalf("pass from %s with recovery 5 = %+v, want up at once [S-T7]", from, got)
		}
	}
}

// S-T7: the threshold in force when a pass arrives decides. Passes already
// counted survive a change: raising it asks for more, lowering it to or below
// the count confirms on the next pass (never without new evidence).
func TestSpecScenario_RecoveryThresholdChangedMidConfirmation(t *testing.T) {
	recovering := Snapshot{State: StateDown, ConsecutiveSuccesses: 2}
	if got := Apply(recovering, false, 2, 5); got.To != StateDown || got.ConsecutiveSuccesses != 3 {
		t.Fatalf("raised to 5 at 2 passes: %+v, want down at 3 [S-T7]", got)
	}
	if got := Apply(recovering, false, 2, 3); got.To != StateUp || !got.ClosedOutage {
		t.Fatalf("at 2 passes, threshold 3: %+v, want up [S-T7]", got)
	}
	if got := Apply(recovering, false, 2, 2); got.To != StateUp || !got.ClosedOutage {
		t.Fatalf("lowered to 2 at 2 passes: %+v, want up on the next pass [S-T7]", got)
	}
	if got := Apply(recovering, true, 2, 1); got.To != StateDown || got.ConsecutiveSuccesses != 0 {
		t.Fatalf("lowered to 1, then a failure: %+v, want down with the count reset [S-T3, S-T7]", got)
	}
}

// S-T7 per location before quorum (S-A1/S-A2): each location confirms its own
// recovery, and the aggregate leaves down only once enough of them have.
func TestSpecScenario_QuorumRecoveryConfirmation(t *testing.T) {
	runRecoveryLocationScenario(t, []string{"a", "b", "c"}, 2, 1, 2, []specStep{
		{loc: "a", failure: true, want: StateDegraded, rules: "S-A2, S-T2"},
		{loc: "b", failure: true, want: StateDown, rules: "S-A1"},
		{loc: "c", failure: false, want: StateDown, rules: "S-A1"},
		{loc: "a", failure: false, want: StateDown, rules: "S-T7 (a: 1 of 2, still down)"},
		{loc: "b", failure: false, want: StateDown, rules: "S-T7 (b: 1 of 2, still down)"},
		{loc: "a", failure: false, want: StateDegraded, rules: "S-T7, S-A2 (a confirmed; b alone is below quorum)"},
		{loc: "b", failure: true, want: StateDegraded, rules: "S-T3, S-T7 (b's count resets)"},
		{loc: "b", failure: false, want: StateDegraded, rules: "S-T7"},
		{loc: "b", failure: false, want: StateUp, rules: "S-T7, S-A4"},
	})
}

// S-T7 at the monitor level of a location-bound monitor: consecutive_successes
// is the progress of the location whose confirmation takes the aggregate out
// of down — the (down − quorum + 1)-th most advanced down location.
func TestSpecRecoveryProgress(t *testing.T) {
	cases := []struct {
		name          string
		downSuccesses []int
		quorum        int
		selected      int
		want          int
	}{
		{"not_down_below_quorum", []int{2}, 2, 3, 0},
		{"no_down_locations", nil, 1, 3, 0},
		{"quorum_one_waits_for_every_down_location", []int{2, 0, 1}, 1, 3, 0},
		{"exactly_quorum_needs_one", []int{0, 2}, 2, 3, 2},
		{"one_over_quorum_needs_two", []int{1, 3, 2}, 2, 3, 2},
		{"quorum_clamped_to_selected", []int{1, 2}, 5, 2, 2},
		{"location_less_parity", []int{1}, 1, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RecoveryProgress(tc.downSuccesses, tc.quorum, tc.selected); got != tc.want {
				t.Fatalf("RecoveryProgress(%v, %d, %d) = %d, want %d [S-T7, S-A6]",
					tc.downSuccesses, tc.quorum, tc.selected, got, tc.want)
			}
		})
	}
	in := []int{3, 1, 2}
	RecoveryProgress(in, 1, 3)
	if in[0] != 3 || in[1] != 1 || in[2] != 2 {
		t.Fatalf("RecoveryProgress reordered its input: %v", in)
	}
}

// The stream-level shape of S-T7 against an independent model: a stream is
// down from the failure that reaches the threshold (or any failure while
// down) until a run of recovery-threshold passes completes; while down its
// consecutive successes equal the trailing pass run.
func TestSpecProperty_RecoveryConfirmationRuns(t *testing.T) {
	rng := rand.New(rand.NewSource(77))
	for i := 0; i < 500; i++ {
		threshold := 1 + rng.Intn(4)
		recovery := 1 + rng.Intn(4)
		snap := Snapshot{State: StateUp}
		down := false
		fails, passes := 0, 0
		for j, n := 0, 1+rng.Intn(40); j < n; j++ {
			failure := rng.Intn(2) == 0
			tr := Apply(snap, failure, threshold, recovery)
			snap = Snapshot{State: tr.To, ConsecutiveFailures: tr.ConsecutiveFailures, ConsecutiveSuccesses: tr.ConsecutiveSuccesses}
			if failure {
				fails, passes = fails+1, 0
				if fails >= threshold {
					down = true
				}
			} else {
				fails, passes = 0, passes+1
				if down && passes >= recovery {
					down = false
				}
			}
			want, wantCS := StateUp, 0
			switch {
			case down:
				want, wantCS = StateDown, passes
			case fails > 0:
				want = StateSuspect
			}
			if snap.State != want || snap.ConsecutiveSuccesses != wantCS || snap.ConsecutiveFailures != fails {
				t.Fatalf("seq %d step %d (threshold %d, recovery %d): got %s/CF %d/CS %d, want %s/CF %d/CS %d [S-T2, S-T3, S-T7]",
					i, j, threshold, recovery, snap.State, snap.ConsecutiveFailures, snap.ConsecutiveSuccesses, want, fails, wantCS)
			}
		}
	}
}

// applySingleSample is the temporal machine as it was before S-T7, frozen
// here so recovery_threshold 1 can be held to it.
func applySingleSample(current Snapshot, resultIsFailure bool, threshold int) Transition {
	if threshold < 1 {
		threshold = 1
	}
	t := Transition{From: current.State}
	if !resultIsFailure {
		t.To = StateUp
		t.Changed = current.State != StateUp
		t.ClosedOutage = current.State == StateDown
		return t
	}
	t.ConsecutiveFailures = current.ConsecutiveFailures + 1
	if current.State == StateDown || t.ConsecutiveFailures >= threshold {
		t.To = StateDown
	} else {
		t.To = StateSuspect
	}
	t.Changed = t.To != current.State
	t.OpenedOutage = t.To == StateDown && current.State != StateDown
	return t
}

// S-T7: at recovery_threshold 1 every observable output — state, failure
// count, outage edges — is exactly the pre-S-T7 machine's, whatever the
// snapshot carries (including the memory of a silent outage), and no pass is
// ever counted. Only the RecoveryPending bookkeeping may differ.
func TestSpecProperty_RecoveryThresholdOneIsSingleSample(t *testing.T) {
	states := []State{StateUnknown, StateUp, StateSuspect, StateDown, StateDegraded}
	rng := rand.New(rand.NewSource(1001))
	for i := 0; i < 3000; i++ {
		cur := Snapshot{State: states[rng.Intn(len(states))], ConsecutiveFailures: rng.Intn(11), RecoveryPending: rng.Intn(2) == 0}
		if cur.State == StateDown {
			cur.ConsecutiveSuccesses = rng.Intn(11)
		}
		failure := rng.Intn(2) == 0
		threshold := rng.Intn(7) - 1
		got := Apply(cur, failure, threshold, 1)
		got.RecoveryPending = false
		if want := applySingleSample(cur, failure, threshold); got != want {
			t.Fatalf("Apply(%+v, %v, %d, 1) = %+v, want the single-sample %+v [S-T7]", cur, failure, threshold, got, want)
		}
	}
}

// S-T7 with S-F2: a stream that went silent while down (confirmed or already
// recovering) re-enters from unknown owing the whole confirmation: its first
// pass after silence is down with one pass counted, never up. A stream that
// was up keeps today's behaviour.
func TestSpecScenario_SilentRecoveryStillConfirms(t *testing.T) {
	for _, before := range []Snapshot{
		{State: StateDown, ConsecutiveFailures: 4},
		{State: StateDown, ConsecutiveSuccesses: 2},
	} {
		silent := AfterSilence(before)
		if silent != (Snapshot{State: StateUnknown, RecoveryPending: true}) {
			t.Fatalf("AfterSilence(%+v) = %+v, want unknown owing a confirmation [S-F2, S-T7]", before, silent)
		}
		first := Apply(silent, false, 2, 3)
		want := Transition{From: StateUnknown, To: StateDown, ConsecutiveSuccesses: 1, RecoveryPending: true, Changed: true, OpenedOutage: true}
		if first != want {
			t.Fatalf("first pass after silence from %+v = %+v, want %+v [S-T7]", before, first, want)
		}
		second := Apply(Snapshot{State: first.To, ConsecutiveSuccesses: first.ConsecutiveSuccesses}, false, 2, 3)
		third := Apply(Snapshot{State: second.To, ConsecutiveSuccesses: second.ConsecutiveSuccesses}, false, 2, 3)
		if second.To != StateDown || second.ConsecutiveSuccesses != 2 || third.To != StateUp || !third.ClosedOutage {
			t.Fatalf("after silence: %+v then %+v, want down at 2 then up [S-T7]", second, third)
		}
	}

	// A failure first keeps the debt through suspect; the next pass counts 1.
	silent := AfterSilence(Snapshot{State: StateDown, ConsecutiveSuccesses: 1})
	fail := Apply(silent, true, 2, 3)
	if fail.To != StateSuspect || !fail.RecoveryPending {
		t.Fatalf("failure after silence = %+v, want suspect still owing [S-T2, S-T7]", fail)
	}
	pass := Apply(Snapshot{State: fail.To, ConsecutiveFailures: fail.ConsecutiveFailures, RecoveryPending: fail.RecoveryPending}, false, 2, 3)
	if pass.To != StateDown || pass.ConsecutiveSuccesses != 1 || pass.ConsecutiveFailures != 0 {
		t.Fatalf("pass after a post-silence failure = %+v, want down at 1 [S-T7]", pass)
	}
	// Two failures settle it as a fresh outage; down needs confirming anyway.
	down := Apply(Snapshot{State: fail.To, ConsecutiveFailures: fail.ConsecutiveFailures, RecoveryPending: true}, true, 2, 3)
	if down.To != StateDown || down.RecoveryPending {
		t.Fatalf("second failure after silence = %+v, want down, the debt folded into it [S-T7]", down)
	}

	// Up before the silence: unknown owes nothing, the first pass is up.
	if up := AfterSilence(Snapshot{State: StateUp}); up.RecoveryPending || Apply(up, false, 2, 3).To != StateUp {
		t.Fatalf("a stream that was up re-enters as today [S-F2]")
	}
	// At threshold 1 the debt changes nothing (single-sample recovery).
	if got := Apply(silent, false, 2, 1); got.To != StateUp {
		t.Fatalf("threshold 1 after silence = %+v, want up [S-T7]", got)
	}
}

// S-T7 per location: a location added to a monitor that is down, or one that
// was down when its evidence went stale, owes a confirmation. While it counts
// it does not vote (LoadFreshLocationCounts leaves pending rows out): its
// passes neither drag a healthy aggregate down nor lift a down one before
// they confirm. A failure makes it an ordinary down vote.
func TestSpecScenario_OwingLocationDoesNotVoteUntilConfirmed(t *testing.T) {
	const quorum, threshold, recovery = 1, 2, 3
	// counts mirrors the counts query: pending down rows do not vote.
	counts := func(locs ...Snapshot) (State, int) {
		c := LocationCounts{Selected: len(locs)}
		var down []int
		for _, s := range locs {
			switch {
			case s.State == StateDown && !s.RecoveryPending:
				c.Down++
				down = append(down, s.ConsecutiveSuccesses)
			case s.State == StateSuspect:
				c.Suspect++
			case s.State == StateUp:
				c.Up++
			}
		}
		return AggregateLocations(c, quorum), RecoveryProgress(down, quorum, len(locs))
	}
	step := func(s Snapshot, failure bool) Snapshot {
		tr := Apply(s, failure, threshold, recovery)
		return Snapshot{State: tr.To, ConsecutiveFailures: tr.ConsecutiveFailures,
			ConsecutiveSuccesses: tr.ConsecutiveSuccesses, RecoveryPending: tr.RecoveryPending}
	}

	// A location that comes back after silence beside a healthy one: the
	// aggregate stays up through its confirmation.
	healthy := Snapshot{State: StateUp}
	returning := AfterSilence(Snapshot{State: StateDown, ConsecutiveFailures: 5})
	for pass := 1; pass <= recovery; pass++ {
		returning = step(returning, false)
		if state, _ := counts(healthy, returning); state != StateUp {
			t.Fatalf("pass %d of a returning location dragged the aggregate to %s [S-T7, S-A10]", pass, state)
		}
	}
	if returning.State != StateUp || returning.RecoveryPending {
		t.Fatalf("returning location after %d passes = %+v, want up [S-T7]", recovery, returning)
	}

	// A location added to a down monitor: it cannot lift it on its first pass.
	added := Snapshot{State: StateUnknown, RecoveryPending: true}
	for pass := 1; pass < recovery; pass++ {
		added = step(added, false)
		if state, _ := counts(added); state == StateUp || added.State != StateDown || added.ConsecutiveSuccesses != pass {
			t.Fatalf("pass %d at the added location: %+v, aggregate %s; want counting, not up [S-T7]", pass, added, state)
		}
	}
	if added = step(added, false); added.State != StateUp {
		t.Fatalf("added location after %d passes = %+v, want up [S-T7]", recovery, added)
	}

	// A failure turns the debt into an ordinary down vote.
	failing := step(step(Snapshot{State: StateUnknown, RecoveryPending: true}, false), true)
	if state, _ := counts(failing); failing.RecoveryPending || state != StateDown {
		t.Fatalf("owing location after a failure = %+v (aggregate %s), want an ordinary down vote [S-T3, S-T7]", failing, state)
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

// S-F6: a location is offline when it is enabled, not deleted, was seen at
// least once, and has been silent for LocationOfflineAfter (180s), and
// enabled for at least as long. Never-seen, disabled, deleted and freshly
// re-enabled locations are never offline. Connected is a separate, shorter
// window: between the two a location is disconnected but not yet offline.
func TestSpecLocationOffline(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { ts := now.Add(-d); return &ts }
	longEnabled := now.Add(-30 * 24 * time.Hour)
	cases := []struct {
		name      string
		enabled   bool
		deleted   bool
		lastSeen  *time.Time
		enabledAt time.Time
		want      bool
	}{
		{"never_seen_is_not_offline", true, false, nil, longEnabled, false},
		{"recent_heartbeat", true, false, ago(15 * time.Second), longEnabled, false},
		{"just_under_threshold", true, false, ago(179 * time.Second), longEnabled, false},
		{"at_threshold", true, false, ago(180 * time.Second), longEnabled, true},
		{"long_silent", true, false, ago(30 * 24 * time.Hour), longEnabled, true},
		{"disabled_is_not_offline", false, false, ago(time.Hour), longEnabled, false},
		{"deleted_is_not_offline", true, true, ago(time.Hour), longEnabled, false},
		{"re_enabled_just_now_is_not_offline", true, false, ago(time.Hour), *ago(10 * time.Second), false},
		{"re_enabled_a_threshold_ago", true, false, ago(time.Hour), *ago(180 * time.Second), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LocationOffline(tc.enabled, tc.deleted, tc.lastSeen, tc.enabledAt, now); got != tc.want {
				t.Fatalf("LocationOffline = %v, want %v [S-F6]", got, tc.want)
			}
		})
	}
	if !LocationConnected(ago(59*time.Second), now) || LocationConnected(ago(60*time.Second), now) {
		t.Fatal("connected is a one-minute window [S-F6]")
	}
	if !LocationDisconnected(true, false, ago(60*time.Second), now) || LocationDisconnected(true, false, nil, now) ||
		LocationDisconnected(false, false, ago(time.Hour), now) {
		t.Fatal("disconnected is seen, enabled, live and past the connected window [S-F6]")
	}
	if LocationConnected(ago(90*time.Second), now) || LocationOffline(true, false, ago(90*time.Second), longEnabled, now) {
		t.Fatal("90s silent must read disconnected but not yet offline [S-F6]")
	}
	if LocationOfflineAfter <= LocationConnectedWindow {
		t.Fatal("offline must come after the connected window ends, or the mesh hold starts late [S-F6]")
	}
}

// S-T7, S-A9: the fast recheck (suspect or recovering) is withheld from the
// monitors whose schedule floor is a rate limit, and only from those.
func TestSpecFastRecheckExempt(t *testing.T) {
	const sipCall = `{"host":"trunk.example.com","method":"invite","call":{"number":"100"}}`
	cases := []struct {
		typ, config string
		exempt      bool
	}{
		{"sip", sipCall, true},
		{"sip", `{"host":"trunk.example.com","method":"options"}`, false},
		{"domain", `{"domain":"example.com"}`, true},
		{"push", `{}`, true},
		{"agent", `{}`, true},
		{"group", `{}`, true},
		{"http", `{"url":"https://example.com"}`, false},
		{"ping", `{"host":"example.com"}`, false},
		{"synthetic_browser", `{"start_url":"https://example.com"}`, false},
		{"synthetic_api", `{}`, false},
		{"prometheus", `{}`, false},
	}
	for _, tc := range cases {
		if got := FastRecheckExempt(tc.typ, []byte(tc.config)); got != tc.exempt {
			t.Errorf("FastRecheckExempt(%s, %s) = %v, want %v [S-T7, S-A9]", tc.typ, tc.config, got, tc.exempt)
		}
	}
}

// Package monitorstate holds the pure monitor state-machine transition logic
// shared by worker (writes), scheduler (cadence), and alerter (lifecycle).
package monitorstate

import "sort"

type State string

const (
	StateUnknown State = "unknown"
	StateUp      State = "up"
	StateSuspect State = "suspect"
	StateDown    State = "down"
	// StateDegraded is monitor-level only (never per-location): some, but
	// fewer than location_quorum, of a multi-location monitor's locations are
	// down. Not an alerting state; unlike suspect it does not trigger the
	// fast recheck — a partially-down monitor can stay degraded indefinitely.
	StateDegraded State = "degraded"
)

// Recovery threshold bounds (S-T7): passing checks in a row a down stream
// needs before it is up again. 1 is single-sample recovery; the column CHECK
// (migration 000111) and the API validator enforce the same range.
const (
	MinRecoveryThreshold = 1
	MaxRecoveryThreshold = 10
)

// Snapshot is a monitor's persisted state.
type Snapshot struct {
	State               State
	ConsecutiveFailures int
	// ConsecutiveSuccesses counts passing evidence toward confirming a
	// recovery (S-T7). It is non-zero only while the stream is down: any
	// failure, and leaving down, reset it.
	ConsecutiveSuccesses int
	// RecoveryPending remembers that the stream was down when its evidence
	// went stale (or, for a location, that it was added to a down monitor):
	// it owes a recovery confirmation before it is up again (S-T7). It holds
	// through unknown and suspect, and through down while that owed
	// confirmation is counted with no failure since; a failure that makes the
	// stream down, or the confirmation, clears it. A location in that state
	// does not vote in the quorum (LoadFreshLocationCounts).
	RecoveryPending bool
}

// Transition is the outcome of applying one check result.
type Transition struct {
	From                 State
	To                   State
	ConsecutiveFailures  int
	ConsecutiveSuccesses int  // passes toward confirming recovery; 0 unless To is down
	RecoveryPending      bool // still owes the confirmation it owed before (see Snapshot)
	Changed              bool // state value changed
	OpenedOutage         bool // entered down
	ClosedOutage         bool // left down via success
	Duplicate            bool // result was already recorded; no state applied
	// HistoryOnly: the result was recorded as history but deliberately did
	// not run the machine — late evidence (S-O2) or a location-less result
	// for a location-bound monitor (S-E5).
	HistoryOnly bool
}

// Apply advances the state machine with one check result. threshold is the
// failures in a row that confirm down (S-T2); recoveryThreshold the passes in
// a row that confirm a down stream is up again (S-T7). Both clamp to >= 1.
func Apply(current Snapshot, resultIsFailure bool, threshold, recoveryThreshold int) Transition {
	if threshold < 1 {
		threshold = 1
	}
	if recoveryThreshold < MinRecoveryThreshold {
		recoveryThreshold = MinRecoveryThreshold
	}
	t := Transition{From: current.State}
	if !resultIsFailure {
		t.ConsecutiveFailures = 0
		// S-T7: a down stream stays down until enough passes confirm the
		// recovery; that time is still an outage (and downtime). Only down,
		// or a stream that owes a confirmation since it went silent while
		// down, needs confirming: otherwise a pass is up at once. A pending
		// stream re-enters down on its first pass, counting from 1.
		if current.State == StateDown || current.RecoveryPending {
			counted := 0
			if current.State == StateDown {
				counted = current.ConsecutiveSuccesses
			}
			if successes := counted + 1; successes < recoveryThreshold {
				t.To = StateDown
				t.ConsecutiveSuccesses = successes
				t.RecoveryPending = current.RecoveryPending
				t.Changed = current.State != StateDown
				t.OpenedOutage = t.Changed
				return t
			}
		}
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
	// Failures keep what is owed until the stream is down again (where any
	// recovery needs confirming anyway).
	t.RecoveryPending = current.RecoveryPending && t.To != StateDown
	return t
}

// IsFailureStatus reports whether a check_results.status counts as failing.
func IsFailureStatus(status string) bool {
	return status == "failure" || status == "error"
}

// LocationCounts summarizes the per-location states of one monitor's
// currently selected (non-deleted) locations.
type LocationCounts struct {
	Down     int
	Suspect  int
	Up       int
	Selected int // total selected locations, whether or not they reported yet
}

// AggregateLocations derives a multi-location monitor's state from its
// per-location states (the spatial rule, applied after the temporal machine
// ran per location):
//
//   - down when >= quorum locations are down
//   - degraded when some, but fewer than quorum, are down
//   - suspect when none are down but any location is mid-confirmation
//     (keeps the fast recheck; makes a 1-location monitor behave exactly
//     like a location-less one)
//   - up when at least one location reports up and none are down/suspect
//   - unknown when nothing has reported yet
//
// Locations that have not reported count as not-down: they can never trip
// quorum.
func AggregateLocations(c LocationCounts, quorum int) State {
	quorum = effectiveQuorum(quorum, c.Selected)
	switch {
	case c.Down >= quorum:
		return StateDown
	case c.Down > 0:
		return StateDegraded
	case c.Suspect > 0:
		return StateSuspect
	case c.Up > 0:
		return StateUp
	default:
		return StateUnknown
	}
}

// effectiveQuorum is S-A6: the configured quorum clamped to
// [1, selected] when any location is selected.
func effectiveQuorum(quorum, selected int) int {
	if quorum < 1 {
		quorum = 1
	}
	if quorum > selected && selected > 0 {
		quorum = selected
	}
	return quorum
}

// RecoveryProgress is a location-bound monitor's consecutive_successes, the
// display-only counterpart of S-T7 at the monitor level: the passes in a row
// it has toward leaving down, given the consecutive_successes of its fresh
// down locations. The aggregate leaves down once enough of them confirm to
// drop below quorum (down - quorum + 1 locations), so its progress is that
// many-th most advanced location's count. Zero when the aggregate is not down.
func RecoveryProgress(downSuccesses []int, quorum, selected int) int {
	q := effectiveQuorum(quorum, selected)
	if len(downSuccesses) < q {
		return 0
	}
	sorted := append([]int(nil), downSuccesses...)
	sort.Sort(sort.Reverse(sort.IntSlice(sorted)))
	return sorted[len(downSuccesses)-q]
}

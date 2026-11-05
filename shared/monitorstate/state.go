// Package monitorstate holds the pure monitor state-machine transition logic
// shared by worker (writes), scheduler (cadence), and alerter (lifecycle).
package monitorstate

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

// Snapshot is a monitor's persisted state.
type Snapshot struct {
	State               State
	ConsecutiveFailures int
	// ConsecutiveSuccesses counts passing results seen while the stream is
	// still down, waiting for recovery confirmation (S-T7). It is 0 on every
	// stream that is not mid-recovery.
	ConsecutiveSuccesses int
}

// Recovery-confirmation bounds (S-T7): the monitors.recovery_threshold
// CHECK constraint (migration 000108) and the API validator use the same.
const (
	MinRecoveryThreshold = 1
	MaxRecoveryThreshold = 10
)

// ClampRecoveryThreshold clamps a recovery threshold into
// [MinRecoveryThreshold, MaxRecoveryThreshold].
func ClampRecoveryThreshold(n int) int {
	if n < MinRecoveryThreshold {
		return MinRecoveryThreshold
	}
	if n > MaxRecoveryThreshold {
		return MaxRecoveryThreshold
	}
	return n
}

// Transition is the outcome of applying one check result.
type Transition struct {
	From                 State
	To                   State
	ConsecutiveFailures  int
	ConsecutiveSuccesses int  // passes toward confirming a recovery (S-T7)
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
// failures needed to enter down (S-T2), recoveryThreshold the consecutive
// passes needed to leave it (S-T7); both clamp to at least 1, and a
// recoveryThreshold of 1 is single-sample recovery.
//
// A pass always resets consecutive failures. From down it counts toward
// confirmation and the stream stays down until the count reaches
// recoveryThreshold; from any other state it is up at once — confirmation
// applies only to leaving down. A failure resets the pass count.
func Apply(current Snapshot, resultIsFailure bool, threshold, recoveryThreshold int) Transition {
	if threshold < 1 {
		threshold = 1
	}
	if recoveryThreshold < 1 {
		recoveryThreshold = 1
	}
	t := Transition{From: current.State}
	if !resultIsFailure {
		t.ConsecutiveFailures = 0
		if current.State == StateDown {
			if successes := current.ConsecutiveSuccesses + 1; successes < recoveryThreshold {
				t.To = StateDown
				t.ConsecutiveSuccesses = successes
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
	if quorum < 1 {
		quorum = 1
	}
	if quorum > c.Selected && c.Selected > 0 {
		quorum = c.Selected
	}
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

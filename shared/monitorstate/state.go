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
}

// Transition is the outcome of applying one check result.
type Transition struct {
	From                State
	To                  State
	ConsecutiveFailures int
	Changed             bool // state value changed
	OpenedOutage        bool // entered down
	ClosedOutage        bool // left down via success
	Duplicate           bool // result was already recorded; no state applied
}

// Apply advances the state machine with one check result.
func Apply(current Snapshot, resultIsFailure bool, threshold int) Transition {
	if threshold < 1 {
		threshold = 1
	}
	t := Transition{From: current.State}
	if !resultIsFailure {
		t.To = StateUp
		t.ConsecutiveFailures = 0
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

// Package monitorstate holds the pure monitor state-machine transition logic
// shared by worker (writes), scheduler (cadence), and alerter (lifecycle).
package monitorstate

type State string

const (
	StateUnknown State = "unknown"
	StateUp      State = "up"
	StateSuspect State = "suspect"
	StateDown    State = "down"
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

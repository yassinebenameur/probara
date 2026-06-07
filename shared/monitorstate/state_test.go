package monitorstate

import "testing"

func TestApply(t *testing.T) {
	tests := []struct {
		name      string
		current   Snapshot
		failure   bool
		threshold int
		want      Transition
	}{
		{"unknown_success", Snapshot{StateUnknown, 0}, false, 2,
			Transition{From: StateUnknown, To: StateUp, ConsecutiveFailures: 0, Changed: true}},
		{"unknown_failure", Snapshot{StateUnknown, 0}, true, 2,
			Transition{From: StateUnknown, To: StateSuspect, ConsecutiveFailures: 1, Changed: true}},
		{"up_failure_becomes_suspect", Snapshot{StateUp, 0}, true, 2,
			Transition{From: StateUp, To: StateSuspect, ConsecutiveFailures: 1, Changed: true}},
		{"up_failure_threshold_one_goes_straight_down", Snapshot{StateUp, 0}, true, 1,
			Transition{From: StateUp, To: StateDown, ConsecutiveFailures: 1, Changed: true, OpenedOutage: true}},
		{"suspect_failure_below_threshold", Snapshot{StateSuspect, 1}, true, 3,
			Transition{From: StateSuspect, To: StateSuspect, ConsecutiveFailures: 2}},
		{"suspect_failure_hits_threshold", Snapshot{StateSuspect, 1}, true, 2,
			Transition{From: StateSuspect, To: StateDown, ConsecutiveFailures: 2, Changed: true, OpenedOutage: true}},
		{"suspect_success_is_blip", Snapshot{StateSuspect, 1}, false, 2,
			Transition{From: StateSuspect, To: StateUp, ConsecutiveFailures: 0, Changed: true}},
		{"down_failure_stays_down", Snapshot{StateDown, 5}, true, 2,
			Transition{From: StateDown, To: StateDown, ConsecutiveFailures: 6}},
		{"down_success_recovers", Snapshot{StateDown, 5}, false, 2,
			Transition{From: StateDown, To: StateUp, ConsecutiveFailures: 0, Changed: true, ClosedOutage: true}},
		{"up_success_stays_up", Snapshot{StateUp, 0}, false, 2,
			Transition{From: StateUp, To: StateUp, ConsecutiveFailures: 0}},
		{"threshold_zero_clamps_to_one", Snapshot{StateUp, 0}, true, 0,
			Transition{From: StateUp, To: StateDown, ConsecutiveFailures: 1, Changed: true, OpenedOutage: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Apply(tc.current, tc.failure, tc.threshold)
			if got != tc.want {
				t.Fatalf("Apply() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestIsFailureStatus(t *testing.T) {
	for status, want := range map[string]bool{"failure": true, "error": true, "success": false} {
		if got := IsFailureStatus(status); got != want {
			t.Errorf("IsFailureStatus(%q) = %v, want %v", status, got, want)
		}
	}
}

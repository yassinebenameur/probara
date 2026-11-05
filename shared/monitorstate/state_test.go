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
		{"unknown_success", Snapshot{State: StateUnknown, ConsecutiveFailures: 0}, false, 2,
			Transition{From: StateUnknown, To: StateUp, ConsecutiveFailures: 0, Changed: true}},
		{"unknown_failure", Snapshot{State: StateUnknown, ConsecutiveFailures: 0}, true, 2,
			Transition{From: StateUnknown, To: StateSuspect, ConsecutiveFailures: 1, Changed: true}},
		{"up_failure_becomes_suspect", Snapshot{State: StateUp, ConsecutiveFailures: 0}, true, 2,
			Transition{From: StateUp, To: StateSuspect, ConsecutiveFailures: 1, Changed: true}},
		{"up_failure_threshold_one_goes_straight_down", Snapshot{State: StateUp, ConsecutiveFailures: 0}, true, 1,
			Transition{From: StateUp, To: StateDown, ConsecutiveFailures: 1, Changed: true, OpenedOutage: true}},
		{"suspect_failure_below_threshold", Snapshot{State: StateSuspect, ConsecutiveFailures: 1}, true, 3,
			Transition{From: StateSuspect, To: StateSuspect, ConsecutiveFailures: 2}},
		{"suspect_failure_hits_threshold", Snapshot{State: StateSuspect, ConsecutiveFailures: 1}, true, 2,
			Transition{From: StateSuspect, To: StateDown, ConsecutiveFailures: 2, Changed: true, OpenedOutage: true}},
		{"suspect_success_is_blip", Snapshot{State: StateSuspect, ConsecutiveFailures: 1}, false, 2,
			Transition{From: StateSuspect, To: StateUp, ConsecutiveFailures: 0, Changed: true}},
		{"down_failure_stays_down", Snapshot{State: StateDown, ConsecutiveFailures: 5}, true, 2,
			Transition{From: StateDown, To: StateDown, ConsecutiveFailures: 6}},
		{"down_success_recovers", Snapshot{State: StateDown, ConsecutiveFailures: 5}, false, 2,
			Transition{From: StateDown, To: StateUp, ConsecutiveFailures: 0, Changed: true, ClosedOutage: true}},
		{"up_success_stays_up", Snapshot{State: StateUp, ConsecutiveFailures: 0}, false, 2,
			Transition{From: StateUp, To: StateUp, ConsecutiveFailures: 0}},
		{"threshold_zero_clamps_to_one", Snapshot{State: StateUp, ConsecutiveFailures: 0}, true, 0,
			Transition{From: StateUp, To: StateDown, ConsecutiveFailures: 1, Changed: true, OpenedOutage: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Apply(tc.current, tc.failure, tc.threshold, 1)
			if got != tc.want {
				t.Fatalf("Apply() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// S-T7: recovery confirmation. From down a pass counts toward the
// recovery threshold and the stream stays down until it is reached; a
// failure resets the count; from any other state a pass is up at once.
func TestApplyRecoveryConfirmation(t *testing.T) {
	tests := []struct {
		name     string
		current  Snapshot
		failure  bool
		recovery int
		want     Transition
	}{
		{"down_first_pass_stays_down", Snapshot{State: StateDown, ConsecutiveFailures: 4}, false, 3,
			Transition{From: StateDown, To: StateDown, ConsecutiveSuccesses: 1}},
		{"down_second_pass_stays_down", Snapshot{State: StateDown, ConsecutiveSuccesses: 1}, false, 3,
			Transition{From: StateDown, To: StateDown, ConsecutiveSuccesses: 2}},
		{"down_pass_reaches_threshold_recovers", Snapshot{State: StateDown, ConsecutiveSuccesses: 2}, false, 3,
			Transition{From: StateDown, To: StateUp, Changed: true, ClosedOutage: true}},
		{"down_failure_resets_passes", Snapshot{State: StateDown, ConsecutiveSuccesses: 2}, true, 3,
			Transition{From: StateDown, To: StateDown, ConsecutiveFailures: 1}},
		{"lowered_threshold_confirms_on_next_pass", Snapshot{State: StateDown, ConsecutiveSuccesses: 4}, false, 2,
			Transition{From: StateDown, To: StateUp, Changed: true, ClosedOutage: true}},
		{"suspect_pass_is_up_at_once", Snapshot{State: StateSuspect, ConsecutiveFailures: 1}, false, 3,
			Transition{From: StateSuspect, To: StateUp, Changed: true}},
		{"unknown_pass_is_up_at_once", Snapshot{State: StateUnknown}, false, 3,
			Transition{From: StateUnknown, To: StateUp, Changed: true}},
		{"up_pass_stays_up", Snapshot{State: StateUp}, false, 3,
			Transition{From: StateUp, To: StateUp}},
		{"recovery_zero_clamps_to_one", Snapshot{State: StateDown, ConsecutiveFailures: 2}, false, 0,
			Transition{From: StateDown, To: StateUp, Changed: true, ClosedOutage: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Apply(tc.current, tc.failure, 2, tc.recovery)
			if got != tc.want {
				t.Fatalf("Apply() = %+v, want %+v [S-T7]", got, tc.want)
			}
		})
	}
}

func TestClampRecoveryThreshold(t *testing.T) {
	for in, want := range map[int]int{-3: 1, 0: 1, 1: 1, 5: 5, 10: 10, 11: 10, 99: 10} {
		if got := ClampRecoveryThreshold(in); got != want {
			t.Errorf("ClampRecoveryThreshold(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestIsFailureStatus(t *testing.T) {
	for status, want := range map[string]bool{"failure": true, "error": true, "success": false} {
		if got := IsFailureStatus(status); got != want {
			t.Errorf("IsFailureStatus(%q) = %v, want %v", status, got, want)
		}
	}
}

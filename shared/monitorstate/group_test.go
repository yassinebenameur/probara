package monitorstate

import "testing"

// S-G2 / S-G3: one summary, two views.
func TestGroupSummaryViews(t *testing.T) {
	for _, tc := range []struct {
		name    string
		members []State
		alert   State
		display State
	}{
		{"empty group", nil, StateUnknown, StateUnknown},
		{"all up", []State{StateUp, StateUp}, StateUp, StateUp},
		{"suspect still serves", []State{StateUp, StateSuspect}, StateUp, StateUp},
		{"one of three down", []State{StateUp, StateDown, StateUp}, StateDown, StateDegraded},
		{"all down", []State{StateDown, StateDown}, StateDown, StateDown},
		{"a degraded member", []State{StateUp, StateDegraded}, StateUp, StateDegraded},
		{"degraded and down", []State{StateDegraded, StateDown}, StateDown, StateDegraded},
		{"unknown member blocks an up verdict", []State{StateUp, StateUnknown}, StateUnknown, StateUp},
		{"down wins over unknown", []State{StateDown, StateUnknown}, StateDown, StateDown},
		{"nothing known", []State{StateUnknown, StateUnknown}, StateUnknown, StateUnknown},
		{"unrecognized state is unknown", []State{StateUp, State("paused")}, StateUnknown, StateUp},
	} {
		summary := SummarizeGroup(tc.members)
		if got := summary.AlertState(); got != tc.alert {
			t.Errorf("%s: AlertState = %q, want %q", tc.name, got, tc.alert)
		}
		if got := summary.DisplayState(); got != tc.display {
			t.Errorf("%s: DisplayState = %q, want %q", tc.name, got, tc.display)
		}
		if summary.Total() != len(tc.members) {
			t.Errorf("%s: Total = %d, want %d", tc.name, summary.Total(), len(tc.members))
		}
	}
}

// The alert view never reports "degraded" and is never healthier than the
// display view allows: if display says down, alerting says down too.
func TestGroupSummaryViewInvariants(t *testing.T) {
	states := []State{StateUp, StateSuspect, StateDegraded, StateDown, StateUnknown}
	var walk func(members []State, depth int)
	walk = func(members []State, depth int) {
		summary := SummarizeGroup(members)
		alert, display := summary.AlertState(), summary.DisplayState()
		if alert == StateDegraded {
			t.Fatalf("%v: AlertState is degraded", members)
		}
		if display == StateDown && alert != StateDown {
			t.Fatalf("%v: display down but alert %q", members, alert)
		}
		if alert == StateDown && display == StateUp {
			t.Fatalf("%v: alert down but display up", members)
		}
		if depth == 0 {
			return
		}
		for _, s := range states {
			walk(append(append([]State{}, members...), s), depth-1)
		}
	}
	walk(nil, 4)
}

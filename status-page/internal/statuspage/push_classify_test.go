package statuspage

import "testing"

// The notify rule as a table: this is the requirement -- "down and recovery,
// not degraded, not suspect" -- written as an executable spec.
func TestClassifyPushTransition(t *testing.T) {
	states := []string{"", "unknown", "up", "suspect", "degraded", "down"}

	tests := []struct {
		previous string
		current  string
		wantKind pushKind
		wantOK   bool
	}{
		// Entering down from anything not already announced down.
		{"", "down", pushKindDown, true},
		{"up", "down", pushKindDown, true},
		{"unknown", "down", pushKindDown, true},
		// previous is the last ANNOUNCED state, so a suspect or degraded step
		// in between is invisible here by construction.
		{"up", "down", pushKindDown, true},
		// Already announced as down: no repeat.
		{"down", "down", "", false},

		// Recovery only from an announced outage.
		{"down", "up", pushKindRecovered, true},
		{"", "up", "", false},
		{"up", "up", "", false},
		{"unknown", "up", "", false},

		// Degraded is never notified, in either direction. It means some but
		// not enough locations are failing; the service is still up for most
		// of the world.
		{"up", "degraded", "", false},
		{"down", "degraded", "", false},
		{"degraded", "degraded", "", false},

		// Suspect is mid-confirmation, by design not yet an outage.
		{"up", "suspect", "", false},
		{"down", "suspect", "", false},

		// Unknown means absence of evidence, which is never health news.
		{"up", "unknown", "", false},
		{"down", "unknown", "", false},
	}

	for _, tt := range tests {
		gotKind, gotOK := classifyPushTransition(tt.previous, tt.current)
		if gotKind != tt.wantKind || gotOK != tt.wantOK {
			t.Errorf("classifyPushTransition(%q, %q) = (%q, %v), want (%q, %v)",
				tt.previous, tt.current, gotKind, gotOK, tt.wantKind, tt.wantOK)
		}
	}

	// Exhaustive sweep: nothing outside the two rules above may ever notify.
	for _, prev := range states {
		for _, cur := range states {
			kind, ok := classifyPushTransition(prev, cur)
			wantOK := (cur == "down" && prev != "down") || (cur == "up" && prev == "down")
			if ok != wantOK {
				t.Errorf("classifyPushTransition(%q, %q) notified = %v, want %v", prev, cur, ok, wantOK)
			}
			if ok && kind != pushKindDown && kind != pushKindRecovered {
				t.Errorf("classifyPushTransition(%q, %q) produced unknown kind %q", prev, cur, kind)
			}
		}
	}
}

// A monitor that never leaves down must not re-notify, no matter how many
// failing results arrive. The timeline only records a row when the state
// value changes, but the rule must hold independently of that.
func TestClassifyPushTransition_DownIsAnnouncedOnce(t *testing.T) {
	if _, ok := classifyPushTransition("down", "down"); ok {
		t.Fatalf("a monitor staying down re-notified")
	}
}

// Flapping produces alternating notifications by design -- the debounce in
// reconcileNotifications is what stops a fast flap creating rows at all, not
// this function.
func TestClassifyPushTransition_FlapAlternates(t *testing.T) {
	sequence := []string{"up", "down", "up", "down", "up"}
	var kinds []pushKind
	for i := 1; i < len(sequence); i++ {
		if kind, ok := classifyPushTransition(sequence[i-1], sequence[i]); ok {
			kinds = append(kinds, kind)
		}
	}

	want := []pushKind{pushKindDown, pushKindRecovered, pushKindDown, pushKindRecovered}
	if len(kinds) != len(want) {
		t.Fatalf("got %d notifications %v, want %d %v", len(kinds), kinds, len(want), want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("notification %d = %q, want %q", i, kinds[i], want[i])
		}
	}
}

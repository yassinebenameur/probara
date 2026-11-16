package alertrouting

import (
	"strings"
	"testing"
)

// The mute holds an open alert's pages and reminders, for any reason it is
// muted; the resolve, the acknowledgement and the suspected-cause follow-up
// still go out.
func TestMuteHolds(t *testing.T) {
	for _, tc := range []struct {
		event          string
		acked, snoozed bool
		want           bool
	}{
		{"created", false, false, false},
		{"reminder", false, false, false},
		{"suspected_cause", false, false, false},
		{"created", true, false, true},
		{"reminder", true, false, true},
		{"suspected_cause", true, false, false},
		{"created", false, true, true},
		{"reminder", false, true, true},
		{"suspected_cause", false, true, false},
		{"reminder", true, true, true},
		{"resolved", true, true, false},
		{"resolved", false, true, false},
		{"acknowledged", true, false, false},
		{"acknowledged", true, true, false},
	} {
		if got := MuteHolds(tc.event, tc.acked, tc.snoozed); got != tc.want {
			t.Errorf("MuteHolds(%q, acked=%v, snoozed=%v) = %v, want %v", tc.event, tc.acked, tc.snoozed, got, tc.want)
		}
	}
}

func TestMuteFragments(t *testing.T) {
	if got := MutedPredicate("al"); !strings.Contains(got, "al.status = 'acknowledged'") ||
		!strings.Contains(got, "COALESCE(al.snoozed_until > NOW(), FALSE)") {
		t.Fatalf("MutedPredicate = %s", got)
	}
	if got := DispatchResumedAtExpr("a"); got != "GREATEST(a.unacknowledged_at, a.snoozed_until)" {
		t.Fatalf("DispatchResumedAtExpr = %s", got)
	}
}

package monitorstate

import "testing"

// S-F1: the evidence window is interval × 3 (floor 90s), except push, which
// uses interval + grace_period_seconds (a missing grace falls back to one
// extra interval, an explicit 0 is honoured).
func TestEvidenceWindowSeconds(t *testing.T) {
	for _, c := range []struct {
		name     string
		kind     string
		interval int
		config   string
		want     int
	}{
		{"floor", "http", 20, `{}`, 90},
		{"multiplier", "tcp", 60, `{}`, 180},
		{"agent uses the default", "agent", 60, `{"grace_period_seconds":5}`, 180},
		{"push grace", "push", 60, `{"grace_period_seconds":30}`, 90},
		{"push explicit zero grace", "push", 60, `{"grace_period_seconds":0}`, 60},
		{"push missing grace", "push", 60, `{}`, 120},
		{"push malformed config", "push", 60, `not json`, 120},
	} {
		if got := EvidenceWindowSeconds(c.kind, c.interval, []byte(c.config)); got != c.want {
			t.Errorf("S-F1 %s: got %d, want %d", c.name, got, c.want)
		}
	}
}

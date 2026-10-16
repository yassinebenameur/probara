package scheduler

import (
	"testing"
	"time"
)

func TestNextCheckDelay(t *testing.T) {
	const (
		sipCall    = `{"host":"trunk.example.com","method":"invite","call":{"number":"100"}}`
		sipOptions = `{"host":"trunk.example.com","method":"options"}`
	)
	tests := []struct {
		name     string
		typ      string
		config   string
		state    string
		interval int
		want     time.Duration
	}{
		{"up_uses_interval", "http", "", "up", 60, 60 * time.Second},
		{"down_uses_interval", "http", "", "down", 60, 60 * time.Second},
		{"unknown_uses_interval", "http", "", "unknown", 60, 60 * time.Second},
		{"suspect_fast_rechecks", "http", "", "suspect", 60, 20 * time.Second},
		{"suspect_never_slower_than_interval", "http", "", "suspect", 10, 10 * time.Second},
		{"suspect_sip_options_fast_rechecks", "sip", sipOptions, "suspect", 60, 20 * time.Second},
		// S-A9: a test call keeps its interval (the call-rate floor) while
		// suspect.
		{"suspect_sip_call_keeps_interval", "sip", sipCall, "suspect", 60, 60 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := Monitor{Type: tc.typ, Config: []byte(tc.config), CurrentState: tc.state, IntervalSeconds: tc.interval}
			if got := nextCheckDelay(m); got != tc.want {
				t.Fatalf("nextCheckDelay(%s %s, %d) = %v, want %v", tc.typ, tc.state, tc.interval, got, tc.want)
			}
		})
	}
}

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
		recover  bool
		interval int
		want     time.Duration
	}{
		{"up_uses_interval", "http", "", "up", false, 60, 60 * time.Second},
		{"down_uses_interval", "http", "", "down", false, 60, 60 * time.Second},
		{"unknown_uses_interval", "http", "", "unknown", false, 60, 60 * time.Second},
		{"suspect_fast_rechecks", "http", "", "suspect", false, 60, 20 * time.Second},
		{"suspect_never_slower_than_interval", "http", "", "suspect", false, 10, 10 * time.Second},
		{"suspect_sip_options_fast_rechecks", "sip", sipOptions, "suspect", false, 60, 20 * time.Second},
		// S-A9: a test call keeps its interval (the call-rate floor) while
		// suspect.
		{"suspect_sip_call_keeps_interval", "sip", sipCall, "suspect", false, 60, 60 * time.Second},
		// S-T7: a recovering monitor confirms on the same fast cadence, with
		// the same exemption.
		{"recovering_fast_rechecks", "http", "", "down", true, 300, 20 * time.Second},
		{"recovering_never_slower_than_interval", "http", "", "down", true, 10, 10 * time.Second},
		{"recovering_sip_options_fast_rechecks", "sip", sipOptions, "down", true, 60, 20 * time.Second},
		{"recovering_sip_call_keeps_interval", "sip", sipCall, "down", true, 60, 60 * time.Second},
		// Registries rate-limit lookups: a domain monitor keeps its interval
		// (monitorstate.FastRecheckExempt), confirming either way.
		{"suspect_domain_keeps_interval", "domain", `{"domain":"example.com"}`, "suspect", false, 3600, time.Hour},
		{"recovering_domain_keeps_interval", "domain", `{"domain":"example.com"}`, "down", true, 3600, time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := Monitor{Type: tc.typ, Config: []byte(tc.config), CurrentState: tc.state, Recovering: tc.recover, IntervalSeconds: tc.interval}
			if got := nextCheckDelay(m); got != tc.want {
				t.Fatalf("nextCheckDelay(%s %s, %d) = %v, want %v", tc.typ, tc.state, tc.interval, got, tc.want)
			}
		})
	}
}

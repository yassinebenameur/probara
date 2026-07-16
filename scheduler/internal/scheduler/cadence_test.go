package scheduler

import (
	"testing"
	"time"
)

func TestNextCheckDelay(t *testing.T) {
	tests := []struct {
		name     string
		state    string
		interval int
		want     time.Duration
	}{
		{"up_uses_interval", "up", 60, 60 * time.Second},
		{"down_uses_interval", "down", 60, 60 * time.Second},
		{"unknown_uses_interval", "unknown", 60, 60 * time.Second},
		{"suspect_fast_rechecks", "suspect", 60, 20 * time.Second},
		{"suspect_never_slower_than_interval", "suspect", 10, 10 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextCheckDelay(tc.state, tc.interval); got != tc.want {
				t.Fatalf("nextCheckDelay(%s, %d) = %v, want %v", tc.state, tc.interval, got, tc.want)
			}
		})
	}
}

package ingest

import (
	"testing"
	"time"
)

// A heartbeat is dropped when its worker clock is more than heartbeatMaxSkew
// from the scheduler's, in either direction.
func TestHeartbeatSkew(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		ts   time.Time
		drop bool
	}{
		{now, false},
		{now.Add(-heartbeatMaxSkew), false},
		{now.Add(heartbeatMaxSkew), false},
		{now.Add(-heartbeatMaxSkew - time.Second), true},
		{now.Add(heartbeatMaxSkew + time.Second), true},
	} {
		if got := heartbeatSkew(tc.ts, now) > heartbeatMaxSkew; got != tc.drop {
			t.Errorf("ts %v: drop = %v, want %v", tc.ts.Sub(now), got, tc.drop)
		}
	}
}

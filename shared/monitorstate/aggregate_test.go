package monitorstate

import "testing"

func TestAggregateLocations(t *testing.T) {
	tests := []struct {
		name   string
		counts LocationCounts
		quorum int
		want   State
	}{
		{"nothing reported", LocationCounts{Selected: 3}, 1, StateUnknown},
		{"all up", LocationCounts{Up: 3, Selected: 3}, 1, StateUp},
		{"one down trips quorum 1", LocationCounts{Down: 1, Up: 2, Selected: 3}, 1, StateDown},
		{"one down below quorum 2", LocationCounts{Down: 1, Up: 2, Selected: 3}, 2, StateDegraded},
		{"quorum reached exactly", LocationCounts{Down: 2, Up: 1, Selected: 3}, 2, StateDown},
		{"all down", LocationCounts{Down: 3, Selected: 3}, 3, StateDown},
		{"suspect only", LocationCounts{Suspect: 1, Up: 2, Selected: 3}, 2, StateSuspect},
		{"down beats suspect for degraded", LocationCounts{Down: 1, Suspect: 1, Up: 1, Selected: 3}, 3, StateDegraded},
		{"partial reporting up", LocationCounts{Up: 1, Selected: 3}, 2, StateUp},
		{"unreported locations never trip quorum", LocationCounts{Down: 1, Selected: 3}, 2, StateDegraded},

		// Single-location parity with the legacy temporal machine: with one
		// location and quorum 1, the aggregate mirrors that location's state.
		{"single location up", LocationCounts{Up: 1, Selected: 1}, 1, StateUp},
		{"single location suspect", LocationCounts{Suspect: 1, Selected: 1}, 1, StateSuspect},
		{"single location down", LocationCounts{Down: 1, Selected: 1}, 1, StateDown},
		{"single location unreported", LocationCounts{Selected: 1}, 1, StateUnknown},

		// Defensive clamping.
		{"quorum zero clamps to 1", LocationCounts{Down: 1, Up: 1, Selected: 2}, 0, StateDown},
		{"quorum above selected clamps down", LocationCounts{Down: 2, Selected: 2}, 5, StateDown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AggregateLocations(tt.counts, tt.quorum); got != tt.want {
				t.Fatalf("AggregateLocations(%+v, %d) = %s, want %s", tt.counts, tt.quorum, got, tt.want)
			}
		})
	}
}

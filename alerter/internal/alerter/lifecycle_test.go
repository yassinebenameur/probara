package alerter

import (
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// The orphan resolvers (host_metric, tls_expiry, latency_anomaly) all feed
// their keep-set through pq.Array. A nil slice must become an empty array,
// never SQL NULL, or `NOT (monitor_id = ANY($1))` matches no rows and the
// "resolve everything" path resolves nothing.
func TestNonNilIDsNeverEncodesAsSQLNull(t *testing.T) {
	got, err := pq.Array(nonNilIDs(nil)).Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	if got == nil {
		t.Fatalf("nonNilIDs(nil) encoded as SQL NULL; want empty array")
	}
	if s, ok := got.(string); !ok || s != "{}" {
		t.Fatalf("nonNilIDs(nil) encoded as %#v; want \"{}\"", got)
	}

	ids := []uuid.UUID{uuid.New(), uuid.New()}
	kept := nonNilIDs(ids)
	if len(kept) != 2 || kept[0] != ids[0] || kept[1] != ids[1] {
		t.Fatalf("nonNilIDs(%v) = %v; want unchanged", ids, kept)
	}
}

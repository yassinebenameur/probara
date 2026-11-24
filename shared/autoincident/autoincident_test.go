package autoincident

import (
	"testing"

	"github.com/google/uuid"
)

// The lock key is a cross-service, cross-release contract: the alerter and
// the API's automatic publishing must compute the same bytes, and older
// alerters computed exactly these.
func TestLockKey(t *testing.T) {
	tenant := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	monitor := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	want := "11111111-1111-1111-1111-111111111111:22222222-2222-2222-2222-222222222222:00000000-0000-0000-0000-000000000000"
	if got := LockKey(tenant, monitor); got != want {
		t.Fatalf("LockKey = %q, want %q", got, want)
	}
}

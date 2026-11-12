package alerter

import (
	"testing"

	"github.com/google/uuid"
)

// The advisory-lock key must stay byte-identical across releases: alerter
// replicas of two versions run side by side during a rolling deploy, and they
// only serialize auto-incident creation if they hash the same string.
func TestAutoIncidentLockKeyIsStableAcrossReleases(t *testing.T) {
	tenantID := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	monitorID := uuid.MustParse("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")

	const want = "ffffffff-ffff-ffff-ffff-ffffffffffff:eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee:00000000-0000-0000-0000-000000000000"
	if got := autoIncidentLockKey(tenantID, monitorID); got != want {
		t.Fatalf("autoIncidentLockKey = %q, want %q", got, want)
	}
}

func TestBuildAlerterAutoIncidentNarrative(t *testing.T) {
	title, summary := buildAlerterAutoIncidentNarrative("  api-e ")
	if title != "api-e incident" {
		t.Fatalf("title = %q, want %q", title, "api-e incident")
	}
	if summary != "Automatically created from a firing alert policy." {
		t.Fatalf("summary = %q", summary)
	}
	if title, _ := buildAlerterAutoIncidentNarrative(" "); title != "Auto-created incident" {
		t.Fatalf("unnamed title = %q, want %q", title, "Auto-created incident")
	}
}

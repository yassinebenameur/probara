package ai

import "testing"

// The relay's claim and the API's SLA "Triage pending" bucket both build
// triage eligibility from these fragments; pin their text so a change is a
// deliberate one that both sides pick up.
func TestTriageEligibilityFragments(t *testing.T) {
	if got, want := TriageSettingsEnabledSQL("ais"), `ais.enabled AND ais.auto_triage_enabled`; got != want {
		t.Fatalf("TriageSettingsEnabledSQL = %q, want %q", got, want)
	}
	want := `a.status IN ('active','acknowledged') AND a.resolved_at IS NULL
      AND a.triggered_at >= $5
      AND (a.monitor_id IS NULL OR EXISTS (SELECT 1 FROM monitors tcm WHERE tcm.id = a.monitor_id
            AND tcm.tenant_id = a.tenant_id AND tcm.deleted_at IS NULL))`
	if got := TriageClaimableAlertSQL("a", "$5"); got != want {
		t.Fatalf("TriageClaimableAlertSQL =\n%s\nwant\n%s", got, want)
	}
}

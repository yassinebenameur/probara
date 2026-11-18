//go:build !unit

package importservice

import (
	"errors"
	"strings"
	"testing"
)

// An applied document's thresholds are held to what a save accepts: a value
// out of range is a per-monitor plan error and nothing is written, where the
// importer would clamp it (recovery_threshold 50 must not apply as 10).
func TestApply_OutOfRangeThresholdsRefused(t *testing.T) {
	h := newApplyHarness(t)
	doc := func(field string, value string) string {
		return `
kind: monitor_export
version: 2
monitors:
  - key: api
    name: API
    type: http
    interval_seconds: 60
    timeout_seconds: 10
    ` + field + `: ` + value + `
    config: {url: "https://api.example.com", method: GET, expected_status: 200}
`
	}
	for _, tc := range []struct{ field, value string }{
		{"recovery_threshold", "50"},
		{"recovery_threshold", "-1"},
		{"consecutive_failures_threshold", "11"},
		{"consecutive_failures_threshold", "-2"},
	} {
		t.Run(tc.field+"="+tc.value, func(t *testing.T) {
			bad := doc(tc.field, tc.value)
			plan := h.plan(h.admin, bad)
			if plan.Valid || len(plan.Errors) != 1 || plan.Errors[0].Key != "api" || !strings.Contains(plan.Errors[0].Message, tc.field) {
				t.Fatalf("plan = %+v", plan)
			}
			if _, err := h.svc.Apply(h.admin, h.tenantID, applyRequest(bad)); !errors.Is(err, ErrApplyInvalid) {
				t.Fatalf("apply = %v, want ErrApplyInvalid", err)
			}
			if h.liveMonitors() != 0 || h.versionCount() != 0 {
				t.Fatal("a refused document wrote")
			}
		})
	}

	// The bounds themselves apply as written.
	h.apply(applyRequest(doc("recovery_threshold", "10")))
	if got := h.monitorByKey("api").RecoveryThreshold; got != 10 {
		t.Fatalf("recovery_threshold = %d, want 10", got)
	}
}

package validation

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestValidateRecoveryThreshold(t *testing.T) {
	n := func(v int) *int { return &v }

	if err := ValidateRecoveryThreshold(nil); err != nil {
		t.Fatalf("nil (field absent) must be accepted, got %v", err)
	}
	for _, ok := range []int{1, 2, 10} {
		if err := ValidateRecoveryThreshold(n(ok)); err != nil {
			t.Fatalf("%d must be accepted, got %v", ok, err)
		}
	}
	for _, bad := range []int{-1, 0, 11} {
		if err := ValidateRecoveryThreshold(n(bad)); err == nil {
			t.Fatalf("%d must be rejected", bad)
		}
	}

	// Create and update both run it.
	create := &models.CreateMonitorRequest{
		Name: "api", Type: models.MonitorTypeHTTP,
		Config:          json.RawMessage(`{"url":"https://example.com","method":"GET"}`),
		IntervalSeconds: 60, TimeoutSeconds: 10, RecoveryThreshold: n(11),
	}
	if err := ValidateMonitor(create); err == nil {
		t.Fatal("create with recovery_threshold 11 must be rejected")
	}
	create.RecoveryThreshold = n(3)
	if err := ValidateMonitor(create); err != nil {
		t.Fatalf("create with recovery_threshold 3: %v", err)
	}
	existing := &models.Monitor{
		Name: "api", Type: models.MonitorTypeHTTP, Config: create.Config,
		IntervalSeconds: 60, TimeoutSeconds: 10, ConsecutiveFailuresThreshold: 2, RecoveryThreshold: 1,
	}
	if err := ValidateMonitorUpdate(&models.UpdateMonitorRequest{RecoveryThreshold: n(0)}, existing); err == nil {
		t.Fatal("update to recovery_threshold 0 must be rejected")
	}
	if err := ValidateMonitorUpdate(&models.UpdateMonitorRequest{RecoveryThreshold: n(10)}, existing); err != nil {
		t.Fatalf("update to recovery_threshold 10: %v", err)
	}
}

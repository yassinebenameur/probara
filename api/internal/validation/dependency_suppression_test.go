package validation

import "testing"

func TestValidateDependencySuppression(t *testing.T) {
	str := func(s string) *string { return &s }

	if err := ValidateDependencySuppression(nil); err != nil {
		t.Fatalf("nil (field absent) must be accepted, got %v", err)
	}
	for _, ok := range []string{"inherit", "on", "off"} {
		if err := ValidateDependencySuppression(str(ok)); err != nil {
			t.Fatalf("%q must be accepted, got %v", ok, err)
		}
	}
	for _, bad := range []string{"", "yes", "true", "ON", "suppress"} {
		if err := ValidateDependencySuppression(str(bad)); err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
}

func TestValidateRecoveryThreshold(t *testing.T) {
	n := func(v int) *int { return &v }
	if err := ValidateRecoveryThreshold(nil); err != nil {
		t.Fatalf("nil (field absent) must be accepted, got %v", err)
	}
	for _, ok := range []int{1, 3, 10} {
		if err := ValidateRecoveryThreshold(n(ok)); err != nil {
			t.Fatalf("%d must be accepted, got %v", ok, err)
		}
	}
	for _, bad := range []int{-1, 0, 11} {
		if err := ValidateRecoveryThreshold(n(bad)); err == nil {
			t.Fatalf("%d must be rejected [S-T7]", bad)
		}
	}
}

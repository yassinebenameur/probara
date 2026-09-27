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

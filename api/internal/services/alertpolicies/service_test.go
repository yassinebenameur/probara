package alertpolicies

import "testing"

func TestNormalizeOptionalTemplatePointer(t *testing.T) {
	if got := normalizeOptionalTemplatePointer(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %v", got)
	}

	empty := ""
	if got := normalizeOptionalTemplatePointer(&empty); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}

	value := "Hello {{monitor_name}}"
	got := normalizeOptionalTemplatePointer(&value)
	if got != value {
		t.Fatalf("expected %q, got %v", value, got)
	}
}

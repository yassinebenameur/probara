package validation

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateTenantDataRetentionDays(t *testing.T) {
	tests := []struct {
		name string
		days int
		ok   bool
	}{
		{name: "unlimited", days: 0, ok: true},
		{name: "min", days: 30, ok: true},
		{name: "max", days: 3650, ok: true},
		{name: "below min", days: 29, ok: false},
		{name: "above max", days: 3651, ok: false},
		{name: "negative", days: -1, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTenantDataRetentionDays(tt.days)
			if tt.ok && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("expected validation error for %d", tt.days)
			}
		})
	}
}

func TestValidateDashboardGroupTags_AcceptsEmpty(t *testing.T) {
	if err := ValidateDashboardGroupTags([]string{}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidateDashboardGroupTags_AcceptsTypicalTags(t *testing.T) {
	if err := ValidateDashboardGroupTags([]string{"api", "payments", "internal"}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidateDashboardGroupTags_RejectsEmptyString(t *testing.T) {
	if err := ValidateDashboardGroupTags([]string{"api", ""}); err == nil {
		t.Fatal("expected error for empty tag")
	}
}

func TestValidateDashboardGroupTags_RejectsWhitespaceOnly(t *testing.T) {
	if err := ValidateDashboardGroupTags([]string{"  "}); err == nil {
		t.Fatal("expected error for whitespace-only tag")
	}
}

func TestValidateDashboardGroupTags_RejectsDuplicates(t *testing.T) {
	if err := ValidateDashboardGroupTags([]string{"api", "api"}); err == nil {
		t.Fatal("expected error for duplicate tag")
	}
}

func TestValidateDashboardGroupTags_RejectsTooLong(t *testing.T) {
	long := strings.Repeat("a", 65)
	if err := ValidateDashboardGroupTags([]string{long}); err == nil {
		t.Fatal("expected error for tag longer than 64 chars")
	}
}

func TestValidateDashboardGroupTags_RejectsTooMany(t *testing.T) {
	var tags []string
	for i := 0; i < 51; i++ {
		tags = append(tags, fmt.Sprintf("t%d", i))
	}
	if err := ValidateDashboardGroupTags(tags); err == nil {
		t.Fatal("expected error for >50 tags")
	}
}

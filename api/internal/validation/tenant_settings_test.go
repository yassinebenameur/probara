package validation

import "testing"

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

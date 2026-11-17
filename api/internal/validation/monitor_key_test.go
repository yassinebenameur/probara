package validation

import (
	"strings"
	"testing"
)

func TestValidateMonitorKey(t *testing.T) {
	for _, key := range []string{"web", "db-primary", "eu.api_v2", "0day", strings.Repeat("a", MaxMonitorKeyLength)} {
		if err := ValidateMonitorKey(key); err != nil {
			t.Errorf("%q refused: %v", key, err)
		}
	}
	for _, key := range []string{"", "Web", "-web", ".web", "has space", "a/b", "é", strings.Repeat("a", MaxMonitorKeyLength+1)} {
		if ValidateMonitorKey(key) == nil {
			t.Errorf("%q accepted", key)
		}
	}
}

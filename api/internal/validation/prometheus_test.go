package validation

import (
	"encoding/json"
	"github.com/yassinebenameur/probara/api/internal/models"
	"testing"
)

func TestPrometheusValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		key     string
		value   any
		invalid bool
	}{
		{"valid", "query", "sum(up)", false}, {"zero threshold", "threshold", 0, false},
		{"negative threshold", "threshold", -1, false}, {"missing threshold", "threshold", nil, true},
		{"empty query", "query", " ", true}, {"invalid operator", "operator", "bad", true},
		{"userinfo", "url", "https://user:secret@example.com", true}, {"query parameter", "url", "https://example.com?token=secret", true},
		{"scheme", "url", "file:///etc/passwd", true}, {"fragment", "url", "https://example.com/#fragment", true},
		{"no data", "no_data_status", "ignore", true}, {"no data success", "no_data_status", "success", false},
		{"auth", "auth_type", "oauth", true}, {"basic username missing", "auth_type", "basic", true},
		{"header injection", "bearer_token", "a\r\nb", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{"url": "https://example.com/prometheus", "query": "up", "operator": "gte", "threshold": 1}
			cfg[tc.key] = tc.value
			raw, _ := json.Marshal(cfg)
			err := DefaultRegistry.Validate(models.MonitorTypePrometheus, raw)
			if (err != nil) != tc.invalid {
				t.Fatalf("error %v, invalid %v", err, tc.invalid)
			}
		})
	}
	if !IsActiveCheckType(models.MonitorTypePrometheus) {
		t.Fatal("must enforce active timeout")
	}
}

package validation

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestVaultValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		key     string
		value   any
		invalid bool
	}{
		{"valid", "token", "hvs.secret", false},
		{"no token", "token", "", false},
		{"masked token", "token", "***", false},
		{"http", "url", "http://vault.internal:8200", false},
		{"path prefix", "url", "https://example.com/vault", false},
		{"v1 suffix", "url", "https://vault.example.com:8200/v1", true},
		{"v1 path", "url", "https://vault.example.com:8200/v1/sys/health", true},
		{"userinfo", "url", "https://user:secret@vault.example.com", true},
		{"query parameter", "url", "https://vault.example.com?x=1", true},
		{"scheme", "url", "file:///etc/passwd", true},
		{"token newline", "token", "a\r\nb", true},
		{"namespace", "namespace", "team-a/sub", false},
		{"namespace newline", "namespace", "a\nb", true},
		{"ttl", "token_min_ttl_seconds", 3600, false},
		{"negative ttl", "token_min_ttl_seconds", -1, true},
		{"latency", "max_latency_ms", 500, false},
		{"negative latency", "max_latency_ms", -5, true},
		{"server name", "tls_server_name", "vault.example.com", false},
		{"bad server name", "tls_server_name", "a b", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{"url": "https://vault.example.com:8200"}
			cfg[tc.key] = tc.value
			raw, _ := json.Marshal(cfg)
			err := DefaultRegistry.Validate(models.MonitorTypeVault, raw)
			if (err != nil) != tc.invalid {
				t.Fatalf("error %v, invalid %v", err, tc.invalid)
			}
		})
	}
	tlsOnHTTP, _ := json.Marshal(map[string]any{"url": "http://vault.internal:8200", "tls_skip_verify": true})
	if DefaultRegistry.Validate(models.MonitorTypeVault, tlsOnHTTP) == nil {
		t.Fatal("TLS options on an http url must be rejected")
	}
	if !IsActiveCheckType(models.MonitorTypeVault) {
		t.Fatal("must enforce active timeout")
	}
}

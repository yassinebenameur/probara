package config

import (
	"strings"
	"testing"
)

func clearEdgeEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"TRUSTED_PROXY_CIDRS", "SECURITY_HSTS_MAX_AGE", "API_RATE_LIMIT_PER_MINUTE", "API_RATE_LIMIT_BURST", "API_ALLOWED_ORIGINS", "APP_BASE_URL", "STATUS_PAGE_TRUSTED_PROXY"} {
		t.Setenv(k, "")
	}
}

func TestLoadAPIConfig_EdgeDefaults(t *testing.T) {
	setRequiredAPIEnv(t)
	clearEdgeEnv(t)
	cfg, err := LoadAPIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxyCIDRs) != 0 || cfg.HSTSMaxAge != 0 {
		t.Fatalf("trusted=%v hsts=%d, want none and 0", cfg.TrustedProxyCIDRs, cfg.HSTSMaxAge)
	}
	if cfg.RateLimitPerMinute != 600 || cfg.RateLimitBurst != 120 {
		t.Fatalf("rate limit = %d/%d, want 600/120", cfg.RateLimitPerMinute, cfg.RateLimitBurst)
	}
	if len(cfg.AllowedOrigins) != 0 {
		t.Fatalf("allowed origins = %v, want none", cfg.AllowedOrigins)
	}
}

func TestLoadAPIConfig_EdgeValues(t *testing.T) {
	setRequiredAPIEnv(t)
	clearEdgeEnv(t)
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/8, 192.168.1.7")
	t.Setenv("SECURITY_HSTS_MAX_AGE", "31536000")
	t.Setenv("API_RATE_LIMIT_PER_MINUTE", "0")
	t.Setenv("API_ALLOWED_ORIGINS", "https://Ops.example.com/, http://localhost:3000")
	t.Setenv("APP_BASE_URL", "https://ignored.example.com")
	cfg, err := LoadAPIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxyCIDRs) != 2 || cfg.TrustedProxyCIDRs[1].String() != "192.168.1.7/32" {
		t.Fatalf("trusted = %v", cfg.TrustedProxyCIDRs)
	}
	if cfg.HSTSMaxAge != 31536000 || cfg.RateLimitPerMinute != 0 {
		t.Fatalf("hsts=%d rate=%d", cfg.HSTSMaxAge, cfg.RateLimitPerMinute)
	}
	if got := strings.Join(cfg.AllowedOrigins, " "); got != "https://ops.example.com http://localhost:3000" {
		t.Fatalf("allowed origins = %q", got)
	}
}

// Unset API_ALLOWED_ORIGINS falls back to the operator UI origin.
func TestLoadAPIConfig_AllowedOriginsDefaultToAppBaseURL(t *testing.T) {
	setRequiredAPIEnv(t)
	clearEdgeEnv(t)
	t.Setenv("APP_BASE_URL", "https://Probara.example.com/app/")
	cfg, err := LoadAPIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.AllowedOrigins, " "); got != "https://probara.example.com" {
		t.Fatalf("allowed origins = %q", got)
	}
}

func TestLoadAPIConfig_EdgeRejectsBadValues(t *testing.T) {
	for key, value := range map[string]string{
		"TRUSTED_PROXY_CIDRS":       "10.0.0.0/33",
		"SECURITY_HSTS_MAX_AGE":     "-1",
		"API_RATE_LIMIT_PER_MINUTE": "lots",
		"API_RATE_LIMIT_BURST":      "-5",
		"API_ALLOWED_ORIGINS":       "*",
	} {
		t.Run(key, func(t *testing.T) {
			setRequiredAPIEnv(t)
			clearEdgeEnv(t)
			t.Setenv(key, value)
			if _, err := LoadAPIConfig(); err == nil {
				t.Fatalf("%s=%q: expected an error", key, value)
			}
		})
	}
	for _, bad := range []string{"https://ops.example.com/path", "ops.example.com", "https://u@ops.example.com", "ftp://ops.example.com"} {
		if _, err := parseAllowedOrigins(bad, ""); err == nil {
			t.Errorf("parseAllowedOrigins(%q): expected an error", bad)
		}
	}
}

func setRequiredStatusPageEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HTTP_PORT", "8082")
	t.Setenv("METRICS_PORT", "9093")
	clearEdgeEnv(t)
}

func TestLoadStatusPageConfig_Edge(t *testing.T) {
	setRequiredStatusPageEnv(t)
	t.Setenv("TRUSTED_PROXY_CIDRS", "fd00::/8")
	t.Setenv("SECURITY_HSTS_MAX_AGE", "600")
	t.Setenv("APP_BASE_URL", "https://ops.example.com/")
	cfg, err := LoadStatusPageConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxyCIDRs) != 1 || cfg.HSTSMaxAge != 600 || cfg.AppBaseURL != "https://ops.example.com" || cfg.LegacyTrustedProxy {
		t.Fatalf("unexpected edge config: %+v", cfg.EdgeConfig)
	}
}

// The deprecated STATUS_PAGE_TRUSTED_PROXY=true keeps its trust-everyone
// behavior only while TRUSTED_PROXY_CIDRS is unset.
func TestLoadStatusPageConfig_LegacyTrustedProxy(t *testing.T) {
	setRequiredStatusPageEnv(t)
	t.Setenv("STATUS_PAGE_TRUSTED_PROXY", "true")
	cfg, err := LoadStatusPageConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.LegacyTrustedProxy || len(cfg.TrustedProxyCIDRs) != 2 {
		t.Fatalf("legacy flag not honored: %+v", cfg.EdgeConfig)
	}

	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/8")
	cfg, err = LoadStatusPageConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LegacyTrustedProxy || len(cfg.TrustedProxyCIDRs) != 1 {
		t.Fatalf("TRUSTED_PROXY_CIDRS must win over the legacy flag: %+v", cfg.EdgeConfig)
	}
}

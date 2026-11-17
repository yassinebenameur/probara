package config

import (
	"strings"
	"testing"
)

// STATUS_PAGE_SESSION_SECRET is optional, but one too short to sign with
// refuses to start instead of leaving gated pages quietly unavailable.
func TestLoadStatusPageConfig_SessionSecret(t *testing.T) {
	setRequiredStatusPageEnv(t)

	t.Setenv("STATUS_PAGE_SESSION_SECRET", "")
	cfg, err := LoadStatusPageConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionSecret != "" {
		t.Fatalf("unset secret loaded as %q", cfg.SessionSecret)
	}

	t.Setenv("STATUS_PAGE_SESSION_SECRET", "too-short")
	if _, err := LoadStatusPageConfig(); err == nil || !strings.Contains(err.Error(), "STATUS_PAGE_SESSION_SECRET") {
		t.Fatalf("short secret: err = %v", err)
	}

	secret := strings.Repeat("s", MinStatusPageSessionSecretLength)
	t.Setenv("STATUS_PAGE_SESSION_SECRET", "  "+secret+"\n")
	cfg, err = LoadStatusPageConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionSecret != secret {
		t.Fatalf("secret = %q, want it trimmed", cfg.SessionSecret)
	}
}

// The session cookie's Secure flag follows STATUS_PAGE_COOKIE_SECURE, else an
// https STATUS_PAGE_BASE_URL, else request detection (like the API's
// ADMIN_COOKIE_SECURE).
func TestLoadStatusPageConfig_CookieSecure(t *testing.T) {
	cases := []struct {
		raw, baseURL   string
		secure, detect bool
	}{
		{"", "", false, true},
		{"", "http://localhost:8082", false, true},
		{"", "HTTPS://status.example.com", true, false},
		{"false", "https://status.example.com", false, false},
		{"true", "", true, false},
	}
	for _, c := range cases {
		setRequiredStatusPageEnv(t)
		t.Setenv("STATUS_PAGE_COOKIE_SECURE", c.raw)
		t.Setenv("STATUS_PAGE_BASE_URL", c.baseURL)
		cfg, err := LoadStatusPageConfig()
		if err != nil {
			t.Fatal(err)
		}
		if secure, detect := cfg.SessionCookieSecure(); secure != c.secure || detect != c.detect {
			t.Errorf("%q/%q: secure=%v detect=%v, want %v %v", c.raw, c.baseURL, secure, detect, c.secure, c.detect)
		}
	}
	setRequiredStatusPageEnv(t)
	t.Setenv("STATUS_PAGE_COOKIE_SECURE", "sometimes")
	if _, err := LoadStatusPageConfig(); err == nil || !strings.Contains(err.Error(), "STATUS_PAGE_COOKIE_SECURE") {
		t.Fatalf("invalid value: err = %v", err)
	}
}

// On a custom domain the Secure flag follows STATUS_PAGE_COOKIE_SECURE when
// set and the request otherwise: an https STATUS_PAGE_BASE_URL describes the
// platform host only.
func TestStatusPageConfig_CustomHostCookieSecure(t *testing.T) {
	on, off := true, false
	cases := []struct {
		flag           *bool
		baseURL        string
		secure, detect bool
	}{
		{nil, "", false, true},
		{nil, "https://status.example.com", false, true},
		{&on, "", true, false},
		{&off, "https://status.example.com", false, false},
	}
	for _, c := range cases {
		cfg := &StatusPageConfig{StatusPageBaseURL: c.baseURL, CookieSecure: c.flag}
		if secure, detect := cfg.CustomHostCookieSecure(); secure != c.secure || detect != c.detect {
			t.Errorf("%v/%q: secure=%v detect=%v, want %v %v", c.flag, c.baseURL, secure, detect, c.secure, c.detect)
		}
	}
}

// X-Forwarded-Host is believed only when STATUS_PAGE_TRUST_FORWARDED_HOST
// says so (default off).
func TestLoadStatusPageConfig_TrustForwardedHost(t *testing.T) {
	setRequiredStatusPageEnv(t)
	t.Setenv("STATUS_PAGE_TRUST_FORWARDED_HOST", "")
	cfg, err := LoadStatusPageConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrustForwardedHost {
		t.Fatal("trusted X-Forwarded-Host by default")
	}
	t.Setenv("STATUS_PAGE_TRUST_FORWARDED_HOST", "true")
	if cfg, err = LoadStatusPageConfig(); err != nil || !cfg.TrustForwardedHost {
		t.Fatalf("true: trust=%v err=%v", cfg != nil && cfg.TrustForwardedHost, err)
	}
	t.Setenv("STATUS_PAGE_TRUST_FORWARDED_HOST", "maybe")
	if _, err := LoadStatusPageConfig(); err == nil || !strings.Contains(err.Error(), "STATUS_PAGE_TRUST_FORWARDED_HOST") {
		t.Fatalf("invalid value: err = %v", err)
	}
}

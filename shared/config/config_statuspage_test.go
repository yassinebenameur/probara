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

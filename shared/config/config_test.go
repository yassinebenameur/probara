package config

import "testing"

func TestLoadBaseConfig_MissingHTTPPort(t *testing.T) {
	t.Setenv("HTTP_PORT", "")
	t.Setenv("METRICS_PORT", "9090")

	if _, err := LoadBaseConfig("test"); err == nil {
		t.Fatalf("expected error for missing HTTP_PORT")
	}
}

func TestLoadBaseConfig_MissingMetricsPort(t *testing.T) {
	t.Setenv("HTTP_PORT", "8080")
	t.Setenv("METRICS_PORT", "")

	if _, err := LoadBaseConfig("test"); err == nil {
		t.Fatalf("expected error for missing METRICS_PORT")
	}
}

func TestLoadAlerterConfig_Defaults(t *testing.T) {
	t.Setenv("HTTP_PORT", "8080")
	t.Setenv("METRICS_PORT", "9090")
	t.Setenv("SMTP_USERNAME", "user@example.com")
	t.Setenv("SMTP_FROM", "")

	cfg, err := LoadAlerterConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AlertStream != "ALERTS" {
		t.Fatalf("expected default AlertStream, got %s", cfg.AlertStream)
	}
	if cfg.AlertSubject != "alerts" {
		t.Fatalf("expected default AlertSubject, got %s", cfg.AlertSubject)
	}
	if cfg.AlertEvalIntervalSeconds != 30 {
		t.Fatalf("expected default eval interval 30, got %d", cfg.AlertEvalIntervalSeconds)
	}
	if cfg.AlertReminderIntervalSeconds != 3600 {
		t.Fatalf("expected default reminder interval 3600, got %d", cfg.AlertReminderIntervalSeconds)
	}
	if cfg.AlertGroupWindowSeconds != 60 {
		t.Fatalf("expected default group window 60, got %d", cfg.AlertGroupWindowSeconds)
	}
	if cfg.AlertGroupMaxChildren != 5 {
		t.Fatalf("expected default group max children 5, got %d", cfg.AlertGroupMaxChildren)
	}
	if cfg.SMTPPort != 587 {
		t.Fatalf("expected default SMTP port 587, got %d", cfg.SMTPPort)
	}
	if !cfg.SMTPUseTLS {
		t.Fatalf("expected SMTPUseTLS true by default")
	}
	if cfg.SMTPFrom != "user@example.com" {
		t.Fatalf("expected SMTPFrom to default to SMTP_USERNAME, got %s", cfg.SMTPFrom)
	}
}

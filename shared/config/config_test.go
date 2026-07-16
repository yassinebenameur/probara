package config

import "testing"

func setRequiredAPIEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HTTP_PORT", "8080")
	t.Setenv("METRICS_PORT", "9090")
	t.Setenv("ADMIN_JWT_SECRET", "test-secret")
	t.Setenv("OIDC_ENABLED", "false")
}

func TestLoadAPIConfigRejectsPublicNATSWithoutLocationAuth(t *testing.T) {
	setRequiredAPIEnv(t)
	t.Setenv("PUBLIC_NATS_URL", "tls://nats.example.test:4222")
	t.Setenv("NATS_LOCATION_AUTH_ISSUER_SEED", "")
	if _, err := LoadAPIConfig(); err == nil {
		t.Fatal("expected public NATS URL without location authorization to fail")
	}
}

func TestLoadAPIConfigLocationAuthSubjects(t *testing.T) {
	setRequiredAPIEnv(t)
	t.Setenv("PUBLIC_NATS_URL", "")
	t.Setenv("NATS_LOCATION_AUTH_ISSUER_SEED", "seed")
	t.Setenv("PROBARA_SECRETS_KEY", "configured")
	t.Setenv("CHECK_JOB_STREAM", "PRIVATE_JOBS")
	t.Setenv("CHECK_RESULT_SUBJECT", "private.results")
	cfg, err := LoadAPIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CheckJobStream != "PRIVATE_JOBS" || cfg.CheckResultSubject != "private.results" {
		t.Fatalf("unexpected location auth subjects: %#v", cfg)
	}
}

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

func TestLoadSchedulerConfig_RetentionDefaults(t *testing.T) {
	t.Setenv("HTTP_PORT", "8080")
	t.Setenv("METRICS_PORT", "9090")

	cfg, err := LoadSchedulerConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !cfg.RetentionCleanupEnabled {
		t.Fatalf("expected retention cleanup to be enabled by default")
	}
	if cfg.RetentionCleanupHourUTC != 2 {
		t.Fatalf("expected default retention hour 2, got %d", cfg.RetentionCleanupHourUTC)
	}
	if cfg.RetentionCleanupBatchSize != 5000 {
		t.Fatalf("expected default retention batch size 5000, got %d", cfg.RetentionCleanupBatchSize)
	}
	if cfg.RetentionCleanupMaxRowsPerRun != 200000 {
		t.Fatalf("expected default retention max rows per run 200000, got %d", cfg.RetentionCleanupMaxRowsPerRun)
	}
}

func TestLoadSchedulerConfig_InvalidRetentionHour(t *testing.T) {
	t.Setenv("HTTP_PORT", "8080")
	t.Setenv("METRICS_PORT", "9090")
	t.Setenv("RETENTION_CLEANUP_HOUR_UTC", "24")

	if _, err := LoadSchedulerConfig(); err == nil {
		t.Fatalf("expected error for invalid retention hour")
	}
}

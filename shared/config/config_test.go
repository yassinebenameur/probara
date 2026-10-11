package config

import (
	"strings"
	"testing"
)

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

// The API needs the same SMTP transport as the alerter: POST
// /alert-channels/{id}/test runs the email plugin's Send path in-process, so a
// missing mailer there fails every email channel test even when delivery works.
func TestLoadAPIConfig_SMTP(t *testing.T) {
	setRequiredAPIEnv(t)
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "465")
	t.Setenv("SMTP_USE_TLS", "true")
	t.Setenv("SMTP_USERNAME", "user@example.com")
	t.Setenv("SMTP_FROM", "")

	cfg, err := LoadAPIConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.SMTPHost != "smtp.example.com" {
		t.Fatalf("expected SMTPHost smtp.example.com, got %s", cfg.SMTPHost)
	}
	if cfg.SMTPPort != 465 {
		t.Fatalf("expected SMTPPort 465, got %d", cfg.SMTPPort)
	}
	if !cfg.SMTPUseTLS {
		t.Fatalf("expected SMTPUseTLS true")
	}
	if cfg.SMTPFrom != "user@example.com" {
		t.Fatalf("expected SMTPFrom to default to SMTP_USERNAME, got %s", cfg.SMTPFrom)
	}
}

func TestLoadAPIConfig_SMTPInvalidPort(t *testing.T) {
	setRequiredAPIEnv(t)
	t.Setenv("SMTP_PORT", "not-a-port")

	if _, err := LoadAPIConfig(); err == nil {
		t.Fatalf("expected error for invalid SMTP_PORT")
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

func TestLoadNotificationEgressConfig(t *testing.T) {
	t.Setenv("NOTIFICATION_BLOCK_PRIVATE_IPS", "")
	t.Setenv("NOTIFICATION_ALLOWED_CIDRS", "")
	cfg, err := loadNotificationEgressConfig()
	if err != nil || !cfg.NotificationBlockPrivateIPs || len(cfg.NotificationAllowedCIDRs) != 0 {
		t.Fatalf("default = %+v, %v; want blocking on, no allow-list", cfg, err)
	}

	t.Setenv("NOTIFICATION_BLOCK_PRIVATE_IPS", "false")
	t.Setenv("NOTIFICATION_ALLOWED_CIDRS", " 10.20.0.0/16, ,fd00::/8")
	cfg, err = loadNotificationEgressConfig()
	if err != nil || cfg.NotificationBlockPrivateIPs || len(cfg.NotificationAllowedCIDRs) != 2 {
		t.Fatalf("explicit = %+v, %v", cfg, err)
	}

	t.Setenv("NOTIFICATION_ALLOWED_CIDRS", "10.0.0.0/33")
	if _, err := loadNotificationEgressConfig(); err == nil {
		t.Fatal("an invalid CIDR must fail startup")
	}
	t.Setenv("NOTIFICATION_ALLOWED_CIDRS", "")
	t.Setenv("NOTIFICATION_BLOCK_PRIVATE_IPS", "maybe")
	if _, err := loadNotificationEgressConfig(); err == nil {
		t.Fatal("an invalid boolean must fail startup")
	}
}

func TestLoadWorkerConfig_AITriageDefaults(t *testing.T) {
	t.Setenv("HTTP_PORT", "8083")
	t.Setenv("METRICS_PORT", "9092")
	cfg, err := LoadWorkerConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AITriageEnabled || cfg.AITriageStream != "AI_TRIAGE" || cfg.AITriageSubject != "ai.triage.jobs" || cfg.AITriageConsumerName != "ai-triage-workers" {
		t.Fatalf("unexpected triage defaults: enabled=%t stream=%q subject=%q consumer=%q",
			cfg.AITriageEnabled, cfg.AITriageStream, cfg.AITriageSubject, cfg.AITriageConsumerName)
	}

	t.Setenv("AI_TRIAGE_ENABLED", "false")
	if cfg, err = LoadWorkerConfig(); err != nil || cfg.AITriageEnabled {
		t.Fatalf("AI_TRIAGE_ENABLED=false: enabled=%t err=%v", cfg != nil && cfg.AITriageEnabled, err)
	}
}

func TestLoadWorkerConfig_AITriageRejectsBadValues(t *testing.T) {
	t.Setenv("HTTP_PORT", "8083")
	t.Setenv("METRICS_PORT", "9092")

	t.Setenv("AI_TRIAGE_ENABLED", "sometimes")
	if _, err := LoadWorkerConfig(); err == nil || !strings.Contains(err.Error(), "invalid AI_TRIAGE_ENABLED") {
		t.Fatalf("invalid AI_TRIAGE_ENABLED accepted: %v", err)
	}
	t.Setenv("AI_TRIAGE_ENABLED", "")

	const clash = "AI_TRIAGE_STREAM/AI_TRIAGE_SUBJECT must differ from AI_RCA_STREAM/AI_RCA_SUBJECT"
	t.Setenv("AI_TRIAGE_STREAM", "AI_RCA")
	if _, err := LoadWorkerConfig(); err == nil || err.Error() != clash {
		t.Fatalf("shared stream accepted: %v", err)
	}
	t.Setenv("AI_TRIAGE_STREAM", "")
	t.Setenv("AI_TRIAGE_SUBJECT", "ai.rca.jobs")
	if _, err := LoadWorkerConfig(); err == nil || err.Error() != clash {
		t.Fatalf("shared subject accepted: %v", err)
	}
}

// The alerter honours the installation's local AI kill switches for its
// suspected-cause follow-ups, so disabling AI also stops follow-ups from
// triages that were already ready.
func TestLoadAlerterConfig_SuspectedCauseKillSwitches(t *testing.T) {
	t.Setenv("HTTP_PORT", "8084")
	t.Setenv("METRICS_PORT", "9094")
	cases := []struct {
		localAIDisabled, triageEnabled string
		wantDisabled                   bool
	}{
		{"", "", false},
		{"false", "true", false},
		{"true", "", true},
		{"", "false", true},
		{"true", "false", true},
	}
	for _, tc := range cases {
		t.Setenv("LOCAL_AI_DISABLED", tc.localAIDisabled)
		t.Setenv("AI_TRIAGE_ENABLED", tc.triageEnabled)
		cfg, err := LoadAlerterConfig()
		if err != nil {
			t.Fatalf("LOCAL_AI_DISABLED=%q AI_TRIAGE_ENABLED=%q: %v", tc.localAIDisabled, tc.triageEnabled, err)
		}
		if cfg.SuspectedCausesDisabled != tc.wantDisabled {
			t.Fatalf("LOCAL_AI_DISABLED=%q AI_TRIAGE_ENABLED=%q: disabled=%t, want %t",
				tc.localAIDisabled, tc.triageEnabled, cfg.SuspectedCausesDisabled, tc.wantDisabled)
		}
	}

	t.Setenv("LOCAL_AI_DISABLED", "")
	t.Setenv("AI_TRIAGE_ENABLED", "sometimes")
	if _, err := LoadAlerterConfig(); err == nil || !strings.Contains(err.Error(), "invalid AI_TRIAGE_ENABLED") {
		t.Fatalf("invalid AI_TRIAGE_ENABLED accepted: %v", err)
	}
	t.Setenv("AI_TRIAGE_ENABLED", "")
	t.Setenv("LOCAL_AI_DISABLED", "maybe")
	if _, err := LoadAlerterConfig(); err == nil || !strings.Contains(err.Error(), "invalid LOCAL_AI_DISABLED") {
		t.Fatalf("invalid LOCAL_AI_DISABLED accepted: %v", err)
	}
}

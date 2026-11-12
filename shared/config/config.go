package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/models"
)

// BaseConfig contains common configuration for all services
type BaseConfig struct {
	ServiceName string
	HTTPPort    int
	MetricsPort int
	LogLevel    string
	PostgresURL string
	NATSURL     string
}

// SMTPConfig is the process-level SMTP transport for the builtin email
// notification plugin. Every service that can invoke email Send needs it: the
// alerter (evaluation loop), the worker (async notifications consumer), and the
// API (the alert-channel test endpoint). Embedded rather than nested so
// existing cfg.SMTPHost-style reads keep working.
type SMTPConfig struct {
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPFromName string
	SMTPUseTLS   bool
	// AppBaseURL is the public origin of the operator UI, e.g.
	// "https://probara.example.com". Alert emails link to the affected monitor
	// when it is set and omit the button when it is not — a guessed host in a
	// notification is worse than no link. It lives with the SMTP block because
	// the same three workloads need it, for the same reason.
	AppBaseURL string
}

// loadSMTPConfig reads the SMTP_* variables. Sole parser for these — services
// must not re-read the env, so defaults and validation stay identical across
// api, worker, and alerter. All fields are optional: when SMTP_HOST is empty no
// mailer is installed and the email plugin's Send returns an explicit error.
func loadSMTPConfig() (SMTPConfig, error) {
	var cfg SMTPConfig

	cfg.SMTPHost = os.Getenv("SMTP_HOST")

	cfg.SMTPPort = 587
	if v := os.Getenv("SMTP_PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil {
			return SMTPConfig{}, fmt.Errorf("invalid SMTP_PORT: %w", err)
		}
		cfg.SMTPPort = port
	}

	cfg.SMTPUsername = os.Getenv("SMTP_USERNAME")
	cfg.SMTPPassword = os.Getenv("SMTP_PASSWORD")

	cfg.SMTPFrom = os.Getenv("SMTP_FROM")
	if cfg.SMTPFrom == "" {
		cfg.SMTPFrom = cfg.SMTPUsername
	}

	// Display name on the From header. Defaults to a product name so alerts do
	// not arrive as a bare address; set SMTP_FROM_NAME="" is not distinguishable
	// from unset, so an operator who wants a bare address sets it to a space.
	cfg.SMTPFromName = os.Getenv("SMTP_FROM_NAME")
	if cfg.SMTPFromName == "" {
		cfg.SMTPFromName = "Probara Alerts"
	}

	cfg.AppBaseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("APP_BASE_URL")), "/")

	cfg.SMTPUseTLS = true
	if v := os.Getenv("SMTP_USE_TLS"); v != "" {
		useTLS, err := strconv.ParseBool(v)
		if err != nil {
			return SMTPConfig{}, fmt.Errorf("invalid SMTP_USE_TLS: %w", err)
		}
		cfg.SMTPUseTLS = useTLS
	}

	return cfg, nil
}

// NotificationEgressConfig is the outbound-connection policy for notification
// plugins (generic webhook, chat, paging and SMS channels). Every service that
// can invoke a plugin's Send needs it — the same three workloads as
// SMTPConfig. Channel URLs are tenant-supplied, so private and reserved
// addresses (including cloud metadata) are refused unless the operator opts
// out or allow-lists a range for an internal receiver.
type NotificationEgressConfig struct {
	NotificationBlockPrivateIPs bool
	NotificationAllowedCIDRs    []*net.IPNet
}

// loadNotificationEgressConfig reads NOTIFICATION_BLOCK_PRIVATE_IPS (default
// true) and NOTIFICATION_ALLOWED_CIDRS (comma-separated). Sole parser for
// these, like loadSMTPConfig.
func loadNotificationEgressConfig() (NotificationEgressConfig, error) {
	cfg := NotificationEgressConfig{NotificationBlockPrivateIPs: true}
	if v := strings.TrimSpace(os.Getenv("NOTIFICATION_BLOCK_PRIVATE_IPS")); v != "" {
		block, err := strconv.ParseBool(v)
		if err != nil {
			return NotificationEgressConfig{}, fmt.Errorf("invalid NOTIFICATION_BLOCK_PRIVATE_IPS: %w", err)
		}
		cfg.NotificationBlockPrivateIPs = block
	}
	for _, p := range strings.Split(os.Getenv("NOTIFICATION_ALLOWED_CIDRS"), ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		_, cidr, err := net.ParseCIDR(p)
		if err != nil {
			return NotificationEgressConfig{}, fmt.Errorf("invalid NOTIFICATION_ALLOWED_CIDRS entry %q: %w", p, err)
		}
		cfg.NotificationAllowedCIDRs = append(cfg.NotificationAllowedCIDRs, cidr)
	}
	return cfg, nil
}

// LoadBaseConfig loads base configuration from environment variables
func LoadBaseConfig(serviceName string) (*BaseConfig, error) {
	cfg := &BaseConfig{
		ServiceName: serviceName,
	}

	// HTTP_PORT
	httpPortStr := os.Getenv("HTTP_PORT")
	if httpPortStr == "" {
		return nil, fmt.Errorf("HTTP_PORT environment variable is required")
	}
	httpPort, err := strconv.Atoi(httpPortStr)
	if err != nil {
		return nil, fmt.Errorf("invalid HTTP_PORT: %w", err)
	}
	cfg.HTTPPort = httpPort

	// METRICS_PORT
	metricsPortStr := os.Getenv("METRICS_PORT")
	if metricsPortStr == "" {
		return nil, fmt.Errorf("METRICS_PORT environment variable is required")
	}
	metricsPort, err := strconv.Atoi(metricsPortStr)
	if err != nil {
		return nil, fmt.Errorf("invalid METRICS_PORT: %w", err)
	}
	cfg.MetricsPort = metricsPort

	// LOG_LEVEL
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}
	cfg.LogLevel = logLevel

	// POSTGRES_URL (optional for some services)
	cfg.PostgresURL = os.Getenv("POSTGRES_URL")

	// NATS_URL
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}
	cfg.NATSURL = natsURL

	return cfg, nil
}

// CheckQueueConfig names the check-job and check-result queues.
type CheckQueueConfig struct {
	JobStream     string
	JobSubject    string
	ResultStream  string
	ResultSubject string
}

// loadCheckQueueConfig is the sole reader of the CHECK_JOB_* and
// CHECK_RESULT_* variables. The API, the scheduler and the worker all load
// through it, so the three cannot disagree on a default: a service on a
// different job subject makes run-now publish where no worker listens.
func loadCheckQueueConfig() CheckQueueConfig {
	return CheckQueueConfig{
		JobStream:     envOrDefault("CHECK_JOB_STREAM", models.CheckJobStream),
		JobSubject:    envOrDefault("CHECK_JOB_SUBJECT", models.CheckJobSubject),
		ResultStream:  envOrDefault("CHECK_RESULT_STREAM", models.CheckResultStream),
		ResultSubject: envOrDefault("CHECK_RESULT_SUBJECT", models.CheckResultSubject),
	}
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// loadAITriageEnabled parses AI_TRIAGE_ENABLED (default true), the platform
// kill switch for automatic alert triage, shared by the worker, the alerter
// and the API (SLA cause breakdown).
func loadAITriageEnabled() (bool, error) {
	v := strings.TrimSpace(os.Getenv("AI_TRIAGE_ENABLED"))
	if v == "" {
		return true, nil
	}
	enabled, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid AI_TRIAGE_ENABLED: %w", err)
	}
	return enabled, nil
}

// loadLocalAIDisabled parses LOCAL_AI_DISABLED (default false).
func loadLocalAIDisabled() (bool, error) {
	v := os.Getenv("LOCAL_AI_DISABLED")
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid LOCAL_AI_DISABLED")
	}
	return b, nil
}

// envDurationSeconds reads an integer number of seconds from the environment,
// falling back to def when unset or invalid.
func envDurationSeconds(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return def
	}
	return time.Duration(seconds) * time.Second
}

// loadLocalAIConfig is shared by API and platform worker. Retired LLM_*
// variables are ignored, including legacy destination and credential values.
func loadLocalAIConfig() (ai.Config, error) {
	c := ai.Config{BaseURL: envOrDefault("LOCAL_AI_URL", "http://127.0.0.1:8090"), APIKey: strings.TrimSpace(os.Getenv("LOCAL_AI_TOKEN")), Timeout: 90 * time.Second}
	disabled, err := loadLocalAIDisabled()
	if err != nil {
		return c, err
	}
	c.Disabled = disabled
	if v := os.Getenv("LOCAL_AI_TIMEOUT_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 300 {
			return c, fmt.Errorf("LOCAL_AI_TIMEOUT_SECONDS must be 1..300")
		}
		c.Timeout = time.Duration(n) * time.Second
	}
	return c, nil
}

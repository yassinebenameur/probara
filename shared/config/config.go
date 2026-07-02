package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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

// APIConfig contains configuration for the API service
type APIConfig struct {
	BaseConfig
	AlertStream       string
	AlertSubject      string
	AlertConsumerName string
	CheckJobSubject   string
	AIRCASubject      string
	AIAnalysisEnabled bool
	// LLM_* env defaults — optional global fallback used when a tenant has no
	// ai_settings row. Mirrors the worker's fields.
	LLMProvider           string
	LLMBaseURL            string
	LLMAPIKey             string
	LLMModel              string
	LLMJSONMode           string
	LLMMaxTokens          int
	LLMTimeoutSeconds     int
	AdminJWTSecret        string
	AdminAccessTTLMinutes int
	AdminRefreshTTLDays   int
	AdminCookieSecure     bool
	AdminBcryptCost       int
	SyntheticArtifactsDir string
	// PublicBaseURL is the externally-reachable URL of this API (e.g.
	// "https://probara.example.com"). When set, it is used as the BACKEND_URL
	// baked into agent install scripts and push webhook URLs, bypassing
	// Host-header inspection which is unreliable behind reverse proxies.
	PublicBaseURL string
	// PublicNATSURL is the NATS address reachable from remote networks (e.g.
	// "nats://nats.example.com:4222"), baked into private-location worker
	// deploy snippets. Empty renders a placeholder.
	PublicNATSURL string
}

// SchedulerConfig contains configuration for the scheduler service
type SchedulerConfig struct {
	BaseConfig
	ScheduleIntervalSeconds int
	SchedulerBatchSize      int
	CheckJobSubject         string
	CheckJobStream          string
	// Results ingest: the scheduler hosts the consumer that persists check
	// results published by workers over NATS (workers have no DB access).
	CheckResultStream        string
	CheckResultSubject       string
	ResultIngestConsumerName string
	ResultIngestConcurrency  int
	ResultIngestEnabled      bool
	// LegacyCheckConsumers are pre-locations filterless consumer names the
	// scheduler deletes on startup: a work-queue stream can't host both a
	// filterless consumer and the filtered per-location ones.
	LegacyCheckConsumers          []string
	RetentionCleanupEnabled       bool
	RetentionCleanupHourUTC       int
	RetentionCleanupBatchSize     int
	RetentionCleanupMaxRowsPerRun int
	// Monitor purge: removes child rows of soft-deleted monitors then the row itself.
	MonitorPurgeEnabled         bool
	MonitorPurgeIntervalSeconds int
	MonitorPurgeBatchSize       int
	MonitorPurgeMaxRowsPerRun   int
}

// WorkerConfig contains configuration for the worker service
type WorkerConfig struct {
	BaseConfig
	WorkerConcurrency  int
	NATSConsumerName   string
	CheckJobStream     string
	CheckJobSubject    string
	CheckResultStream  string
	CheckResultSubject string
	// LocationID pins this worker to a private location: it consumes only
	// that location's job subject and heartbeats its liveness. Empty = the
	// default platform fleet.
	LocationID            string
	MaxHTTPTimeoutSeconds int
	MaxBodySizeBytes      int
	HTTPBlockPrivateIPs   bool
	HTTPAllowedCIDRs      []*net.IPNet
	SyntheticArtifactsDir string

	// AI root cause analysis consumer + LLM provider settings. The consumer is
	// enabled only when LLMBaseURL is set; otherwise the worker skips it and
	// the feature degrades to "not configured".
	AIRCAStream       string
	AIRCASubject      string
	AIRCAConsumerName string
	LLMProvider       string
	LLMBaseURL        string
	LLMAPIKey         string
	LLMModel          string
	LLMJSONMode       string
	LLMMaxTokens      int
	LLMTimeoutSeconds int

	// Notification dispatch consumer settings — only used when the alerter is
	// publishing to the NOTIFICATIONS stream (ALERTER_ASYNC_DISPATCH=true).
	NotificationsEnabled      bool
	NotificationsStream       string
	NotificationsSubjectGlob  string
	NotificationsConsumerName string

	// SMTP backend wired into the builtin email plugin (worker side, used by
	// the notifications consumer). Mirrors AlerterConfig fields.
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPUseTLS   bool
}

// AlerterConfig contains configuration for the alerter service
type AlerterConfig struct {
	BaseConfig
	AlertStream                  string
	AlertSubject                 string
	AlertEvalIntervalSeconds     int
	AlertReminderIntervalSeconds int
	AlertGroupWindowSeconds      int
	AlertGroupMaxChildren        int
	LatencyAnomalyEnabled        bool
	SMTPHost                     string
	SMTPPort                     int
	SMTPUsername                 string
	SMTPPassword                 string
	SMTPFrom                     string
	SMTPUseTLS                   bool
	AlertEmailTo                 string

	// AsyncDispatch toggles publishing channel notifications to the NATS
	// NOTIFICATIONS stream instead of dispatching synchronously inside the
	// alerter loop. When true, the worker consumes "alerts.dispatch.*" and
	// invokes the plugin Send path.
	AsyncDispatch            bool
	NotificationsStream      string
	NotificationsSubjectGlob string
}

// StatusPageConfig contains configuration for the status page service
type StatusPageConfig struct {
	BaseConfig
	StatusPageBaseURL string
	APIBaseURL        string
	// HTTP server timeouts. These are a safety net only; the public status page
	// is expected to render well within them. See LoadStatusPageConfig for defaults.
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
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

// LoadAPIConfig loads API service configuration
func LoadAPIConfig() (*APIConfig, error) {
	base, err := LoadBaseConfig("api")
	if err != nil {
		return nil, err
	}

	cfg := &APIConfig{BaseConfig: *base}

	// ALERT_STREAM
	alertStream := os.Getenv("ALERT_STREAM")
	if alertStream == "" {
		cfg.AlertStream = "ALERTS"
	} else {
		cfg.AlertStream = alertStream
	}

	// ALERT_SUBJECT
	alertSubject := os.Getenv("ALERT_SUBJECT")
	if alertSubject == "" {
		cfg.AlertSubject = "alerts"
	} else {
		cfg.AlertSubject = alertSubject
	}

	// ALERT_CONSUMER_NAME
	alertConsumerName := os.Getenv("ALERT_CONSUMER_NAME")
	if alertConsumerName == "" {
		cfg.AlertConsumerName = "api-alerts"
	} else {
		cfg.AlertConsumerName = alertConsumerName
	}

	// CHECK_JOB_SUBJECT
	checkJobSubject := os.Getenv("CHECK_JOB_SUBJECT")
	if checkJobSubject == "" {
		cfg.CheckJobSubject = "check.jobs"
	} else {
		cfg.CheckJobSubject = checkJobSubject
	}

	// AI_RCA_SUBJECT — subject the API publishes AI root cause jobs to (the
	// worker consumes them). Must match the worker's AI_RCA_SUBJECT.
	cfg.AIRCASubject = envOrDefault("AI_RCA_SUBJECT", "ai.rca.jobs")

	// LLM_* env defaults — optional global fallback. When set, the API can build
	// an analyzer/advisor and gate AI features even before a tenant configures
	// its own ai_settings row.
	cfg.LLMProvider = envOrDefault("LLM_PROVIDER", "openai_compat")
	cfg.LLMBaseURL = strings.TrimSpace(os.Getenv("LLM_BASE_URL"))
	cfg.LLMAPIKey = strings.TrimSpace(os.Getenv("LLM_API_KEY"))
	cfg.LLMModel = strings.TrimSpace(os.Getenv("LLM_MODEL"))
	cfg.LLMJSONMode = strings.TrimSpace(os.Getenv("LLM_JSON_MODE"))
	cfg.LLMMaxTokens = 1024
	if v := strings.TrimSpace(os.Getenv("LLM_MAX_TOKENS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid LLM_MAX_TOKENS: %q", v)
		}
		cfg.LLMMaxTokens = n
	}
	cfg.LLMTimeoutSeconds = 60
	if v := strings.TrimSpace(os.Getenv("LLM_TIMEOUT_SECONDS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid LLM_TIMEOUT_SECONDS: %q", v)
		}
		cfg.LLMTimeoutSeconds = n
	}
	cfg.AIAnalysisEnabled = cfg.LLMBaseURL != ""

	// ADMIN_JWT_SECRET
	adminJWTSecret := os.Getenv("ADMIN_JWT_SECRET")
	if adminJWTSecret == "" {
		return nil, fmt.Errorf("ADMIN_JWT_SECRET environment variable is required")
	}
	cfg.AdminJWTSecret = adminJWTSecret

	// ADMIN_ACCESS_TTL_MINUTES
	accessTTLStr := os.Getenv("ADMIN_ACCESS_TTL_MINUTES")
	if accessTTLStr == "" {
		cfg.AdminAccessTTLMinutes = 15
	} else {
		ttl, err := strconv.Atoi(accessTTLStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ADMIN_ACCESS_TTL_MINUTES: %w", err)
		}
		cfg.AdminAccessTTLMinutes = ttl
	}

	// ADMIN_REFRESH_TTL_DAYS
	refreshTTLStr := os.Getenv("ADMIN_REFRESH_TTL_DAYS")
	if refreshTTLStr == "" {
		cfg.AdminRefreshTTLDays = 30
	} else {
		ttl, err := strconv.Atoi(refreshTTLStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ADMIN_REFRESH_TTL_DAYS: %w", err)
		}
		cfg.AdminRefreshTTLDays = ttl
	}

	// ADMIN_COOKIE_SECURE
	cookieSecureStr := os.Getenv("ADMIN_COOKIE_SECURE")
	if cookieSecureStr == "" {
		cfg.AdminCookieSecure = false
	} else {
		secure, err := strconv.ParseBool(cookieSecureStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ADMIN_COOKIE_SECURE: %w", err)
		}
		cfg.AdminCookieSecure = secure
	}

	// ADMIN_BCRYPT_COST
	bcryptCostStr := os.Getenv("ADMIN_BCRYPT_COST")
	if bcryptCostStr == "" {
		cfg.AdminBcryptCost = 12
	} else {
		cost, err := strconv.Atoi(bcryptCostStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ADMIN_BCRYPT_COST: %w", err)
		}
		cfg.AdminBcryptCost = cost
	}

	// SYNTHETIC_BROWSER_ARTIFACTS_DIR
	artifactsDir := strings.TrimSpace(os.Getenv("SYNTHETIC_BROWSER_ARTIFACTS_DIR"))
	if artifactsDir == "" {
		artifactsDir = filepath.Join(os.TempDir(), "probara", "synthetic-browser-artifacts")
	}
	cfg.SyntheticArtifactsDir = artifactsDir

	// PUBLIC_BASE_URL: externally-reachable URL of the API, used for
	// agent install scripts and push webhook URLs. Trailing slash is stripped.
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/")

	// PUBLIC_NATS_URL: externally-reachable NATS address baked into
	// private-location worker deploy snippets.
	cfg.PublicNATSURL = strings.TrimSpace(os.Getenv("PUBLIC_NATS_URL"))

	return cfg, nil
}

// LoadSchedulerConfig loads scheduler service configuration
func LoadSchedulerConfig() (*SchedulerConfig, error) {
	base, err := LoadBaseConfig("scheduler")
	if err != nil {
		return nil, err
	}

	cfg := &SchedulerConfig{BaseConfig: *base}

	// SCHEDULE_INTERVAL_SECONDS
	scheduleIntervalStr := os.Getenv("SCHEDULE_INTERVAL_SECONDS")
	if scheduleIntervalStr == "" {
		cfg.ScheduleIntervalSeconds = 2
	} else {
		scheduleInterval, err := strconv.Atoi(scheduleIntervalStr)
		if err != nil {
			return nil, fmt.Errorf("invalid SCHEDULE_INTERVAL_SECONDS: %w", err)
		}
		cfg.ScheduleIntervalSeconds = scheduleInterval
	}

	// SCHEDULER_BATCH_SIZE
	batchSizeStr := os.Getenv("SCHEDULER_BATCH_SIZE")
	if batchSizeStr == "" {
		cfg.SchedulerBatchSize = 500
	} else {
		batchSize, err := strconv.Atoi(batchSizeStr)
		if err != nil {
			return nil, fmt.Errorf("invalid SCHEDULER_BATCH_SIZE: %w", err)
		}
		cfg.SchedulerBatchSize = batchSize
	}

	// CHECK_JOB_SUBJECT
	checkJobSubject := os.Getenv("CHECK_JOB_SUBJECT")
	if checkJobSubject == "" {
		cfg.CheckJobSubject = "check.jobs"
	} else {
		cfg.CheckJobSubject = checkJobSubject
	}

	// CHECK_JOB_STREAM
	checkJobStream := os.Getenv("CHECK_JOB_STREAM")
	if checkJobStream == "" {
		cfg.CheckJobStream = "CHECK_JOBS"
	} else {
		cfg.CheckJobStream = checkJobStream
	}

	// Results ingest — the scheduler-side consumer persisting worker results.
	cfg.CheckResultStream = envOrDefault("CHECK_RESULT_STREAM", "CHECK_RESULTS")
	cfg.CheckResultSubject = envOrDefault("CHECK_RESULT_SUBJECT", "check.results")
	cfg.ResultIngestConsumerName = envOrDefault("RESULT_INGEST_CONSUMER_NAME", "result-ingest")
	cfg.ResultIngestConcurrency = 10
	if v := strings.TrimSpace(os.Getenv("RESULT_INGEST_CONCURRENCY")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid RESULT_INGEST_CONCURRENCY: %q", v)
		}
		cfg.ResultIngestConcurrency = n
	}
	cfg.ResultIngestEnabled = true
	if v := strings.TrimSpace(os.Getenv("RESULT_INGEST_ENABLED")); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid RESULT_INGEST_ENABLED: %w", err)
		}
		cfg.ResultIngestEnabled = enabled
	}

	// CHECK_JOB_LEGACY_CONSUMERS — comma-separated filterless consumer names
	// to delete on startup. Defaults cover the code default and the compose
	// value used before per-location consumers existed.
	legacyConsumers := envOrDefault("CHECK_JOB_LEGACY_CONSUMERS", "check-workers,worker")
	for _, name := range strings.Split(legacyConsumers, ",") {
		if name = strings.TrimSpace(name); name != "" {
			cfg.LegacyCheckConsumers = append(cfg.LegacyCheckConsumers, name)
		}
	}

	// RETENTION_CLEANUP_ENABLED
	retentionCleanupEnabledStr := strings.TrimSpace(os.Getenv("RETENTION_CLEANUP_ENABLED"))
	if retentionCleanupEnabledStr == "" {
		cfg.RetentionCleanupEnabled = true
	} else {
		enabled, err := strconv.ParseBool(retentionCleanupEnabledStr)
		if err != nil {
			return nil, fmt.Errorf("invalid RETENTION_CLEANUP_ENABLED: %w", err)
		}
		cfg.RetentionCleanupEnabled = enabled
	}

	// RETENTION_CLEANUP_HOUR_UTC
	retentionCleanupHourUTCStr := strings.TrimSpace(os.Getenv("RETENTION_CLEANUP_HOUR_UTC"))
	if retentionCleanupHourUTCStr == "" {
		cfg.RetentionCleanupHourUTC = 2
	} else {
		hour, err := strconv.Atoi(retentionCleanupHourUTCStr)
		if err != nil {
			return nil, fmt.Errorf("invalid RETENTION_CLEANUP_HOUR_UTC: %w", err)
		}
		if hour < 0 || hour > 23 {
			return nil, fmt.Errorf("invalid RETENTION_CLEANUP_HOUR_UTC: must be between 0 and 23")
		}
		cfg.RetentionCleanupHourUTC = hour
	}

	// RETENTION_CLEANUP_BATCH_SIZE
	retentionCleanupBatchSizeStr := strings.TrimSpace(os.Getenv("RETENTION_CLEANUP_BATCH_SIZE"))
	if retentionCleanupBatchSizeStr == "" {
		cfg.RetentionCleanupBatchSize = 5000
	} else {
		size, err := strconv.Atoi(retentionCleanupBatchSizeStr)
		if err != nil {
			return nil, fmt.Errorf("invalid RETENTION_CLEANUP_BATCH_SIZE: %w", err)
		}
		if size <= 0 {
			return nil, fmt.Errorf("invalid RETENTION_CLEANUP_BATCH_SIZE: must be greater than 0")
		}
		cfg.RetentionCleanupBatchSize = size
	}

	// RETENTION_CLEANUP_MAX_ROWS_PER_RUN
	retentionCleanupMaxRowsStr := strings.TrimSpace(os.Getenv("RETENTION_CLEANUP_MAX_ROWS_PER_RUN"))
	if retentionCleanupMaxRowsStr == "" {
		cfg.RetentionCleanupMaxRowsPerRun = 200000
	} else {
		maxRows, err := strconv.Atoi(retentionCleanupMaxRowsStr)
		if err != nil {
			return nil, fmt.Errorf("invalid RETENTION_CLEANUP_MAX_ROWS_PER_RUN: %w", err)
		}
		if maxRows <= 0 {
			return nil, fmt.Errorf("invalid RETENTION_CLEANUP_MAX_ROWS_PER_RUN: must be greater than 0")
		}
		cfg.RetentionCleanupMaxRowsPerRun = maxRows
	}

	cfg.MonitorPurgeEnabled = true
	if v := os.Getenv("MONITOR_PURGE_ENABLED"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid MONITOR_PURGE_ENABLED: %w", err)
		}
		cfg.MonitorPurgeEnabled = enabled
	}

	cfg.MonitorPurgeIntervalSeconds = 30
	if v := os.Getenv("MONITOR_PURGE_INTERVAL_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MONITOR_PURGE_INTERVAL_SECONDS: %q", v)
		}
		cfg.MonitorPurgeIntervalSeconds = n
	}

	cfg.MonitorPurgeBatchSize = 5000
	if v := os.Getenv("MONITOR_PURGE_BATCH_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MONITOR_PURGE_BATCH_SIZE: %q", v)
		}
		cfg.MonitorPurgeBatchSize = n
	}

	cfg.MonitorPurgeMaxRowsPerRun = 200000
	if v := os.Getenv("MONITOR_PURGE_MAX_ROWS_PER_RUN"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MONITOR_PURGE_MAX_ROWS_PER_RUN: %q", v)
		}
		cfg.MonitorPurgeMaxRowsPerRun = n
	}

	return cfg, nil
}

// LoadWorkerConfig loads worker service configuration
func LoadWorkerConfig() (*WorkerConfig, error) {
	base, err := LoadBaseConfig("worker")
	if err != nil {
		return nil, err
	}

	cfg := &WorkerConfig{BaseConfig: *base}

	// WORKER_CONCURRENCY
	concurrencyStr := os.Getenv("WORKER_CONCURRENCY")
	if concurrencyStr == "" {
		cfg.WorkerConcurrency = 10
	} else {
		concurrency, err := strconv.Atoi(concurrencyStr)
		if err != nil {
			return nil, fmt.Errorf("invalid WORKER_CONCURRENCY: %w", err)
		}
		cfg.WorkerConcurrency = concurrency
	}

	// NATS_CONSUMER_NAME
	consumerName := os.Getenv("NATS_CONSUMER_NAME")
	if consumerName == "" {
		cfg.NATSConsumerName = "check-workers"
	} else {
		cfg.NATSConsumerName = consumerName
	}

	// CHECK_JOB_STREAM
	checkJobStream := os.Getenv("CHECK_JOB_STREAM")
	if checkJobStream == "" {
		cfg.CheckJobStream = "CHECK_JOBS"
	} else {
		cfg.CheckJobStream = checkJobStream
	}

	// CHECK_JOB_SUBJECT
	checkJobSubject := os.Getenv("CHECK_JOB_SUBJECT")
	if checkJobSubject == "" {
		cfg.CheckJobSubject = "check.jobs"
	} else {
		cfg.CheckJobSubject = checkJobSubject
	}

	// Results publishing — workers publish results here instead of writing
	// Postgres, so remote location workers only need NATS reachability.
	cfg.CheckResultStream = envOrDefault("CHECK_RESULT_STREAM", "CHECK_RESULTS")
	cfg.CheckResultSubject = envOrDefault("CHECK_RESULT_SUBJECT", "check.results")

	// WORKER_LOCATION_ID — pins this worker to a private location (UUID from
	// the Locations page). Empty = default platform fleet.
	cfg.LocationID = strings.TrimSpace(os.Getenv("WORKER_LOCATION_ID"))
	if cfg.LocationID != "" {
		if _, err := uuid.Parse(cfg.LocationID); err != nil {
			return nil, fmt.Errorf("invalid WORKER_LOCATION_ID: %q is not a UUID", cfg.LocationID)
		}
	}

	// MAX_HTTP_TIMEOUT_SECONDS
	maxTimeoutStr := os.Getenv("MAX_HTTP_TIMEOUT_SECONDS")
	if maxTimeoutStr == "" {
		cfg.MaxHTTPTimeoutSeconds = 30
	} else {
		maxTimeout, err := strconv.Atoi(maxTimeoutStr)
		if err != nil {
			return nil, fmt.Errorf("invalid MAX_HTTP_TIMEOUT_SECONDS: %w", err)
		}
		cfg.MaxHTTPTimeoutSeconds = maxTimeout
	}

	// MAX_BODY_SIZE_BYTES
	maxBodySizeStr := os.Getenv("MAX_BODY_SIZE_BYTES")
	if maxBodySizeStr == "" {
		cfg.MaxBodySizeBytes = 1048576 // 1 MB
	} else {
		maxBodySize, err := strconv.Atoi(maxBodySizeStr)
		if err != nil {
			return nil, fmt.Errorf("invalid MAX_BODY_SIZE_BYTES: %w", err)
		}
		cfg.MaxBodySizeBytes = maxBodySize
	}

	// HTTP_BLOCK_PRIVATE_IPS
	blockPrivateIPsStr := os.Getenv("HTTP_BLOCK_PRIVATE_IPS")
	if blockPrivateIPsStr == "" {
		cfg.HTTPBlockPrivateIPs = false
	} else {
		block, err := strconv.ParseBool(blockPrivateIPsStr)
		if err != nil {
			return nil, fmt.Errorf("invalid HTTP_BLOCK_PRIVATE_IPS: %w", err)
		}
		cfg.HTTPBlockPrivateIPs = block
	}

	// HTTP_ALLOWED_CIDRS (comma-separated CIDRs that bypass the private IP block)
	allowedCIDRsStr := os.Getenv("HTTP_ALLOWED_CIDRS")
	if strings.TrimSpace(allowedCIDRsStr) != "" {
		parts := strings.Split(allowedCIDRsStr, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			_, cidr, err := net.ParseCIDR(p)
			if err != nil {
				return nil, fmt.Errorf("invalid HTTP_ALLOWED_CIDRS entry %q: %w", p, err)
			}
			cfg.HTTPAllowedCIDRs = append(cfg.HTTPAllowedCIDRs, cidr)
		}
	}

	// SYNTHETIC_BROWSER_ARTIFACTS_DIR
	artifactsDir := strings.TrimSpace(os.Getenv("SYNTHETIC_BROWSER_ARTIFACTS_DIR"))
	if artifactsDir == "" {
		artifactsDir = filepath.Join(os.TempDir(), "probara", "synthetic-browser-artifacts")
	}
	cfg.SyntheticArtifactsDir = artifactsDir

	// NOTIFICATIONS_ENABLED — opt-in for the notifications consumer goroutine.
	enabledStr := strings.TrimSpace(os.Getenv("NOTIFICATIONS_ENABLED"))
	if enabledStr != "" {
		enabled, err := strconv.ParseBool(enabledStr)
		if err != nil {
			return nil, fmt.Errorf("invalid NOTIFICATIONS_ENABLED: %w", err)
		}
		cfg.NotificationsEnabled = enabled
	}

	cfg.NotificationsStream = envOrDefault("NOTIFICATIONS_STREAM", "NOTIFICATIONS")
	cfg.NotificationsSubjectGlob = envOrDefault("NOTIFICATIONS_SUBJECT_GLOB", "alerts.dispatch.>")
	cfg.NotificationsConsumerName = envOrDefault("NOTIFICATIONS_CONSUMER_NAME", "notifications-worker")

	// AI root cause analysis — provider-agnostic LLM, wired by env. The
	// consumer self-disables when LLM_BASE_URL is unset.
	cfg.AIRCAStream = envOrDefault("AI_RCA_STREAM", "AI_RCA")
	cfg.AIRCASubject = envOrDefault("AI_RCA_SUBJECT", "ai.rca.jobs")
	cfg.AIRCAConsumerName = envOrDefault("AI_RCA_CONSUMER_NAME", "ai-rca-workers")
	cfg.LLMProvider = envOrDefault("LLM_PROVIDER", "openai_compat")
	cfg.LLMBaseURL = strings.TrimSpace(os.Getenv("LLM_BASE_URL"))
	cfg.LLMAPIKey = strings.TrimSpace(os.Getenv("LLM_API_KEY"))
	cfg.LLMModel = strings.TrimSpace(os.Getenv("LLM_MODEL"))
	cfg.LLMJSONMode = strings.TrimSpace(os.Getenv("LLM_JSON_MODE"))
	cfg.LLMMaxTokens = 1024
	if v := strings.TrimSpace(os.Getenv("LLM_MAX_TOKENS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid LLM_MAX_TOKENS: %q", v)
		}
		cfg.LLMMaxTokens = n
	}
	cfg.LLMTimeoutSeconds = 60
	if v := strings.TrimSpace(os.Getenv("LLM_TIMEOUT_SECONDS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid LLM_TIMEOUT_SECONDS: %q", v)
		}
		cfg.LLMTimeoutSeconds = n
	}

	// SMTP — only required when the email plugin is registered AND the
	// notifications consumer is wired in. Otherwise these stay empty and the
	// email plugin Send() returns an explicit error.
	cfg.SMTPHost = os.Getenv("SMTP_HOST")
	smtpPortStr := os.Getenv("SMTP_PORT")
	if smtpPortStr == "" {
		cfg.SMTPPort = 587
	} else {
		port, err := strconv.Atoi(smtpPortStr)
		if err != nil {
			return nil, fmt.Errorf("invalid SMTP_PORT: %w", err)
		}
		cfg.SMTPPort = port
	}
	cfg.SMTPUsername = os.Getenv("SMTP_USERNAME")
	cfg.SMTPPassword = os.Getenv("SMTP_PASSWORD")
	cfg.SMTPFrom = os.Getenv("SMTP_FROM")
	if cfg.SMTPFrom == "" {
		cfg.SMTPFrom = cfg.SMTPUsername
	}
	useTLSStr := os.Getenv("SMTP_USE_TLS")
	if useTLSStr == "" {
		cfg.SMTPUseTLS = true
	} else {
		useTLS, err := strconv.ParseBool(useTLSStr)
		if err != nil {
			return nil, fmt.Errorf("invalid SMTP_USE_TLS: %w", err)
		}
		cfg.SMTPUseTLS = useTLS
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// LoadAlerterConfig loads alerter service configuration
func LoadAlerterConfig() (*AlerterConfig, error) {
	base, err := LoadBaseConfig("alerter")
	if err != nil {
		return nil, err
	}

	cfg := &AlerterConfig{BaseConfig: *base}

	// ALERT_STREAM
	alertStream := os.Getenv("ALERT_STREAM")
	if alertStream == "" {
		cfg.AlertStream = "ALERTS"
	} else {
		cfg.AlertStream = alertStream
	}

	// ALERT_SUBJECT
	alertSubject := os.Getenv("ALERT_SUBJECT")
	if alertSubject == "" {
		cfg.AlertSubject = "alerts"
	} else {
		cfg.AlertSubject = alertSubject
	}

	// ALERT_EVAL_INTERVAL_SECONDS
	evalIntervalStr := os.Getenv("ALERT_EVAL_INTERVAL_SECONDS")
	if evalIntervalStr == "" {
		cfg.AlertEvalIntervalSeconds = 30
	} else {
		val, err := strconv.Atoi(evalIntervalStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ALERT_EVAL_INTERVAL_SECONDS: %w", err)
		}
		cfg.AlertEvalIntervalSeconds = val
	}

	// ALERTER_LATENCY_ANOMALY_ENABLED (default true): kill-switch for the
	// latency anomaly detection step.
	cfg.LatencyAnomalyEnabled = true
	if v := os.Getenv("ALERTER_LATENCY_ANOMALY_ENABLED"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid ALERTER_LATENCY_ANOMALY_ENABLED: %w", err)
		}
		cfg.LatencyAnomalyEnabled = enabled
	}

	// ALERT_REMINDER_INTERVAL_SECONDS
	reminderIntervalStr := os.Getenv("ALERT_REMINDER_INTERVAL_SECONDS")
	if reminderIntervalStr == "" {
		cfg.AlertReminderIntervalSeconds = 3600
	} else {
		val, err := strconv.Atoi(reminderIntervalStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ALERT_REMINDER_INTERVAL_SECONDS: %w", err)
		}
		cfg.AlertReminderIntervalSeconds = val
	}

	// ALERT_GROUP_WINDOW_SECONDS
	groupWindowStr := os.Getenv("ALERT_GROUP_WINDOW_SECONDS")
	if groupWindowStr == "" {
		cfg.AlertGroupWindowSeconds = 60
	} else {
		val, err := strconv.Atoi(groupWindowStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ALERT_GROUP_WINDOW_SECONDS: %w", err)
		}
		cfg.AlertGroupWindowSeconds = val
	}

	// ALERT_GROUP_MAX_CHILDREN
	groupMaxChildrenStr := os.Getenv("ALERT_GROUP_MAX_CHILDREN")
	if groupMaxChildrenStr == "" {
		cfg.AlertGroupMaxChildren = 5
	} else {
		val, err := strconv.Atoi(groupMaxChildrenStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ALERT_GROUP_MAX_CHILDREN: %w", err)
		}
		cfg.AlertGroupMaxChildren = val
	}

	// SMTP_HOST
	cfg.SMTPHost = os.Getenv("SMTP_HOST")

	// SMTP_PORT
	smtpPortStr := os.Getenv("SMTP_PORT")
	if smtpPortStr == "" {
		cfg.SMTPPort = 587
	} else {
		smtpPort, err := strconv.Atoi(smtpPortStr)
		if err != nil {
			return nil, fmt.Errorf("invalid SMTP_PORT: %w", err)
		}
		cfg.SMTPPort = smtpPort
	}

	// SMTP_USERNAME
	cfg.SMTPUsername = os.Getenv("SMTP_USERNAME")

	// SMTP_PASSWORD
	cfg.SMTPPassword = os.Getenv("SMTP_PASSWORD")

	// SMTP_FROM
	cfg.SMTPFrom = os.Getenv("SMTP_FROM")
	if cfg.SMTPFrom == "" {
		cfg.SMTPFrom = cfg.SMTPUsername
	}

	// SMTP_USE_TLS
	smtpUseTLSStr := os.Getenv("SMTP_USE_TLS")
	if smtpUseTLSStr == "" {
		cfg.SMTPUseTLS = true
	} else {
		useTLS, err := strconv.ParseBool(smtpUseTLSStr)
		if err != nil {
			return nil, fmt.Errorf("invalid SMTP_USE_TLS: %w", err)
		}
		cfg.SMTPUseTLS = useTLS
	}

	// ALERT_EMAIL_TO
	cfg.AlertEmailTo = os.Getenv("ALERT_EMAIL_TO")

	// ALERTER_ASYNC_DISPATCH — when true, dispatchNotifications publishes to
	// NATS rather than calling plugin.Send inline. Defaults to false so
	// rollout is opt-in per environment.
	asyncStr := strings.TrimSpace(os.Getenv("ALERTER_ASYNC_DISPATCH"))
	if asyncStr != "" {
		async, err := strconv.ParseBool(asyncStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ALERTER_ASYNC_DISPATCH: %w", err)
		}
		cfg.AsyncDispatch = async
	}

	cfg.NotificationsStream = envOrDefault("NOTIFICATIONS_STREAM", "NOTIFICATIONS")
	cfg.NotificationsSubjectGlob = envOrDefault("NOTIFICATIONS_SUBJECT_GLOB", "alerts.dispatch.>")

	return cfg, nil
}

// LoadStatusPageConfig loads status page service configuration
func LoadStatusPageConfig() (*StatusPageConfig, error) {
	base, err := LoadBaseConfig("status-page")
	if err != nil {
		return nil, err
	}

	cfg := &StatusPageConfig{BaseConfig: *base}

	// STATUS_PAGE_BASE_URL
	cfg.StatusPageBaseURL = os.Getenv("STATUS_PAGE_BASE_URL")

	// STATUS_PAGE_API_BASE_URL (optional; enables /_sp_api/* proxy for the in-page customizer)
	cfg.APIBaseURL = os.Getenv("STATUS_PAGE_API_BASE_URL")

	// HTTP server timeouts (seconds). Optional; defaults are generous so a large
	// status page never trips the timeout while page generation itself is the real fix.
	cfg.ReadTimeout = envDurationSeconds("STATUS_PAGE_READ_TIMEOUT_SECONDS", 15*time.Second)
	cfg.WriteTimeout = envDurationSeconds("STATUS_PAGE_WRITE_TIMEOUT_SECONDS", 60*time.Second)

	return cfg, nil
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

// API service configuration, including OIDC sign-in.

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yassinebenameur/probara/shared/ai"
)

// APIConfig contains configuration for the API service
type APIConfig struct {
	BaseConfig
	SMTPConfig
	NotificationEgressConfig
	AlertStream        string
	AlertSubject       string
	AlertConsumerName  string
	CheckJobSubject    string
	CheckJobStream     string
	CheckResultSubject string
	AIRCASubject       string
	LocalAI            ai.Config
	// AITriageEnabled is AI_TRIAGE_ENABLED (default true). The API never
	// publishes triage jobs; it reads the kill switch so SLA reports do not
	// list alerts as "Triage pending" while the relay claims nothing.
	AITriageEnabled       bool
	AdminJWTSecret        string
	AdminAccessTTLMinutes int
	AdminRefreshTTLDays   int
	AdminCookieSecure     bool
	AdminBcryptCost       int
	// AuditRetentionDays controls how long audit_log rows are kept.
	// 0 disables pruning (keep forever). Audit retention is deliberately
	// separate from tenants.data_retention_days: that governs monitoring
	// telemetry, while audit data is compliance data with opposite pressure.
	AuditRetentionDays int
	// OIDC SSO (platform-level: one IdP per install). Read through this
	// struct so a per-tenant DB-backed loader can be swapped in later.
	OIDC                  OIDCConfig
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
	// NATSLocationAuthIssuerSeed enables the API-hosted NATS authorization
	// callout used to issue per-location broker permissions.
	NATSLocationAuthIssuerSeed string
	// Mesh knobs mirrored from the scheduler (same env vars) so edge
	// staleness and probe-now timeouts agree with the actual probe cadence.
	MeshProbeIntervalSeconds int
	MeshProbeTimeoutSeconds  int
	// OTLP ingest guardrails for agent monitors pushing OpenTelemetry
	// metrics: max distinct series per monitor (overflow points are rejected
	// via OTLP partial_success) and max export requests per monitor per
	// minute (excess gets 429 + Retry-After, which the collector retries).
	OTLPMaxSeriesPerMonitor int
	OTLPMonitorRatePerMin   int
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

	queues := loadCheckQueueConfig()
	cfg.CheckJobSubject = queues.JobSubject
	cfg.CheckJobStream = queues.JobStream
	cfg.CheckResultSubject = queues.ResultSubject

	// AI_RCA_SUBJECT — subject the API publishes AI root cause jobs to (the
	// worker consumes them). Must match the worker's AI_RCA_SUBJECT.
	cfg.AIRCASubject = envOrDefault("AI_RCA_SUBJECT", "ai.rca.jobs")

	cfg.MeshProbeIntervalSeconds = 30
	if v := os.Getenv("MESH_PROBE_INTERVAL_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MESH_PROBE_INTERVAL_SECONDS: %q", v)
		}
		cfg.MeshProbeIntervalSeconds = n
	}
	cfg.MeshProbeTimeoutSeconds = 5
	if v := os.Getenv("MESH_PROBE_TIMEOUT_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MESH_PROBE_TIMEOUT_SECONDS: %q", v)
		}
		cfg.MeshProbeTimeoutSeconds = n
	}

	cfg.LocalAI, err = loadLocalAIConfig()
	if err != nil {
		return nil, err
	}
	cfg.AITriageEnabled, err = loadAITriageEnabled()
	if err != nil {
		return nil, err
	}

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

	// AUDIT_RETENTION_DAYS (default 365; 0 = keep forever)
	cfg.AuditRetentionDays = 365
	if v := strings.TrimSpace(os.Getenv("AUDIT_RETENTION_DAYS")); v != "" {
		days, err := strconv.Atoi(v)
		if err != nil || days < 0 {
			return nil, fmt.Errorf("invalid AUDIT_RETENTION_DAYS: %q", v)
		}
		cfg.AuditRetentionDays = days
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
	cfg.NATSLocationAuthIssuerSeed = strings.TrimSpace(os.Getenv("NATS_LOCATION_AUTH_ISSUER_SEED"))
	if cfg.PublicNATSURL != "" && cfg.NATSLocationAuthIssuerSeed == "" {
		return nil, fmt.Errorf("NATS_LOCATION_AUTH_ISSUER_SEED is required when PUBLIC_NATS_URL is set")
	}
	if cfg.NATSLocationAuthIssuerSeed != "" && strings.TrimSpace(os.Getenv("PROBARA_SECRETS_KEY")) == "" {
		return nil, fmt.Errorf("PROBARA_SECRETS_KEY is required when NATS location authorization is enabled")
	}

	// OTLP_* — ingest guardrails for the OTel metrics endpoint.
	cfg.OTLPMaxSeriesPerMonitor = 2000
	if v := strings.TrimSpace(os.Getenv("OTLP_MAX_SERIES_PER_MONITOR")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid OTLP_MAX_SERIES_PER_MONITOR: %q", v)
		}
		cfg.OTLPMaxSeriesPerMonitor = n
	}
	cfg.OTLPMonitorRatePerMin = 60
	if v := strings.TrimSpace(os.Getenv("OTLP_MONITOR_RATE_PER_MIN")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid OTLP_MONITOR_RATE_PER_MIN: %q", v)
		}
		cfg.OTLPMonitorRatePerMin = n
	}

	// OIDC_* — platform-level SSO configuration (needs PublicBaseURL for the
	// default redirect URL, so it loads last).
	oidcCfg, err := loadOIDCConfig(cfg.PublicBaseURL)
	if err != nil {
		return nil, err
	}
	cfg.OIDC = *oidcCfg

	// SMTP_* — the API needs the transport for POST
	// /alert-channels/{id}/test on email channels. Unset means the test
	// endpoint reports email as not configured; alert delivery itself is
	// unaffected (that runs in the alerter).
	smtpCfg, err := loadSMTPConfig()
	if err != nil {
		return nil, err
	}
	cfg.SMTPConfig = smtpCfg
	egressCfg, err := loadNotificationEgressConfig()
	if err != nil {
		return nil, err
	}
	cfg.NotificationEgressConfig = egressCfg

	return cfg, nil
}

// OIDCConfig is the platform-level OIDC SSO configuration.
type OIDCConfig struct {
	Enabled      bool
	IssuerURL    string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	// GroupsClaim is the ID-token claim read for group→role mappings. The
	// scope that makes the IdP emit it stays operator-managed via OIDC_SCOPES.
	GroupsClaim string
	// ProviderLabel is shown on the login button ("Continue with <label>").
	ProviderLabel string
	// JITProvision creates users on first OIDC login when no matching
	// account exists. Disable to require pre-created (invited) users.
	JITProvision       bool
	JITDefaultRole     string
	JITDefaultTenantID string
}

func loadOIDCConfig(publicBaseURL string) (*OIDCConfig, error) {
	cfg := &OIDCConfig{}

	if v := strings.TrimSpace(os.Getenv("OIDC_ENABLED")); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid OIDC_ENABLED: %q", v)
		}
		cfg.Enabled = enabled
	}
	if !cfg.Enabled {
		return cfg, nil
	}

	cfg.IssuerURL = strings.TrimSpace(os.Getenv("OIDC_ISSUER_URL"))
	cfg.ClientID = strings.TrimSpace(os.Getenv("OIDC_CLIENT_ID"))
	cfg.ClientSecret = strings.TrimSpace(os.Getenv("OIDC_CLIENT_SECRET"))
	if cfg.IssuerURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, fmt.Errorf("OIDC_ISSUER_URL, OIDC_CLIENT_ID and OIDC_CLIENT_SECRET are required when OIDC_ENABLED=true")
	}

	cfg.RedirectURL = strings.TrimSpace(os.Getenv("OIDC_REDIRECT_URL"))
	if cfg.RedirectURL == "" {
		if publicBaseURL == "" {
			return nil, fmt.Errorf("OIDC_REDIRECT_URL is required when PUBLIC_BASE_URL is not set")
		}
		cfg.RedirectURL = publicBaseURL + "/api/v1/auth/oidc/callback"
	}

	scopes := envOrDefault("OIDC_SCOPES", "openid profile email")
	cfg.Scopes = strings.Fields(scopes)

	cfg.GroupsClaim = envOrDefault("OIDC_GROUPS_CLAIM", "groups")

	cfg.ProviderLabel = envOrDefault("OIDC_PROVIDER_LABEL", "SSO")

	cfg.JITProvision = true
	if v := strings.TrimSpace(os.Getenv("OIDC_JIT_PROVISION")); v != "" {
		jit, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid OIDC_JIT_PROVISION: %q", v)
		}
		cfg.JITProvision = jit
	}
	cfg.JITDefaultRole = envOrDefault("OIDC_JIT_DEFAULT_ROLE", "viewer")
	switch cfg.JITDefaultRole {
	case "admin", "editor", "viewer":
	default:
		return nil, fmt.Errorf("invalid OIDC_JIT_DEFAULT_ROLE: %q", cfg.JITDefaultRole)
	}
	cfg.JITDefaultTenantID = envOrDefault("OIDC_JIT_DEFAULT_TENANT_ID", "00000000-0000-0000-0000-000000000001")

	return cfg, nil
}

// Worker service configuration.

package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/yassinebenameur/probara/shared/ai"

	"github.com/google/uuid"
)

// WorkerConfig contains configuration for the worker service
type WorkerConfig struct {
	BaseConfig
	// SMTP backend wired into the builtin email plugin (worker side, used by
	// the notifications consumer).
	SMTPConfig
	NotificationEgressConfig
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
	LocationCredential    string
	MaxHTTPTimeoutSeconds int
	MaxBodySizeBytes      int
	HTTPBlockPrivateIPs   bool
	HTTPAllowedCIDRs      []*net.IPNet
	SyntheticArtifactsDir string

	// SIPCallEnabled lets this worker place SIP test calls (INVITE monitors:
	// real, possibly billed, calls). SIP_CALL_ENABLED; defaults to true on
	// platform workers and false on private-location workers (LocationID
	// set), so a customer-run worker places no call until its operator opts
	// in.
	SIPCallEnabled bool
	// SIPCallAllowedDestinations, when non-empty, limits test calls to
	// Request-URI user parts (the called number) matching one of these
	// patterns, each anchored to the whole user part.
	// SIP_CALL_ALLOWED_DESTINATIONS, comma-separated regular expressions; an
	// invalid one fails startup.
	SIPCallAllowedDestinations []*regexp.Regexp

	// Installation-owned local inference configuration; private workers receive none.
	AIRCAStream       string
	AIRCASubject      string
	AIRCAConsumerName string
	LocalAI           ai.Config

	// Automatic alert triage (worker/internal/airca relay and triage
	// consumer). Read only by DB-connected platform workers; the API never
	// publishes triage jobs and reads only the kill switch
	// (APIConfig.AITriageEnabled). Triage never changes
	// paging; the alerter honours the same kill switch for its opt-in
	// suspected-cause follow-ups (AlerterConfig.SuspectedCausesDisabled).
	AITriageEnabled      bool   // AI_TRIAGE_ENABLED, default true; kill switch: no new claims, queued rows drain as skipped
	AITriageStream       string // AI_TRIAGE_STREAM, default "AI_TRIAGE"
	AITriageSubject      string // AI_TRIAGE_SUBJECT, default "ai.triage.jobs"
	AITriageConsumerName string // AI_TRIAGE_CONSUMER_NAME, default "ai-triage-workers"

	// Notification dispatch consumer settings — only used when the alerter is
	// publishing to the NOTIFICATIONS stream (ALERTER_ASYNC_DISPATCH=true).
	NotificationsEnabled      bool
	NotificationsStream       string
	NotificationsSubjectGlob  string
	NotificationsConsumerName string
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

	queues := loadCheckQueueConfig()
	cfg.CheckJobSubject = queues.JobSubject
	cfg.CheckJobStream = queues.JobStream

	// Results publishing — workers publish results here instead of writing
	// Postgres, so remote location workers only need NATS reachability.
	cfg.CheckResultStream = queues.ResultStream
	cfg.CheckResultSubject = queues.ResultSubject

	// WORKER_LOCATION_ID — pins this worker to a private location (UUID from
	// the Locations page). Empty = default platform fleet.
	cfg.LocationID = strings.TrimSpace(os.Getenv("WORKER_LOCATION_ID"))
	if cfg.LocationID != "" {
		if _, err := uuid.Parse(cfg.LocationID); err != nil {
			return nil, fmt.Errorf("invalid WORKER_LOCATION_ID: %q is not a UUID", cfg.LocationID)
		}
	}
	cfg.LocationCredential = strings.TrimSpace(os.Getenv("LOCATION_CREDENTIAL"))
	if cfg.LocationID != "" && cfg.LocationCredential == "" {
		return nil, fmt.Errorf("LOCATION_CREDENTIAL is required when WORKER_LOCATION_ID is set")
	}
	if cfg.LocationID == "" && cfg.LocationCredential != "" {
		return nil, fmt.Errorf("LOCATION_CREDENTIAL requires WORKER_LOCATION_ID")
	}

	// SIP_CALL_ENABLED — the default depends on the location mode, so it is
	// read after WORKER_LOCATION_ID.
	cfg.SIPCallEnabled = cfg.LocationID == ""
	if v := strings.TrimSpace(os.Getenv("SIP_CALL_ENABLED")); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid SIP_CALL_ENABLED: %w", err)
		}
		cfg.SIPCallEnabled = enabled
	}
	allowed, err := parseSIPCallAllowedDestinations(os.Getenv("SIP_CALL_ALLOWED_DESTINATIONS"))
	if err != nil {
		return nil, err
	}
	cfg.SIPCallAllowedDestinations = allowed

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

	cfg.AIRCAStream = envOrDefault("AI_RCA_STREAM", "AI_RCA")
	cfg.AIRCASubject = envOrDefault("AI_RCA_SUBJECT", "ai.rca.jobs")
	cfg.AIRCAConsumerName = envOrDefault("AI_RCA_CONSUMER_NAME", "ai-rca-workers")
	cfg.LocalAI, err = loadLocalAIConfig()
	if err != nil {
		return nil, err
	}

	cfg.AITriageEnabled, err = loadAITriageEnabled()
	if err != nil {
		return nil, err
	}
	cfg.AITriageStream = envOrDefault("AI_TRIAGE_STREAM", "AI_TRIAGE")
	cfg.AITriageSubject = envOrDefault("AI_TRIAGE_SUBJECT", "ai.triage.jobs")
	cfg.AITriageConsumerName = envOrDefault("AI_TRIAGE_CONSUMER_NAME", "ai-triage-workers")
	// Sharing a stream or subject would hand triage jobs to the RCA consumer
	// (and the reverse), which drops them as unknown payloads.
	if cfg.AITriageStream == cfg.AIRCAStream || cfg.AITriageSubject == cfg.AIRCASubject {
		return nil, fmt.Errorf("AI_TRIAGE_STREAM/AI_TRIAGE_SUBJECT must differ from AI_RCA_STREAM/AI_RCA_SUBJECT")
	}

	// SMTP — only required when the email plugin is registered AND the
	// notifications consumer is wired in. Otherwise these stay empty and the
	// email plugin Send() returns an explicit error.
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

// parseSIPCallAllowedDestinations compiles SIP_CALL_ALLOWED_DESTINATIONS:
// comma-separated patterns, each anchored so it must match the whole called
// user part (`\+33.*` allows French numbers, never one that merely contains
// them). Empty allows every destination.
func parseSIPCallAllowedDestinations(raw string) ([]*regexp.Regexp, error) {
	var patterns []*regexp.Regexp
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		re, err := regexp.Compile(`^(?:` + p + `)$`)
		if err != nil {
			return nil, fmt.Errorf("invalid SIP_CALL_ALLOWED_DESTINATIONS entry %q: %w", p, err)
		}
		patterns = append(patterns, re)
	}
	return patterns, nil
}

package worker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
)

const vaultMaxResponseBytes = 256 * 1024

// VaultChecker implements the `vault` monitor type. See
// models.VaultMonitorConfig for why the check is two requests: sys/health is
// never audited, so only an authenticated request can observe Vault failing
// closed on a broken audit device.
type VaultChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
}

func NewVaultChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *VaultChecker {
	return &VaultChecker{blockPrivateIPs: blockPrivateIPs, allowedCIDRs: allowedCIDRs}
}

// vaultHealth is the sys/health body. Vault returns it with every health
// status code (200 active, 429 standby, 472 DR secondary, 473 performance
// standby, 501 uninitialized, 503 sealed, 530 removed from cluster), so the
// checker evaluates the body rather than the code.
type vaultHealth struct {
	Initialized                bool   `json:"initialized"`
	Sealed                     bool   `json:"sealed"`
	Standby                    bool   `json:"standby"`
	PerformanceStandby         bool   `json:"performance_standby"`
	ReplicationPerformanceMode string `json:"replication_performance_mode,omitempty"`
	ReplicationDRMode          string `json:"replication_dr_mode,omitempty"`
	Version                    string `json:"version,omitempty"`
	ClusterName                string `json:"cluster_name,omitempty"`
	RemovedFromCluster         *bool  `json:"removed_from_cluster,omitempty"`
}

type vaultTokenProbe struct {
	HTTPStatus int    `json:"http_status"`
	LatencyMs  int64  `json:"latency_ms"`
	OK         bool   `json:"ok"`
	TTLSeconds *int64 `json:"ttl_seconds,omitempty"`
	ExpireTime string `json:"expire_time,omitempty"`
	Renewable  *bool  `json:"renewable,omitempty"`
}

type vaultMetrics struct {
	HealthHTTPStatus int              `json:"health_http_status,omitempty"`
	HealthLatencyMs  int64            `json:"health_latency_ms"`
	Role             string           `json:"role,omitempty"` // active | standby | performance_standby | dr_secondary
	Initialized      *bool            `json:"initialized,omitempty"`
	Sealed           *bool            `json:"sealed,omitempty"`
	Version          string           `json:"version,omitempty"`
	ClusterName      string           `json:"cluster_name,omitempty"`
	ReplicationPerf  string           `json:"replication_performance_mode,omitempty"`
	ReplicationDR    string           `json:"replication_dr_mode,omitempty"`
	TokenProbe       *vaultTokenProbe `json:"token_probe,omitempty"`
}

func (c *VaultChecker) Check(ctx context.Context, raw json.RawMessage, timeoutSeconds int) CheckResult {
	start := time.Now()
	metrics := &vaultMetrics{}
	result := func(status, message string, withMetrics bool) CheckResult {
		latency := time.Since(start).Milliseconds()
		r := CheckResult{Status: status, LatencyMs: &latency}
		if message != "" {
			r.ErrorMessage = &message
		}
		if withMetrics {
			r.MetricsData, _ = json.Marshal(map[string]any{"vault": metrics})
		}
		if metrics.HealthHTTPStatus != 0 {
			code := metrics.HealthHTTPStatus
			r.HTTPStatus = &code
		}
		return r
	}

	var cfg models.VaultMonitorConfig
	if json.Unmarshal(raw, &cfg) != nil {
		return result("error", "invalid vault config", false)
	}
	if err := cfg.Validate(); err != nil {
		return result("error", err.Error(), false)
	}
	if timeoutSeconds <= 0 {
		return result("error", "timeout must be positive", false)
	}
	// A masked placeholder means the secret was never resolved for this job;
	// sending it would authenticate with the literal string.
	if cfg.Token == "***" {
		return result("error", "vault token was not resolved", false)
	}
	if pemValue(cfg.TLSClientKeyPem) == "***" {
		return result("error", "TLS client key was not resolved", false)
	}
	if cfg.TokenMinTTLSeconds != nil && *cfg.TokenMinTTLSeconds > 0 && cfg.Token == "" {
		return result("error", "token_min_ttl_seconds requires a token", false)
	}

	base, _ := url.Parse(cfg.URL)
	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var tlsCfg *tls.Config
	if base.Scheme == "https" {
		serverName := base.Hostname()
		if cfg.TLSServerName != nil && *cfg.TLSServerName != "" {
			serverName = *cfg.TLSServerName
		}
		tlsCfg = &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
		var err error
		if tlsCfg, err = applyDBTLSMaterial(tlsCfg, cfg.DBTLSConfig, serverName); err != nil {
			return result("error", err.Error(), false)
		}
		if cfg.TLSSkipVerify != nil && *cfg.TLSSkipVerify {
			tlsCfg.InsecureSkipVerify = true
		}
	}
	// Direct guarded dialing prevents proxy bypass and DNS rebinding. Never
	// follow redirects: a standby without request forwarding answers with a
	// 307 to the active node, and following it would carry the token to
	// whatever host the Location header names.
	transport := &http.Transport{
		DialContext:         newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout).DialContext,
		TLSClientConfig:     tlsCfg,
		TLSHandshakeTimeout: timeout,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	endpoint := func(path string) string { return strings.TrimRight(cfg.URL, "/") + path }
	connErr := func(stage string, err error) CheckResult {
		if ctx.Err() != nil {
			return result("error", fmt.Sprintf("Vault %s timed out", stage), true)
		}
		var certErr *tls.CertificateVerificationError
		if errors.As(err, &certErr) {
			return result("error", fmt.Sprintf("Vault %s: TLS certificate verification failed (set a CA PEM or server name)", stage), true)
		}
		return result("error", fmt.Sprintf("Vault %s: connection failed (check address, TLS and worker network policy)", stage), true)
	}

	// Stage 1: sys/health. No token is sent; the endpoint is unauthenticated.
	healthStart := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint("/v1/sys/health"), nil)
	if err != nil {
		return result("error", "invalid Vault address", false)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return connErr("health check", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, vaultMaxResponseBytes))
	resp.Body.Close()
	metrics.HealthLatencyMs = time.Since(healthStart).Milliseconds()
	metrics.HealthHTTPStatus = resp.StatusCode
	var health vaultHealth
	if readErr != nil || json.Unmarshal(body, &health) != nil || !looksLikeVaultHealth(body) {
		return result("failure", fmt.Sprintf("sys/health returned HTTP %d without a Vault health body", resp.StatusCode), true)
	}
	metrics.Initialized = &health.Initialized
	metrics.Sealed = &health.Sealed
	metrics.Version = health.Version
	metrics.ClusterName = health.ClusterName
	metrics.ReplicationPerf = health.ReplicationPerformanceMode
	metrics.ReplicationDR = health.ReplicationDRMode
	metrics.Role = vaultRole(health)

	switch {
	case !health.Initialized:
		return result("failure", "Vault is not initialized", true)
	case health.Sealed:
		return result("failure", "Vault is sealed", true)
	case health.RemovedFromCluster != nil && *health.RemovedFromCluster:
		return result("failure", "node was removed from the Raft cluster", true)
	case cfg.RequireActive && metrics.Role != "active":
		return result("failure", fmt.Sprintf("node is a %s, not the active node (require_active)", strings.ReplaceAll(metrics.Role, "_", " ")), true)
	}

	// Stage 2: an audited, authenticated round trip.
	if cfg.Token != "" {
		probe := &vaultTokenProbe{}
		metrics.TokenProbe = probe
		probeStart := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint("/v1/auth/token/lookup-self"), nil)
		if err != nil {
			return result("error", "invalid Vault address", false)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Vault-Token", cfg.Token)
		if cfg.Namespace != "" {
			req.Header.Set("X-Vault-Namespace", cfg.Namespace)
		}
		resp, err := client.Do(req)
		if err != nil {
			return connErr("token probe", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, vaultMaxResponseBytes))
		resp.Body.Close()
		probe.LatencyMs = time.Since(probeStart).Milliseconds()
		probe.HTTPStatus = resp.StatusCode
		if readErr != nil {
			return result("error", "failed to read Vault token probe response", true)
		}
		if status, msg := vaultProbeVerdict(resp.StatusCode, body); status != "" {
			return result(status, msg, true)
		}
		probe.OK = true
		var lookup struct {
			Data struct {
				TTL        *int64 `json:"ttl"`
				ExpireTime string `json:"expire_time"`
				Renewable  *bool  `json:"renewable"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &lookup) == nil {
			probe.TTLSeconds = lookup.Data.TTL
			probe.ExpireTime = lookup.Data.ExpireTime
			probe.Renewable = lookup.Data.Renewable
		}
		if cfg.TokenMinTTLSeconds != nil && *cfg.TokenMinTTLSeconds > 0 && probe.TTLSeconds != nil &&
			*probe.TTLSeconds > 0 && *probe.TTLSeconds < *cfg.TokenMinTTLSeconds {
			return result("failure", fmt.Sprintf("monitor token expires in %s (minimum %s); renew or replace it before the monitor goes blind",
				time.Duration(*probe.TTLSeconds)*time.Second, time.Duration(*cfg.TokenMinTTLSeconds)*time.Second), true)
		}
	}

	if latency := time.Since(start).Milliseconds(); cfg.MaxLatencyMs != nil && *cfg.MaxLatencyMs > 0 && latency > *cfg.MaxLatencyMs {
		return result("failure", fmt.Sprintf("latency: %dms > %dms", latency, *cfg.MaxLatencyMs), true)
	}
	return result("success", "", true)
}

// looksLikeVaultHealth guards against a proxy or ingress answering for a dead
// Vault with some other JSON object: a real health body always carries the
// initialized and sealed keys.
func looksLikeVaultHealth(body []byte) bool {
	var keys map[string]json.RawMessage
	if json.Unmarshal(body, &keys) != nil {
		return false
	}
	_, hasInit := keys["initialized"]
	_, hasSealed := keys["sealed"]
	return hasInit && hasSealed
}

func vaultRole(h vaultHealth) string {
	switch {
	case h.ReplicationDRMode == "secondary":
		return "dr_secondary"
	case h.PerformanceStandby:
		return "performance_standby"
	case h.Standby:
		return "standby"
	default:
		return "active"
	}
}

// vaultProbeVerdict maps a lookup-self response to a result. An empty status
// means the probe passed.
func vaultProbeVerdict(code int, body []byte) (string, string) {
	detail := vaultErrorDetail(body)
	switch {
	case code == http.StatusOK:
		return "", ""
	case code == http.StatusInternalServerError:
		// Vault hides the cause from clients ("internal error"); a 500 here
		// while sys/health is healthy is the audit fail-closed signature.
		return "failure", "authenticated request failed with HTTP 500 while sys/health reports healthy — Vault blocks every audited request when no audit device can write (full disk, unwritable path or dead socket target); check the audit devices" + detail
	case code == http.StatusForbidden:
		return "error", "monitor token rejected (HTTP 403): it is invalid, expired, revoked or lacks auth/token/lookup-self" + detail
	case code == http.StatusServiceUnavailable:
		return "failure", "token probe returned HTTP 503 (sealed, or no active node reachable from this node)" + detail
	case code == http.StatusTooManyRequests:
		return "failure", "token probe was rate limited (HTTP 429): a Vault rate-limit quota is rejecting requests" + detail
	case code >= 300 && code < 400:
		return "error", fmt.Sprintf("token probe was redirected (HTTP %d): this standby does not forward requests; point the monitor at the active node or load balancer", code)
	default:
		return "failure", fmt.Sprintf("token probe returned HTTP %d", code) + detail
	}
}

// vaultErrorDetail extracts Vault's `errors` array as a short, single-line
// suffix. Vault error strings never contain the token.
func vaultErrorDetail(body []byte) string {
	var env struct {
		Errors []string `json:"errors"`
	}
	if json.Unmarshal(body, &env) != nil || len(env.Errors) == 0 {
		return ""
	}
	msg := strings.Join(strings.Fields(strings.Join(env.Errors, "; ")), " ")
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	if msg == "" {
		return ""
	}
	return " (Vault: " + msg + ")"
}

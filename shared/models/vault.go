package models

import (
	"fmt"
	"net/url"
	"strings"
)

// VaultMonitorConfig checks a HashiCorp Vault (or OpenBao) server in two
// stages:
//
//  1. GET /v1/sys/health — unauthenticated, reports initialized / sealed /
//     standby / replication state and the server version.
//  2. When `token` is set, GET /v1/auth/token/lookup-self with that token —
//     an authenticated request that passes through Vault's audit broker.
//
// Stage 2 exists because sys/health is never audited: when every enabled
// audit device fails (full disk under a file device, unwritable path, dead
// socket/syslog target) Vault fails closed and answers every authenticated
// request with HTTP 500, while sys/health keeps returning 200. An HTTP check
// on sys/health therefore stays green through a full outage. lookup-self is
// permitted by Vault's built-in `default` policy, so the token needs no
// extra grants. The same response carries the token's remaining TTL, which
// `token_min_ttl_seconds` uses to warn before the monitor goes blind.
//
// `token` and `tls_client_key_pem` are secret fields (see
// shared/secrets.MonitorSecretFields).
type VaultMonitorConfig struct {
	URL       string `json:"url"`
	Namespace string `json:"namespace,omitempty"` // Vault Enterprise / OpenBao namespace for the token probe
	Token     string `json:"token,omitempty"`

	// RequireActive fails the check when the node answers as a standby,
	// performance standby or DR secondary. Leave false when the URL points
	// at an individual cluster member; set it when the URL is the load
	// balancer address clients use and must always land on the active node.
	RequireActive bool `json:"require_active,omitempty"`

	// TokenMinTTLSeconds fails the check when the monitor token's remaining
	// TTL drops below this many seconds. Tokens without a TTL (ttl 0) are
	// never flagged. Requires `token`.
	TokenMinTTLSeconds *int64 `json:"token_min_ttl_seconds,omitempty"`

	MaxLatencyMs  *int64  `json:"max_latency_ms,omitempty"`
	TLSSkipVerify *bool   `json:"tls_skip_verify,omitempty"`
	TLSServerName *string `json:"tls_server_name,omitempty"`
	DBTLSConfig
}

func (c VaultMonitorConfig) Validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.TrimSpace(c.URL) != c.URL {
		return fmt.Errorf("url must be an HTTP(S) base URL without credentials, query parameters or fragment")
	}
	if len(c.URL) > 2048 {
		return fmt.Errorf("url must not exceed 2048 bytes")
	}
	if strings.Contains(strings.TrimRight(u.Path, "/"), "/v1/") || strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1") {
		return fmt.Errorf("url must be the Vault address without /v1 (e.g. https://vault.example.com:8200)")
	}
	if len(c.Namespace) > 255 || strings.ContainsAny(c.Namespace, "\r\n") || strings.TrimSpace(c.Namespace) != c.Namespace {
		return fmt.Errorf("namespace must be at most 255 bytes without surrounding whitespace or newlines")
	}
	if len(c.Token) > 4096 || strings.ContainsAny(c.Token, "\r\n ") {
		return fmt.Errorf("token must be at most 4096 bytes without whitespace")
	}
	if c.TokenMinTTLSeconds != nil && (*c.TokenMinTTLSeconds < 0 || *c.TokenMinTTLSeconds > 10*365*24*3600) {
		return fmt.Errorf("token_min_ttl_seconds must be between 0 and 315360000")
	}
	if c.MaxLatencyMs != nil && (*c.MaxLatencyMs < 0 || *c.MaxLatencyMs > 600000) {
		return fmt.Errorf("max_latency_ms must be between 0 and 600000")
	}
	if c.TLSServerName != nil && (len(*c.TLSServerName) > 253 || strings.ContainsAny(*c.TLSServerName, " \r\n/")) {
		return fmt.Errorf("tls_server_name must be a hostname")
	}
	if (u.Scheme == "http") && (c.TLSSkipVerify != nil && *c.TLSSkipVerify || c.TLSServerName != nil && *c.TLSServerName != "" ||
		vaultPEMSet(c.TLSCAPem) || vaultPEMSet(c.TLSClientCertPem)) {
		return fmt.Errorf("TLS options require an https url")
	}
	return nil
}

func vaultPEMSet(v *string) bool {
	return v != nil && strings.TrimSpace(*v) != ""
}

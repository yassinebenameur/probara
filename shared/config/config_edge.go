// Edge configuration: how the api and status-page resolve client addresses
// and which browser security headers and request limits they apply. Sole
// parser for these variables.

package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/yassinebenameur/probara/shared/clientip"
)

// EdgeConfig is shared by the api and status-page, the two services browsers
// reach directly.
type EdgeConfig struct {
	// TrustedProxyCIDRs is TRUSTED_PROXY_CIDRS: the reverse proxies whose
	// X-Forwarded-For / X-Real-IP / X-Forwarded-Proto are believed
	// (shared/clientip). Empty trusts nobody: the client is the TCP peer.
	TrustedProxyCIDRs []netip.Prefix
	// HSTSMaxAge is SECURITY_HSTS_MAX_AGE in seconds. 0 (the default) sends
	// no Strict-Transport-Security; when positive it is still sent only on
	// requests that arrived over HTTPS (shared/securityheaders).
	HSTSMaxAge int
}

// APIEdgeConfig holds the API-only edge controls.
type APIEdgeConfig struct {
	// RateLimitPerMinute and RateLimitBurst are API_RATE_LIMIT_PER_MINUTE
	// (default 600; 0 disables the limiter) and API_RATE_LIMIT_BURST
	// (default 120): the general token bucket, per credential and per API
	// replica.
	RateLimitPerMinute int
	RateLimitBurst     int
	// AllowedOrigins are the browser origins allowed credentialed
	// cross-origin calls: API_ALLOWED_ORIGINS, else the origin of
	// APP_BASE_URL, else none (same-origin only).
	AllowedOrigins []string
}

// loadEdgeConfig reads TRUSTED_PROXY_CIDRS and SECURITY_HSTS_MAX_AGE.
func loadEdgeConfig() (EdgeConfig, error) {
	var cfg EdgeConfig
	trusted, err := clientip.ParseTrustedProxies(os.Getenv("TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return cfg, fmt.Errorf("invalid TRUSTED_PROXY_CIDRS: %w", err)
	}
	cfg.TrustedProxyCIDRs = trusted
	if cfg.HSTSMaxAge, err = envNonNegativeInt("SECURITY_HSTS_MAX_AGE", 0); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// loadAPIEdgeConfig reads the API rate limit and CORS allowlist.
// appBaseURL is the already-parsed APP_BASE_URL.
func loadAPIEdgeConfig(appBaseURL string) (APIEdgeConfig, error) {
	var cfg APIEdgeConfig
	var err error
	if cfg.RateLimitPerMinute, err = envNonNegativeInt("API_RATE_LIMIT_PER_MINUTE", 600); err != nil {
		return cfg, err
	}
	if cfg.RateLimitBurst, err = envNonNegativeInt("API_RATE_LIMIT_BURST", 120); err != nil {
		return cfg, err
	}
	if cfg.AllowedOrigins, err = parseAllowedOrigins(os.Getenv("API_ALLOWED_ORIGINS"), appBaseURL); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// parseAllowedOrigins parses API_ALLOWED_ORIGINS: comma-separated exact
// origins (scheme://host[:port]), normalized to lowercase without a trailing
// slash. "*" is refused: credentialed CORS cannot mean "any origin". Unset,
// the operator UI origin (APP_BASE_URL) is the one allowed origin, and
// without that the API serves same-origin callers only.
func parseAllowedOrigins(raw, appBaseURL string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimRight(strings.TrimSpace(part), "/")
		if part == "" {
			continue
		}
		u, err := url.Parse(part)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, fmt.Errorf("invalid API_ALLOWED_ORIGINS entry %q: want scheme://host[:port]", part)
		}
		out = append(out, strings.ToLower(u.Scheme+"://"+u.Host))
	}
	if len(out) > 0 {
		return out, nil
	}
	if u, err := url.Parse(appBaseURL); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		return []string{strings.ToLower(u.Scheme + "://" + u.Host)}, nil
	}
	return nil, nil
}

// loadAppBaseURL reads APP_BASE_URL, the operator UI origin, without a
// trailing slash.
func loadAppBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("APP_BASE_URL")), "/")
}

// envNonNegativeInt reads an integer >= 0 from key, def when unset.
func envNonNegativeInt(key string, def int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid %s: %q", key, v)
	}
	return n, nil
}

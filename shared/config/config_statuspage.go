// Status-page service configuration.

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/clientip"
)

// StatusPageConfig contains configuration for the status page service
type StatusPageConfig struct {
	BaseConfig
	StatusPageBaseURL string
	APIBaseURL        string
	// HTTP server timeouts. These are a safety net only; the public status page
	// is expected to render well within them. See LoadStatusPageConfig for defaults.
	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	// VAPID keypair for visitor browser notifications (RFC 8292). Both halves
	// must be set for the feature to be live; a page's enable_push_notifications
	// setting alone does nothing without them.
	//
	// The public key is NOT a credential -- it ships inside every rendered page
	// as the applicationServerKey. The private key signs the push JWT.
	//
	// Never generate these at startup: with more than one status-page replica
	// each would mint a different pair, so a subscription created against one
	// replica is unusable by another, and a restart would silently invalidate
	// every stored subscription.
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	// VAPIDSubject is the JWT "sub" claim: a mailto: or https: contact the push
	// service can reach. Some push services reject a missing or malformed one.
	VAPIDSubject string

	// Client IP resolution (push rate limit) and HSTS (config_edge.go).
	EdgeConfig
	// LegacyTrustedProxy reports that the deprecated
	// STATUS_PAGE_TRUSTED_PROXY=true stood in for an unset
	// TRUSTED_PROXY_CIDRS, so TrustedProxyCIDRs trusts every peer (its old
	// leftmost-X-Forwarded-For behavior). The service logs a warning.
	LegacyTrustedProxy bool
	// AppBaseURL is APP_BASE_URL, the operator UI origin. Every page's CSP
	// frame-ancestors admits it so the template editor can preview pages.
	AppBaseURL string
	// SessionSecret is STATUS_PAGE_SESSION_SECRET, the key that signs visitor
	// sessions on password and private pages. Optional: without it those
	// pages cannot be opened (fail closed) while public and unlisted pages
	// serve as usual. Shorter than MinStatusPageSessionSecretLength refuses
	// to start rather than leaving gated pages quietly unavailable.
	SessionSecret string
	// CookieSecure is STATUS_PAGE_COOKIE_SECURE: nil when unset. See
	// SessionCookieSecure.
	CookieSecure *bool
	// InternalAPIURL is where the service forwards visitors' email
	// subscription requests (the API stores subscribers; this service stays
	// read-only): STATUS_PAGE_INTERNAL_API_URL, else STATUS_PAGE_API_BASE_URL.
	// Empty means no page shows a subscribe form.
	InternalAPIURL string
	// InternalToken is STATUS_PAGE_INTERNAL_TOKEN: the service presents it
	// on every forwarded subscription request, and the API refuses the
	// subscribe and confirm routes without it. Empty: no page shows a
	// subscribe form.
	InternalToken string
}

// MinStatusPageInternalTokenLength is the shortest STATUS_PAGE_INTERNAL_TOKEN
// accepted.
const MinStatusPageInternalTokenLength = 32

// loadStatusPageInternalToken reads STATUS_PAGE_INTERNAL_TOKEN, shared by the
// api and status-page services, refusing one too short to be a secret.
func loadStatusPageInternalToken() (string, error) {
	token := strings.TrimSpace(os.Getenv("STATUS_PAGE_INTERNAL_TOKEN"))
	if token != "" && len(token) < MinStatusPageInternalTokenLength {
		return "", fmt.Errorf("STATUS_PAGE_INTERNAL_TOKEN must be at least %d characters", MinStatusPageInternalTokenLength)
	}
	return token, nil
}

// SessionCookieSecure decides the visitor session cookie's Secure flag, like
// the API's ADMIN_COOKIE_SECURE: STATUS_PAGE_COOKIE_SECURE when set, else on
// when STATUS_PAGE_BASE_URL is https. detect is true when neither decides,
// and the service then sets it exactly for requests that arrived over HTTPS
// (which behind a TLS-terminating proxy needs TRUSTED_PROXY_CIDRS).
func (c *StatusPageConfig) SessionCookieSecure() (secure, detect bool) {
	if c == nil {
		return false, true
	}
	if c.CookieSecure != nil {
		return *c.CookieSecure, false
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.StatusPageBaseURL)), "https://") {
		return true, false
	}
	return false, true
}

// MinStatusPageSessionSecretLength is the shortest STATUS_PAGE_SESSION_SECRET
// accepted.
const MinStatusPageSessionSecretLength = 32

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

	// Web Push (optional; unset means the feature is off everywhere).
	cfg.VAPIDPublicKey = strings.TrimSpace(os.Getenv("STATUS_PAGE_VAPID_PUBLIC_KEY"))
	cfg.VAPIDPrivateKey = strings.TrimSpace(os.Getenv("STATUS_PAGE_VAPID_PRIVATE_KEY"))
	cfg.VAPIDSubject = strings.TrimSpace(os.Getenv("STATUS_PAGE_VAPID_SUBJECT"))

	if cfg.EdgeConfig, err = loadEdgeConfig(); err != nil {
		return nil, err
	}
	// STATUS_PAGE_TRUSTED_PROXY=true predates TRUSTED_PROXY_CIDRS and
	// believed X-Forwarded-For from any peer. Honored only while the new
	// variable is unset, so an upgrade keeps per-visitor push rate limiting.
	if len(cfg.TrustedProxyCIDRs) == 0 && strings.EqualFold(strings.TrimSpace(os.Getenv("STATUS_PAGE_TRUSTED_PROXY")), "true") {
		cfg.TrustedProxyCIDRs = clientip.TrustAll()
		cfg.LegacyTrustedProxy = true
	}
	cfg.AppBaseURL = loadAppBaseURL()

	if raw := strings.TrimSpace(os.Getenv("STATUS_PAGE_COOKIE_SECURE")); raw != "" {
		secure, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid STATUS_PAGE_COOKIE_SECURE: %w", err)
		}
		cfg.CookieSecure = &secure
	}

	// A separate variable from STATUS_PAGE_API_BASE_URL, which also turns
	// on the /_sp_api customizer proxy: an install can forward subscriptions
	// to the API without exposing that proxy.
	cfg.InternalAPIURL = strings.TrimRight(strings.TrimSpace(os.Getenv("STATUS_PAGE_INTERNAL_API_URL")), "/")
	if cfg.InternalAPIURL == "" {
		cfg.InternalAPIURL = strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	}

	if cfg.InternalToken, err = loadStatusPageInternalToken(); err != nil {
		return nil, err
	}

	cfg.SessionSecret = strings.TrimSpace(os.Getenv("STATUS_PAGE_SESSION_SECRET"))
	if cfg.SessionSecret != "" && len(cfg.SessionSecret) < MinStatusPageSessionSecretLength {
		return nil, fmt.Errorf("STATUS_PAGE_SESSION_SECRET must be at least %d characters", MinStatusPageSessionSecretLength)
	}

	return cfg, nil
}

// WebPushConfigured reports whether the deployment can send browser
// notifications. Both key halves are required; the subject is validated by the
// sender rather than here so a bad value fails loudly at send time instead of
// silently disabling the feature at boot.
func (c *StatusPageConfig) WebPushConfigured() bool {
	return c != nil && c.VAPIDPublicKey != "" && c.VAPIDPrivateKey != ""
}

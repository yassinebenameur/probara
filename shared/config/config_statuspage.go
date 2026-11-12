// Status-page service configuration.

package config

import (
	"os"
	"strings"
	"time"
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

	// Web Push (optional; unset means the feature is off everywhere).
	cfg.VAPIDPublicKey = strings.TrimSpace(os.Getenv("STATUS_PAGE_VAPID_PUBLIC_KEY"))
	cfg.VAPIDPrivateKey = strings.TrimSpace(os.Getenv("STATUS_PAGE_VAPID_PRIVATE_KEY"))
	cfg.VAPIDSubject = strings.TrimSpace(os.Getenv("STATUS_PAGE_VAPID_SUBJECT"))

	return cfg, nil
}

// WebPushConfigured reports whether the deployment can send browser
// notifications. Both key halves are required; the subject is validated by the
// sender rather than here so a bad value fails loudly at send time instead of
// silently disabling the feature at boot.
func (c *StatusPageConfig) WebPushConfigured() bool {
	return c != nil && c.VAPIDPublicKey != "" && c.VAPIDPrivateKey != ""
}

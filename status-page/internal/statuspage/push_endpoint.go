package statuspage

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// defaultPushEndpointHosts is the set of push services a subscription may
// point at, as host suffixes.
//
// This allowlist is the single most important control on the subscribe
// endpoint. The sender later POSTs to whatever URL was stored, from inside
// the cluster, on a schedule -- so without it an unauthenticated visitor
// could turn the subscriptions table into a stored-SSRF primitive and an
// outbound spam relay, with the platform doing the sending.
//
// Suffixes are matched on a dot boundary (see hostAllowed), so
// "fcm.googleapis.com.evil.com" does not match "fcm.googleapis.com".
var defaultPushEndpointHosts = []string{
	"fcm.googleapis.com",        // Chrome, Edge, and other Chromium browsers
	"push.services.mozilla.com", // Firefox
	"notify.windows.com",        // Windows / legacy Edge
	"wns.windows.com",           // Windows
	"push.apple.com",            // Safari, macOS and iOS
}

// pushEndpointAllowlistEnvVar overrides the built-in host list, as a
// comma-separated set of host suffixes. A single "*" disables the check
// entirely, which is only appropriate for a self-hosted push service on a
// trusted network -- it re-opens the SSRF surface described above.
const pushEndpointAllowlistEnvVar = "STATUS_PAGE_PUSH_ENDPOINT_ALLOWLIST"

// pushEndpointHosts returns the configured allowlist.
func pushEndpointHosts() []string {
	raw := strings.TrimSpace(os.Getenv(pushEndpointAllowlistEnvVar))
	if raw == "" {
		return defaultPushEndpointHosts
	}
	var hosts []string
	for _, part := range strings.Split(raw, ",") {
		if h := strings.ToLower(strings.TrimSpace(part)); h != "" {
			hosts = append(hosts, h)
		}
	}
	if len(hosts) == 0 {
		return defaultPushEndpointHosts
	}
	return hosts
}

// validatePushEndpoint checks a browser-supplied endpoint before it is stored.
//
// Rejecting here rather than at send time matters: a stored bad endpoint is a
// liability that outlives the request that created it, and the visitor who
// submitted it is long gone by the time the sender would notice.
func validatePushEndpoint(endpoint string, allowedHosts []string) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}
	// Matches the CHECK constraint on the column, so a value that passes here
	// can never be rejected by the database instead.
	if len(endpoint) < 20 || len(endpoint) > 2048 {
		return fmt.Errorf("endpoint length is out of range")
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("endpoint is not a valid URL")
	}
	// Plaintext would expose the payload in transit and is never what a real
	// push service hands out.
	if u.Scheme != "https" {
		return fmt.Errorf("endpoint must use https")
	}
	if u.Host == "" {
		return fmt.Errorf("endpoint must have a host")
	}
	// Credentials in the URL would be replayed by the sender on every push.
	if u.User != nil {
		return fmt.Errorf("endpoint must not carry credentials")
	}

	if !hostAllowed(u.Hostname(), allowedHosts) {
		return fmt.Errorf("endpoint host %q is not an allowed push service", u.Hostname())
	}
	return nil
}

// hostAllowed reports whether host matches any allowed suffix on a dot
// boundary. Plain strings.HasSuffix would accept "evil-fcm.googleapis.com"
// and "fcm.googleapis.com.evil.com"; both must be rejected.
func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return false
	}
	for _, suffix := range allowed {
		if suffix == "*" {
			return true
		}
		if host == suffix {
			return true
		}
		if strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

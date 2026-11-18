// API configuration for status page custom domains.

package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/yassinebenameur/probara/shared/pagedomains"
)

// StatusPageDomainsConfig is what the API needs to verify custom domains.
type StatusPageDomainsConfig struct {
	// StatusPageCNAMETarget is STATUS_PAGE_CNAME_TARGET, normalized: the host
	// every custom domain must CNAME to, shown in the DNS instructions and
	// checked by verification. Empty turns custom domains off (none can be
	// added, none is re-verified).
	StatusPageCNAMETarget string
	// StatusPagePublicBaseURL is STATUS_PAGE_BASE_URL, the status-page
	// service's public origin (no trailing slash; empty when unset). The API
	// refuses that host as a custom domain, links subscriber mail of pages
	// without a custom domain there (empty: pages cannot take email
	// subscriptions), and hands the origin to the operator UI
	// (GET /auth-context) for its status-page links and template preview:
	// the published UI image is built once, so a build-time setting there
	// cannot carry a per-install value.
	StatusPagePublicBaseURL string
	// StatusPagePlatformHosts is STATUS_PAGE_PLATFORM_HOSTS, the status-page
	// service's other platform hosts: refused as custom domains too.
	StatusPagePlatformHosts []string
}

// StatusPageFailsClosed reports whether the status-page service, configured
// with the same STATUS_PAGE_BASE_URL and STATUS_PAGE_PLATFORM_HOSTS, refuses
// public hostnames that are not verified custom domains: it does only once
// one of its platform hosts is a public name. Without that it serves every
// page on any host, so custom domains must stay off.
func (c StatusPageDomainsConfig) StatusPageFailsClosed() bool {
	if pagedomains.CanBeCustomDomain(pagedomains.HostOfURL(c.StatusPagePublicBaseURL)) {
		return true
	}
	for _, h := range c.StatusPagePlatformHosts {
		if pagedomains.CanBeCustomDomain(h) {
			return true
		}
	}
	return false
}

func loadStatusPageDomainsConfig() (StatusPageDomainsConfig, error) {
	var cfg StatusPageDomainsConfig
	if raw := strings.TrimSpace(os.Getenv("STATUS_PAGE_CNAME_TARGET")); raw != "" {
		cfg.StatusPageCNAMETarget = pagedomains.HostOfURL(raw)
		if cfg.StatusPageCNAMETarget == "" || strings.ContainsAny(raw, ":/") {
			return cfg, fmt.Errorf("invalid STATUS_PAGE_CNAME_TARGET %q: want a host name such as status.example.com", raw)
		}
	}
	cfg.StatusPagePublicBaseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("STATUS_PAGE_BASE_URL")), "/")
	for _, raw := range strings.Split(os.Getenv("STATUS_PAGE_PLATFORM_HOSTS"), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		host := pagedomains.HostFromHeader(raw)
		if host == "" || strings.ContainsAny(raw, "/@") {
			return cfg, fmt.Errorf("invalid STATUS_PAGE_PLATFORM_HOSTS entry %q: want a host name", raw)
		}
		cfg.StatusPagePlatformHosts = append(cfg.StatusPagePlatformHosts, host)
	}
	return cfg, nil
}

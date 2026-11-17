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
	// service's public origin. The API reads it only to refuse that host as a
	// custom domain.
	StatusPagePublicBaseURL string
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
	return cfg, nil
}

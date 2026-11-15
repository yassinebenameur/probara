package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/domainexpiry"
	"github.com/yassinebenameur/probara/shared/models"
)

// DomainChecker implements Checker for domain monitors: the registration
// expiry of a domain, looked up through RDAP with a WHOIS fallback
// (shared/domainexpiry). Its verdict:
//
//   - failure: the registration has expired, is in redemption or pending
//     delete, or the name is not registered;
//   - success: everything else, including an expiry that cannot be
//     determined (no date published, TLD unsupported, registries
//     unreachable). An undetermined expiry is a fact about the registry,
//     not an outage of the domain, so it never pages as one: the snapshot
//     carries unknown_reason, the monitor page shows it, and the alerter
//     raises a domain_expiry warning when it persists.
//
// An expiry inside the warning window is the alerter's domain_expiry alert,
// not a failure.
type DomainChecker struct {
	allowedCIDRs []*net.IPNet
	directory    *domainexpiry.Directory
	// lookup asks the registries about the registrable name; a test seam.
	lookup func(ctx context.Context, name string, timeout time.Duration) *domainexpiry.Info
	now    func() time.Time
}

// NewDomainChecker creates a domain checker. Its lookups always refuse
// private, loopback and link-local destinations, whatever the worker's
// HTTP_BLOCK_PRIVATE_IPS says (the first parameter is accepted for the
// checker signature every dialing checker shares): registry servers are
// public, and every server it reaches is chosen by the bootstrap data or by
// a remote referral, never by the operator, so an open policy would let a
// registry answer steer the worker into its own network. HTTP_ALLOWED_CIDRS
// still admits ranges the operator trusts.
func NewDomainChecker(_ bool, allowedCIDRs []*net.IPNet) *DomainChecker {
	c := &DomainChecker{allowedCIDRs: allowedCIDRs, now: time.Now}
	c.directory = domainexpiry.NewDirectory(domainexpiry.Fetcher(domainDialGuard(allowedCIDRs, 30*time.Second).DialContext))
	c.lookup = c.registryLookup
	return c
}

// domainDialGuard is the dial policy of every domain lookup connection.
func domainDialGuard(allowedCIDRs []*net.IPNet, timeout time.Duration) *dialGuard {
	return newDialGuard(true, allowedCIDRs, timeout)
}

func (c *DomainChecker) registryLookup(ctx context.Context, name string, timeout time.Duration) *domainexpiry.Info {
	client := &domainexpiry.Client{
		Directory: c.directory,
		Dial:      domainDialGuard(c.allowedCIDRs, timeout).DialContext,
		Now:       c.now,
	}
	return client.Lookup(ctx, name)
}

// Check looks the domain up and records metrics_data.domain
// (domainexpiry.Info) on every outcome but an invalid config.
func (c *DomainChecker) Check(ctx context.Context, raw json.RawMessage, timeoutSeconds int) CheckResult {
	start := time.Now()
	result := func(status, message string, info *domainexpiry.Info) CheckResult {
		latency := time.Since(start).Milliseconds()
		r := CheckResult{Status: status, LatencyMs: &latency}
		if message != "" {
			r.ErrorMessage = &message
		}
		if info != nil {
			r.MetricsData, _ = json.Marshal(map[string]any{"domain": info})
		}
		return r
	}

	var cfg models.DomainMonitorConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return result("error", "invalid domain config", nil)
	}
	if err := cfg.Validate(); err != nil {
		return result("error", err.Error(), nil)
	}
	if timeoutSeconds <= 0 {
		return result("error", "timeout must be positive", nil)
	}
	name, _ := cfg.LookupName()
	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	info := c.lookup(ctx, name, timeout)
	status, message := domainVerdict(info, c.now())
	return result(status, message, info)
}

// domainVerdict decides the check from a lookup.
func domainVerdict(info *domainexpiry.Info, now time.Time) (string, string) {
	if info.IsNotRegistered() {
		return "failure", fmt.Sprintf("domain: %s is not registered (%s)", info.Domain, info.Source)
	}
	if info.ExpiresAt != "" {
		if expires, err := time.Parse(time.RFC3339, info.ExpiresAt); err == nil && !now.Before(expires) {
			return "failure", fmt.Sprintf("domain: registration expired %s (%s)",
				daysAgo(int(now.Sub(expires).Hours()/24)), expires.Format("2006-01-02"))
		}
	}
	for _, s := range info.Statuses {
		switch strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(s)) {
		case "redemptionperiod", "pendingdelete", "pendingrestore":
			return "failure", "domain: registration is in " + s
		}
	}
	return "success", ""
}

func daysAgo(days int) string {
	switch {
	case days <= 0:
		return "today"
	case days == 1:
		return "1 day ago"
	default:
		return fmt.Sprintf("%d days ago", days)
	}
}

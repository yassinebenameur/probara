package models

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/idna"
)

// Domain monitor bounds. The API validator and the worker both apply them
// through DomainMonitorConfig.Validate and ValidateDomainSchedule.
const (
	// DomainMinIntervalSeconds is the shortest schedule a domain monitor may
	// run on: RDAP and WHOIS servers rate-limit, and a registration date
	// moves in days, not minutes.
	DomainMinIntervalSeconds = 3600
	// DomainMaxMinDaysValid bounds the alert threshold to a registration
	// term (registries sell at most ten years).
	DomainMaxMinDaysValid = 3650
)

// DomainMonitorConfig watches a registered domain's expiry date through RDAP,
// falling back to WHOIS. The check fails only when the domain is expired or
// not registered; an approaching expiry is a domain_expiry alert (alerter,
// shared/domainexpiry) while the check still passes.
type DomainMonitorConfig struct {
	// Domain is the registered name to look up, e.g. example.com (not a
	// subdomain: registries only know the names they sell). Unicode names
	// are accepted and looked up in their ASCII (punycode) form.
	Domain string `json:"domain"`
	// MinDaysValid opens a domain_expiry alert when fewer days remain before
	// the registration expires. Absent: no expiry alert, the check still
	// fails once the domain has expired.
	MinDaysValid *int `json:"min_days_valid,omitempty"`
	// RDAPServerOverride is an RDAP base URL (https) used instead of the IANA
	// bootstrap registry, for TLDs the registry does not list.
	RDAPServerOverride string `json:"rdap_server_override,omitempty"`
	// WHOISFallback queries WHOIS (TCP 43) when RDAP has no server for the
	// TLD or the lookup fails. Absent means true.
	WHOISFallback *bool `json:"whois_fallback,omitempty"`
}

// WHOISFallbackEnabled reports whether a failed RDAP lookup falls back to
// WHOIS (absent = true).
func (c DomainMonitorConfig) WHOISFallbackEnabled() bool {
	return c.WHOISFallback == nil || *c.WHOISFallback
}

// LookupName is the domain in the form registries are queried with:
// trimmed, lower-case ASCII (punycode for an internationalized name), with
// no trailing dot. ok is false when the name is not a valid domain.
func (c DomainMonitorConfig) LookupName() (string, bool) {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(c.Domain)), ".")
	if name == "" {
		return "", false
	}
	ascii, err := idna.Lookup.ToASCII(name)
	if err != nil {
		return "", false
	}
	if net.ParseIP(ascii) != nil || !IsValidHostname(ascii) || !strings.Contains(ascii, ".") {
		return "", false
	}
	// The TLD is never numeric (RFC 3696 §2), which also keeps a dotted
	// IPv4 literal out.
	tld := ascii[strings.LastIndex(ascii, ".")+1:]
	if strings.Trim(tld, "0123456789") == "" {
		return "", false
	}
	return ascii, true
}

// Validate checks a domain config. The worker runs it on every job too.
func (c DomainMonitorConfig) Validate() error {
	if strings.TrimSpace(c.Domain) == "" {
		return fmt.Errorf("domain is required")
	}
	if trimmed := strings.TrimSpace(c.Domain); strings.ContainsAny(trimmed, "/:@ \t") {
		return fmt.Errorf("domain must be a bare domain name such as example.com, without scheme, port or path")
	}
	if _, ok := c.LookupName(); !ok {
		return fmt.Errorf("domain must be a registered domain name such as example.com")
	}
	if c.MinDaysValid != nil && (*c.MinDaysValid < 0 || *c.MinDaysValid > DomainMaxMinDaysValid) {
		return fmt.Errorf("min_days_valid must be between 0 and %d", DomainMaxMinDaysValid)
	}
	if c.RDAPServerOverride != "" {
		u, err := url.Parse(c.RDAPServerOverride)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
			strings.TrimSpace(c.RDAPServerOverride) != c.RDAPServerOverride || len(c.RDAPServerOverride) > 2048 {
			return fmt.Errorf("rdap_server_override must be an https:// RDAP base URL without credentials, query or fragment")
		}
	}
	return nil
}

// ValidateDomainSchedule enforces the domain monitor's interval floor
// (DomainMinIntervalSeconds).
func ValidateDomainSchedule(_ json.RawMessage, intervalSeconds, _ int) error {
	if intervalSeconds < DomainMinIntervalSeconds {
		return fmt.Errorf("interval_seconds must be at least %d for domain monitors (registries rate-limit lookups)", DomainMinIntervalSeconds)
	}
	return nil
}

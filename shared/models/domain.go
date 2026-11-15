package models

import (
	"encoding/json"
	"fmt"

	"github.com/yassinebenameur/probara/shared/domainexpiry"
)

// Domain monitor bounds. The API validator and the worker both apply them
// through DomainMonitorConfig.Validate and ValidateDomainSchedule.
const (
	// DomainMinIntervalSeconds is the shortest schedule a domain monitor may
	// run on: RDAP and WHOIS servers rate-limit, and a registration date
	// moves in days, not minutes.
	DomainMinIntervalSeconds = 3600
	// DomainDefaultMinDaysValid is the warning window when min_days_valid
	// is absent.
	DomainDefaultMinDaysValid = 30
	// DomainMaxMinDaysValid bounds the warning window to a registration
	// term (registries sell at most ten years).
	DomainMaxMinDaysValid = 3650
)

// DomainMonitorConfig watches a domain's registration expiry through RDAP,
// falling back to WHOIS (shared/domainexpiry). The check fails only when the
// registration has expired or the name is not registered; an expiry inside
// the warning window is a domain_expiry alert while the check still passes.
type DomainMonitorConfig struct {
	// Domain is the name to watch. A subdomain is accepted and its
	// registrable domain is looked up ("www.example.co.uk" watches
	// example.co.uk); Unicode names are looked up in punycode.
	Domain string `json:"domain"`
	// MinDaysValid opens a domain_expiry alert when fewer days remain before
	// the registration expires. Absent means DomainDefaultMinDaysValid; 0
	// alerts only once the registration has expired.
	MinDaysValid *int `json:"min_days_valid,omitempty"`
}

// EffectiveMinDaysValid is the warning window in days.
func (c DomainMonitorConfig) EffectiveMinDaysValid() int {
	if c.MinDaysValid == nil {
		return DomainDefaultMinDaysValid
	}
	return *c.MinDaysValid
}

// LookupName is the registrable domain the registries are asked about.
func (c DomainMonitorConfig) LookupName() (string, error) {
	return domainexpiry.RegistrableDomain(c.Domain)
}

// Validate checks a domain config. The worker runs it on every job too.
func (c DomainMonitorConfig) Validate() error {
	if _, err := c.LookupName(); err != nil {
		return err
	}
	if c.MinDaysValid != nil && (*c.MinDaysValid < 0 || *c.MinDaysValid > DomainMaxMinDaysValid) {
		return fmt.Errorf("min_days_valid must be between 0 and %d", DomainMaxMinDaysValid)
	}
	return nil
}

// ValidateDomainSchedule enforces the domain monitor's interval floor.
func ValidateDomainSchedule(_ json.RawMessage, intervalSeconds, _ int) error {
	if intervalSeconds < DomainMinIntervalSeconds {
		return fmt.Errorf("interval_seconds must be at least %d for domain monitors (registries rate-limit lookups)", DomainMinIntervalSeconds)
	}
	return nil
}

// Package domainexpiry looks up a domain's registration expiry and owns the
// snapshot a domain check records in check_results.metrics_data, including
// WHERE in metrics_data it lives ({"domain": {...}}), the way shared/tlscert
// owns certificates.
//
// Lookups (Client) ask RDAP first, using the IANA bootstrap registry to find
// the TLD's server (Directory), and fall back to WHOIS over TCP 43 for
// registries without RDAP or without an expiry date in it. Every connection
// goes through the caller's dialer (the worker's dialGuard) and is bounded in
// time and size. Servers come only from the bootstrap data and from WHOIS
// referrals, never from the monitor's config, and every server a remote
// party names is validated first (validHostname, redirect rules).
//
// Readers go through this package instead of hard-coding a path: the
// alerter's domain_expiry evaluation selects through SQLExpr and
// ExpiresAtSQL, Go readers decode through Extract. MetricsPaths is the one
// list both are built from, and TestSQLExprMatchesExtract keeps them choosing
// the same object for the same row.
package domainexpiry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// Sources: which protocol answered.
const (
	SourceRDAP  = "rdap"
	SourceWHOIS = "whois"
)

// Info is the registration snapshot a domain check records. The JSON keys
// are a stored contract (raw check rows, web DomainInfo, the alerter's
// expires_at projection): add keys, never rename them.
//
// Three outcomes are possible: the expiry is known (ExpiresAt set), the name
// is not registered (Registered false), or the expiry could not be determined
// (UnknownReason set, Registered true or nil).
type Info struct {
	// Domain is the registrable name looked up, in ASCII (punycode) form.
	Domain string `json:"domain"`
	// ExpiresAt is the registration expiry (RFC 3339, UTC).
	ExpiresAt string `json:"expires_at,omitempty"`
	// DaysUntilExpiry is whole days from the check to ExpiresAt, rounded
	// down; negative once expired. The alerter recomputes it from
	// expires_at at evaluation time.
	DaysUntilExpiry *int `json:"days_until_expiry,omitempty"`
	// RegisteredAt and UpdatedAt are the registration and last-change
	// dates when the registry publishes them.
	RegisteredAt string `json:"registered_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	// Registrar is the sponsoring registrar's name.
	Registrar string `json:"registrar,omitempty"`
	// Statuses are the registry's status values (RDAP spelling, e.g.
	// "client transfer prohibited", or WHOIS EPP codes as given).
	Statuses []string `json:"statuses,omitempty"`
	// Registered is true when a registry holds the name, false when it
	// reported the name as not found, and nil when no registry answered.
	Registered *bool `json:"registered,omitempty"`
	// Source is SourceRDAP or SourceWHOIS, the protocol that answered;
	// empty when none did.
	Source string `json:"source,omitempty"`
	// Server is the RDAP origin or WHOIS host that answered.
	Server string `json:"server,omitempty"`
	// RDAPError is why RDAP did not answer when WHOIS did.
	RDAPError string `json:"rdap_error,omitempty"`
	// UnknownReason says why the expiry could not be determined (the
	// registry publishes none, no RDAP or WHOIS service for the TLD, every
	// lookup failed). Empty whenever ExpiresAt is set or the name is not
	// registered.
	UnknownReason string `json:"unknown_reason,omitempty"`
}

// SetExpiry records expiresAt and the whole days left at now.
func (i *Info) SetExpiry(expiresAt, now time.Time) {
	i.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	days := DaysUntil(expiresAt, now)
	i.DaysUntilExpiry = &days
}

// DaysUntil is the whole days from now to t, rounded down: negative as soon
// as t has passed, the same rule the alerter applies.
func DaysUntil(t, now time.Time) int {
	return int(math.Floor(t.Sub(now).Hours() / 24))
}

// IsRegistered reports whether a registry answered that it holds the name.
func (i *Info) IsRegistered() bool { return i.Registered != nil && *i.Registered }

// IsNotRegistered reports whether a registry answered that it does not hold
// the name.
func (i *Info) IsNotRegistered() bool { return i.Registered != nil && !*i.Registered }

// MetricsPaths lists where a registration snapshot may sit in metrics_data,
// in precedence order: the first path holding a JSON object wins.
var MetricsPaths = [][]string{
	{"domain"},
}

// SQLExpr returns a jsonb expression selecting the snapshot object of the
// metrics_data column (or any jsonb expression) col: the first MetricsPaths
// entry whose value is a JSON object, else SQL NULL.
//
// col is interpolated, never a user value: pass a column reference.
func SQLExpr(col string) string {
	parts := make([]string, 0, len(MetricsPaths))
	for _, path := range MetricsPaths {
		lit := "'{" + strings.Join(path, ",") + "}'"
		parts = append(parts, fmt.Sprintf(
			"CASE WHEN jsonb_typeof(%s #> %s) = 'object' THEN %s #> %s END",
			col, lit, col, lit))
	}
	return "COALESCE(" + strings.Join(parts, ", ") + ")"
}

// ExpiresAtSQL returns a text expression selecting expires_at from a
// snapshot object obj (SQLExpr's result): the twin of Info.ExpiresAt.
func ExpiresAtSQL(obj string) string {
	return fmt.Sprintf("NULLIF(%s ->> 'expires_at', '')", obj)
}

// UnknownReasonSQL returns a text expression selecting unknown_reason from a
// snapshot object obj: the twin of Info.UnknownReason.
func UnknownReasonSQL(obj string) string {
	return fmt.Sprintf("NULLIF(%s ->> 'unknown_reason', '')", obj)
}

// ExtractRaw returns the raw snapshot object SQLExpr would select from a
// metrics_data document, or nil when there is none.
func ExtractRaw(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	for _, path := range MetricsPaths {
		if v := lookupPath(raw, path); v != nil {
			return v
		}
	}
	return nil
}

// Extract decodes the snapshot SQLExpr would select. It returns nil when
// there is none, and when the selected object does not decode as an Info.
func Extract(raw json.RawMessage) *Info {
	obj := ExtractRaw(raw)
	if obj == nil {
		return nil
	}
	var info Info
	if err := json.Unmarshal(obj, &info); err != nil {
		return nil
	}
	return &info
}

// lookupPath walks path through nested objects and returns the value there
// when it is a JSON object, matching jsonb's #> plus the jsonb_typeof guard.
func lookupPath(raw json.RawMessage, path []string) json.RawMessage {
	cur := raw
	for _, key := range path {
		var obj map[string]json.RawMessage
		if !isObject(cur) || json.Unmarshal(cur, &obj) != nil {
			return nil
		}
		next, ok := obj[key]
		if !ok {
			return nil
		}
		cur = next
	}
	if !isObject(cur) {
		return nil
	}
	return cur
}

func isObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

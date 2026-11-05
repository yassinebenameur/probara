// Package domainexpiry owns the registration snapshot a domain check records
// in check_results.metrics_data and WHERE in metrics_data it lives
// ({"domain": {...}}), the same way shared/tlscert owns certificates.
//
// Every reader goes through this package instead of hard-coding a path: the
// alerter's domain_expiry evaluation selects through SQLExpr and ExpirySQL,
// Go readers decode through Extract. MetricsPaths is the one list both are
// built from, and the pin test (TestSQLExprMatchesExtract) keeps the SQL and
// Go readers choosing the same object and the same expiry for the same row.
package domainexpiry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Info is the registration snapshot a domain check records. The JSON keys
// are a stored contract (raw check rows, web DomainMetrics, the alerter's
// expires_at/days_until_expiry projection): add keys, never rename them.
type Info struct {
	// Domain is the name looked up (ASCII form).
	Domain string `json:"domain"`
	// ExpiresAt is the registration expiry (RFC 3339, UTC). Absent when the
	// registry publishes none.
	ExpiresAt string `json:"expires_at,omitempty"`
	// DaysUntilExpiry is whole days from the check to ExpiresAt, truncated
	// toward zero; negative once expired. The alerter recomputes from
	// expires_at at evaluation time.
	DaysUntilExpiry *int `json:"days_until_expiry,omitempty"`
	// RegisteredAt and UpdatedAt are the registration and last-change dates
	// when the registry publishes them.
	RegisteredAt string `json:"registered_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	// Registrar is the sponsoring registrar's name.
	Registrar string `json:"registrar,omitempty"`
	// Statuses are the registry's status values (RDAP spelling, e.g.
	// "client transfer prohibited"; WHOIS EPP codes as given).
	Statuses []string `json:"statuses,omitempty"`
	// Nameservers are the delegated name servers, lower-case.
	Nameservers []string `json:"nameservers,omitempty"`
	// Registered is false when the registry reports the name as not found.
	Registered bool `json:"registered"`
	// Source is "rdap" or "whois": the protocol that answered.
	Source string `json:"source"`
	// Server is the RDAP base URL or WHOIS host that answered.
	Server string `json:"server,omitempty"`
	// RDAPError is why RDAP did not answer when WHOIS did.
	RDAPError string `json:"rdap_error,omitempty"`
	// Note explains a registered domain without an expiry date.
	Note string `json:"note,omitempty"`
}

// SetExpiry records expiresAt and the whole days left at now.
func (i *Info) SetExpiry(expiresAt, now time.Time) {
	i.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	days := int(expiresAt.Sub(now).Hours() / 24)
	i.DaysUntilExpiry = &days
}

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

// ExpirySQL returns text expressions selecting (expires_at,
// days_until_expiry) from a snapshot object obj (SQLExpr's result): the
// twin of Info.ExpiresAt / Info.DaysUntilExpiry.
func ExpirySQL(obj string) (expiresAt, days string) {
	return fmt.Sprintf("%s ->> 'expires_at'", obj), fmt.Sprintf("%s ->> 'days_until_expiry'", obj)
}

// ExtractRaw returns the raw snapshot object SQLExpr would select from a
// metrics_data document, or nil when there is none.
func ExtractRaw(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	for _, path := range MetricsPaths {
		if v := lookup(raw, path); v != nil {
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

// lookup walks path through nested objects and returns the value there when
// it is a JSON object, matching jsonb's #> plus the jsonb_typeof guard.
func lookup(raw json.RawMessage, path []string) json.RawMessage {
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

// Package tlscert owns the certificate snapshot checks record in
// check_results.metrics_data and, more importantly, WHERE in metrics_data it
// lives. A check that completes a TLS handshake writes an Info under its own
// envelope key: {"http": {"tls": …}} for http, {"sip": {"tls": …}} for sip.
//
// Every reader goes through this package instead of hard-coding a path:
// the alerter's tls_expiry evaluation selects through SQLExpr, the status
// page decodes through Extract. MetricsPaths is the one list both are built
// from, so a new TLS-capable type is one entry here, and the pin test
// (TestSQLExprMatchesExtract) keeps the SQL and Go readers choosing the same
// object for the same row.
package tlscert

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Info is the certificate and session snapshot a check records. The JSON
// keys are a stored contract (raw check rows, web HTTPTLSInfo/TLSCertInfo,
// the alerter's not_after/days_until_expiry projection): add keys, never
// rename them.
type Info struct {
	Version          string   `json:"version,omitempty"`
	CipherSuite      string   `json:"cipher_suite,omitempty"`
	ServerName       string   `json:"server_name,omitempty"`
	NotBefore        string   `json:"not_before,omitempty"`
	NotAfter         string   `json:"not_after,omitempty"`
	DaysUntilExpiry  *int     `json:"days_until_expiry,omitempty"`
	Subject          string   `json:"subject,omitempty"`
	Issuer           string   `json:"issuer,omitempty"`
	SerialNumber     string   `json:"serial_number,omitempty"`
	DNSNames         []string `json:"dns_names,omitempty"`
	IPAddresses      []string `json:"ip_addresses,omitempty"`
	VerifiedChains   int      `json:"verified_chains,omitempty"`
	PeerCertificates int      `json:"peer_certificates,omitempty"`
}

// FromConnectionState snapshots the leaf certificate of a completed
// handshake. serverName is the identity the client asked for (SNI and
// verification name). It returns nil when the peer sent no certificate.
// The state is captured under InsecureSkipVerify too: expiry is worth
// reporting on a certificate the operator chose not to verify.
//
// days_until_expiry truncates toward zero (a certificate with 9.9 days left
// reports 9); the alerter recomputes from not_after at evaluation time.
func FromConnectionState(cs tls.ConnectionState, serverName string, now time.Time) *Info {
	if len(cs.PeerCertificates) == 0 {
		return nil
	}
	leaf := cs.PeerCertificates[0]
	days := int(leaf.NotAfter.Sub(now).Hours() / 24)
	info := &Info{
		Version:          tls.VersionName(cs.Version),
		CipherSuite:      tls.CipherSuiteName(cs.CipherSuite),
		ServerName:       serverName,
		NotBefore:        leaf.NotBefore.UTC().Format(time.RFC3339),
		NotAfter:         leaf.NotAfter.UTC().Format(time.RFC3339),
		DaysUntilExpiry:  &days,
		Subject:          leaf.Subject.String(),
		Issuer:           leaf.Issuer.String(),
		SerialNumber:     leaf.SerialNumber.String(),
		DNSNames:         leaf.DNSNames,
		VerifiedChains:   len(cs.VerifiedChains),
		PeerCertificates: len(cs.PeerCertificates),
	}
	for _, ip := range leaf.IPAddresses {
		info.IPAddresses = append(info.IPAddresses, ip.String())
	}
	return info
}

// MetricsPaths lists where a certificate may sit in metrics_data, in
// precedence order: the first path holding a JSON object wins. A row only
// ever carries one envelope today; the order matters for the pin test's
// "both" fixture and keeps the two readers deterministic.
var MetricsPaths = [][]string{
	{"http", "tls"},
	{"sip", "tls"},
}

// SQLExpr returns a jsonb expression selecting the certificate object of the
// metrics_data column (or any jsonb expression) col: the first MetricsPaths
// entry whose value is a JSON object, else SQL NULL. A path holding JSON
// null, a string or an array is skipped, exactly as Extract skips it.
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

// ExtractRaw returns the raw certificate object SQLExpr would select from a
// metrics_data document, or nil when there is none (also for empty, invalid
// or non-object input).
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

// Extract decodes the certificate SQLExpr would select. It returns nil when
// there is none, and also when the selected object does not decode as an
// Info (a field of the wrong JSON type): the location rule is shared with
// SQL, field leniency is not.
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
// it is a JSON object. jsonb's #> also yields NULL when a step is not an
// object (or is an array indexed by a non-integer key, which no path here
// uses), so a failed object decode is "absent" on both sides.
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

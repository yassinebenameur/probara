// Package tlscert owns the certificate snapshot checks record in
// check_results.metrics_data and, more importantly, WHERE in metrics_data it
// lives. A check that completes a TLS handshake writes an Info under its own
// envelope key: {"http": {"tls": …}} for http, {"sip": {"tls": …}} for sip,
// {"tcp": {"tls": …}} for TLS-wrapped TCP.
//
// A snapshot also carries the earliest expiry of the presented chain
// (chain_not_after, chain_min_days, chain_expiring_subject): the leaf and the
// intermediates on its path, never a trust-store root. That, not the leaf's
// own expiry, is what every reader acts on: Info.Expiry is the Go rule and
// ExpirySQL its SQL twin (TestExpirySQLMatchesExpiry pins them). Snapshots
// recorded before chain capture fall back to the leaf.
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
	"crypto/x509"
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
	// ChainNotAfter is the earliest NotAfter across the chain (chainOf):
	// the leaf plus every intermediate on its path. It equals NotAfter when
	// the leaf expires first.
	ChainNotAfter string `json:"chain_not_after,omitempty"`
	// ChainMinDays is days_until_expiry for ChainNotAfter.
	ChainMinDays *int `json:"chain_min_days,omitempty"`
	// ChainExpiringSubject is the subject of the certificate ChainNotAfter
	// belongs to: Subject when it is the leaf, an intermediate's otherwise.
	ChainExpiringSubject string `json:"chain_expiring_subject,omitempty"`
}

// Expiry is the expiry readers act on (the tls_expiry alert, the status
// page): the chain's earliest when the snapshot carries one, else the
// leaf's. subject names the certificate it belongs to.
func (i *Info) Expiry() (notAfter string, days *int, subject string) {
	if i.ChainNotAfter != "" {
		return i.ChainNotAfter, i.ChainMinDays, i.ChainExpiringSubject
	}
	return i.NotAfter, i.DaysUntilExpiry, i.Subject
}

// ExpirySQL returns text expressions selecting Expiry's (not_after,
// days_until_expiry, subject) from a certificate object cert (a jsonb
// expression, typically SQLExpr's result). cert is interpolated, never a
// user value.
func ExpirySQL(cert string) (notAfter, days, subject string) {
	pick := func(chainKey, leafKey string) string {
		return fmt.Sprintf(
			"CASE WHEN COALESCE(%[1]s ->> 'chain_not_after', '') <> '' THEN %[1]s ->> '%[2]s' ELSE %[1]s ->> '%[3]s' END",
			cert, chainKey, leafKey)
	}
	return pick("chain_not_after", "not_after"), pick("chain_min_days", "days_until_expiry"), pick("chain_expiring_subject", "subject")
}

// FromConnectionState snapshots the leaf certificate of a completed
// handshake, and the earliest expiry of its chain. serverName is the identity the client asked for (SNI and
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
	earliest := leaf
	for _, c := range chainOf(cs, now) {
		if c.NotAfter.Before(earliest.NotAfter) {
			earliest = c
		}
	}
	chainDays := int(earliest.NotAfter.Sub(now).Hours() / 24)
	info.ChainNotAfter = earliest.NotAfter.UTC().Format(time.RFC3339)
	info.ChainMinDays = &chainDays
	info.ChainExpiringSubject = earliest.Subject.String()
	return info
}

// chainOf is the chain whose expiry matters, leaf first.
//
// With a verified chain it is the first one minus its last certificate,
// which Go's verifier always takes from the trust store (system roots, or a
// monitor's own CA): a trust anchor's expiry is the store's business. A leaf
// that is itself trusted stays.
//
// Without one (tls_skip_verify) the presented list is not trusted to be a
// path: servers send stale or extra certificates. So it walks issuer to
// issuer from the leaf (issuer name matches subject and the signature
// checks), and stops at a self-signed certificate, at an issuer the server
// did not present, and before an expired certificate that cross-signs a
// root (isExpiredRootCrossSign), e.g. ISRG Root X1 signed by the expired
// DST Root CA X3, which real clients skip because they hold the root.
func chainOf(cs tls.ConnectionState, now time.Time) []*x509.Certificate {
	if len(cs.VerifiedChains) > 0 && len(cs.VerifiedChains[0]) > 0 {
		certs := cs.VerifiedChains[0]
		if len(certs) == 1 {
			return certs
		}
		return certs[:len(certs)-1]
	}

	presented := cs.PeerCertificates
	out := []*x509.Certificate{presented[0]}
	used := map[int]bool{0: true}
	current := presented[0]
	for !isSelfSigned(current) {
		next := -1
		for i, c := range presented {
			if used[i] || !bytes.Equal(c.RawSubject, current.RawIssuer) || current.CheckSignatureFrom(c) != nil {
				continue
			}
			next = i
			break
		}
		if next < 0 {
			break // issuer not presented
		}
		issuer := presented[next]
		if isSelfSigned(issuer) || isExpiredRootCrossSign(issuer, current, presented, now) {
			break
		}
		used[next] = true
		out = append(out, issuer)
		current = issuer
	}
	return out
}

// trustedRoots is the root store isExpiredRootCrossSign consults; a test
// seam (production: the system store).
var trustedRoots = func() *x509.CertPool {
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil
	}
	return pool
}

// isExpiredRootCrossSign reports whether issuer (the certificate that
// signed child) is an expired cross-sign of a root: expired at now, and its
// subject is also a root, either self-signed among the presented
// certificates or a trusted root that child verifies against directly.
func isExpiredRootCrossSign(issuer, child *x509.Certificate, presented []*x509.Certificate, now time.Time) bool {
	if !now.After(issuer.NotAfter) {
		return false
	}
	for _, c := range presented {
		if c != issuer && bytes.Equal(c.RawSubject, issuer.RawSubject) && isSelfSigned(c) {
			return true
		}
	}
	roots := trustedRoots()
	if roots == nil {
		return false
	}
	_, err := child.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	return err == nil
}

func isSelfSigned(c *x509.Certificate) bool {
	return bytes.Equal(c.RawSubject, c.RawIssuer) && c.CheckSignatureFrom(c) == nil
}

// MetricsPaths lists where a certificate may sit in metrics_data, in
// precedence order: the first path holding a JSON object wins. A row only
// ever carries one envelope today; the order matters for the pin test's
// "both" fixture and keeps the two readers deterministic.
var MetricsPaths = [][]string{
	{"http", "tls"},
	{"sip", "tls"},
	{"tcp", "tls"},
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

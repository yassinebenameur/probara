package notifications

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// TLSExpiryCertificate is which certificate of a chain a tls_expiry alert is
// about: the alerter measures the chain's earliest expiry (shared/tlscert),
// which may be an intermediate's rather than the server's own.
//
// It rides the alert's last_error, the one field every event of the alert
// carries from the alerts row (created, reminder, acknowledged, resolved,
// async delivery), so FormatTLSExpiryError writes it and
// AlertDetails.TLSExpiryCertificate reads it back. The web twin is
// tlsExpiryCertificate in web/lib/tls-cert.ts;
// testdata/tls_expiry_errors.json pins all three.
type TLSExpiryCertificate struct {
	// Subject is the certificate's subject (RFC 4514), empty when unknown.
	Subject string
	// Intermediate is set when it is not the server's own certificate.
	Intermediate bool
	// NotAfter is its expiry date, zero when unknown.
	NotAfter time.Time
}

// FormatTLSExpiryError is a tls_expiry alert's last_error, e.g.
// "tls: intermediate certificate CN=R3,O=Example expires on 2026-12-01 (in
// 9d, < 14d)". The subject is the certificate's own text, never a URL or
// secret.
func FormatTLSExpiryError(cert TLSExpiryCertificate, days, minDays int) string {
	which := "certificate"
	if cert.Intermediate {
		which = "intermediate certificate"
	}
	if cert.Subject != "" {
		which += " " + cert.Subject
	}
	when := ""
	if !cert.NotAfter.IsZero() {
		when = " on " + cert.NotAfter.UTC().Format("2006-01-02")
	}
	if days < 0 {
		return fmt.Sprintf("tls: %s expired%s (%dd ago, threshold %dd)", which, when, -days, minDays)
	}
	return fmt.Sprintf("tls: %s expires%s (in %dd, < %dd)", which, when, days, minDays)
}

// tlsExpiryError matches FormatTLSExpiryError's output. The subject is
// greedy: the fixed suffix is anchored at the end, so a subject that happens
// to contain "expires" still parses whole.
var tlsExpiryError = regexp.MustCompile(`^tls: (intermediate certificate|certificate)(?: (.+))? (?:expires|expired)(?: on (\d{4}-\d{2}-\d{2}))? \((?:in -?\d+d, < \d+d|\d+d ago, threshold \d+d)\)$`)

// ParseTLSExpiryError reads FormatTLSExpiryError's output back. ok is false
// for any other text (alerts opened before the certificate was named).
func ParseTLSExpiryError(lastError string) (cert TLSExpiryCertificate, ok bool) {
	m := tlsExpiryError.FindStringSubmatch(strings.TrimSpace(lastError))
	if m == nil {
		return TLSExpiryCertificate{}, false
	}
	cert.Intermediate = m[1] == "intermediate certificate"
	cert.Subject = m[2]
	if m[3] != "" {
		cert.NotAfter, _ = time.Parse("2006-01-02", m[3])
	}
	return cert, true
}

// TLSExpiryCertificate is the certificate a tls_expiry alert is about, read
// from its last_error; ok is false for other kinds and older alerts.
func (d AlertDetails) TLSExpiryCertificate() (TLSExpiryCertificate, bool) {
	if !d.IsTLSExpiry() || d.LastError == nil {
		return TLSExpiryCertificate{}, false
	}
	return ParseTLSExpiryError(*d.LastError)
}

// CommonName is the CN of an RFC 4514 subject ("CN=R3,O=Example" is "R3"),
// else the whole subject.
func CommonName(subject string) string {
	for _, attr := range splitUnescaped(subject) {
		if name, value, found := strings.Cut(attr, "="); found && strings.EqualFold(strings.TrimSpace(name), "CN") {
			return unescapeRDN(value)
		}
	}
	return subject
}

// splitUnescaped splits an RFC 4514 string on the ',' and '+' that are not
// escaped with a backslash.
func splitUnescaped(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case ',', '+':
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

func unescapeRDN(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) {
			i++
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

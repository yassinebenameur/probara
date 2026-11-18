// Package pagedomains is the custom-domain contract shared by the API, which
// stores a status page's domains and verifies them, and the status-page
// service, which serves a page on its verified domains (read-only).
//
// A domain is served only while its status is Verified. It is verified by two
// DNS records the operator creates: a TXT record at VerificationRecordName
// holding the domain's token (ownership), and a CNAME from the domain to the
// installation's STATUS_PAGE_CNAME_TARGET (routing).
//
// A hostname is unique only among verified domains: several pages, in any
// workspaces, may hold an unverified claim on it, each with its own token.
// The first claim that verifies gets the hostname and supersedes the others;
// a claim that never verifies expires after ClaimTTL.
package pagedomains

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// Domain statuses, as stored in status_page_domains.status (migration 000120
// holds the same list in its CHECK).
const (
	// Pending: added, not verified yet. Never served.
	Pending = "pending"
	// Verified: both records found. Served, including while a later check
	// fails within the tolerance (the API's runner owns that rule).
	Verified = "verified"
	// Failed: was verified, then failed every check for the tolerance. Not
	// served until a check passes again.
	Failed = "failed"
	// Superseded: another page verified the hostname first. Not served, not
	// rechecked except on demand; expires like a pending claim.
	Superseded = "superseded"
)

// ClaimTTL is how long a claim that never verified (pending or superseded)
// is kept before the API's runner removes it.
const ClaimTTL = 14 * 24 * time.Hour

// EventDomainsChanged is the statusupdates event type the API publishes
// (with StatusPageID) when a page's served domains change: one added,
// removed, verified or failed. The status-page service reloads its host
// table and the page's render on it.
const EventDomainsChanged = "status_page_domains_changed"

// VerificationLabel is the label the TXT record sits under.
const VerificationLabel = "_probara-verify"

// VerificationRecordName is the TXT record name for hostname.
func VerificationRecordName(hostname string) string {
	return VerificationLabel + "." + hostname
}

// PageURL is the URL of a page served on hostname: the page lives at the
// root of its own origin. Always https: custom domains are served behind TLS
// (Helm's Ingress with cert-manager, or the operator's own proxy).
func PageURL(hostname string) string {
	return PublicURL(PublicBaseURL(hostname, "", ""), "")
}

// PublicBaseURL is the absolute URL a page's visitor routes hang off, with
// no trailing slash: "https://<primaryHost>" on its canonical custom domain,
// else "<platformBaseURL>/public/status/<slug>", else "" when neither is
// known. Every link to a page or one of its routes (canonical link, feeds,
// summary, badges, push deep links, forms and mail links) is built from it
// with PublicURL, in the API and the status-page service alike.
func PublicBaseURL(primaryHost, platformBaseURL, slug string) string {
	if primaryHost != "" {
		return "https://" + primaryHost
	}
	base := strings.TrimRight(strings.TrimSpace(platformBaseURL), "/")
	if base == "" || slug == "" {
		return ""
	}
	return base + "/public/status/" + slug
}

// PublicURL joins a sub-route ("/feed.atom"; "" for the page itself) onto a
// PublicBaseURL. The page itself on a custom domain is its root, "/". ""
// when base is.
func PublicURL(base, suffix string) string {
	if base == "" {
		return ""
	}
	if suffix == "" && !strings.Contains(strings.TrimPrefix(base, "https://"), "/") {
		return base + "/"
	}
	return base + suffix
}

// CanBeCustomDomain reports whether host (as HostFromHeader returns it)
// could ever be accepted as a custom domain. IP addresses, single labels
// (localhost, a Service name) and names under a TLD outside the public
// suffix list (".local", ".internal", "cluster.local") never can: the
// status-page service treats requests for them as internal (probes,
// in-cluster calls) and never looks them up.
func CanBeCustomDomain(host string) bool {
	if host == "" {
		return false
	}
	normalized, err := NormalizeHostname(host)
	return err == nil && normalized == host
}

// PrimaryHostSQL is a scalar subquery selecting the canonical domain of the
// page whose ID is the SQL expression pageID: its oldest verified domain
// (ties by name), or NULL when it has none. The API's public_url and the
// status-page service's canonical link, feed links and push deep links all
// use it, so they always name the same host.
func PrimaryHostSQL(pageID string) string {
	return `(SELECT primary_domain.hostname FROM status_page_domains primary_domain
		WHERE primary_domain.status_page_id = ` + pageID + ` AND primary_domain.status = 'verified'
		ORDER BY primary_domain.created_at, primary_domain.hostname LIMIT 1)`
}

// MaxHostnameLength is the longest hostname DNS allows (RFC 1035).
const MaxHostnameLength = 253

// Hostname errors. Their text reaches API clients, so keep it stable and
// free of the submitted value.
var (
	ErrRequired = errors.New("hostname is required")
	ErrNotBare  = errors.New("hostname must be a bare host name such as status.example.com, without scheme, port or path")
	ErrIP       = errors.New("hostname must be a domain name, not an IP address")
	ErrWildcard = errors.New("wildcard hostnames are not supported; add each host separately")
	ErrInvalid  = errors.New("hostname must be a valid domain name such as status.example.com")
	ErrTooLong  = errors.New("hostname is longer than 253 characters")
	ErrSuffix   = errors.New("hostname must be a name under a registered domain, not a top-level domain or public suffix")
	ErrTLD      = errors.New("hostname must end in a known top-level domain")
)

// NormalizeHostname validates an operator-supplied hostname and returns the
// lower-case ASCII form it is stored and matched in: an internationalized
// name becomes punycode ("status.bücher.de" → "status.xn--bcher-kva.de"), a
// trailing dot is dropped. It refuses IP addresses, wildcards, a scheme,
// port, path or user part, a bare TLD or public suffix, a TLD the public
// suffix list does not know (so internal names such as "status.local" never
// pass), and names over 253 characters.
func NormalizeHostname(input string) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", ErrRequired
	}
	if strings.Contains(s, "*") {
		return "", ErrWildcard
	}
	if _, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil {
		return "", ErrIP
	}
	if strings.ContainsAny(s, "/:@?#[]\\ \t\r\n") {
		return "", ErrNotBare
	}
	s = strings.TrimSuffix(s, ".")
	ascii, err := idna.Lookup.ToASCII(s)
	if err != nil {
		return "", ErrInvalid
	}
	if len(ascii) > MaxHostnameLength {
		return "", ErrTooLong
	}
	if !validHostname(ascii) {
		return "", ErrInvalid
	}
	if net.ParseIP(ascii) != nil {
		return "", ErrIP
	}
	labels := strings.Split(ascii, ".")
	if len(labels) < 2 {
		return "", ErrSuffix
	}
	// A TLD is never all digits (RFC 3696 §2); this also keeps partial
	// dotted-quad literals such as "10.1" out.
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return "", ErrIP
	}
	if _, icann := publicsuffix.PublicSuffix(labels[len(labels)-1]); !icann {
		return "", ErrTLD
	}
	if _, err := publicsuffix.EffectiveTLDPlusOne(ascii); err != nil {
		return "", ErrSuffix
	}
	return ascii, nil
}

// HostFromHeader normalizes a Host (or X-Forwarded-Host) header value for
// lookup: the port and a trailing dot are dropped and the name lower-cased.
// Browsers already send internationalized names as punycode. It returns ""
// for anything that is not a plausible host name, so a malformed header can
// only ever miss.
func HostFromHeader(value string) string {
	host := strings.TrimSpace(value)
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if len(host) > MaxHostnameLength || !validHostname(host) {
		return ""
	}
	return host
}

// HostOfURL is the normalized host of an absolute URL ("" when raw has
// none), for comparing configured origins with hostnames.
func HostOfURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		// A bare host[:port], as STATUS_PAGE_CNAME_TARGET is written.
		return HostFromHeader(raw)
	}
	_, rest, _ := strings.Cut(raw, "://")
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndexByte(rest, '@'); i >= 0 {
		rest = rest[i+1:]
	}
	return HostFromHeader(rest)
}

// validHostname reports whether s is a lower-case ASCII hostname: labels of
// 1-63 letters, digits and inner hyphens.
func validHostname(s string) bool {
	if s == "" {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

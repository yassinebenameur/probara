package domainexpiry

import (
	"errors"
	"net"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// Name errors. Their text reaches API clients through the config validator,
// so keep it stable and free of the submitted value.
var (
	ErrNameRequired = errors.New("domain is required")
	ErrNameNotBare  = errors.New("domain must be a bare domain name such as example.com, without scheme, port or path")
	ErrNameIP       = errors.New("domain must be a domain name, not an IP address")
	ErrNameInvalid  = errors.New("domain must be a valid domain name such as example.com")
	ErrNameSuffix   = errors.New("domain must be a registered domain such as example.com, not a top-level domain or public suffix")
	ErrNameTLD      = errors.New("domain must end in a known top-level domain")
)

// RegistrableDomain returns the name a registry sells for input, in the
// lower-case ASCII form registries are queried with: the ICANN public suffix
// plus one label, so "www.shop.example.co.uk" watches "example.co.uk" and an
// internationalized name becomes punycode ("bücher.de" → "xn--bcher-kva.de").
//
// Only the ICANN section of the public suffix list counts. Its private
// section (github.io, blogspot.com, …) names hosting platforms, not
// registries: "me.github.io" is a subdomain of the registered github.io, and
// asking the .io registry for "me.github.io" would find nothing. A TLD the
// list does not know is refused (ErrNameTLD): no registry can answer for it,
// and each made-up TLD would otherwise cost a query to IANA's WHOIS. The
// list ships with golang.org/x/net and lists every ICANN TLD (the RDAP
// bootstrap lists only those with RDAP), so a TLD delegated after the
// dependency was last updated is refused until it is bumped.
//
// It refuses IP addresses, a bare TLD or public suffix, and anything with a
// scheme, port, path or user part.
func RegistrableDomain(input string) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", ErrNameRequired
	}
	if net.ParseIP(strings.Trim(s, "[]")) != nil {
		return "", ErrNameIP
	}
	if strings.ContainsAny(s, "/:@?#[] \t\r\n") {
		return "", ErrNameNotBare
	}
	s = strings.TrimSuffix(s, ".")
	ascii, err := idna.Lookup.ToASCII(s)
	if err != nil || !validHostname(ascii) {
		return "", ErrNameInvalid
	}
	if net.ParseIP(ascii) != nil {
		return "", ErrNameIP
	}
	labels := strings.Split(ascii, ".")
	// A TLD is never all digits (RFC 3696 §2); this also keeps partial
	// dotted-quad literals such as "10.1" out.
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return "", ErrNameIP
	}
	if _, icann := publicsuffix.PublicSuffix(labels[len(labels)-1]); !icann {
		return "", ErrNameTLD
	}
	suffix := icannSuffix(ascii)
	suffixLabels := strings.Count(suffix, ".") + 1
	if suffixLabels >= len(labels) {
		return "", ErrNameSuffix
	}
	return strings.Join(labels[len(labels)-suffixLabels-1:], "."), nil
}

// icannSuffix is name's public suffix from the ICANN section of the list.
// A private suffix (github.io) is walked up to the ICANN one beneath it
// (io).
func icannSuffix(name string) string {
	suffix, icann := publicsuffix.PublicSuffix(name)
	for !icann {
		dot := strings.IndexByte(suffix, '.')
		if dot < 0 {
			return suffix
		}
		suffix, icann = publicsuffix.PublicSuffix(suffix[dot+1:])
	}
	return suffix
}

// TLD returns the last label of an ASCII domain name.
func TLD(name string) string {
	return name[strings.LastIndex(name, ".")+1:]
}

// validHostname reports whether s is a lower-case ASCII hostname: at most 253
// bytes, labels of 1-63 letters, digits and inner hyphens. It also guards the
// WHOIS referral and RDAP redirect targets, which come from remote servers.
func validHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 {
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

package models

import "regexp"

// hostnameRE matches an RFC 1123 hostname: dot-separated labels of
// alphanumerics and inner hyphens, each at most 63 characters.
var hostnameRE = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?$`)

// IsValidHostname reports whether s is a syntactically valid hostname of at
// most 253 bytes, without a trailing dot. A dotted IPv4 literal also matches
// and IPv6 does not; callers that accept IPs check net.ParseIP first. The API's config validators delegate here so the shared config structs'
// Validate methods and the API apply one hostname rule.
func IsValidHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	return hostnameRE.MatchString(s)
}

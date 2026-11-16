package secrets

import (
	"regexp"
	"strings"
)

// urlScheme is the start of an absolute URL: a scheme, then "://".
var urlScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// isSensitiveQueryName applies the credential name rules to a query
// parameter: the variable rule, which reads a name by its last part
// (token, access_token, api_key, password — not token_url), or one of the
// exact credential header names (authorization, x-api-key, …).
func isSensitiveQueryName(name string) bool {
	return IsSensitiveVariableName(name) || sensitiveHeaderExact[strings.ToLower(strings.TrimSpace(name))]
}

// urlParts splits an absolute URL into what is before the userinfo, the
// userinfo (without "@"), the rest of the authority and path, the raw query
// (without "?") and the fragment (with "#"). ok is false for anything that
// is not an absolute URL.
func urlParts(s string) (scheme, userinfo, hostPath, query, fragment string, hasUserinfo, hasQuery, ok bool) {
	loc := urlScheme.FindStringIndex(s)
	if loc == nil {
		return
	}
	scheme, rest := s[:loc[1]], s[loc[1]:]
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest, fragment = rest[:i], rest[i:]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest, query, hasQuery = rest[:i], rest[i+1:], true
	}
	authorityEnd := strings.IndexByte(rest, '/')
	if authorityEnd < 0 {
		authorityEnd = len(rest)
	}
	if at := strings.LastIndex(rest[:authorityEnd], "@"); at >= 0 {
		userinfo, rest, hasUserinfo = rest[:at], rest[at+1:], true
	}
	return scheme, userinfo, rest, query, fragment, hasUserinfo, hasQuery, true
}

func joinURL(scheme, userinfo, hostPath, query, fragment string, hasUserinfo, hasQuery bool) string {
	var b strings.Builder
	b.WriteString(scheme)
	if hasUserinfo {
		b.WriteString(userinfo)
		b.WriteByte('@')
	}
	b.WriteString(hostPath)
	if hasQuery {
		b.WriteByte('?')
		b.WriteString(query)
	}
	b.WriteString(fragment)
	return b.String()
}

// isLiteralCredential is a value worth masking: set, not already the mask,
// and not a {{reference}} (the reference is the protection).
func isLiteralCredential(value string) bool {
	return value != "" && value != MaskedSecret && !syntheticTemplateRef.MatchString(value)
}

// MaskURLCredentials replaces, in an absolute URL, the password of its
// userinfo and the values of its credential-named query parameters with
// "***", leaving the rest byte for byte. changed is false for anything else
// (not a URL, no credential).
func MaskURLCredentials(s string) (string, bool) {
	scheme, userinfo, hostPath, query, fragment, hasUserinfo, hasQuery, ok := urlParts(s)
	if !ok {
		return s, false
	}
	changed := false
	if hasUserinfo {
		if colon := strings.IndexByte(userinfo, ':'); colon >= 0 && isLiteralCredential(userinfo[colon+1:]) {
			userinfo = userinfo[:colon+1] + MaskedSecret
			changed = true
		}
	}
	if hasQuery && query != "" {
		pairs := strings.Split(query, "&")
		for i, pair := range pairs {
			name, value, found := strings.Cut(pair, "=")
			if found && isSensitiveQueryName(name) && isLiteralCredential(value) {
				pairs[i] = name + "=" + MaskedSecret
				changed = true
			}
		}
		query = strings.Join(pairs, "&")
	}
	if !changed {
		return s, false
	}
	return joinURL(scheme, userinfo, hostPath, query, fragment, hasUserinfo, hasQuery), true
}

// StripMaskedURLCredentials removes from an absolute URL the credentials
// MaskURLCredentials masked: a "***" userinfo password (the user name
// stays) and credential-named query parameters valued "***". A restore uses
// it when the URL a version holds no longer matches the stored one, so a
// mask is never written as if it were the credential. changed reports
// whether anything was removed.
func StripMaskedURLCredentials(s string) (string, bool) {
	scheme, userinfo, hostPath, query, fragment, hasUserinfo, hasQuery, ok := urlParts(s)
	if !ok {
		return s, false
	}
	changed := false
	if hasUserinfo {
		if colon := strings.IndexByte(userinfo, ':'); colon >= 0 && userinfo[colon+1:] == MaskedSecret {
			userinfo = userinfo[:colon]
			changed = true
		}
	}
	if hasQuery && query != "" {
		var kept []string
		for _, pair := range strings.Split(query, "&") {
			name, value, found := strings.Cut(pair, "=")
			if found && value == MaskedSecret && isSensitiveQueryName(name) {
				changed = true
				continue
			}
			kept = append(kept, pair)
		}
		query = strings.Join(kept, "&")
		hasQuery = len(kept) > 0
	}
	if !changed {
		return s, false
	}
	return joinURL(scheme, userinfo, hostPath, query, fragment, hasUserinfo, hasQuery), true
}

// maskURLStrings applies MaskURLCredentials to every string in a decoded
// config, at any depth. It reports whether anything changed.
func maskURLStrings(node any) (any, bool) {
	switch value := node.(type) {
	case string:
		return MaskURLCredentials(value)
	case map[string]any:
		changed := false
		for key, child := range value {
			if masked, did := maskURLStrings(child); did {
				value[key] = masked
				changed = true
			}
		}
		return value, changed
	case []any:
		changed := false
		for i, child := range value {
			if masked, did := maskURLStrings(child); did {
				value[i] = masked
				changed = true
			}
		}
		return value, changed
	default:
		return node, false
	}
}

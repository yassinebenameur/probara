package secrets

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// urlField is a config field the worker dials as a URL. when names sibling
// values the field's object must have (a browser step's url is a URL to
// load only for `goto`; for `assert_url` it is an expected value).
type urlField struct {
	path []string
	when map[string]string
}

// URLFields lists, per monitor type, the config fields that are URLs by
// schema (shared/models: HTTPMonitorConfig.URL, WebSocketMonitorConfig.URL,
// PrometheusMonitorConfig.URL, SyntheticAPIMonitorConfig.BaseURL and a
// step's request URL, SyntheticBrowserMonitorConfig.StartURL and a goto
// step's URL). Only these are scanned for URL credentials: bodies,
// assertion values and typed text are free text, stored as written. A new
// type with a URL field adds it here.
var URLFields = map[string][]urlField{
	"http":              {{path: []string{"url"}}},
	"websocket":         {{path: []string{"url"}}},
	"prometheus":        {{path: []string{"url"}}},
	"synthetic_api":     {{path: []string{"base_url"}}, {path: []string{"steps", "*", "request", "url"}}},
	"synthetic_browser": {{path: []string{"start_url"}}, {path: []string{"steps", "*", "url"}, when: map[string]string{"action": "goto"}}},
}

// WalkURLFields calls fn for every string value of a URL field
// (URLFields) present in a decoded config. where is the field's path
// ("url", "steps[0].request.url"); fn may replace parent[key].
func WalkURLFields(monitorType string, cfg map[string]any, fn func(where string, parent map[string]any, key string)) {
	for _, field := range URLFields[monitorType] {
		walkURLField(cfg, field, field.path, "", fn)
	}
}

func walkURLField(node any, field urlField, path []string, where string, fn func(string, map[string]any, string)) {
	if path[0] == "*" {
		items, _ := node.([]any)
		for i, item := range items {
			walkURLField(item, field, path[1:], where+"["+strconv.Itoa(i)+"]", fn)
		}
		return
	}
	object, ok := node.(map[string]any)
	if !ok {
		return
	}
	if len(path) == 1 {
		for key, want := range field.when {
			if object[key] != want {
				return
			}
		}
		if _, isString := object[path[0]].(string); isString {
			fn(joinPath(where, path[0]), object, path[0])
		}
		return
	}
	walkURLField(object[path[0]], field, path[1:], joinPath(where, path[0]), fn)
}

// urlScheme is the start of an absolute URL: a scheme, then "://".
var urlScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

var allDigits = regexp.MustCompile(`^[0-9]+$`)

// isSensitiveQueryName applies the credential name rules to a query
// parameter, after percent-decoding it (api%5Fkey is api_key): the variable
// rule, which reads a name by its last part (token, access_token, api_key,
// password — not token_url), or one of the exact credential header names
// (authorization, x-api-key, …).
func isSensitiveQueryName(name string) bool {
	if decoded, err := url.QueryUnescape(name); err == nil {
		name = decoded
	}
	return IsSensitiveVariableName(name) || sensitiveHeaderExact[strings.ToLower(strings.TrimSpace(name))]
}

// urlParts splits an absolute URL into the scheme with "://", the userinfo
// (without "@"), the host and path, the raw query (without "?") and the
// fragment (with "#"). The userinfo is found before the query and fragment
// are split off, so a password holding an unencoded '/', '?', '#' or '@'
// still reads as one: it ends at the last '@' before the first '/' that
// follows the first '@', and must hold a ':' after a user name free of
// '/', '?' and '#' (a host:port/path with an '@' later in the path is not a
// userinfo). ok is false for anything that is not an absolute URL.
func urlParts(s string) (scheme, userinfo, hostPath, query, fragment string, hasUserinfo, hasQuery, ok bool) {
	loc := urlScheme.FindStringIndex(s)
	if loc == nil {
		return
	}
	scheme, rest := s[:loc[1]], s[loc[1]:]
	if first := strings.IndexByte(rest, '@'); first >= 0 {
		end := len(rest)
		if slash := strings.IndexByte(rest[first:], '/'); slash >= 0 {
			end = first + slash
		}
		at := strings.LastIndex(rest[:end], "@")
		candidate := rest[:at]
		if colon := strings.IndexByte(candidate, ':'); colon >= 0 && !strings.ContainsAny(candidate[:colon], "/?#") {
			password := candidate[colon+1:]
			beforeSlash, _, _ := strings.Cut(password, "/")
			if !(strings.Contains(password, "/") && allDigits.MatchString(beforeSlash)) {
				userinfo, rest, hasUserinfo = candidate, rest[at+1:], true
			}
		}
	}
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest, fragment = rest[:i], rest[i:]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest, query, hasQuery = rest[:i], rest[i+1:], true
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
// (not a URL, no credential). Credentials carried in path segments are not
// recognized.
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

// maskURLFields applies MaskURLCredentials to the URL fields of a decoded
// config (URLFields). It reports whether anything changed.
func maskURLFields(monitorType string, cfg map[string]any) bool {
	changed := false
	WalkURLFields(monitorType, cfg, func(_ string, parent map[string]any, key string) {
		if masked, did := MaskURLCredentials(parent[key].(string)); did {
			parent[key] = masked
			changed = true
		}
	})
	return changed
}

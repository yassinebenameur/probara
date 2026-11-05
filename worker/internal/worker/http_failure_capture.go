package worker

import (
	"encoding/base64"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// httpFailureResponse is the redacted excerpt of the response a failed HTTP
// check got (capture_failure_response), stored at
// metrics_data.http.failure_response. It never reaches ErrorMessage: that
// text is quoted in notifications, while this stays in the check row.
//
// Every string is budgeted by its JSON-encoded size (jsonCost), so the
// stored document stays bounded whatever the server sends: the body by
// capture_body_bytes (at most 8 KiB), the headers by
// captureHeaderBudget in all. That keeps a row far below the AI evidence
// cutoff (64 KiB of metrics_data, worker/internal/airca).
type httpFailureResponse struct {
	StatusLine       string            `json:"status_line"`
	ContentType      string            `json:"content_type,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	HeadersTruncated bool              `json:"headers_truncated,omitempty"`
	BodyExcerpt      string            `json:"body_excerpt,omitempty"`
	BodyTruncated    bool              `json:"body_truncated,omitempty"`
}

const (
	captureRedacted       = "[REDACTED]"
	captureHeaderBudget   = 3072 // encoded bytes for all header names and values
	captureHeaderValueMax = 256  // encoded bytes per header value
	captureStatusLineMax  = 256
	captureMaxHeaders     = 40
)

// captureRedactedHeaderMarkers are substrings of a response header name
// whose value is never stored (besides secrets.IsSensitiveHeaderName and the
// exact names below).
var captureRedactedHeaderMarkers = []string{"token", "key", "secret", "auth", "session", "password", "cookie"}

// captureFailureResponse builds the excerpt of resp (whose body's first
// bytes are body; readCapped means the read stopped at the worker's body
// limit). Redaction drops the values of credential-like headers and of
// every header name the monitor sends, and scrubs the monitor's secret
// header values and basic-auth credentials wherever they appear.
func captureFailureResponse(resp *http.Response, body []byte, readCapped bool, config *models.HTTPMonitorConfig) *httpFailureResponse {
	redactor := newSecretRedactor(captureSecretValues(config))
	sentNames := map[string]bool{}
	for name := range config.Headers {
		sentNames[strings.ToLower(strings.TrimSpace(name))] = true
	}
	for name := range config.SecretHeaders {
		sentNames[strings.ToLower(strings.TrimSpace(name))] = true
	}

	out := &httpFailureResponse{}
	out.StatusLine, _ = truncateJSONString(redactor.redact(strings.TrimSpace(resp.Proto+" "+resp.Status)), captureStatusLineMax)
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		out.ContentType, _ = truncateJSONString(redactor.redact(ct), captureHeaderValueMax)
	}

	names := make([]string, 0, len(resp.Header))
	for name := range resp.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	used := 0
	for _, name := range names {
		if len(out.Headers) >= captureMaxHeaders {
			out.HeadersTruncated = true
			break
		}
		value := captureRedacted
		if !captureRedactsHeader(name, sentNames) {
			value, _ = truncateJSONString(redactor.redact(strings.Join(resp.Header.Values(name), ", ")), captureHeaderValueMax)
		}
		key, _ := truncateJSONString(name, 128)
		cost := jsonStringCost(key) + jsonStringCost(value) + 6 // quotes, colon, comma
		if used+cost > captureHeaderBudget {
			out.HeadersTruncated = true
			break
		}
		if out.Headers == nil {
			out.Headers = map[string]string{}
		}
		out.Headers[key] = value
		used += cost
	}

	budget := config.CaptureBodyLimit()
	// Redact a prefix long enough that a secret starting inside the excerpt
	// is wholly inside it; JSON cost is at least one byte per input byte, so
	// the excerpt never takes more than budget input bytes.
	prefix := body
	if limit := budget + redactor.longestNeedle(); len(prefix) > limit {
		prefix = prefix[:limit]
	}
	excerpt, cut := truncateJSONString(redactor.redact(string(prefix)), budget)
	out.BodyExcerpt = excerpt
	out.BodyTruncated = cut || len(prefix) < len(body) || readCapped
	return out
}

// captureRedactsHeader reports whether a response header's value is
// withheld: credentials and cookies, Proxy-*, anything token/key/secret
// shaped, and any name the monitor itself sends (a server that echoes
// request headers echoes the monitor's).
func captureRedactsHeader(name string, sentNames map[string]bool) bool {
	n := strings.ToLower(name)
	if sentNames[n] || secrets.IsSensitiveHeaderName(n) || strings.HasPrefix(n, "proxy-") {
		return true
	}
	switch n {
	case "authorization", "cookie", "set-cookie", "set-cookie2", "www-authenticate":
		return true
	}
	for _, marker := range captureRedactedHeaderMarkers {
		if strings.Contains(n, marker) {
			return true
		}
	}
	return false
}

// captureSecretValues are the monitor's credential values, scrubbed from
// every captured string: secret header values, sensitive-named plain header
// values, and basic auth (the password, and the base64 token the
// Authorization header carried).
func captureSecretValues(config *models.HTTPMonitorConfig) map[string]string {
	values := map[string]string{}
	add := func(key, v string) {
		if strings.TrimSpace(v) != "" && v != "***" {
			values[key] = v
		}
	}
	for name, v := range config.SecretHeaders {
		add("secret:"+name, v)
		// "Bearer abc": the token alone is the secret part too.
		if _, token, ok := strings.Cut(v, " "); ok && len(token) >= 8 {
			add("secret-token:"+name, token)
		}
	}
	for name, v := range config.Headers {
		if secrets.IsSensitiveHeaderName(name) {
			add("plain:"+name, v)
		}
	}
	if config.BasicAuthUsername != "" {
		add("basic-password", config.BasicAuthPassword)
		add("basic-token", base64.StdEncoding.EncodeToString([]byte(config.BasicAuthUsername+":"+config.BasicAuthPassword)))
	}
	return values
}

// longestNeedle is the longest form a redactor matches (0 for none).
func (r *secretRedactor) longestNeedle() int {
	if r == nil || len(r.needles) == 0 {
		return 0
	}
	return len(r.needles[0]) // sorted longest first
}

// truncateJSONString cuts s so that its JSON string encoding (as
// encoding/json writes it, quotes excluded) takes at most budget bytes,
// replacing invalid UTF-8 with U+FFFD and never splitting a rune. cut
// reports whether anything was dropped.
func truncateJSONString(s string, budget int) (out string, cut bool) {
	var b strings.Builder
	used := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			r = utf8.RuneError
		}
		cost := jsonRuneCost(r)
		if used+cost > budget {
			return b.String(), true
		}
		b.WriteRune(r)
		used += cost
		i += size
	}
	return b.String(), false
}

// jsonStringCost is the encoded size of s's content (quotes excluded).
func jsonStringCost(s string) int {
	n := 0
	for _, r := range s {
		n += jsonRuneCost(r)
	}
	return n
}

// jsonRuneCost is an upper bound of the bytes encoding/json spends on r
// inside a string: HTML-significant characters and control characters are
// \u-escaped (6 bytes), quote and backslash take 2.
func jsonRuneCost(r rune) int {
	switch {
	case r == '"' || r == '\\' || r == '\n' || r == '\r' || r == '\t':
		return 2
	case r < 0x20 || r == '<' || r == '>' || r == '&' || r == 0x2028 || r == 0x2029:
		return 6
	default:
		return utf8.RuneLen(r)
	}
}

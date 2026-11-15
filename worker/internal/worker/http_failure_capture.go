package worker

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// httpFailureResponse is the redacted excerpt of the response a failed HTTP
// check got, stored at metrics_data.http.failure_response on every failed or
// errored check that received a response (never on a success). It never
// reaches ErrorMessage: that text is quoted in notifications, while this
// stays in the check row. The keys are a stored contract (web
// HTTPFailureResponse): add keys, never rename them.
//
// Every string is budgeted by its JSON-encoded size, so the stored object
// stays under captureMaxBytes whatever the server sends. That keeps the row
// far below the 64 KiB of metrics_data past which the AI analysis drops a
// row's metrics (worker/internal/airca, shared/ai), and small on the NATS
// result message a private-location worker publishes.
type httpFailureResponse struct {
	StatusLine       string            `json:"status_line"`
	Headers          map[string]string `json:"headers,omitempty"`
	HeadersTruncated bool              `json:"headers_truncated,omitempty"`
	BodyExcerpt      string            `json:"body_excerpt,omitempty"`
	BodyTruncated    bool              `json:"body_truncated,omitempty"`
	// BodyNote stands in for a body not stored as text (binary content).
	BodyNote string `json:"body_note,omitempty"`
}

const (
	captureRedacted       = syntheticRedacted
	captureStatusLineMax  = 256  // encoded bytes
	captureHeaderNameMax  = 128  // encoded bytes per header name
	captureHeaderValueMax = 512  // encoded bytes per header value
	captureHeaderBudget   = 4096 // encoded bytes for all header names and values
	captureMaxHeaders     = 50
	captureBodyBudget     = 8192 // encoded bytes of body excerpt
	// captureMaxBytes bounds the whole encoded object: the budgets above
	// plus keys and punctuation stay below it (TestFailureCaptureBounded).
	captureMaxBytes = 16 * 1024
)

// captureFailureResponse builds the excerpt of resp, whose body's first
// bytes are body (readCapped: the read stopped at the worker's body limit,
// so more followed). Redaction comes first and covers the whole read body:
// the monitor's own credentials (secret headers, credential-named headers,
// basic auth) wherever they appear in any spelling secretRedactor knows,
// credential-shaped fields of the body, and the values of credential
// headers, cookies and every header the monitor itself sends (a server that
// echoes request headers echoes the monitor's). Only then is the text cut.
func captureFailureResponse(resp *http.Response, body []byte, readCapped bool, config *models.HTTPMonitorConfig) *httpFailureResponse {
	redactor := newSecretRedactor(captureSecretValues(config))
	scrub := func(s string) string { return redactCredentialFields(redactor.redact(s)) }
	sentNames := map[string]bool{}
	for name := range config.Headers {
		sentNames[strings.ToLower(strings.TrimSpace(name))] = true
	}
	for name := range config.SecretHeaders {
		sentNames[strings.ToLower(strings.TrimSpace(name))] = true
	}

	out := &httpFailureResponse{}
	out.StatusLine, _ = truncateJSONString(scrub(strings.TrimSpace(resp.Proto+" "+resp.Status)), captureStatusLineMax)

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
			value, _ = truncateJSONString(scrub(strings.Join(resp.Header.Values(name), ", ")), captureHeaderValueMax)
		}
		key, _ := truncateJSONString(name, captureHeaderNameMax)
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

	if len(body) == 0 {
		return out
	}
	if mediaType, binary := binaryBody(resp.Header.Get("Content-Type"), body); binary {
		size := fmt.Sprintf("%d bytes", len(body))
		if readCapped {
			size = "more than " + size
		}
		out.BodyNote = fmt.Sprintf("binary body not stored (%s, %s)", mediaType, size)
		return out
	}

	// Redact the whole read body, then cut. Cutting first leaks: every
	// earlier match shrinks the text and pulls an unredacted partial copy
	// from past the cut into the excerpt. A read that stopped at the limit
	// may end inside a secret, so a tail that begins one is dropped too.
	text := scrub(string(body))
	tailCut := false
	if readCapped {
		text, tailCut = redactor.trimPartialNeedle(text)
	}
	excerpt, cut := truncateJSONString(text, captureBodyBudget)
	out.BodyExcerpt = excerpt
	out.BodyTruncated = cut || tailCut || readCapped
	return out
}

// captureSecretValues are the monitor's credential values, scrubbed from
// every captured string: secret header values, credential-named plain
// header values, and basic auth (the password, and the base64 token the
// Authorization header carried).
func captureSecretValues(config *models.HTTPMonitorConfig) []string {
	var values []string
	add := func(v string) {
		if strings.TrimSpace(v) != "" && v != "***" {
			values = append(values, v)
		}
	}
	for _, v := range config.SecretHeaders {
		add(v)
		// "Bearer abc…": the token alone is the secret part too.
		if _, token, ok := strings.Cut(v, " "); ok && len(token) >= 8 {
			add(token)
		}
	}
	for name, v := range config.Headers {
		if secrets.IsSensitiveHeaderName(name) {
			add(v)
		}
	}
	if config.BasicAuthUsername != "" {
		add(config.BasicAuthPassword)
		add(base64.StdEncoding.EncodeToString([]byte(config.BasicAuthUsername + ":" + config.BasicAuthPassword)))
	}
	return values
}

// captureRedactsHeader reports whether a response header's value is
// withheld: cookies and credential headers (the request-header rule,
// secrets.IsSensitiveHeaderName, plus Set-Cookie and Proxy-*), anything
// session- or credential-named, and any name the monitor itself sends.
func captureRedactsHeader(name string, sentNames map[string]bool) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if sentNames[n] || isCredentialName(n) || strings.HasPrefix(n, "proxy-") {
		return true
	}
	switch n {
	case "set-cookie", "set-cookie2":
		return true
	}
	return false
}

// isCredentialName is the rule for a header or body field name whose value
// is withheld: the request-header rule (token, secret, password, apikey,
// authorization, cookie, …), the synthetic-variable rule (access_token,
// api_key, authToken, …), and session, cookie or credential names.
func isCredentialName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	if secrets.IsSensitiveHeaderName(n) || secrets.IsSensitiveVariableName(name) {
		return true
	}
	for _, marker := range []string{"session", "cookie", "credential", "private_key", "privatekey"} {
		if strings.Contains(n, marker) {
			return true
		}
	}
	return false
}

var (
	// "name": "value" (JSON), up to the closing quote or the end of the
	// read (a capped body may stop inside a value).
	credentialJSONField = regexp.MustCompile(`"([^"\\]{1,64})"(\s*:\s*")((?:[^"\\]|\\.)*)("|$)`)
	// name=value (forms, query strings, cookies echoed in text).
	credentialFormField = regexp.MustCompile(`(^|[?&;,\s{(])([A-Za-z0-9_.\-\[\]]{1,64})=([^&;,\s"'<>{}()]+)`)
	// name: value on a line of its own (YAML, header dumps, logs).
	credentialLineField = regexp.MustCompile(`(?m)^([ \t]*-?[ \t]*["']?)([A-Za-z0-9_.\-]{1,64})(["']?[ \t]*:[ \t]*)([^\r\n]*\S)`)
	// A bearer or basic credential in free text.
	credentialScheme = regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)([A-Za-z0-9\-._~+/]{8,}=*)`)
)

// redactCredentialFields replaces the values of obvious credential-shaped
// fields in captured text: JSON members, form and query parameters and
// "name: value" lines whose name isCredentialName, and bearer/basic tokens.
// It is best effort for credentials the worker does not know; the monitor's
// own are scrubbed exactly by secretRedactor first.
func redactCredentialFields(text string) string {
	text = replaceSubmatch(credentialJSONField, text, func(m []string) string {
		if !isCredentialName(m[1]) {
			return m[0]
		}
		return `"` + m[1] + `"` + m[2] + captureRedacted + m[4]
	})
	text = replaceSubmatch(credentialFormField, text, func(m []string) string {
		if !isCredentialName(m[2]) || m[3] == captureRedacted {
			return m[0]
		}
		return m[1] + m[2] + "=" + captureRedacted
	})
	text = replaceSubmatch(credentialLineField, text, func(m []string) string {
		if !isCredentialName(m[2]) || strings.Contains(m[4], captureRedacted) {
			return m[0]
		}
		return m[1] + m[2] + m[3] + captureRedacted
	})
	return replaceSubmatch(credentialScheme, text, func(m []string) string {
		// "Bearer authentication required" is prose: a credential is long or
		// carries a digit (JWTs, base64, opaque tokens all do).
		if len(m[3]) < 16 && !strings.ContainsAny(m[3], "0123456789") {
			return m[0]
		}
		return m[1] + m[2] + captureRedacted
	})
}

func replaceSubmatch(re *regexp.Regexp, text string, repl func([]string) string) string {
	return re.ReplaceAllStringFunc(text, func(match string) string {
		return repl(re.FindStringSubmatch(match))
	})
}

// binaryBody reports whether a body is stored as a note instead of text: a
// declared media type that is not textual, unless the bytes sniff as text,
// or no declared type and bytes that sniff as binary. A textual type with
// stray NUL or invalid UTF-8 (UTF-16 without a BOM, a broken encoder) is
// still text: truncateJSONString replaces what cannot be stored.
func binaryBody(contentType string, body []byte) (string, bool) {
	declared, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		declared = ""
	}
	if isTextualMediaType(declared) {
		return declared, false
	}
	sniffed, _, _ := mime.ParseMediaType(http.DetectContentType(body))
	if isTextualMediaType(sniffed) {
		return sniffed, false
	}
	if declared == "" {
		declared = sniffed
	}
	return declared, true
}

func isTextualMediaType(t string) bool {
	t = strings.ToLower(t)
	if strings.HasPrefix(t, "text/") || strings.HasSuffix(t, "+json") || strings.HasSuffix(t, "+xml") {
		return true
	}
	switch t {
	case "application/json", "application/xml", "application/javascript", "application/ecmascript",
		"application/x-www-form-urlencoded", "application/x-ndjson", "application/yaml",
		"application/x-yaml", "application/graphql", "application/soap+xml":
		return true
	}
	return false
}

// trimPartialNeedle drops the longest suffix of text that is a proper prefix
// of a secret spelling: the start of a secret whose rest was never read.
func (r *secretRedactor) trimPartialNeedle(text string) (string, bool) {
	if r == nil {
		return text, false
	}
	drop := 0
	for _, needle := range r.needles {
		for k := min(len(needle.text)-1, len(text)); k > drop; k-- {
			if strings.HasSuffix(text, needle.text[:k]) {
				drop = k
				break
			}
		}
	}
	return text[:len(text)-drop], drop > 0
}

// truncateJSONString cuts s so that its JSON string encoding (as
// encoding/json writes it, quotes excluded) takes at most budget bytes,
// replacing invalid UTF-8 and NUL with U+FFFD and never splitting a rune.
// cut reports whether anything was dropped.
//
// NUL must go: encoding/json writes it as \u0000, which PostgreSQL's jsonb
// rejects, so one NUL in a UTF-16 or binary-ish error body would make the
// whole check result unstorable (monitorstate scrubs it too, as a backstop).
func truncateJSONString(s string, budget int) (out string, cut bool) {
	var b strings.Builder
	used := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == 0 {
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
// \u-escaped (6 bytes), quote, backslash and the common controls take 2.
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

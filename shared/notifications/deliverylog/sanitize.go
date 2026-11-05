package deliverylog

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxErrorLength caps the stored error text, in characters.
const MaxErrorLength = 512

var (
	// urlRE finds absolute URLs, including the quoted ones Go's net/http
	// errors carry (`Post "https://…": …`).
	urlRE = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.\-]*://[^\s"'<>` + "`" + `]+`)
	// schemeAuthRE finds bearer tokens.
	schemeAuthRE = regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/=\-]{8,}`)
	// keyValueRE finds credential-named parameters in text, query-string
	// or JSON form.
	keyValueRE = regexp.MustCompile(`(?i)("?\b(?:access_token|refresh_token|token|api[_-]?key|apikey|app[_-]?key|routing[_-]?key|integration[_-]?key|secret|client_secret|password|passwd|pwd|authorization|signature|sig)"?\s*[:=]\s*)("[^"]*"|[^\s&,;"'}]+)`)
	// botTokenRE finds Telegram-style bot tokens outside a URL.
	botTokenRE = regexp.MustCompile(`\b\d{6,}:[A-Za-z0-9_\-]{30,}\b`)
)

// SanitizeError makes a send error safe to store and show: channel URLs
// often carry their credential in the path or query (Slack, Discord and
// Teams webhooks, Telegram bot tokens), and provider errors echo request
// details. Every URL is reduced to its scheme and host, credential-looking
// parameters and bearer tokens are masked, and the text is cut to
// MaxErrorLength characters.
func SanitizeError(text string) string {
	text = urlRE.ReplaceAllStringFunc(text, redactURL)
	text = schemeAuthRE.ReplaceAllString(text, "$1 ***")
	text = keyValueRE.ReplaceAllString(text, "$1***")
	text = botTokenRE.ReplaceAllString(text, "***")
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > MaxErrorLength {
		runes := []rune(text)
		text = string(runes[:MaxErrorLength-1]) + "…"
	}
	return text
}

// redactURL keeps a URL's scheme and host (which provider failed is useful)
// and drops its user info, path, query and fragment, where tokens live.
func redactURL(raw string) string {
	trailing := ""
	for len(raw) > 0 && strings.ContainsRune(".,;:)]", rune(raw[len(raw)-1])) {
		trailing = raw[len(raw)-1:] + trailing
		raw = raw[:len(raw)-1]
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "[url]" + trailing
	}
	out := u.Scheme + "://" + u.Host
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		out += "/…"
	}
	return out + trailing
}

package deliverylog

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// MaxErrorLength caps the stored error text, in characters.
const MaxErrorLength = 512

// minSecretLength keeps short values (a "true", a port) from masking
// unrelated text; credentials are longer.
const minSecretLength = 6

const mask = "***"

var (
	// urlRE finds absolute URLs, including the quoted ones Go's net/http
	// errors carry (`Post "https://…": …`).
	urlRE = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.\-]*://[^\s"'<>` + "`" + `]+`)
	// schemeAuthRE finds Authorization header values.
	schemeAuthRE = regexp.MustCompile(`(?i)\b(bearer|basic|token)\s+[A-Za-z0-9._~+/=\-]{8,}`)
	// keyValueRE finds credential-named parameters in text, query-string or
	// JSON form.
	// A quoted value may be unterminated: provider bodies are excerpted, and
	// the cut can fall inside the value.
	keyValueRE = regexp.MustCompile(`(?i)("?\b(?:access_token|refresh_token|token|api[_-]?key|apikey|app[_-]?key|routing[_-]?key|integration[_-]?key|secret|client_secret|password|passwd|pwd|authorization|signature|sig|auth_token)"?\s*[:=]\s*)("[^"]*"?|[^\s&,;"'}]+)`)
	// botTokenRE finds Telegram-style bot tokens outside a URL.
	botTokenRE = regexp.MustCompile(`\b\d{6,}:[A-Za-z0-9_\-]{30,}\b`)
	// emailRE and phoneRE find recipients a provider echoed (an SMTP
	// rejection names the address, an SMS API the number); they are masked
	// the way stored recipients are.
	emailRE = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)+`)
	phoneRE = regexp.MustCompile(`\+[1-9][0-9]{6,14}\b`)
)

// excerptCut is the mark plugin.CheckResponse leaves where it cut a
// provider body.
const excerptCut = "…"

// SanitizeError makes a send error safe to store and show. plugin.Post
// already drops the request URL from network errors, but provider responses
// are echoed into errors (CheckResponse's body excerpt), SMTP and other
// non-HTTP plugins report their own way, and channel URLs carry their
// credential in the path or query (Slack, Discord and Teams webhooks,
// Telegram bot tokens). So, in order:
//
//  1. JSON-escaped slashes are unescaped, so an echoed `https:\/\/…` URL is
//     seen as one;
//  2. every URL is reduced to its scheme and host (which provider failed
//     stays readable; paths, queries and user info go);
//  3. every value in secrets (the channel's secret config, SecretValues) is
//     replaced wherever it still appears, raw or URL-escaped, and so is any
//     prefix of one where the text was cut (an excerpt ending inside it);
//  4. the channel's configured recipients (recipients, RecipientValues) and
//     anything shaped like an email address or an E.164 number are masked
//     the way stored recipients are (MaskRecipient);
//  5. bot tokens, bearer/basic tokens and credential-named parameters
//     (quoted values terminated or not) are masked;
//  6. the text is cut to MaxErrorLength characters.
func SanitizeError(text string, secrets ...string) string {
	return sanitize(text, secrets, nil)
}

func sanitize(text string, secrets, recipients []string) string {
	text = strings.ReplaceAll(text, `\/`, "/")
	text = urlRE.ReplaceAllStringFunc(text, redactURL)
	text = maskSecrets(text, secrets)
	text = maskRecipients(text, recipients)
	// Bot tokens first: the other patterns would mask only their prefix.
	text = botTokenRE.ReplaceAllString(text, mask)
	text = schemeAuthRE.ReplaceAllString(text, "$1 "+mask)
	text = keyValueRE.ReplaceAllString(text, "$1"+mask)
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > MaxErrorLength {
		runes := []rune(text)
		text = string(runes[:MaxErrorLength-1]) + "…"
	}
	return text
}

func maskSecrets(text string, secrets []string) string {
	var forms []string
	for _, s := range secrets {
		if len(s) < minSecretLength {
			continue
		}
		forms = append(forms, s, url.PathEscape(s), url.QueryEscape(s))
	}
	// Longest first, so a secret that contains another is masked whole.
	sort.Slice(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })
	for _, f := range forms {
		text = strings.ReplaceAll(text, f, mask)
	}
	// An excerpt cut inside a secret leaves only its prefix, which the exact
	// match misses: mask the longest prefix that ends at a cut or at the end
	// of the text. URL-shaped values are left to the URL step, which already
	// cut them to their host (whose "https://host/…" would otherwise read as
	// a cut prefix).
	for _, f := range forms {
		if strings.Contains(f, "://") || strings.Contains(f, "%2F%2F") {
			continue
		}
		for k := len(f) - 1; k >= minSecretLength; k-- {
			prefix := f[:k]
			if strings.Contains(text, prefix+excerptCut) {
				text = strings.ReplaceAll(text, prefix+excerptCut, mask+excerptCut)
				break
			}
			if strings.HasSuffix(text, prefix) {
				text = strings.TrimSuffix(text, prefix) + mask
				break
			}
		}
	}
	return text
}

// maskRecipients replaces the known recipients, then anything that looks
// like an email address or a phone number, with its masked form.
func maskRecipients(text string, recipients []string) string {
	known := append([]string(nil), recipients...)
	sort.Slice(known, func(i, j int) bool { return len(known[i]) > len(known[j]) })
	for _, r := range known {
		if len(r) < minSecretLength {
			continue
		}
		text = strings.ReplaceAll(text, r, MaskRecipient(r))
	}
	text = emailRE.ReplaceAllStringFunc(text, MaskRecipient)
	return phoneRE.ReplaceAllStringFunc(text, MaskRecipient)
}

// RecipientValues returns a channel's configured recipients (email lists, the
// `to` field of an SMS or email channel), which a provider error may echo and
// SanitizeError masks.
func RecipientValues(manifest plugin.Manifest, config map[string]any) []string {
	var out []string
	for _, f := range manifest.Fields {
		if f.Type != plugin.FieldTypeEmailList && f.Key != "to" {
			continue
		}
		var raw []string
		switch v := config[f.Key].(type) {
		case string:
			raw = []string{v}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					raw = append(raw, s)
				}
			}
		case []string:
			raw = v
		}
		for _, r := range raw {
			for _, part := range strings.FieldsFunc(r, func(c rune) bool {
				return c == ',' || c == ';' || c == '\n' || c == '\r' || c == ' ' || c == '\t'
			}) {
				if part = strings.TrimSpace(part); part != "" {
					out = append(out, part)
				}
			}
		}
	}
	return out
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

// SecretValues returns the values SanitizeError must never let through for a
// channel: its secret fields and URL fields (a webhook URL is a credential
// even where the manifest does not mark it secret), as decrypted for the
// send, plus each line and each long word of a multi-line secret (a headers
// field), since a provider may echo one part of it.
func SecretValues(manifest plugin.Manifest, config map[string]any) []string {
	var out []string
	for _, f := range manifest.Fields {
		if !f.Secret && f.Type != plugin.FieldTypeURL && f.Type != plugin.FieldTypeSecret {
			continue
		}
		s, ok := config[f.Key].(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
		if f.Type == plugin.FieldTypeURL {
			// A URL that shows up in part is reduced to its host by the
			// URL step anyway.
			continue
		}
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			if line != s && line != "" {
				out = append(out, line)
			}
			if _, value, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(value) != "" {
				out = append(out, strings.TrimSpace(value))
			}
			for _, word := range strings.Fields(line) {
				if len(word) >= 12 {
					out = append(out, word)
				}
			}
		}
	}
	return out
}

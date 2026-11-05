package deliverylog

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

func TestSanitizeErrorStripsURLsAndCredentials(t *testing.T) {
	cases := []struct {
		name, in    string
		keep, leaks []string
	}{
		{
			name:  "webhook token in path",
			in:    `Post "https://hooks.slack.com/services/T000/B000/abcdefSECRET": context deadline exceeded`,
			keep:  []string{"https://hooks.slack.com/…", "context deadline exceeded"},
			leaks: []string{"abcdefSECRET", "T000"},
		},
		{
			name:  "bot token in path and outside a URL",
			in:    "telegram https://api.telegram.org/bot123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw/sendMessage failed; token 123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw",
			keep:  []string{"https://api.telegram.org/…", "failed"},
			leaks: []string{"AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"},
		},
		{
			name:  "query string and user info",
			in:    "dial https://user:pass@example.com:8443/x?api_key=k3y: refused.",
			keep:  []string{"https://example.com:8443/…", "refused."},
			leaks: []string{"user:pass", "k3y"},
		},
		{
			name:  "credential parameters",
			in:    `provider said {"error":"bad","api_key":"sk_live_123"} password=hunter2 Authorization: Bearer abcdefghijkl`,
			keep:  []string{`"error":"bad"`, "password=***"},
			leaks: []string{"sk_live_123", "hunter2", "abcdefghijkl"},
		},
		{
			name: "plain error untouched",
			in:   "webhook returned HTTP 404: not_found",
			keep: []string{"webhook returned HTTP 404: not_found"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeError(tc.in)
			for _, k := range tc.keep {
				if !strings.Contains(got, k) {
					t.Errorf("SanitizeError(%q) = %q, missing %q", tc.in, got, k)
				}
			}
			for _, l := range tc.leaks {
				if strings.Contains(got, l) {
					t.Errorf("SanitizeError(%q) = %q, leaks %q", tc.in, got, l)
				}
			}
		})
	}
}

func TestSanitizeErrorTruncates(t *testing.T) {
	got := SanitizeError(strings.Repeat("é", 2000))
	if n := utf8.RuneCountInString(got); n != MaxErrorLength {
		t.Fatalf("length = %d runes, want %d", n, MaxErrorLength)
	}
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated text must stay valid UTF-8 and end with an ellipsis")
	}
}

func TestSendOutcome(t *testing.T) {
	if got := SendOutcome(nil); got != OutcomeDelivered {
		t.Errorf("nil = %s", got)
	}
	if got := SendOutcome(errors.New("503")); got != OutcomeFailed {
		t.Errorf("transient = %s", got)
	}
	if got := SendOutcome(fmt.Errorf("send: %w", plugin.Permanent(errors.New("404")))); got != OutcomePermanentFailure {
		t.Errorf("permanent = %s", got)
	}
}

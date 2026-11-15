package deliverylog

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/netguard"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

func TestSanitizeErrorStripsCredentials(t *testing.T) {
	const (
		webhook  = "https://hooks.slack.com/services/T0000/B0000/XXXXXXXXXXXXXXXXXXXXXXXX"
		botToken = "123456789:AAH4kq3x-very-secret-telegram-bot-token"
		apiKey   = "sk_live_51HxYzVerySecretKey"
	)
	for _, tc := range []struct {
		name    string
		in      string
		secrets []string
		absent  []string
		present []string
	}{
		{
			name:    "net/http error quoting the webhook URL",
			in:      `Post "` + webhook + `": dial tcp 10.0.0.1:443: i/o timeout`,
			absent:  []string{"/services/", "XXXXXXXX"},
			present: []string{"https://hooks.slack.com/…", "i/o timeout"},
		},
		{
			name:    "telegram bot token in a path and alone",
			in:      "https://api.telegram.org/bot" + botToken + "/sendMessage failed; token " + botToken,
			absent:  []string{botToken, "very-secret"},
			present: []string{"https://api.telegram.org/…"},
		},
		{
			name:   "bearer and basic authorization echoed by a provider",
			in:     "provider said: Authorization: Bearer abcdefghijklmnop1234 rejected; Basic dXNlcjpwYXNzd29yZA==",
			absent: []string{"abcdefghijklmnop1234", "dXNlcjpwYXNzd29yZA=="},
		},
		{
			name:   "credential-named parameters in query, JSON and text",
			in:     `api_key=` + apiKey + `&x=1 {"routing_key":"R0UT1NGKEYVALUE"} password: hunter2hunter2`,
			absent: []string{apiKey, "R0UT1NGKEYVALUE", "hunter2hunter2"},
		},
		{
			name:    "the channel's own secret values, raw and URL-escaped, outside any pattern",
			in:      "provider echoed sk_live_51HxYzVerySecretKey and " + url.QueryEscape("p@ss w/rd+longer"),
			secrets: []string{apiKey, "p@ss w/rd+longer"},
			absent:  []string{apiKey, url.QueryEscape("p@ss w/rd+longer")},
			present: []string{"provider echoed ***"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeError(tc.in, tc.secrets...)
			for _, s := range tc.absent {
				if strings.Contains(got, s) {
					t.Errorf("SanitizeError(%q) = %q, still contains %q", tc.in, got, s)
				}
			}
			for _, s := range tc.present {
				if !strings.Contains(got, s) {
					t.Errorf("SanitizeError(%q) = %q, want it to contain %q", tc.in, got, s)
				}
			}
		})
	}
}

func TestSanitizeErrorTruncates(t *testing.T) {
	got := SanitizeError(strings.Repeat("é", MaxErrorLength*2))
	if n := len([]rune(got)); n != MaxErrorLength {
		t.Fatalf("length = %d runes, want %d", n, MaxErrorLength)
	}
}

func TestSecretValuesCoversSecretAndURLFields(t *testing.T) {
	manifest := plugin.Manifest{Fields: []plugin.Field{
		{Key: "webhook_url", Type: plugin.FieldTypeURL},
		{Key: "token", Type: plugin.FieldTypeSecret, Secret: true},
		{Key: "headers", Type: plugin.FieldTypeTextarea, Secret: true},
		{Key: "channel", Type: plugin.FieldTypeString},
	}}
	got := SecretValues(manifest, map[string]any{
		"webhook_url": "https://example.com/hook/abc",
		"token":       "tok-123456",
		"headers":     "X-Api-Key: header-secret-value\nX-Other: second-secret-value",
		"channel":     "#alerts",
	})
	text := SanitizeError("a https://example.com/hook/abc b tok-123456 c header-secret-value d second-secret-value e #alerts", got...)
	for _, s := range []string{"/hook/abc", "tok-123456", "header-secret-value", "second-secret-value"} {
		if strings.Contains(text, s) {
			t.Errorf("sanitized %q still contains %q", text, s)
		}
	}
	if !strings.Contains(text, "#alerts") {
		t.Errorf("sanitized %q lost a non-secret value", text)
	}
}

func TestMaskRecipient(t *testing.T) {
	for in, want := range map[string]string{
		"+15557654321":       "********4321",
		"alice@example.com":  "a***@example.com",
		"éloise@example.com": "é***@example.com",
		"123":                "****",
	} {
		if got := MaskRecipient(in); got != want {
			t.Errorf("MaskRecipient(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSendOutcome(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusUnauthorized, Body: http.NoBody, Header: http.Header{}}
	rejected := plugin.CheckResponse(resp, "stub")
	for _, tc := range []struct {
		err     error
		outcome Outcome
		reason  Reason
	}{
		{nil, OutcomeDelivered, ""},
		{errors.New("timeout"), OutcomeFailed, ""},
		{rejected, OutcomePermanentFailure, ""},
		{plugin.Permanent(errors.Join(errors.New("post stub"), netguard.ErrBlocked)), OutcomePermanentFailure, ReasonEgressBlocked},
	} {
		o, r := SendOutcome(tc.err)
		if o != tc.outcome || r != tc.reason {
			t.Errorf("SendOutcome(%v) = (%s, %s), want (%s, %s)", tc.err, o, r, tc.outcome, tc.reason)
		}
	}
	if s, ok := plugin.ResponseStatus(rejected); !ok || s != http.StatusUnauthorized {
		t.Errorf("ResponseStatus = (%d, %v), want 401", s, ok)
	}
}

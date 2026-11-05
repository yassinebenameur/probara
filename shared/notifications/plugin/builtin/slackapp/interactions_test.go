package slackapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signedHeader(secret string, ts time.Time, body []byte) http.Header {
	h := http.Header{}
	raw := strconv.FormatInt(ts.Unix(), 10)
	h.Set("X-Slack-Request-Timestamp", raw)
	h.Set("X-Slack-Signature", Sign(secret, raw, body))
	return h
}

func TestVerifySignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte("payload=%7B%7D")
	const secret = "8f742231b10e8888abcd99yyyzzz85a5"

	if err := VerifySignature(secret, signedHeader(secret, now, body), body, now); err != nil {
		t.Fatalf("valid signature: %v", err)
	}
	// Slack's own documented example.
	slackBody := []byte("token=xyzz0WbapA4vBCDEFasx0q6G&team_id=T1DC2JH3J&team_domain=testteamnow&channel_id=G8PSS9T3V&channel_name=foobar&user_id=U2CERLKJA&user_name=roadrunner&command=%2Fwebhook-collect&text=&response_url=https%3A%2F%2Fhooks.slack.com%2Fcommands%2FT1DC2JH3J%2F397700885554%2F96rGlfmibIGlgcZRskXaIFfN&trigger_id=398738663015.47445629121.803a0bc887a14d10d2c447fce8b6703c")
	h := http.Header{}
	h.Set("X-Slack-Request-Timestamp", "1531420618")
	h.Set("X-Slack-Signature", "v0=a2114d57b48eac39b9ad189dd8316235a7b4a8d21a10bd27519666489c69b503")
	if err := VerifySignature(secret, h, slackBody, time.Unix(1531420618, 0)); err != nil {
		t.Fatalf("Slack's documented example: %v", err)
	}

	cases := []struct {
		name   string
		header http.Header
		body   []byte
		secret string
		want   error
	}{
		{"tampered body", signedHeader(secret, now, body), []byte("payload=%7B%22x%22%7D"), secret, ErrSignature},
		{"wrong secret", signedHeader("other", now, body), body, secret, ErrSignature},
		{"no secret configured", signedHeader("", now, body), body, "", ErrSignature},
		{"stale", signedHeader(secret, now.Add(-MaxSkew-time.Second), body), body, secret, ErrStale},
		{"future", signedHeader(secret, now.Add(MaxSkew+time.Second), body), body, secret, ErrStale},
		{"missing headers", http.Header{}, body, secret, ErrSignature},
	}
	for _, tc := range cases {
		if err := VerifySignature(tc.secret, tc.header, tc.body, now); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	// Inside the skew window either way is fine.
	if err := VerifySignature(secret, signedHeader(secret, now.Add(-4*time.Minute), body), body, now); err != nil {
		t.Fatalf("4 minutes old: %v", err)
	}
}

func TestParseInteraction(t *testing.T) {
	payload := `{"type":"block_actions","team":{"id":"T0TEAM"},"user":{"id":"U1","username":"ada","team_id":"T0TEAM"},
		"container":{"type":"message","channel_id":"C0PROBARA","message_ts":"1.2"},"channel":{"id":"C0PROBARA"},
		"response_url":"https://hooks.slack.com/actions/T/1/x",
		"actions":[{"action_id":"probara_ack","value":"a-1:ch-1","type":"button"}]}`
	in, err := ParseInteraction([]byte(url.Values{"payload": {payload}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	if in.Type != "block_actions" || in.TeamID != "T0TEAM" || in.ChannelID != "C0PROBARA" || in.UserName != "ada" ||
		len(in.Actions) != 1 || in.Actions[0].ActionID != ActionAcknowledge {
		t.Fatalf("parsed = %+v", in)
	}
	alertID, ch, ok := ParseAckValue(in.Actions[0].Value)
	if !ok || alertID != "a-1" || ch != "ch-1" {
		t.Fatalf("ack value = %q %q %v", alertID, ch, ok)
	}
	if _, _, ok := ParseAckValue("nocolon"); ok {
		t.Fatal("malformed value accepted")
	}
	if _, err := ParseInteraction([]byte("nopayload=1")); err == nil {
		t.Fatal("missing payload accepted")
	}
}

func TestResponderReply(t *testing.T) {
	r := &Responder{HTTP: http.DefaultClient}
	if err := r.Reply(context.Background(), "https://example.com/hook", "x"); err == nil {
		t.Fatal("non-Slack response_url accepted")
	}
	if err := r.Reply(context.Background(), "http://hooks.slack.com/actions/x", "x"); err == nil {
		t.Fatal("plain-http response_url accepted")
	}
	if !strings.Contains(ReplyText(OutcomeAcknowledged, "API <x>"), "*API &lt;x&gt;*") {
		t.Fatal("reply does not escape the subject")
	}
}

func TestOAuthExchange(t *testing.T) {
	var gotUser, gotPass, gotCode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		gotCode = form.Get("code")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "access_token": "xoxb-new", "token_type": "bot",
			"team":             map[string]any{"id": "T0TEAM", "name": "Acme"},
			"incoming_webhook": map[string]any{"channel": "#alerts", "channel_id": "C0ALERTS", "url": "https://hooks.slack.com/services/x"},
		})
	}))
	defer srv.Close()
	c := &OAuthClient{BaseURL: srv.URL, HTTP: srv.Client()}
	inst, err := c.Exchange(context.Background(), "cid", "csecret", "the-code", "https://probara.example.com/cb")
	if err != nil {
		t.Fatal(err)
	}
	if gotUser != "cid" || gotPass != "csecret" || gotCode != "the-code" {
		t.Fatalf("request: user=%q pass=%q code=%q", gotUser, gotPass, gotCode)
	}
	want := Installation{BotToken: "xoxb-new", TeamID: "T0TEAM", TeamName: "Acme", ChannelID: "C0ALERTS", ChannelName: "alerts"}
	if inst != want {
		t.Fatalf("installation = %+v", inst)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_code"}`))
	}))
	defer bad.Close()
	if _, err := (&OAuthClient{BaseURL: bad.URL, HTTP: bad.Client()}).Exchange(context.Background(), "a", "b", "c", "d"); err == nil {
		t.Fatal("invalid_code accepted")
	}
	if u := AuthorizeURL("cid", "st", "https://x/cb"); !strings.Contains(u, "scope=chat%3Awrite%2Cchat%3Awrite.public%2Cincoming-webhook") {
		t.Fatalf("authorize URL = %s", u)
	}
}

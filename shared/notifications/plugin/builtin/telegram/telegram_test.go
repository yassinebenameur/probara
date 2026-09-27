package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

const token = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"

func TestMain(m *testing.M) {
	// httptest servers listen on loopback, which the default egress policy
	// refuses; the policy itself is covered in package plugin.
	plugin.Configure(plugin.Runtime{AppBaseURL: "https://probara.example.com"})
	os.Exit(m.Run())
}

func TestValidate(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr bool
	}{
		{`{"bot_token":"` + token + `","chat_id":"-1001234567890"}`, false},
		{`{"bot_token":"` + token + `","chat_id":"@ops_alerts","message_thread_id":"42"}`, false},
		{`{"bot_token":"nope","chat_id":"1"}`, true},
		{`{"bot_token":"` + token + `","chat_id":"ops alerts"}`, true},
		{`{"bot_token":"` + token + `","chat_id":"1","message_thread_id":"x"}`, true},
		{`{}`, true},
	}
	for _, tc := range cases {
		if err := New().Validate(json.RawMessage(tc.raw)); (err != nil) != tc.wantErr {
			t.Errorf("Validate(%s) err=%v wantErr=%v", tc.raw, err, tc.wantErr)
		}
	}
}

func sampleEvent() notifications.AlertEvent {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	lastErr := `upstream said <b>nope</b> & "bye"`
	return notifications.AlertEvent{
		Type:      "created",
		TenantID:  "tenant-1",
		Timestamp: now,
		Alert: notifications.AlertDetails{
			ID:           "a-1",
			MonitorID:    "3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234",
			MonitorName:  "API <health>",
			PolicyName:   "Critical",
			Status:       "active",
			TriggeredAt:  now,
			FailureCount: 3,
			LastError:    &lastErr,
		},
	}
}

func TestSend_PostsEscapedHTMLToBotPath(t *testing.T) {
	var gotPath string
	var got sendMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	p := New()
	p.base = srv.URL
	err := p.Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"bot_token": token, "chat_id": "-100123", "message_thread_id": "7"}},
		Event:   sampleEvent(),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/bot"+token+"/sendMessage" {
		t.Errorf("path = %q", gotPath)
	}
	if got.ChatID != "-100123" || got.MessageThreadID != 7 || got.ParseMode != "HTML" {
		t.Errorf("message = %+v", got)
	}
	for _, want := range []string{
		"<b>Alert Triggered: API &lt;health&gt;</b>",
		"<pre>upstream said &lt;b&gt;nope&lt;/b&gt; &amp; &#34;bye&#34;</pre>",
		`<a href="https://probara.example.com/monitors/3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234">Open the monitor</a>`,
	} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("text missing %q:\n%s", want, got.Text)
		}
	}
}

func TestRender_TruncatesLongErrorButKeepsLink(t *testing.T) {
	ev := sampleEvent()
	long := strings.Repeat("x", 10000)
	ev.Alert.LastError = &long
	text := render(plugin.DispatchRequest{Event: ev}.View())
	if n := len([]rune(text)); n > maxMessage {
		t.Fatalf("len = %d, over the %d limit", n, maxMessage)
	}
	if !strings.HasSuffix(text, "Open the monitor</a>") {
		t.Fatalf("link was truncated away: …%s", text[len(text)-80:])
	}
}

func TestSend_ChatNotFoundIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	defer srv.Close()
	p := New()
	p.base = srv.URL
	err := p.Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"bot_token": token, "chat_id": "1"}},
		Event:   sampleEvent(),
	})
	if !plugin.IsPermanent(err) || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal("error leaked the bot token")
	}
}

// assertWellFormed checks what Telegram's HTML parser rejects: a bare or
// partial entity, or an unbalanced tag.
func assertWellFormed(t *testing.T, text string) {
	t.Helper()
	if n := len([]rune(text)); n > maxMessage {
		t.Fatalf("len = %d, over the %d limit", n, maxMessage)
	}
	for i := strings.IndexByte(text, '&'); i >= 0; {
		rest := text[i:]
		if !regexp.MustCompile(`^&(amp|lt|gt|quot|#[0-9]+);`).MatchString(rest) {
			t.Fatalf("broken entity at %q", rest[:min(len(rest), 12)])
		}
		next := strings.IndexByte(rest[1:], '&')
		if next < 0 {
			break
		}
		i += next + 1
	}
	for _, tag := range []string{"b", "pre", "a"} {
		open := strings.Count(text, "<"+tag+">") + strings.Count(text, "<"+tag+" ")
		if open != strings.Count(text, "</"+tag+">") {
			t.Fatalf("unbalanced <%s> in %q", tag, text[len(text)-120:])
		}
	}
}

// Characters that expand when escaped must not push markup past the limit:
// the old renderer measured the error before escaping, then cut the finished
// HTML, leaving an unclosed <pre> that Telegram rejects outright.
func TestRender_ExpandingErrorStaysWellFormed(t *testing.T) {
	for _, s := range []string{
		strings.Repeat("&", 5000),
		strings.Repeat(`<"x">`, 3000),
		strings.Repeat("é&", 4000),
	} {
		ev := sampleEvent()
		ev.Alert.LastError = &s
		text := render(plugin.DispatchRequest{Event: ev}.View())
		assertWellFormed(t, text)
		if !strings.HasSuffix(text, "Open the monitor</a>") {
			t.Fatalf("link was dropped")
		}
	}
}

func TestRender_HugeFactsStayWellFormed(t *testing.T) {
	ev := sampleEvent()
	for i := 0; i < 200; i++ {
		ev.Alert.ImpactedMonitors = append(ev.Alert.ImpactedMonitors, notifications.ImpactedMonitor{ID: "x", Name: strings.Repeat("&<>", 40)})
	}
	name := strings.Repeat("&", 2000)
	ev.Alert.RootCauseMonitorName = &name
	assertWellFormed(t, render(plugin.DispatchRequest{Event: ev}.View()))
}

package ntfy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

func TestMain(m *testing.M) {
	plugintest.AllowLoopback()
	os.Exit(m.Run())
}

func TestManifest(t *testing.T) {
	m := New().Manifest()
	if m.Type != "ntfy" || !m.HasCapability(plugin.CapabilityTestable) || !m.HasCapability(plugin.CapabilityFollowUp) {
		t.Fatalf("manifest = %+v", m)
	}
	if m.HasCapability(plugin.CapabilityAcknowledge) {
		t.Error("ntfy has no acknowledge API")
	}
	plugintest.AssertSecrets(t, New(), map[string]any{
		"server_url": "https://ntfy.example.com", "topic": "alerts", "access_token": "tk_x",
		"username": "bot", "password": "pw",
	}, "topic", "access_token", "password")
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"default server", `{"topic":"probara-alerts"}`, false},
		{"own server with token", `{"server_url":"https://ntfy.example.com","topic":"a_b-1","access_token":"tk_abc"}`, false},
		{"basic auth", `{"topic":"t","username":"u","password":"p"}`, false},
		{"missing topic", `{}`, true},
		{"bad topic", `{"topic":"has space"}`, true},
		{"topic too long", `{"topic":"` + strings.Repeat("a", 65) + `"}`, true},
		{"http server", `{"server_url":"http://ntfy.example.com","topic":"t"}`, true},
		{"embedded credentials", `{"server_url":"https://u:p@ntfy.example.com","topic":"t"}`, true},
		{"token and basic", `{"topic":"t","access_token":"tk","username":"u","password":"p"}`, true},
		{"username without password", `{"topic":"t","username":"u"}`, true},
		{"empty", ``, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := New().Validate(json.RawMessage(tc.raw)); (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

type captured struct {
	method, path, auth, contentType string
	body                            message
}

func capture(t *testing.T) (*httptest.Server, *captured) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path = r.Method, r.URL.Path
		got.auth, got.contentType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got.body); err != nil {
			t.Errorf("body is not JSON: %s", raw)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestSend_PublishesJSONWithToken(t *testing.T) {
	srv, got := capture(t)
	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{ID: "ch-1", Config: map[string]any{
			"server_url": srv.URL + "/", "topic": "alerts", "access_token": "tk_secret",
		}},
		Event: plugintest.SampleEvent(),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.method != http.MethodPost || got.path != "/" || got.contentType != "application/json" {
		t.Errorf("request = %s %s (%s), want POST / JSON", got.method, got.path, got.contentType)
	}
	if got.auth != "Bearer tk_secret" {
		t.Errorf("Authorization = %q", got.auth)
	}
	b := got.body
	if b.Topic != "alerts" || b.Priority != 5 || len(b.Tags) != 1 || b.Tags[0] != "rotating_light" {
		t.Errorf("body = %+v", b)
	}
	if !strings.Contains(b.Title, "API health") || !strings.Contains(b.Message, "i/o timeout") {
		t.Errorf("wording missing: title %q message %q", b.Title, b.Message)
	}
	if b.Click != "https://probara.example.com/monitors/m-1" || len(b.Actions) != 1 || b.Actions[0].URL != b.Click {
		t.Errorf("deep link missing: click %q actions %+v", b.Click, b.Actions)
	}
}

func TestSend_BasicAuth(t *testing.T) {
	srv, got := capture(t)
	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{
			"server_url": srv.URL, "topic": "alerts", "username": "bot", "password": "pw",
		}},
		Event: plugintest.SampleEvent(),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("bot:pw")); got.auth != want {
		t.Errorf("Authorization = %q, want %q", got.auth, want)
	}
}

func TestSend_TestSendAndResolvedPriority(t *testing.T) {
	srv, got := capture(t)
	ev := plugintest.SampleEvent()
	if err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"server_url": srv.URL, "topic": "alerts"}},
		Event:   ev, EventType: "created", Test: true,
	}); err != nil {
		t.Fatalf("test send: %v", err)
	}
	if got.auth != "" {
		t.Errorf("unauthenticated topic sent Authorization %q", got.auth)
	}
	ev.Type, ev.Alert.Status = "resolved", "resolved"
	if err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"server_url": srv.URL, "topic": "alerts"}},
		Event:   ev, EventType: "resolved",
	}); err != nil {
		t.Fatalf("resolved send: %v", err)
	}
	if got.body.Priority != 3 || got.body.Tags[0] != "white_check_mark" {
		t.Errorf("resolved body = %+v", got.body)
	}
}

func TestSend_Classification(t *testing.T) {
	plugintest.AssertClassification(t, func(ctx context.Context, url string) error {
		return New().Send(ctx, plugin.DispatchRequest{
			Channel: plugin.ChannelRef{Config: map[string]any{"server_url": url, "topic": "alerts"}},
			Event:   plugintest.SampleEvent(),
		})
	})
}

func TestSend_MissingTopicIsPermanent(t *testing.T) {
	err := New().Send(context.Background(), plugin.DispatchRequest{Channel: plugin.ChannelRef{Config: map[string]any{}}})
	if !plugin.IsPermanent(err) {
		t.Fatalf("err = %v, want permanent", err)
	}
}

func TestBuild_MessageFitsLimit(t *testing.T) {
	ev := plugintest.SampleEvent()
	long := strings.Repeat("é", 5000)
	ev.Alert.LastError = &long
	msg := build("t", plugin.DispatchRequest{Event: ev}.View())
	if len(msg.Message) > maxMessage {
		t.Fatalf("message is %d bytes, limit %d", len(msg.Message), maxMessage)
	}
	if !strings.Contains(msg.Message, "Last error") {
		t.Error("clipped error dropped entirely")
	}
}

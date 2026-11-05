package gotify

import (
	"context"
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
	if m.Type != "gotify" || !m.HasCapability(plugin.CapabilityTestable) || !m.HasCapability(plugin.CapabilityFollowUp) {
		t.Fatalf("manifest = %+v", m)
	}
	if m.HasCapability(plugin.CapabilityAcknowledge) {
		t.Error("gotify has no acknowledge API")
	}
	plugintest.AssertSecrets(t, New(), map[string]any{
		"server_url": "https://gotify.example.com", "app_token": "Axyz",
	}, "app_token")
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid", `{"server_url":"https://gotify.example.com","app_token":"AbC.123"}`, false},
		{"path prefix", `{"server_url":"https://example.com/gotify/","app_token":"A"}`, false},
		{"missing token", `{"server_url":"https://gotify.example.com"}`, true},
		{"missing server", `{"app_token":"A"}`, true},
		{"http", `{"server_url":"http://gotify.example.com","app_token":"A"}`, true},
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

func TestSend_PostsMessageWithHeaderToken(t *testing.T) {
	var method, path, query, key string
	var body message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, query, key = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Gotify-Key")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("body is not JSON: %s", raw)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"server_url": srv.URL + "/gotify/", "app_token": "Asecret"}},
		Event:   plugintest.SampleEvent(),
		Test:    true,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if method != http.MethodPost || path != "/gotify/message" {
		t.Errorf("request = %s %s, want POST /gotify/message", method, path)
	}
	if key != "Asecret" || strings.Contains(query, "token") {
		t.Errorf("token must ride in X-Gotify-Key only: header %q query %q", key, query)
	}
	if body.Priority != 8 || !strings.Contains(body.Title, "API health") || !strings.Contains(body.Message, "i/o timeout") {
		t.Errorf("body = %+v", body)
	}
	if !strings.Contains(body.Message, "https://probara.example.com/monitors/m-1") {
		t.Errorf("deep link missing from message: %q", body.Message)
	}
	notif, _ := body.Extras["client::notification"].(map[string]any)
	click, _ := notif["click"].(map[string]any)
	if click["url"] != "https://probara.example.com/monitors/m-1" {
		t.Errorf("click extra = %+v", body.Extras)
	}
}

func TestSend_Classification(t *testing.T) {
	plugintest.AssertClassification(t, func(ctx context.Context, url string) error {
		return New().Send(ctx, plugin.DispatchRequest{
			Channel: plugin.ChannelRef{Config: map[string]any{"server_url": url, "app_token": "A"}},
			Event:   plugintest.SampleEvent(),
		})
	})
}

func TestSend_MissingConfigIsPermanent(t *testing.T) {
	err := New().Send(context.Background(), plugin.DispatchRequest{Channel: plugin.ChannelRef{Config: map[string]any{"server_url": "https://g.example.com"}}})
	if !plugin.IsPermanent(err) {
		t.Fatalf("err = %v, want permanent", err)
	}
}

func TestPriorityFor(t *testing.T) {
	ev := plugintest.SampleEvent()
	ev.Type, ev.Alert.Status = "resolved", "resolved"
	if got := build(plugin.DispatchRequest{Event: ev}.View()).Priority; got != 4 {
		t.Errorf("resolved priority = %d, want 4", got)
	}
}

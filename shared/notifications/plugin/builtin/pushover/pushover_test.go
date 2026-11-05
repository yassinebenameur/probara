package pushover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

const (
	token = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"
	user  = "uuuuuuuuuuuuuuuuuuuuuuuuuuuuu1"
)

func TestMain(m *testing.M) {
	plugintest.AllowLoopback()
	os.Exit(m.Run())
}

func testPlugin(base string) *Plugin {
	p := New()
	p.base = base
	return p
}

func TestManifest(t *testing.T) {
	m := New().Manifest()
	if m.Type != "pushover" || !m.HasCapability(plugin.CapabilityTestable) || !m.HasCapability(plugin.CapabilityFollowUp) {
		t.Fatalf("manifest = %+v", m)
	}
	if m.HasCapability(plugin.CapabilityAcknowledge) {
		t.Error("pushover is sent without emergency receipts, so there is nothing to acknowledge")
	}
	if New().base != "https://api.pushover.net" {
		t.Error("production base must be the fixed Pushover API")
	}
	plugintest.AssertSecrets(t, New(), map[string]any{
		"app_token": token, "user_key": user, "device": "iphone",
	}, "app_token", "user_key")
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid", `{"app_token":"` + token + `","user_key":"` + user + `"}`, false},
		{"devices", `{"app_token":"` + token + `","user_key":"` + user + `","device":"iphone,desk_top-2"}`, false},
		{"short token", `{"app_token":"abc","user_key":"` + user + `"}`, true},
		{"missing user", `{"app_token":"` + token + `"}`, true},
		{"bad device", `{"app_token":"` + token + `","user_key":"` + user + `","device":"my phone"}`, true},
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

func TestSend_PostsMessage(t *testing.T) {
	var method, path string
	var body message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("body is not JSON: %s", raw)
		}
		_, _ = w.Write([]byte(`{"status":1,"request":"r"}`))
	}))
	defer srv.Close()

	err := testPlugin(srv.URL).Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"app_token": token, "user_key": user, "device": "iphone"}},
		Event:   plugintest.SampleEvent(),
		Test:    true,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if method != http.MethodPost || path != "/1/messages.json" {
		t.Errorf("request = %s %s", method, path)
	}
	if body.Token != token || body.User != user || body.Device != "iphone" {
		t.Errorf("credentials = %+v", body)
	}
	if body.Priority != 1 {
		t.Errorf("down priority = %d, want 1 (never emergency)", body.Priority)
	}
	if !strings.Contains(body.Title, "API health") || !strings.Contains(body.Message, "i/o timeout") {
		t.Errorf("wording missing: %+v", body)
	}
	if body.URL != "https://probara.example.com/monitors/m-1" || body.URLTitle == "" {
		t.Errorf("deep link = %q %q", body.URL, body.URLTitle)
	}
	if body.Timestamp != plugintest.SampleEvent().Timestamp.Unix() {
		t.Errorf("timestamp = %d", body.Timestamp)
	}
}

func TestPriority_NeverEmergency(t *testing.T) {
	ev := plugintest.SampleEvent()
	for _, typ := range []string{"created", "reminder", "suspected_cause", "resolved"} {
		ev.Type = typ
		if p := build(plugin.DispatchRequest{Event: ev, EventType: typ}.View()).Priority; p >= 2 || p < -1 {
			t.Errorf("%s: priority %d outside -1..1", typ, p)
		}
	}
}

func TestBuild_MessageFitsLimit(t *testing.T) {
	ev := plugintest.SampleEvent()
	long := strings.Repeat("x", 5000)
	ev.Alert.LastError = &long
	msg := build(plugin.DispatchRequest{Event: ev}.View())
	if n := utf8.RuneCountInString(msg.Message); n > maxMessage {
		t.Fatalf("message is %d characters, limit %d", n, maxMessage)
	}
}

func TestSend_Classification(t *testing.T) {
	plugintest.AssertClassification(t, func(ctx context.Context, url string) error {
		return testPlugin(url).Send(ctx, plugin.DispatchRequest{
			Channel: plugin.ChannelRef{Config: map[string]any{"app_token": token, "user_key": user}},
			Event:   plugintest.SampleEvent(),
		})
	})
}

func TestSend_MissingConfigIsPermanent(t *testing.T) {
	err := New().Send(context.Background(), plugin.DispatchRequest{Channel: plugin.ChannelRef{Config: map[string]any{"app_token": token}}})
	if !plugin.IsPermanent(err) {
		t.Fatalf("err = %v, want permanent", err)
	}
}

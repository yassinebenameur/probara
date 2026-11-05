package googlechat

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
	if m.Type != "google_chat" || !m.HasCapability(plugin.CapabilityTestable) || !m.HasCapability(plugin.CapabilityFollowUp) {
		t.Fatalf("manifest = %+v", m)
	}
	if m.HasCapability(plugin.CapabilityAcknowledge) {
		t.Error("chat channels must not opt into acknowledged events")
	}
	plugintest.AssertSecrets(t, New(), map[string]any{
		"webhook_url": "https://chat.googleapis.com/v1/spaces/AAA/messages?key=k&token=t",
	}, "webhook_url")
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid", `{"webhook_url":"https://chat.googleapis.com/v1/spaces/AAA/messages?key=k&token=t"}`, false},
		{"wrong host", `{"webhook_url":"https://example.com/v1/spaces/AAA/messages"}`, true},
		{"lookalike suffix", `{"webhook_url":"https://chat.googleapis.com.evil.io/v1/spaces/AAA/messages"}`, true},
		{"lookalike prefix", `{"webhook_url":"https://evilchat.googleapis.com/v1/spaces/AAA/messages"}`, true},
		{"other google host", `{"webhook_url":"https://googleapis.com/v1/spaces/AAA/messages"}`, true},
		{"not a space path", `{"webhook_url":"https://chat.googleapis.com/v1/other"}`, true},
		{"http", `{"webhook_url":"http://chat.googleapis.com/v1/spaces/AAA/messages"}`, true},
		{"missing", `{}`, true},
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

func TestSend_PostsThreadedCard(t *testing.T) {
	var method, path string
	var query map[string][]string
	var body message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, query = r.Method, r.URL.Path, r.URL.Query()
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("body is not JSON: %s", raw)
		}
		_, _ = w.Write([]byte(`{"name":"spaces/AAA/messages/1"}`))
	}))
	defer srv.Close()

	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"webhook_url": srv.URL + "/v1/spaces/AAA/messages?key=k&token=t"}},
		Event:   plugintest.SampleEvent(),
		Test:    true,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if method != http.MethodPost || path != "/v1/spaces/AAA/messages" {
		t.Errorf("request = %s %s", method, path)
	}
	if query["key"][0] != "k" || query["token"][0] != "t" || query["messageReplyOption"][0] != "REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD" {
		t.Errorf("query = %v", query)
	}
	if body.Thread == nil || body.Thread.ThreadKey != "probara-a-1" {
		t.Errorf("thread = %+v, want the alert's thread key", body.Thread)
	}
	if len(body.CardsV2) != 1 || !strings.Contains(body.CardsV2[0].Card.Header.Title, "API health") {
		t.Fatalf("cards = %+v", body.CardsV2)
	}
	raw, _ := json.Marshal(body)
	for _, want := range []string{"i/o timeout", "https://probara.example.com/monitors/m-1", "Open the monitor"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("card missing %q: %s", want, raw)
		}
	}
}

func TestBuild_EscapesCardHTMLAndMentions(t *testing.T) {
	ev := plugintest.SampleEvent()
	ev.Alert.MonitorName = "<users/all>"
	errText := `<a href="http://evil">x</a>`
	ev.Alert.LastError = &errText
	msg := build(plugin.DispatchRequest{Event: ev}.View())
	if strings.Contains(msg.FallbackText, "<users/all>") {
		t.Errorf("fallback text can mention everyone: %q", msg.FallbackText)
	}
	raw, _ := json.Marshal(msg)
	if strings.Contains(string(raw), `<a href`) {
		t.Errorf("card text carries raw HTML: %s", raw)
	}
}

func TestSend_Classification(t *testing.T) {
	plugintest.AssertClassification(t, func(ctx context.Context, url string) error {
		return New().Send(ctx, plugin.DispatchRequest{
			Channel: plugin.ChannelRef{Config: map[string]any{"webhook_url": url + "/v1/spaces/AAA/messages?key=k&token=t"}},
			Event:   plugintest.SampleEvent(),
		})
	})
}

func TestSend_MissingWebhookIsPermanent(t *testing.T) {
	err := New().Send(context.Background(), plugin.DispatchRequest{Channel: plugin.ChannelRef{Config: map[string]any{}}})
	if !plugin.IsPermanent(err) {
		t.Fatalf("err = %v, want permanent", err)
	}
}

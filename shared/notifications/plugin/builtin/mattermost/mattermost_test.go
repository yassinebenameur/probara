package mattermost

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
	if m.Type != "mattermost" || !m.HasCapability(plugin.CapabilityTestable) || !m.HasCapability(plugin.CapabilityFollowUp) {
		t.Fatalf("manifest = %+v", m)
	}
	if m.HasCapability(plugin.CapabilityAcknowledge) {
		t.Error("chat channels must not opt into acknowledged events")
	}
	plugintest.AssertSecrets(t, New(), map[string]any{
		"webhook_url": "https://mm.example.com/hooks/abc",
	}, "webhook_url")
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid", `{"webhook_url":"https://mm.example.com/hooks/abc123"}`, false},
		{"subpath", `{"webhook_url":"https://example.com/chat/hooks/abc123"}`, false},
		{"not a hook", `{"webhook_url":"https://mm.example.com/api/v4/posts"}`, true},
		{"http", `{"webhook_url":"http://mm.example.com/hooks/abc"}`, true},
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

func TestSend_PostsAttachment(t *testing.T) {
	var method, contentType string
	var body payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, contentType = r.Method, r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("body is not JSON: %s", raw)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"webhook_url": srv.URL + "/hooks/abc"}},
		Event:   plugintest.SampleEvent(),
		Test:    true,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if method != http.MethodPost || contentType != "application/json" || len(body.Attachments) != 1 {
		t.Fatalf("request = %s %s %+v", method, contentType, body)
	}
	a := body.Attachments[0]
	if !strings.Contains(a.Title, "API health") || a.Color != "#E74C3C" {
		t.Errorf("attachment = %+v", a)
	}
	if a.TitleLink != "https://probara.example.com/monitors/m-1" || !strings.Contains(a.Text, "](https://probara.example.com/monitors/m-1)") {
		t.Errorf("deep link missing: %+v", a)
	}
	if !strings.Contains(a.Text, "i/o timeout") || len(a.Fields) == 0 {
		t.Errorf("wording missing: %+v", a)
	}
}

func TestBuild_NeutralisesMentionsAndMarkdown(t *testing.T) {
	ev := plugintest.SampleEvent()
	ev.Alert.MonitorName = "@channel *bold*"
	ev.Alert.PolicyName = "[x](http://evil) @here"
	errText := "upstream said @all ``` break"
	ev.Alert.LastError = &errText
	a := build(plugin.DispatchRequest{Event: ev}.View()).Attachments[0]
	all, _ := json.Marshal(a)
	for _, bad := range []string{"@channel", "@all", "@here", "``` break"} {
		if strings.Contains(string(all), bad) {
			t.Errorf("payload carries %q unneutralised: %s", bad, all)
		}
	}
	// Title and fallback are plain text; the Markdown-rendered parts must
	// not carry a live link.
	rendered, _ := json.Marshal([]any{a.Text, a.Fields})
	if strings.Contains(string(rendered), "[x](http://evil)") {
		t.Errorf("markdown reached the post unescaped: %s", rendered)
	}
}

func TestSend_Classification(t *testing.T) {
	plugintest.AssertClassification(t, func(ctx context.Context, url string) error {
		return New().Send(ctx, plugin.DispatchRequest{
			Channel: plugin.ChannelRef{Config: map[string]any{"webhook_url": url + "/hooks/abc"}},
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

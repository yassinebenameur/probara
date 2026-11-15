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
	"github.com/yassinebenameur/probara/shared/notifications/present"
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
		"server_url": "https://gotify.example.com", "app_token": "Axyz", "priority": "10",
	}, "app_token")
}

// The manifest's select options and default must be values Validate accepts,
// or the form's own default would be rejected on save.
func TestManifest_PriorityOptionsValidate(t *testing.T) {
	for _, f := range New().Manifest().Fields {
		if f.Key != "priority" {
			continue
		}
		values := []string{f.Default.(string)}
		for _, o := range f.Options {
			values = append(values, o.Value)
		}
		for _, v := range values {
			raw, _ := json.Marshal(map[string]string{"server_url": "https://g.example.com", "app_token": "A", "priority": v})
			if err := New().Validate(raw); err != nil {
				t.Errorf("priority %q: %v", v, err)
			}
		}
		return
	}
	t.Fatal("no priority field")
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid", `{"server_url":"https://gotify.example.com","app_token":"AbC.123"}`, false},
		{"path prefix", `{"server_url":"https://example.com/gotify/","app_token":"A"}`, false},
		{"highest priority", `{"server_url":"https://gotify.example.com","app_token":"A","priority":"10"}`, false},
		{"missing token", `{"server_url":"https://gotify.example.com"}`, true},
		{"token with space", `{"server_url":"https://gotify.example.com","app_token":"A b"}`, true},
		{"missing server", `{"app_token":"A"}`, true},
		{"http", `{"server_url":"http://gotify.example.com","app_token":"A"}`, true},
		{"embedded credentials", `{"server_url":"https://u:p@gotify.example.com","app_token":"A"}`, true},
		{"token in query", `{"server_url":"https://gotify.example.com/?token=Areal","app_token":"A"}`, true},
		{"empty query", `{"server_url":"https://gotify.example.com/?","app_token":"A"}`, true},
		{"fragment", `{"server_url":"https://gotify.example.com/#x","app_token":"A"}`, true},
		{"unknown priority", `{"server_url":"https://gotify.example.com","app_token":"A","priority":"7"}`, true},
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

func TestValidate_QueryErrorPointsAtTokenField(t *testing.T) {
	err := New().Validate(json.RawMessage(`{"server_url":"https://gotify.example.com/?token=Areal","app_token":"A"}`))
	if err == nil || !strings.Contains(err.Error(), "Application token") || strings.Contains(err.Error(), "Areal") {
		t.Errorf("err = %v, want a pointer to the token field that does not echo the token", err)
	}
}

func TestSend_PostsMessageWithHeaderToken(t *testing.T) {
	var method, path, query, key, contentType string
	var requests int
	var body message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		method, path, query = r.Method, r.URL.Path, r.URL.RawQuery
		key, contentType = r.Header.Get("X-Gotify-Key"), r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("body is not JSON: %s", raw)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// A test send is an ordinary message, the only thing it leaves behind.
	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"server_url": srv.URL + "/gotify/", "app_token": "Asecret"}},
		Event:   plugintest.SampleEvent(),
		Test:    true,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if requests != 1 {
		t.Errorf("test send made %d requests, want exactly one", requests)
	}
	if method != http.MethodPost || path != "/gotify/message" || contentType != "application/json" {
		t.Errorf("request = %s %s (%s), want POST /gotify/message JSON", method, path, contentType)
	}
	if key != "Asecret" || strings.Contains(query, "token") {
		t.Errorf("token must ride in X-Gotify-Key only: header %q query %q", key, query)
	}
	if body.Priority != 8 || !strings.Contains(body.Title, "API health") || !strings.Contains(body.Message, "i/o timeout") {
		t.Errorf("body = %+v", body)
	}
	const deepLink = plugintest.AppBaseURL + "/monitors/m-1"
	if !strings.HasSuffix(body.Message, deepLink) {
		t.Errorf("deep link missing from the end of the message: %q", body.Message)
	}
	display, _ := body.Extras["client::display"].(map[string]any)
	if display["contentType"] != "text/plain" {
		t.Errorf("display extra = %+v, want text/plain", body.Extras)
	}
	notif, _ := body.Extras["client::notification"].(map[string]any)
	click, _ := notif["click"].(map[string]any)
	if click["url"] != deepLink {
		t.Errorf("click extra = %+v", body.Extras)
	}
}

func TestPriorityMapping(t *testing.T) {
	cases := []struct {
		tone   present.Tone
		outage int
		want   int
	}{
		{present.ToneDown, 8, 8},
		{present.ToneDown, 10, 10},
		{present.ToneDown, 5, 5},
		{present.ToneWarn, 10, 8},
		{present.ToneWarn, 8, 5},
		{present.ToneWarn, 5, 5},
		{present.ToneUp, 10, 4},
		{present.ToneInfo, 10, 2},
	}
	for _, tc := range cases {
		if got := priorityFor(tc.tone, tc.outage); got != tc.want {
			t.Errorf("priorityFor(%s, %d) = %d, want %d", tc.tone, tc.outage, got, tc.want)
		}
	}
}

func TestBuild_ResolvedAndChannelPriority(t *testing.T) {
	ev := plugintest.SampleEvent()
	ev.Alert.Status = "resolved"
	if got := build(priorityHighest, plugin.DispatchRequest{Event: ev, EventType: "resolved"}.View()).Priority; got != 4 {
		t.Errorf("resolved priority = %d, want 4", got)
	}
	if got, _ := outagePriority("bogus"); got != defaultOutagePriority {
		t.Errorf("an unknown stored priority must fall back to the default, got %d", got)
	}
}

func TestBuild_MessageFitsLimitWithLinks(t *testing.T) {
	ev := plugintest.SampleEvent()
	long := strings.Repeat("é", 5000)
	ev.Alert.LastError = &long
	msg := build(defaultOutagePriority, plugin.DispatchRequest{Event: ev}.View())
	if len(msg.Message) > maxMessage {
		t.Fatalf("message is %d bytes, limit %d", len(msg.Message), maxMessage)
	}
	if !strings.Contains(msg.Message, "Last error") || !strings.HasSuffix(msg.Message, "/monitors/m-1") {
		t.Errorf("clipping dropped the error or the link: %q", msg.Message[len(msg.Message)-80:])
	}
}

func TestBuild_NoDeepLinkWithoutBaseURL(t *testing.T) {
	msg := build(defaultOutagePriority, present.Build(plugintest.SampleEvent(), "created", ""))
	if _, ok := msg.Extras["client::notification"]; ok || strings.Contains(msg.Message, "http") {
		t.Errorf("no link expected without APP_BASE_URL: %+v", msg)
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

func TestSend_EgressGuard(t *testing.T) {
	plugintest.AssertEgressGuard(t, func(ctx context.Context, url string) error {
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

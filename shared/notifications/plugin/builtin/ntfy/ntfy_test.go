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
	"github.com/yassinebenameur/probara/shared/notifications/present"
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
		"username": "bot", "password": "pw", "priority": "5",
	}, "topic", "access_token", "password")
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
			raw, _ := json.Marshal(map[string]string{"topic": "t", "priority": v})
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
		{"default server", `{"topic":"probara-alerts"}`, false},
		{"own server with token", `{"server_url":"https://ntfy.example.com","topic":"a_b-1","access_token":"tk_abc"}`, false},
		{"basic auth", `{"topic":"t","username":"u","password":"p"}`, false},
		{"urgent outages", `{"topic":"t","priority":"5"}`, false},
		{"missing topic", `{}`, true},
		{"bad topic", `{"topic":"has space"}`, true},
		{"topic too long", `{"topic":"` + strings.Repeat("a", 65) + `"}`, true},
		{"http server", `{"server_url":"http://ntfy.example.com","topic":"t"}`, true},
		{"embedded credentials", `{"server_url":"https://u:p@ntfy.example.com","topic":"t"}`, true},
		{"query string", `{"server_url":"https://ntfy.example.com/?auth=x","topic":"t"}`, true},
		{"empty query", `{"server_url":"https://ntfy.example.com/?","topic":"t"}`, true},
		{"fragment", `{"server_url":"https://ntfy.example.com/#x","topic":"t"}`, true},
		{"path prefix", `{"server_url":"https://example.com/ntfy/","topic":"t"}`, false},
		{"token and basic", `{"topic":"t","access_token":"tk","username":"u","password":"p"}`, true},
		{"token with space", `{"topic":"t","access_token":"tk abc"}`, true},
		{"username without password", `{"topic":"t","username":"u"}`, true},
		{"username with colon", `{"topic":"t","username":"a:b","password":"p"}`, true},
		{"unknown priority", `{"topic":"t","priority":"1"}`, true},
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
		got.body = message{}
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
	if b.Topic != "alerts" || b.Priority != 4 || len(b.Tags) != 1 || b.Tags[0] != "rotating_light" {
		t.Errorf("body = %+v", b)
	}
	if !strings.Contains(b.Title, "API health") || !strings.Contains(b.Message, "i/o timeout") {
		t.Errorf("wording missing: title %q message %q", b.Title, b.Message)
	}
	const deepLink = plugintest.AppBaseURL + "/monitors/m-1"
	if b.Click != deepLink || len(b.Actions) != 1 || b.Actions[0].URL != deepLink || b.Actions[0].Action != "view" || b.Actions[0].Label == "" {
		t.Errorf("deep link missing: click %q actions %+v", b.Click, b.Actions)
	}
}

func TestSend_BasicAuthKeepsPasswordVerbatim(t *testing.T) {
	srv, got := capture(t)
	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{
			"server_url": srv.URL, "topic": "alerts", "username": "bot", "password": " pw ",
		}},
		Event: plugintest.SampleEvent(),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("bot: pw ")); got.auth != want {
		t.Errorf("Authorization = %q, want %q", got.auth, want)
	}
}

func TestSend_TestSendIsAPlainPublish(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "" {
			t.Errorf("unauthenticated topic sent Authorization %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"server_url": srv.URL, "topic": "alerts"}},
		Event:   plugintest.SampleEvent(), EventType: "created", Test: true,
	}); err != nil {
		t.Fatalf("test send: %v", err)
	}
	if requests != 1 {
		t.Errorf("test send made %d requests, want exactly the one publish", requests)
	}
}

func TestPriorityMapping(t *testing.T) {
	cases := []struct {
		tone   present.Tone
		outage int
		want   int
	}{
		{present.ToneDown, 4, 4},
		{present.ToneDown, 5, 5},
		{present.ToneWarn, 5, 4},
		{present.ToneWarn, 4, 3},
		{present.ToneWarn, 3, 3},
		{present.ToneUp, 5, 3},
		{present.ToneInfo, 5, 2},
	}
	for _, tc := range cases {
		if got := priorityFor(tc.tone, tc.outage); got != tc.want {
			t.Errorf("priorityFor(%s, %d) = %d, want %d", tc.tone, tc.outage, got, tc.want)
		}
	}
}

func TestSend_PriorityFollowsToneAndChannel(t *testing.T) {
	srv, got := capture(t)
	send := func(ev string, cfg map[string]any) message {
		t.Helper()
		e := plugintest.SampleEvent()
		if ev == "resolved" {
			e.Alert.Status = "resolved"
		}
		cfg["server_url"], cfg["topic"] = srv.URL, "alerts"
		if err := New().Send(context.Background(), plugin.DispatchRequest{
			Channel: plugin.ChannelRef{Config: cfg}, Event: e, EventType: ev,
		}); err != nil {
			t.Fatalf("%s send: %v", ev, err)
		}
		return got.body
	}
	if b := send("created", map[string]any{"priority": "5"}); b.Priority != 5 {
		t.Errorf("outage with urgent priority = %d", b.Priority)
	}
	if b := send("created", map[string]any{"priority": "bogus"}); b.Priority != 4 {
		t.Errorf("an unknown stored priority must fall back to the default, got %d", b.Priority)
	}
	if b := send("resolved", map[string]any{"priority": "5"}); b.Priority != 3 || b.Tags[0] != "white_check_mark" {
		t.Errorf("resolved body = %+v", b)
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

func TestSend_EgressGuard(t *testing.T) {
	plugintest.AssertEgressGuard(t, func(ctx context.Context, url string) error {
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
	msg := build("t", defaultOutagePriority, plugin.DispatchRequest{Event: ev}.View())
	if len(msg.Message) > maxMessage {
		t.Fatalf("message is %d bytes, limit %d", len(msg.Message), maxMessage)
	}
	if !strings.Contains(msg.Message, "Last error") {
		t.Error("clipped error dropped entirely")
	}
}

func TestBuild_NoDeepLinkWithoutBaseURL(t *testing.T) {
	m := present.Build(plugintest.SampleEvent(), "created", "")
	msg := build("t", defaultOutagePriority, m)
	if msg.Click != "" || len(msg.Actions) != 0 {
		t.Errorf("click %q actions %+v, want none without APP_BASE_URL", msg.Click, msg.Actions)
	}
}

// A probe error dense in characters JSON escapes must still fit ntfy's limit
// once encoded, or the publish is refused with a permanent 413.
func TestSend_EscapeDenseErrorFitsEncodedLimit(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ev := plugintest.SampleEvent()
	dense := strings.Repeat(`<a href="x">&\</a>`+"\t\n", 600)
	ev.Alert.LastError = &dense
	if err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"server_url": srv.URL, "topic": "alerts"}},
		Event:   ev,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if n := len(fields["message"]); n > maxMessage {
		t.Errorf("encoded message is %d bytes, limit %d", n, maxMessage)
	}
	var msg string
	if err := json.Unmarshal(fields["message"], &msg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "Last error: <a href") {
		t.Errorf("clipped error dropped entirely: %q", msg[:min(len(msg), 200)])
	}
	if strings.Contains(string(raw), `\u003c`) || strings.Contains(string(raw), `\u0026`) {
		t.Error("payload is HTML-escaped, wasting the message budget")
	}
}

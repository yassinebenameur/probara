package matrix

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

func TestMain(m *testing.M) {
	plugintest.AllowLoopback()
	os.Exit(m.Run())
}

func TestManifest(t *testing.T) {
	m := New().Manifest()
	if m.Type != "matrix" || !m.HasCapability(plugin.CapabilityTestable) || !m.HasCapability(plugin.CapabilityFollowUp) {
		t.Fatalf("manifest = %+v", m)
	}
	if m.HasCapability(plugin.CapabilityAcknowledge) {
		t.Error("chat channels must not opt into acknowledged events")
	}
	plugintest.AssertSecrets(t, New(), map[string]any{
		"homeserver_url": "https://matrix.example.org", "access_token": "syt_x", "room_id": "!r:example.org",
	}, "access_token")
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid", `{"homeserver_url":"https://matrix.example.org","access_token":"syt_x","room_id":"!abc:example.org"}`, false},
		{"server-less room id", `{"homeserver_url":"https://matrix.example.org","access_token":"syt_x","room_id":"!abcDEF123"}`, false},
		{"alias", `{"homeserver_url":"https://matrix.example.org","access_token":"syt_x","room_id":"#ops:example.org"}`, true},
		{"room id with slash", `{"homeserver_url":"https://matrix.example.org","access_token":"syt_x","room_id":"!a/b:example.org"}`, true},
		{"missing token", `{"homeserver_url":"https://matrix.example.org","room_id":"!a:example.org"}`, true},
		{"http", `{"homeserver_url":"http://matrix.example.org","access_token":"syt_x","room_id":"!a:example.org"}`, true},
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

type request struct {
	method, path, auth string
	body               map[string]any
}

func recorder(t *testing.T) (*httptest.Server, *[]request) {
	t.Helper()
	var got []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("body is not JSON: %s", raw)
		}
		got = append(got, request{r.Method, r.URL.EscapedPath(), r.Header.Get("Authorization"), body})
		_, _ = w.Write([]byte(`{"event_id":"$e"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func send(t *testing.T, url string, req plugin.DispatchRequest) {
	t.Helper()
	req.Channel.ID = "ch-1"
	req.Channel.Config = map[string]any{"homeserver_url": url + "/", "access_token": "syt_secret", "room_id": "!room:example.org"}
	if err := New().Send(context.Background(), req); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestSend_PutsMessageWithTxnID(t *testing.T) {
	srv, got := recorder(t)
	ev := plugintest.SampleEvent()
	send(t, srv.URL, plugin.DispatchRequest{Event: ev, Test: true})
	send(t, srv.URL, plugin.DispatchRequest{Event: ev, Attempt: 2}) // a retry of the same event

	r := (*got)[0]
	if r.method != http.MethodPut {
		t.Errorf("method = %s, want PUT", r.method)
	}
	prefix := "/_matrix/client/v3/rooms/%21room:example.org/send/m.room.message/probara-"
	if !strings.HasPrefix(r.path, prefix) {
		t.Errorf("path = %q, want prefix %q", r.path, prefix)
	}
	if strings.Contains(r.path, "syt_secret") || r.auth != "Bearer syt_secret" {
		t.Errorf("token must ride in the Authorization header only: path %q auth %q", r.path, r.auth)
	}
	if (*got)[1].path != r.path {
		t.Errorf("a retry must reuse the transaction ID: %q vs %q", (*got)[1].path, r.path)
	}

	b := r.body
	if b["msgtype"] != "m.text" || b["format"] != "org.matrix.custom.html" {
		t.Errorf("content = %+v", b)
	}
	if m, ok := b["m.mentions"].(map[string]any); !ok || len(m) != 0 {
		t.Errorf("m.mentions = %#v, want an empty object", b["m.mentions"])
	}
	body, _ := b["body"].(string)
	html, _ := b["formatted_body"].(string)
	for _, want := range []string{"API health", "i/o timeout", "https://probara.example.com/monitors/m-1"} {
		if !strings.Contains(body, want) || !strings.Contains(html, want) {
			t.Errorf("%q missing: body %q html %q", want, body, html)
		}
	}
}

func TestTxnID_DistinctPerEvent(t *testing.T) {
	ev := plugintest.SampleEvent()
	base := plugin.DispatchRequest{Channel: plugin.ChannelRef{ID: "ch-1"}, Event: ev, EventType: "reminder"}
	id := txnID(base, base.View())

	next := base
	next.Event.Timestamp = ev.Timestamp.Add(time.Hour) // the next reminder round
	other := base
	other.Channel.ID = "ch-2"
	resolved := base
	resolved.EventType = "resolved"
	for name, r := range map[string]plugin.DispatchRequest{"next round": next, "other channel": other, "resolved": resolved} {
		if txnID(r, r.View()) == id {
			t.Errorf("%s reuses the transaction ID", name)
		}
	}
}

func TestBuild_EscapesHTML(t *testing.T) {
	ev := plugintest.SampleEvent()
	errText := `<script>alert(1)</script> @room`
	ev.Alert.LastError = &errText
	c := build(plugin.DispatchRequest{Event: ev}.View())
	if strings.Contains(c.FormattedBody, "<script>") {
		t.Fatalf("formatted_body carries raw HTML: %s", c.FormattedBody)
	}
}

func TestSend_Classification(t *testing.T) {
	plugintest.AssertClassification(t, func(ctx context.Context, url string) error {
		return New().Send(ctx, plugin.DispatchRequest{
			Channel: plugin.ChannelRef{Config: map[string]any{"homeserver_url": url, "access_token": "t", "room_id": "!r:x"}},
			Event:   plugintest.SampleEvent(),
		})
	})
}

func TestSend_MissingConfigIsPermanent(t *testing.T) {
	err := New().Send(context.Background(), plugin.DispatchRequest{Channel: plugin.ChannelRef{Config: map[string]any{"homeserver_url": "https://m.example.org"}}})
	if !plugin.IsPermanent(err) {
		t.Fatalf("err = %v, want permanent", err)
	}
}

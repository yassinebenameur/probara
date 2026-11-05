package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

// A page's body gains alert.links.acknowledge and is otherwise the raw
// AlertEvent; a test send's body has no links at all.
func TestSend_AlertLinksAcknowledge(t *testing.T) {
	prev := plugin.CurrentRuntime()
	t.Cleanup(func() { plugin.Configure(prev) })
	plugin.Configure(plugin.Runtime{AppBaseURL: "https://probara.example.com", Links: plugintest.StaticSigner(plugintest.AckToken)})

	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	req := plugin.DispatchRequest{
		Channel: plugin.ChannelRef{ID: "c-1", Config: map[string]any{"url": srv.URL}},
		Event:   sampleEvent(),
	}
	if err := New().Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Alert struct {
			Links struct {
				Acknowledge string `json:"acknowledge"`
			} `json:"links"`
		} `json:"alert"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Alert.Links.Acknowledge != plugintest.AckURL {
		t.Fatalf("alert.links.acknowledge = %q in %s", doc.Alert.Links.Acknowledge, body)
	}
	var event notifications.AlertEvent
	if err := json.Unmarshal(body, &event); err != nil {
		t.Fatal(err)
	}
	want := sampleEvent()
	if !event.Timestamp.Equal(want.Timestamp) || event.Type != want.Type || event.TenantID != want.TenantID {
		t.Fatalf("event envelope changed: %+v", event)
	}
	if !event.Alert.TriggeredAt.Equal(want.Alert.TriggeredAt) {
		t.Fatalf("triggered_at changed: %v", event.Alert.TriggeredAt)
	}
	event.Alert.TriggeredAt = want.Alert.TriggeredAt // same instant, location differs after JSON
	if !reflect.DeepEqual(event.Alert, want.Alert) {
		t.Fatalf("alert fields changed:\n got %+v\nwant %+v", event.Alert, want.Alert)
	}

	req.Test = true
	if err := New().Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Alert map[string]json.RawMessage `json:"alert"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw.Alert["links"]; ok {
		t.Fatalf("test send body carries links: %s", body)
	}
}

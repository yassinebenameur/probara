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

// deliver sends req to a stub receiver and returns the body it got.
func deliver(t *testing.T, req plugin.DispatchRequest) []byte {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	req.Channel.Config = map[string]any{"url": srv.URL}
	if err := New().Send(context.Background(), req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	return body
}

func TestAckLink(t *testing.T) {
	plugintest.CheckAckLink(t, nil, func(t *testing.T, req plugin.DispatchRequest) string {
		return string(deliver(t, req))
	})
}

// A page's body gains alert.links.acknowledge and is otherwise the raw
// AlertEvent, field for field; without a link there is no links key at all.
func TestAckLinkPayloadShape(t *testing.T) {
	cases := plugintest.Cases(nil)
	prev := plugin.CurrentRuntime()
	t.Cleanup(func() { plugin.Configure(prev) })
	rt := cases[0].Runtime
	rt.Egress = prev.Egress
	plugin.Configure(rt)

	req := cases[0].Req
	body := deliver(t, req)
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
	var withLinks, plain map[string]any
	_ = json.Unmarshal(body, &withLinks)
	raw, _ := json.Marshal(req.Event)
	_ = json.Unmarshal(raw, &plain)
	delete(withLinks["alert"].(map[string]any), "links")
	if !reflect.DeepEqual(withLinks, plain) {
		t.Fatalf("body differs from the raw event beyond alert.links:\n got %v\nwant %v", withLinks, plain)
	}
	var event notifications.AlertEvent
	if err := json.Unmarshal(body, &event); err != nil || event.Alert.ID != req.Event.Alert.ID {
		t.Fatalf("body no longer decodes as an AlertEvent: %v", err)
	}

	req.Test = true
	var test struct {
		Alert map[string]json.RawMessage `json:"alert"`
	}
	if err := json.Unmarshal(deliver(t, req), &test); err != nil {
		t.Fatal(err)
	}
	if _, ok := test.Alert["links"]; ok {
		t.Fatal("a test send's body carries links")
	}
}

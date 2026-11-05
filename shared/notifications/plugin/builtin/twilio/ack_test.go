package twilio

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

// An SMS carries the acknowledge link whole, inside Twilio's body limit,
// however long the alert text: the text is clipped, never the link.
func TestSend_AckLinkFitsTheBodyLimit(t *testing.T) {
	prev := plugin.CurrentRuntime()
	t.Cleanup(func() { plugin.Configure(prev) })
	plugin.Configure(plugin.Runtime{AppBaseURL: "https://probara.example.com", Links: plugintest.StaticSigner(plugintest.AckToken)})

	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		mu.Lock()
		bodies = append(bodies, form.Get("Body"))
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	p := New()
	p.base = srv.URL

	for _, name := range []string{"API health", strings.Repeat("very long monitor name ", 200)} {
		bodies = nil
		event := sampleEvent()
		event.Alert.MonitorName = name
		if err := p.Send(context.Background(), plugin.DispatchRequest{
			Channel: plugin.ChannelRef{ID: "c-1", Config: config(nil)}, Event: event, EventType: "created",
		}); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if len(bodies) != 2 {
			t.Fatalf("texts = %d, want 2", len(bodies))
		}
		b := bodies[0]
		if n := len([]rune(b)); n > maxBody {
			t.Fatalf("body is %d runes, over the %d limit", n, maxBody)
		}
		if !strings.HasSuffix(b, "\nAcknowledge: "+plugintest.AckURL) {
			t.Fatalf("body does not end with the whole ack link:\n%s", b)
		}
	}

	// A channel test send texts no acknowledge link.
	bodies = nil
	if err := p.Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{ID: "c-1", Config: config(nil)}, Event: sampleEvent(), EventType: "created", Test: true,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if strings.Contains(bodies[0], "alert-action") {
		t.Fatalf("test send carries the ack link: %q", bodies[0])
	}
}

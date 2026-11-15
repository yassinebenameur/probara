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

	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

// sendBodies sends req through a stub Twilio and returns each text's body.
func sendBodies(t *testing.T, req plugin.DispatchRequest) []string {
	t.Helper()
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
	if err := p.Send(context.Background(), req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	return bodies
}

func TestAckLink(t *testing.T) {
	plugintest.CheckAckLink(t, config(nil), func(t *testing.T, req plugin.DispatchRequest) string {
		return strings.Join(sendBodies(t, req), "\n---\n")
	})
}

// A page texts only the acknowledge link, not the open-the-monitor link, and
// with a real-length token on a realistic origin it stays near two SMS
// segments' worth of characters. However long the alert text, the link
// arrives whole inside Twilio's body limit: the text is clipped, never the
// link. Without an acknowledge link the deep link stays.
func TestAckLinkLength(t *testing.T) {
	prev := plugin.CurrentRuntime()
	t.Cleanup(func() { plugin.Configure(prev) })
	token := strings.Repeat("T", actionlink.EncodedLen)
	plugin.Configure(plugin.Runtime{AppBaseURL: "https://probara.example.com", Links: plugintest.StaticSigner(token), Egress: prev.Egress})

	req := plugintest.Cases(config(nil))[0].Req
	deepLink := "https://probara.example.com/monitors/" + req.Event.Alert.MonitorID
	b := sendBodies(t, req)[0]
	ack := "Acknowledge: https://probara.example.com/alert-action#" + token
	if !strings.HasSuffix(b, "\n"+ack) {
		t.Fatalf("body does not end with the whole ack link:\n%s", b)
	}
	if strings.Contains(b, deepLink) {
		t.Fatalf("a page with an ack link also texts the deep link:\n%s", b)
	}
	if n := len([]rune(b)); n > 240 {
		t.Fatalf("a typical page is %d characters:\n%s", n, b)
	}
	t.Logf("%d runes:\n%s", len([]rune(b)), b)

	req.Event.Alert.MonitorName = strings.Repeat("very long monitor name ", 200)
	b = sendBodies(t, req)[0]
	if n := len([]rune(b)); n > maxBody {
		t.Fatalf("body is %d runes, over the %d limit", n, maxBody)
	}
	if !strings.HasSuffix(b, "\n"+ack) {
		t.Fatalf("long alert lost the whole ack link:\n%s", b)
	}

	var resolved plugin.DispatchRequest
	for _, c := range plugintest.Cases(config(nil)) {
		if c.Name == "resolved" {
			resolved = c.Req
		}
	}
	if b = sendBodies(t, resolved)[0]; !strings.Contains(b, deepLink) {
		t.Fatalf("a text without an ack link must keep the deep link:\n%s", b)
	}
}

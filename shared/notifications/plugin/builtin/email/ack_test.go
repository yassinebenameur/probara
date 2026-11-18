package email

import (
	"context"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

// Send hands the mailer this delivery's acknowledge link, and both MIME parts
// render it: a button in the HTML part, a line in the plain-text part.
func TestAckLink(t *testing.T) {
	cfg := map[string]any{"to": []any{"ops@example.com"}}
	for _, part := range []string{"html", "text"} {
		t.Run(part, func(t *testing.T) {
			plugintest.CheckAckLink(t, cfg, func(t *testing.T, req plugin.DispatchRequest) string {
				mailer := &fakeMailer{}
				p := New()
				p.SetMailer(mailer)
				if err := p.Send(context.Background(), req); err != nil {
					t.Fatalf("Send: %v", err)
				}
				event := mailer.gotEvent
				event.Type = req.Type()
				render := RenderBody
				if part == "html" {
					render = RenderHTML
				}
				out, err := render(event, mailer.gotTemplates, plugin.CurrentRuntime().AppBaseURL)
				if err != nil {
					t.Fatal(err)
				}
				return out
			})
		})
	}
}

// Override templates reach the link as ack_url / ack_label.
func TestAckLinkInOverrideTemplates(t *testing.T) {
	tpl := Templates{Body: "ack: {{.ack_url}} ({{.ack_label}})", AckURL: plugintest.AckURL, AckLabel: "Acknowledge"}
	body, err := RenderBody(plugintest.Event("created"), tpl, plugintest.BaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if body != "ack: "+plugintest.AckURL+" (Acknowledge)" {
		t.Fatalf("body = %q", body)
	}
	// A resolve never prints a link, even one a caller passed by mistake.
	resolved := plugintest.Event("resolved")
	resolved.Alert.Status = "resolved"
	body, _ = RenderBody(resolved, Templates{AckURL: plugintest.AckURL}, plugintest.BaseURL)
	if strings.Contains(body, plugintest.AckURL) {
		t.Fatalf("resolve body carries the ack link:\n%s", body)
	}
}

package email

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

var updateGoldens = flag.Bool("update", false, "rewrite the email golden files in testdata/")

const testAckURL = "https://probara.example.com/alert-action/ack-token"

func ackTemplates() Templates {
	return Templates{AckURL: testAckURL, AckLabel: "Acknowledge"}
}

// golden compares got with testdata/name, rewriting it under -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGoldens {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the golden file (run go test -run %s -update to accept):\n%s", name, t.Name(), got)
	}
}

// The default email of a page carries the acknowledge link in both parts:
// a primary button in HTML next to the (now secondary) deep link, and its
// own line in the text part.
func TestAckLink_DefaultRenderingGoldens(t *testing.T) {
	event := sampleEvent()
	event.Alert.MonitorID = "3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234"

	text, err := RenderBody(event, ackTemplates(), "https://probara.example.com")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "created_ack.txt", text)

	html, err := RenderHTML(event, ackTemplates(), "https://probara.example.com")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "created_ack.html", html)
	if !strings.Contains(html, `href="`+testAckURL+`"`) || !strings.Contains(html, ">Acknowledge</a>") {
		t.Error("html has no acknowledge button")
	}

	// With no app origin there is no deep link, and the ack button stands
	// alone (Send never mints one without APP_BASE_URL anyway).
	html, err = RenderHTML(event, ackTemplates(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, testAckURL) || strings.Contains(html, "Open the monitor") {
		t.Error("ack-only html rendered wrongly")
	}
}

func TestAckLink_OnlyOnPages(t *testing.T) {
	for _, eventType := range []string{"resolved", "acknowledged", "suspected_cause"} {
		event := sampleEvent()
		event.Type = eventType
		if eventType == "resolved" {
			event.Alert.Status = "resolved"
		}
		text, _ := RenderBody(event, ackTemplates(), "https://probara.example.com")
		html, _ := RenderHTML(event, ackTemplates(), "https://probara.example.com")
		if strings.Contains(text, testAckURL) || strings.Contains(html, testAckURL) {
			t.Errorf("%s email carries the acknowledge link", eventType)
		}
	}
	reminder := sampleEvent()
	reminder.Type = "reminder"
	if text, _ := RenderBody(reminder, ackTemplates(), ""); !strings.Contains(text, "Acknowledge: "+testAckURL+"\n") {
		t.Errorf("reminder text part lacks the ack line:\n%s", text)
	}
}

func TestAckLink_ReachesOverrideTemplates(t *testing.T) {
	tpls := ackTemplates()
	tpls.Body = "{{.monitor_name}} down. Ack: {{.ack_url}} ({{.ack_label}})"
	body, err := RenderBody(sampleEvent(), tpls, "")
	if err != nil {
		t.Fatal(err)
	}
	if body != "API health down. Ack: "+testAckURL+" (Acknowledge)" {
		t.Fatalf("body = %q", body)
	}
}

type staticLinks string

func (s staticLinks) ActionToken(action, tenantID, alertID, channelID string) (string, error) {
	return string(s), nil
}

// Send mints the link for the delivering channel and hands it to the mailer;
// a channel test send never gets one.
func TestSend_PassesAckLinkToMailer(t *testing.T) {
	prev := plugin.CurrentRuntime()
	t.Cleanup(func() { plugin.Configure(prev) })
	plugin.Configure(plugin.Runtime{AppBaseURL: "https://probara.example.com", Links: staticLinks("ack-token")})

	mailer := &fakeMailer{}
	p := New()
	p.SetMailer(mailer)
	req := plugin.DispatchRequest{
		Channel:   plugin.ChannelRef{ID: "c-1", Config: map[string]any{"to": []any{"ops@example.com"}}},
		Event:     sampleEvent(),
		EventType: "created",
	}
	if err := p.Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if mailer.gotTemplates.AckURL != testAckURL || mailer.gotTemplates.AckLabel != "Acknowledge" {
		t.Fatalf("mailer got ack %q/%q", mailer.gotTemplates.AckURL, mailer.gotTemplates.AckLabel)
	}

	req.Test = true
	if err := p.Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if mailer.gotTemplates.AckURL != "" {
		t.Fatalf("test send carried ack link %q", mailer.gotTemplates.AckURL)
	}
}

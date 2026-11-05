package plugin

import (
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
)

type stubLinks struct{ calls int }

func (s *stubLinks) ActionToken(action, tenantID, alertID, channelID string) (string, error) {
	s.calls++
	return "tok", nil
}

// View hands the runtime's link signer to present for real deliveries only:
// a test send's synthetic alert never carries an acknowledge link.
func TestViewAckLinkSkipsTestSends(t *testing.T) {
	prev := CurrentRuntime()
	t.Cleanup(func() { Configure(prev) })
	links := &stubLinks{}
	Configure(Runtime{AppBaseURL: "https://probara.example.com/", Links: links})

	req := DispatchRequest{
		Channel:   ChannelRef{ID: "c-1"},
		EventType: "created",
		Event: notifications.AlertEvent{
			Type: "created", TenantID: "t-1", Timestamp: time.Now(),
			Alert: notifications.AlertDetails{ID: "a-1", MonitorName: "API", Status: "active"},
		},
	}
	if got := req.View().AckURL; got != "https://probara.example.com/alert-action/tok" {
		t.Fatalf("real delivery AckURL = %q", got)
	}
	req.Test = true
	if got := req.View().AckURL; got != "" {
		t.Fatalf("test send AckURL = %q, want none", got)
	}
	if links.calls != 1 {
		t.Fatalf("signer called %d times, want once (never for the test send)", links.calls)
	}

	Configure(Runtime{Links: links})
	req.Test = false
	if got := req.View().AckURL; got != "" {
		t.Fatalf("no APP_BASE_URL: AckURL = %q, want none", got)
	}
}

type scopeLinks struct{ channelID string }

func (s *scopeLinks) ActionToken(action, tenantID, alertID, channelID string) (string, error) {
	s.channelID = channelID
	return "tok", nil
}

// A person send (an escalation page) scopes its acknowledge link to the
// contact it goes to, even when it rides a channel (the personal SMS
// channel) or none (the platform mailer).
func TestViewScopesPersonLinksToContact(t *testing.T) {
	prev := CurrentRuntime()
	t.Cleanup(func() { Configure(prev) })
	links := &scopeLinks{}
	Configure(Runtime{AppBaseURL: "https://probara.example.com", Links: links})
	event := notifications.AlertEvent{
		Type: "created", TenantID: "t-1", Timestamp: time.Now(),
		Alert: notifications.AlertDetails{ID: "a-1", MonitorName: "API", Status: "active"},
	}
	for _, channelID := range []string{"sms-channel", ""} {
		req := DispatchRequest{Channel: ChannelRef{ID: channelID}, EventType: "created", Event: event,
			Recipient: &Recipient{ContactID: "contact-1", Address: "+15550001111"}}
		if got := req.View().AckURL; got == "" {
			t.Fatalf("person send on %q has no acknowledge link", channelID)
		}
		if links.channelID != "contact-1" {
			t.Fatalf("person send on %q scoped its link to %q, want the contact", channelID, links.channelID)
		}
	}
}

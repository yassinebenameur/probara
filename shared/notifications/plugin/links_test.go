package plugin

import (
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
)

type stubLinks struct{ calls int }

func (s *stubLinks) ActionToken(action, tenantID, alertID, channelID string, issuedAt time.Time) (string, error) {
	s.calls++
	return "tok", nil
}

// View hands the runtime's signer and the delivering channel to present for
// real deliveries only: a test send's synthetic alert never carries an
// acknowledge link.
func TestViewAckLink(t *testing.T) {
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
	if got := req.View().AckURL; got != "https://probara.example.com/alert-action#tok" {
		t.Fatalf("real delivery AckURL = %q", got)
	}
	req.Test = true
	if got := req.View().AckURL; got != "" {
		t.Fatalf("test send AckURL = %q, want none", got)
	}
	if links.calls != 1 {
		t.Fatalf("signer called %d times, want once (never for the test send)", links.calls)
	}
}

// A binary that never installs a signer renders no acknowledge link.
func TestViewWithoutSigner(t *testing.T) {
	prev := CurrentRuntime()
	t.Cleanup(func() { Configure(prev) })
	Configure(Runtime{AppBaseURL: "https://probara.example.com"})
	req := DispatchRequest{
		Channel:   ChannelRef{ID: "c-1"},
		EventType: "created",
		Event:     notifications.AlertEvent{Type: "created", TenantID: "t-1", Alert: notifications.AlertDetails{ID: "a-1", Status: "active"}},
	}
	if got := req.View().AckURL; got != "" {
		t.Fatalf("AckURL = %q without a signer", got)
	}
}

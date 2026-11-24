// Package plugintest holds the acknowledge-link cases every builtin plugin's
// tests run, so each channel is held to the same rule: the link appears on
// created and reminder pages and nowhere else (paging plugins: never).
package plugintest

import (
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

// BaseURL is the operator-UI origin the cases configure, AckToken the token
// StaticSigner mints and AckURL the resulting acknowledge link.
const (
	BaseURL  = "https://probara.example.com"
	AckToken = "AckTokenAckTokenAckToken"
	AckURL   = BaseURL + present.AckPath + AckToken
)

// StaticSigner mints the same token for every action link.
type StaticSigner string

// ActionToken implements present.LinkSigner.
func (s StaticSigner) ActionToken(action, tenantID, alertID, channelID string, issuedAt time.Time) (string, error) {
	return string(s), nil
}

// Event is a page about a real alert.
func Event(eventType string) notifications.AlertEvent {
	opened := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	lastErr := "dial tcp 203.0.113.10:443: i/o timeout"
	return notifications.AlertEvent{
		Type:      eventType,
		TenantID:  "8f14e45f-ceea-467a-9f0b-2c1a3b4d5e6f",
		Timestamp: opened.Add(30 * time.Minute),
		Alert: notifications.AlertDetails{
			ID:           "b17c2e90-4a1d-4f3e-9c88-71f0a2d5c311",
			MonitorID:    "3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234",
			MonitorName:  "Checkout API",
			Status:       "active",
			TriggeredAt:  opened,
			FailureCount: 3,
			LastError:    &lastErr,
		},
	}
}

// Case is one delivery and whether it must carry the acknowledge link.
type Case struct {
	Name    string
	Req     plugin.DispatchRequest
	Runtime plugin.Runtime
	WantAck bool
}

// Cases are the deliveries every plugin is checked against. config is the
// channel's decrypted config (plugins that send for real need theirs).
func Cases(config map[string]any) []Case {
	linked := plugin.Runtime{AppBaseURL: BaseURL, Links: StaticSigner(AckToken)}
	req := func(eventType string, mutate func(*plugin.DispatchRequest)) plugin.DispatchRequest {
		r := plugin.DispatchRequest{
			Channel:   plugin.ChannelRef{ID: "5a7d3c1e-9b2f-4e8a-8c6d-0f1e2d3c4b5a", Name: "On-call", Config: config},
			Event:     Event(eventType),
			EventType: eventType,
			Attempt:   1,
		}
		if mutate != nil {
			mutate(&r)
		}
		return r
	}
	resolvedAt := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	return []Case{
		{Name: "created", Req: req(present.EventCreated, nil), Runtime: linked, WantAck: true},
		{Name: "reminder", Req: req(present.EventReminder, nil), Runtime: linked, WantAck: true},
		{Name: "resolved", Req: req(present.EventResolved, func(r *plugin.DispatchRequest) {
			r.Event.Alert.Status = "resolved"
			r.Event.Alert.ResolvedAt = &resolvedAt
		}), Runtime: linked},
		{Name: "suspected cause follow-up", Req: req(present.EventSuspectedCause, nil), Runtime: linked},
		{Name: "channel test send", Req: req(present.EventCreated, func(r *plugin.DispatchRequest) { r.Test = true }), Runtime: linked},
		{Name: "no APP_BASE_URL", Req: req(present.EventCreated, nil), Runtime: plugin.Runtime{Links: StaticSigner(AckToken)}},
		{Name: "no signing key", Req: req(present.EventCreated, nil), Runtime: plugin.Runtime{AppBaseURL: BaseURL}},
	}
}

// CheckAckLink runs every case through render (the plugin's output for one
// delivery, as text) and checks the acknowledge link is present exactly when
// it should be, under the runtime the case installs.
func CheckAckLink(t *testing.T, config map[string]any, render func(t *testing.T, req plugin.DispatchRequest) string) {
	t.Helper()
	check(t, config, render, true)
}

// CheckNoAckLink is CheckAckLink for paging plugins, which never render the
// link: people acknowledge in the paging tool.
func CheckNoAckLink(t *testing.T, config map[string]any, render func(t *testing.T, req plugin.DispatchRequest) string) {
	t.Helper()
	check(t, config, render, false)
}

func check(t *testing.T, config map[string]any, render func(t *testing.T, req plugin.DispatchRequest) string, renders bool) {
	t.Helper()
	prev := plugin.CurrentRuntime()
	t.Cleanup(func() { plugin.Configure(prev) })
	for _, tc := range Cases(config) {
		t.Run(tc.Name, func(t *testing.T) {
			rt := tc.Runtime
			// Plugins under test dial httptest servers on loopback.
			rt.Egress = prev.Egress
			plugin.Configure(rt)
			out := render(t, tc.Req)
			has := strings.Contains(out, AckURL)
			want := tc.WantAck && renders
			if want && !has {
				t.Fatalf("missing the acknowledge link %s:\n%s", AckURL, out)
			}
			if !want && (has || strings.Contains(out, AckToken) || strings.Contains(out, present.AckPath)) {
				t.Fatalf("carries an acknowledge link it must not:\n%s", out)
			}
		})
	}
}

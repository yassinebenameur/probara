package pagerduty

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

const testKey = "R0UT1NGKEY0123456789abcdefABCDEF"

func TestMain(m *testing.M) {
	// httptest servers listen on loopback, which the default egress policy
	// refuses; the policy itself is covered in package plugin.
	plugin.Configure(plugin.Runtime{AppBaseURL: "https://probara.example.com"})
	os.Exit(m.Run())
}

func TestManifest(t *testing.T) {
	m := New().Manifest()
	if m.Type != "pagerduty" {
		t.Errorf("Type = %q", m.Type)
	}
	if !m.HasCapability(plugin.CapabilityAcknowledge) || !m.HasCapability(plugin.CapabilityTestable) {
		t.Error("expected acknowledge + testable capabilities")
	}
	if !m.Fields[0].Secret {
		t.Error("routing_key must be Secret")
	}
	if m.Fields[1].Type != plugin.FieldTypeSelect || len(m.Fields[1].Options) != 2 {
		t.Error("region must be a select with us/eu")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid default region", `{"routing_key":"` + testKey + `"}`, false},
		{"valid eu", `{"routing_key":"` + testKey + `","region":"eu"}`, false},
		{"missing key", `{}`, true},
		{"short key", `{"routing_key":"abc"}`, true},
		{"unknown region", `{"routing_key":"` + testKey + `","region":"mars"}`, true},
		{"empty body", ``, true},
	}
	p := New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := p.Validate(json.RawMessage(tc.raw)); (err != nil) != tc.wantErr {
				t.Errorf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

type recorder struct {
	mu     sync.Mutex
	events []event
	status int
}

func (r *recorder) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var ev event
		if err := json.Unmarshal(body, &ev); err != nil {
			t.Errorf("unmarshal: %v", err)
		}
		r.mu.Lock()
		r.events = append(r.events, ev)
		r.mu.Unlock()
		if r.status != 0 {
			w.WriteHeader(r.status)
			_, _ = w.Write([]byte(`{"status":"invalid event","message":"Event object is invalid","errors":["Length of 'routing_key' is incorrect"]}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sampleEvent() notifications.AlertEvent {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	lastErr := "dial tcp: i/o timeout"
	return notifications.AlertEvent{
		Type:      "created",
		TenantID:  "tenant-1",
		Timestamp: now,
		Alert: notifications.AlertDetails{
			ID:           "a-1",
			MonitorID:    "3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234",
			MonitorName:  "API health",
			PolicyName:   "Critical",
			Status:       "active",
			TriggeredAt:  now,
			FailureCount: 3,
			LastError:    &lastErr,
		},
	}
}

func send(t *testing.T, p *Plugin, eventType string, test bool) error {
	t.Helper()
	return p.Send(context.Background(), plugin.DispatchRequest{
		Channel:   plugin.ChannelRef{Config: map[string]any{"routing_key": testKey}},
		Event:     sampleEvent(),
		EventType: eventType,
		Test:      test,
	})
}

func TestSend_LifecycleMapsToEventActions(t *testing.T) {
	rec := &recorder{}
	p := New()
	p.endpoint = rec.server(t).URL

	for _, et := range []string{"created", "reminder", "acknowledged", "resolved"} {
		if err := send(t, p, et, false); err != nil {
			t.Fatalf("Send(%s): %v", et, err)
		}
	}
	var actions []string
	for _, ev := range rec.events {
		actions = append(actions, ev.EventAction)
		if ev.DedupKey != "probara:a-1" || ev.RoutingKey != testKey {
			t.Errorf("event %+v: wrong dedup/routing key", ev)
		}
	}
	if got := strings.Join(actions, ","); got != "trigger,acknowledge,resolve" {
		t.Fatalf("actions = %s, want trigger,acknowledge,resolve (reminder skipped)", got)
	}

	trigger := rec.events[0]
	if trigger.Payload == nil || trigger.Payload.Severity != "critical" || trigger.Payload.Source != "API health" {
		t.Fatalf("trigger payload = %+v", trigger.Payload)
	}
	if !strings.HasPrefix(trigger.Payload.Summary, "Alert Triggered: API health") {
		t.Errorf("summary = %q", trigger.Payload.Summary)
	}
	if trigger.Payload.CustomDetails["Last error"] != "dial tcp: i/o timeout" {
		t.Errorf("custom_details = %+v", trigger.Payload.CustomDetails)
	}
	if trigger.ClientURL != "https://probara.example.com/monitors/3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234" {
		t.Errorf("client_url = %q", trigger.ClientURL)
	}
	if rec.events[1].Payload != nil || rec.events[2].Payload != nil {
		t.Error("acknowledge/resolve must not carry a payload")
	}
}

func TestSend_TestTriggersThenResolves(t *testing.T) {
	rec := &recorder{}
	p := New()
	p.endpoint = rec.server(t).URL
	if err := send(t, p, "created", true); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(rec.events) != 2 || rec.events[0].EventAction != "trigger" || rec.events[1].EventAction != "resolve" {
		t.Fatalf("events = %+v, want trigger then resolve", rec.events)
	}
	if rec.events[0].Payload.Severity != "info" {
		t.Errorf("test trigger severity = %q, want info", rec.events[0].Payload.Severity)
	}
}

func TestSend_BadRequestIsPermanentWithReason(t *testing.T) {
	rec := &recorder{status: http.StatusBadRequest}
	p := New()
	p.endpoint = rec.server(t).URL
	err := send(t, p, "created", false)
	if !plugin.IsPermanent(err) || !strings.Contains(err.Error(), "routing_key") {
		t.Fatalf("err = %v, want permanent carrying PagerDuty's reason", err)
	}
}

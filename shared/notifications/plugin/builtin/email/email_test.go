package email

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

type fakeMailer struct {
	gotEvent      notifications.AlertEvent
	gotTemplates  Templates
	gotRecipients []string
	err           error
}

func (f *fakeMailer) SendAlert(_ context.Context, event notifications.AlertEvent, tpls Templates, recipients []string) error {
	f.gotEvent = event
	f.gotTemplates = tpls
	f.gotRecipients = recipients
	return f.err
}

func TestPlugin_Manifest(t *testing.T) {
	m := New().Manifest()
	if m.Type != "email" {
		t.Errorf("Type = %q, want email", m.Type)
	}
	if !m.HasCapability(plugin.CapabilityRenderedAlert) {
		t.Error("expected CapabilityRenderedAlert")
	}
	if len(m.Fields) != 3 {
		t.Errorf("expected 3 fields, got %d", len(m.Fields))
	}
}

func TestPlugin_Validate(t *testing.T) {
	p := New()
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid", `{"to":["a@b.com"]}`, false},
		{"empty recipients", `{"to":[]}`, true},
		{"missing recipients", `{}`, true},
		{"bad address", `{"to":["not-an-email"]}`, true},
		{"bad subject tpl", `{"to":["a@b.com"],"subject_template":"{{"}`, true},
		{"bad body tpl", `{"to":["a@b.com"],"body_template":"{{"}`, true},
		{"empty body", ``, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Validate(json.RawMessage(tc.raw))
			if (err != nil) != tc.wantErr {
				t.Errorf("err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestPlugin_Send_RequiresMailer(t *testing.T) {
	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"to": []any{"x@y.z"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "mailer not configured") {
		t.Fatalf("expected 'mailer not configured', got %v", err)
	}
}

func TestPlugin_Send_DelegatesToMailer(t *testing.T) {
	p := New()
	m := &fakeMailer{}
	p.SetMailer(m)

	event := sampleEvent()
	err := p.Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{
			Config: map[string]any{
				"to":               []any{"alice@example.com", "bob@example.com"},
				"subject_template": "subj {{.monitor_name}}",
				"body_template":    "body {{.status}}",
			},
		},
		Event: event,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got, want := len(m.gotRecipients), 2; got != want {
		t.Fatalf("recipients len = %d, want %d", got, want)
	}
	if m.gotTemplates.Subject != "subj {{.monitor_name}}" {
		t.Errorf("subject template = %q", m.gotTemplates.Subject)
	}
	if m.gotEvent.Alert.MonitorName != event.Alert.MonitorName {
		t.Errorf("event not passed through")
	}
}

func TestPlugin_Send_PropagatesMailerError(t *testing.T) {
	p := New()
	want := errors.New("smtp down")
	p.SetMailer(&fakeMailer{err: want})

	err := p.Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"to": []any{"x@y.z"}}},
		Event:   sampleEvent(),
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestRenderSubject_DefaultsAndOverride(t *testing.T) {
	event := sampleEvent()
	subj, err := RenderSubject(event, Templates{})
	if err != nil {
		t.Fatalf("default subject: %v", err)
	}
	if !strings.Contains(subj, event.Alert.MonitorName) {
		t.Errorf("default subject missing monitor name: %q", subj)
	}

	subj, err = RenderSubject(event, Templates{Subject: "X-{{.status}}"})
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	if subj != "X-active" {
		t.Errorf("override = %q, want X-active", subj)
	}
}

func TestRenderBody_DefaultIncludesKeyFields(t *testing.T) {
	body, err := RenderBody(sampleEvent(), Templates{})
	if err != nil {
		t.Fatalf("default body: %v", err)
	}
	for _, want := range []string{"API health", "Critical", "active", "tenant-1"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nfull body:\n%s", want, body)
		}
	}
}

func TestNormalizeRecipients_DedupAndTrim(t *testing.T) {
	in := []string{" A@B.com ", "a@b.com", "", "c@d.com"}
	out := normalizeRecipients(in)
	if len(out) != 2 || out[0] != "A@B.com" || out[1] != "c@d.com" {
		t.Errorf("out = %v", out)
	}
}

func sampleEvent() notifications.AlertEvent {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	lastErr := "timeout"
	return notifications.AlertEvent{
		Type:      "created",
		TenantID:  "tenant-1",
		Timestamp: now,
		Alert: notifications.AlertDetails{
			ID:           "a-1",
			MonitorName:  "API health",
			PolicyName:   "Critical",
			Status:       "active",
			TriggeredAt:  now,
			FailureCount: 3,
			LastError:    &lastErr,
		},
	}
}

func TestDefaultBody_RootCauseAnnotation(t *testing.T) {
	event := sampleEvent()
	if strings.Contains(DefaultBody(event), "Likely Caused By") {
		t.Fatal("root-cause line rendered without a root cause set")
	}

	name := "Postgres prod"
	downSince := event.Timestamp.Add(-10 * time.Minute)
	event.Alert.RootCauseMonitorName = &name
	event.Alert.RootCauseDownSince = &downSince

	body := DefaultBody(event)
	if !strings.Contains(body, "Likely Caused By: Postgres prod") {
		t.Fatalf("root-cause line missing from body:\n%s", body)
	}

	data := templateData(event)
	if data["root_cause_monitor_name"] != "Postgres prod" {
		t.Fatalf("template variable root_cause_monitor_name = %v", data["root_cause_monitor_name"])
	}
}

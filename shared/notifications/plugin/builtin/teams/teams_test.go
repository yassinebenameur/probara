package teams

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

func TestMain(m *testing.M) {
	// httptest servers listen on loopback, which the default egress policy
	// refuses; the policy itself is covered in package plugin.
	plugin.Configure(plugin.Runtime{})
	os.Exit(m.Run())
}

func TestPlugin_Manifest(t *testing.T) {
	p := New()
	m := p.Manifest()
	if m.Type != "teams" {
		t.Errorf("Type = %q, want teams", m.Type)
	}
	if !m.HasCapability(plugin.CapabilityTestable) {
		t.Error("expected CapabilityTestable")
	}
	if len(m.Fields) != 1 || m.Fields[0].Key != "webhook_url" || !m.Fields[0].Secret {
		t.Errorf("expected one secret webhook_url field, got %+v", m.Fields)
	}
}

func TestPlugin_Validate(t *testing.T) {
	p := New()

	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid https", `{"webhook_url":"https://example.com/hook"}`, false},
		{"empty url", `{"webhook_url":""}`, true},
		{"http rejected", `{"webhook_url":"http://example.com/hook"}`, true},
		{"missing field", `{}`, true},
		{"empty body", ``, true},
		{"malformed json", `{`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Validate(json.RawMessage(tc.raw))
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate(%q): err=%v, wantErr=%v", tc.raw, err, tc.wantErr)
			}
		})
	}
}

func TestPlugin_Send_PostsAdaptiveCardToWorkflows(t *testing.T) {
	var captured adaptiveMessage
	var contentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("server: unmarshal payload: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	p := New()
	lastErr := "timeout after 5s"
	req := plugin.DispatchRequest{
		Channel: plugin.ChannelRef{
			ID:     "ch-1",
			Name:   "ops-room",
			Config: map[string]any{"webhook_url": srv.URL},
		},
		Event: notifications.AlertEvent{
			Type:      "created",
			TenantID:  "tenant-1",
			Timestamp: time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC),
			Alert: notifications.AlertDetails{
				MonitorName:  "API health",
				PolicyName:   "Critical",
				Status:       "active",
				FailureCount: 3,
				LastError:    &lastErr,
			},
		},
	}

	if err := p.Send(context.Background(), req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
	if captured.Type != "message" || len(captured.Attachments) != 1 {
		t.Fatalf("envelope = %+v, want one message attachment", captured)
	}
	att := captured.Attachments[0]
	if att.ContentType != "application/vnd.microsoft.card.adaptive" || att.Content.Type != "AdaptiveCard" {
		t.Fatalf("attachment = %+v, want an Adaptive Card", att)
	}
	raw, _ := json.Marshal(att.Content.Body)
	for _, want := range []string{"Alert Triggered: API health", "timeout after 5s", "Stopped responding"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("card body missing %q: %s", want, raw)
		}
	}
}

func TestIsLegacyConnector(t *testing.T) {
	for target, want := range map[string]bool{
		"https://contoso.webhook.office.com/webhookb2/abc":                         true,
		"https://outlook.office.com/webhook/abc":                                   true,
		"https://prod-12.westus.logic.azure.com/workflows/abc/triggers/manual/run": false,
		"https://default123.environment.api.powerplatform.com/powerautomate/abc":   false,
		"https://webhook.office.com.evil.io/webhookb2/abc":                         false,
	} {
		if got := isLegacyConnector(target); got != want {
			t.Errorf("isLegacyConnector(%q) = %v, want %v", target, got, want)
		}
	}
}

func TestPlugin_Send_PropagatesNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"webhook_url": srv.URL}},
		Event:   notifications.AlertEvent{Type: "created"},
	})
	if err == nil {
		t.Fatal("expected error on 502")
	}
}

func TestPlugin_Send_MissingWebhookURL(t *testing.T) {
	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{}},
		Event:   notifications.AlertEvent{Type: "created"},
	})
	if err == nil {
		t.Fatal("expected error when webhook_url missing")
	}
}

func TestBuildMessageCard_RootCauseAnnotation(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	event := notifications.AlertEvent{
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
		},
	}

	card := buildMessageCard(plugin.DispatchRequest{Event: event}.View())
	if cardHasFact(card, "Likely caused by") {
		t.Fatal("root-cause fact rendered without a root cause set")
	}

	name := "Postgres prod"
	downSince := now.Add(-10 * time.Minute)
	event.Alert.RootCauseMonitorName = &name
	event.Alert.RootCauseDownSince = &downSince
	card = buildMessageCard(plugin.DispatchRequest{Event: event}.View())
	if !cardHasFact(card, "Likely caused by") {
		t.Fatalf("root-cause fact missing: %+v", card.Sections)
	}
}

func cardHasFact(card messageCard, name string) bool {
	for _, section := range card.Sections {
		for _, f := range section.Facts {
			if f.Name == name {
				return true
			}
		}
	}
	return false
}

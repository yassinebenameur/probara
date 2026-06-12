package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

func TestManifest(t *testing.T) {
	m := New().Manifest()
	if m.Type != "discord" {
		t.Errorf("Type = %q", m.Type)
	}
	if !m.HasCapability(plugin.CapabilityRenderedAlert) {
		t.Error("expected CapabilityRenderedAlert")
	}
	if !m.Fields[0].Secret {
		t.Error("webhook_url should be marked Secret")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid", `{"webhook_url":"https://discord.com/api/webhooks/123/abc"}`, false},
		{"valid discordapp", `{"webhook_url":"https://discordapp.com/api/webhooks/123/abc"}`, false},
		{"missing", `{}`, true},
		{"http", `{"webhook_url":"http://discord.com/api/webhooks/123/abc"}`, true},
		{"wrong host", `{"webhook_url":"https://example.com/api/webhooks/123/abc"}`, true},
	}
	p := New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Validate(json.RawMessage(tc.raw))
			if (err != nil) != tc.wantErr {
				t.Errorf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestSend_PostsEmbed(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"webhook_url": srv.URL}},
		Event:   sampleEvent(),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var decoded discordPayload
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if len(decoded.Embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(decoded.Embeds))
	}
	if !strings.Contains(decoded.Embeds[0].Title, "API health") {
		t.Errorf("title missing monitor name: %q", decoded.Embeds[0].Title)
	}
}

func TestSend_FailsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"webhook_url": srv.URL}},
		Event:   sampleEvent(),
	})
	if err == nil {
		t.Fatal("expected non-2xx to surface")
	}
}

func TestColorFor_DistinguishesEventTypes(t *testing.T) {
	if colorFor("created") == colorFor("resolved") || colorFor("resolved") == colorFor("reminder") {
		t.Error("color should differ per event type")
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

func TestBuildEmbed_RootCauseAnnotation(t *testing.T) {
	event := sampleEvent()
	payload := buildEmbed(plugin.DispatchRequest{Event: event})
	if embedHasField(payload, "Likely Caused By") {
		t.Fatal("root-cause field rendered without a root cause set")
	}

	name := "Postgres prod"
	downSince := event.Timestamp.Add(-10 * time.Minute)
	event.Alert.RootCauseMonitorName = &name
	event.Alert.RootCauseDownSince = &downSince
	payload = buildEmbed(plugin.DispatchRequest{Event: event})
	if !embedHasField(payload, "Likely Caused By") {
		t.Fatalf("root-cause field missing: %+v", payload.Embeds)
	}
}

func embedHasField(payload discordPayload, name string) bool {
	for _, embed := range payload.Embeds {
		for _, f := range embed.Fields {
			if f.Name == name {
				return true
			}
		}
	}
	return false
}

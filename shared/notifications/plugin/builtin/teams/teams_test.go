package teams

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

func TestPlugin_Manifest(t *testing.T) {
	p := New()
	m := p.Manifest()
	if m.Type != "teams" {
		t.Errorf("Type = %q, want teams", m.Type)
	}
	if !m.HasCapability(plugin.CapabilityRawEvent) {
		t.Error("expected CapabilityRawEvent")
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

func TestPlugin_Send_PostsMessageCard(t *testing.T) {
	var captured messageCard
	var contentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("server: unmarshal payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
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
	if captured.Type != "MessageCard" {
		t.Errorf("card @type = %q, want MessageCard", captured.Type)
	}
	if !strings.Contains(captured.Title, "Alert Triggered: API health") {
		t.Errorf("title = %q, want prefix 'Alert Triggered: API health'", captured.Title)
	}
	if len(captured.Sections) == 0 {
		t.Fatal("expected at least one section")
	}
	if len(captured.Sections[0].Facts) < 5 {
		t.Errorf("expected last_error appended, got facts=%+v", captured.Sections[0].Facts)
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

func TestTitlePrefix(t *testing.T) {
	for in, want := range map[string]string{
		"created":  "Alert Triggered",
		"resolved": "Alert Resolved",
		"reminder": "Alert Still Active",
		"unknown":  "Alert",
	} {
		if got := titlePrefix(in); got != want {
			t.Errorf("titlePrefix(%q) = %q, want %q", in, got, want)
		}
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

	card := buildMessageCard(plugin.DispatchRequest{Event: event})
	if cardHasFact(card, "Likely Caused By") {
		t.Fatal("root-cause fact rendered without a root cause set")
	}

	name := "Postgres prod"
	downSince := now.Add(-10 * time.Minute)
	event.Alert.RootCauseMonitorName = &name
	event.Alert.RootCauseDownSince = &downSince
	card = buildMessageCard(plugin.DispatchRequest{Event: event})
	if !cardHasFact(card, "Likely Caused By") {
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

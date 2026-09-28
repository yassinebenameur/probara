package slack

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

func TestManifest(t *testing.T) {
	m := New().Manifest()
	if m.Type != "slack" {
		t.Errorf("Type = %q, want slack", m.Type)
	}
	if !m.HasCapability(plugin.CapabilityTestable) {
		t.Error("expected CapabilityTestable")
	}
	if m.HasCapability(plugin.CapabilityAcknowledge) {
		t.Error("chat channels must not opt into acknowledged events")
	}
	if len(m.Fields) != 1 {
		t.Errorf("expected 1 field, got %d", len(m.Fields))
	}
	if !m.Fields[0].Secret {
		t.Error("webhook_url should be marked Secret for encryption")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid", `{"webhook_url":"https://hooks.slack.com/services/T/B/X"}`, false},
		{"missing url", `{}`, true},
		{"http (not https)", `{"webhook_url":"http://hooks.slack.com/services/T/B/X"}`, true},
		{"wrong host", `{"webhook_url":"https://example.com/webhook"}`, true},
		{"lookalike suffix", `{"webhook_url":"https://notslack.com/services/T/B/X"}`, true},
		{"slack.com as a subdomain label", `{"webhook_url":"https://hooks.slack.com.evil.io/services/T/B/X"}`, true},
		{"embedded credentials", `{"webhook_url":"https://u:p@hooks.slack.com/services/T/B/X"}`, true},
		{"empty body", ``, true},
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

func TestSend_PostsBlockKit(t *testing.T) {
	var gotBody []byte
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New()
	err := p.Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"webhook_url": srv.URL}},
		Event:   sampleEvent(),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q", gotContentType)
	}

	var decoded slackPayload
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if len(decoded.Blocks) == 0 {
		t.Fatalf("expected at least one block")
	}
	if !strings.Contains(decoded.Text, "API health") {
		t.Errorf("fallback text missing monitor name: %q", decoded.Text)
	}
}

func TestSend_FailsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"webhook_url": srv.URL}},
		Event:   sampleEvent(),
	})
	if err == nil {
		t.Fatal("expected non-2xx to surface as error")
	}
}

func TestSend_MissingWebhookFails(t *testing.T) {
	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{}},
		Event:   sampleEvent(),
	})
	if err == nil {
		t.Fatal("expected error when webhook_url is missing")
	}
}

func TestBuildBlockKit_RootCauseAnnotation(t *testing.T) {
	event := sampleEvent()
	payload := buildBlockKit(plugin.DispatchRequest{Event: event}.View())
	if blocksContain(payload, "Likely caused by") {
		t.Fatal("root-cause field rendered without a root cause set")
	}

	name := "Postgres prod"
	downSince := time.Date(2026, 5, 24, 11, 50, 0, 0, time.UTC)
	event.Alert.RootCauseMonitorName = &name
	event.Alert.RootCauseDownSince = &downSince
	payload = buildBlockKit(plugin.DispatchRequest{Event: event}.View())
	if !blocksContain(payload, "Likely caused by") || !blocksContain(payload, "Postgres prod") {
		t.Fatalf("root-cause field missing from payload: %+v", payload.Blocks)
	}
}

func TestBuildBlockKit_EscapesMrkdwnControlSequences(t *testing.T) {
	event := sampleEvent()
	msg := "upstream said <!channel> & <@U123>"
	event.Alert.LastError = &msg
	raw, _ := json.Marshal(buildBlockKit(plugin.DispatchRequest{Event: event}.View()))
	if strings.Contains(string(raw), "<!channel>") || strings.Contains(string(raw), "<@U123>") {
		t.Fatalf("probe error reached Slack unescaped: %s", raw)
	}
}

func TestSend_ClassifiesPermanentFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("no_service"))
	}))
	defer srv.Close()

	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"webhook_url": srv.URL}},
		Event:   sampleEvent(),
	})
	if !plugin.IsPermanent(err) || !strings.Contains(err.Error(), "no_service") {
		t.Fatalf("err = %v, want permanent error carrying the provider reason", err)
	}
}

func blocksContain(payload slackPayload, substr string) bool {
	for _, block := range payload.Blocks {
		if block.Text != nil && strings.Contains(block.Text.Text, substr) {
			return true
		}
		for _, field := range block.Fields {
			if strings.Contains(field.Text, substr) {
				return true
			}
		}
	}
	return false
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

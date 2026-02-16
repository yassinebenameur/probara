package alerter

import (
	"strings"
	"testing"
	"time"
)

func TestRenderSubjectDefault(t *testing.T) {
	event := sampleAlertEvent()
	subject, err := renderSubject(event, EmailTemplates{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(subject, event.Alert.MonitorName) {
		t.Fatalf("expected subject to include monitor name, got %q", subject)
	}
}

func TestRenderBodyTemplate(t *testing.T) {
	event := sampleAlertEvent()
	templates := EmailTemplates{
		BodyTemplate: "Monitor {{.monitor_name}} status {{.status}}",
	}

	body, err := renderBody(event, templates)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(body, event.Alert.MonitorName) {
		t.Fatalf("expected body to include monitor name, got %q", body)
	}
	if !strings.Contains(body, event.Alert.Status) {
		t.Fatalf("expected body to include status, got %q", body)
	}
}

func TestRenderSubjectTemplateFallback(t *testing.T) {
	event := sampleAlertEvent()
	templates := EmailTemplates{
		SubjectTemplate: "{{ .monitor_name",
	}

	subject, err := renderSubject(event, templates)
	if err == nil {
		t.Fatalf("expected template error, got nil")
	}
	if !strings.Contains(subject, event.Alert.MonitorName) {
		t.Fatalf("expected fallback subject to include monitor name, got %q", subject)
	}
}

func sampleAlertEvent() AlertEvent {
	now := time.Date(2024, 2, 1, 10, 30, 0, 0, time.UTC)
	lastError := "timeout"
	return AlertEvent{
		Type:      "created",
		TenantID:  "tenant-123",
		Timestamp: now,
		Alert: AlertDetails{
			ID:            "alert-123",
			MonitorID:     "monitor-123",
			MonitorName:   "API Health",
			AlertPolicyID: "policy-123",
			PolicyName:    "Critical",
			Status:        "active",
			TriggeredAt:   now,
			FailureCount:  3,
			LastError:     &lastError,
		},
	}
}

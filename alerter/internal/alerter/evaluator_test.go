package alerter

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/notifications"
)

func TestShouldSendNotification(t *testing.T) {
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	interval := 10 * time.Minute

	tests := []struct {
		name      string
		eventType string
		state     *notificationState
		now       time.Time
		expected  bool
	}{
		{"created_no_state", "created", nil, now, true},
		{"created_with_state", "created", &notificationState{LastEventType: "created"}, now, false},
		{"resolved_no_state", "resolved", nil, now, true},
		{"resolved_after_resolved", "resolved", &notificationState{LastEventType: "resolved"}, now, false},
		{"resolved_after_created", "resolved", &notificationState{LastEventType: "created"}, now, true},
		{"reminder_no_state", "reminder", nil, now, false},
		{"reminder_recent", "reminder", &notificationState{LastEventType: "created", LastSentAt: now.Add(-5 * time.Minute)}, now, false},
		{"reminder_overdue", "reminder", &notificationState{LastEventType: "created", LastSentAt: now.Add(-15 * time.Minute)}, now, true},
		{"reminder_after_resolved", "reminder", &notificationState{LastEventType: "resolved", LastSentAt: now.Add(-15 * time.Minute)}, now, false},
		{"acknowledged_never_paged", "acknowledged", nil, now, false},
		{"acknowledged_after_created", "acknowledged", &notificationState{LastEventType: "created"}, now, true},
		{"acknowledged_after_reminder", "acknowledged", &notificationState{LastEventType: "reminder"}, now, true},
		{"acknowledged_twice", "acknowledged", &notificationState{LastEventType: "created", AcknowledgedSentAt: &now}, now, false},
		{"acknowledged_after_resolved", "acknowledged", &notificationState{LastEventType: "resolved"}, now, false},
		{"suspected_cause_never_paged", "suspected_cause", nil, now, false},
		{"suspected_cause_after_created", "suspected_cause", &notificationState{LastEventType: "created"}, now, true},
		{"suspected_cause_after_ack_sent", "suspected_cause", &notificationState{LastEventType: "reminder", AcknowledgedSentAt: &now}, now, true},
		{"suspected_cause_twice", "suspected_cause", &notificationState{LastEventType: "created", SuspectedCauseSentAt: &now}, now, false},
		{"suspected_cause_after_resolved", "suspected_cause", &notificationState{LastEventType: "resolved"}, now, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldSendNotification(test.eventType, test.state, test.now, interval); got != test.expected {
				t.Fatalf("expected %v, got %v", test.expected, got)
			}
		})
	}
}

func TestBuildAlertEvent_StatusAndTemplates(t *testing.T) {
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	resolvedAt := now.Add(-1 * time.Minute)
	subjectTemplate := "subject"
	bodyTemplate := "body"

	binding := policyBinding{
		MonitorID:            uuid.MustParse("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"),
		TenantID:             uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff"),
		MonitorName:          "api-e",
		PolicyID:             uuid.MustParse("11111111-2222-3333-4444-555555555555"),
		PolicyName:           "Critical",
		FailureThreshold:     2,
		FailureWindowSeconds: 300,
		EmailSubjectTemplate: &subjectTemplate,
		EmailBodyTemplate:    &bodyTemplate,
	}

	alert := &alertRecord{
		ID:           uuid.MustParse("99999999-9999-9999-9999-999999999999"),
		MonitorID:    binding.MonitorID,
		PolicyID:     binding.PolicyID,
		TriggeredAt:  now.Add(-10 * time.Minute),
		FailureCount: 3,
	}

	event := buildAlertEvent("resolved", binding, alert, &resolvedAt, now)
	if event.Alert.Status != "resolved" {
		t.Fatalf("expected resolved status, got %s", event.Alert.Status)
	}
	if event.Alert.ResolvedAt == nil || !event.Alert.ResolvedAt.Equal(resolvedAt) {
		t.Fatalf("expected resolvedAt %v, got %v", resolvedAt, event.Alert.ResolvedAt)
	}
	if event.Alert.EmailSubjectTemplate == nil || *event.Alert.EmailSubjectTemplate != subjectTemplate {
		t.Fatalf("expected subject template %q, got %v", subjectTemplate, event.Alert.EmailSubjectTemplate)
	}
	if event.Alert.EmailBodyTemplate == nil || *event.Alert.EmailBodyTemplate != bodyTemplate {
		t.Fatalf("expected body template %q, got %v", bodyTemplate, event.Alert.EmailBodyTemplate)
	}
}

func TestBuildAlertEvent_RootCause(t *testing.T) {
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	binding := policyBinding{
		MonitorID: uuid.MustParse("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"),
		TenantID:  uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff"),
	}
	alert := &alertRecord{
		ID:          uuid.MustParse("99999999-9999-9999-9999-999999999999"),
		MonitorID:   binding.MonitorID,
		TriggeredAt: now,
	}

	event := buildAlertEvent("created", binding, alert, nil, now)
	if event.Alert.RootCauseMonitorID != nil || event.Alert.RootCauseMonitorName != nil || event.Alert.RootCauseDownSince != nil {
		t.Fatalf("expected no root-cause fields, got %+v", event.Alert)
	}

	rcID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	rcName := "Postgres prod"
	rcDownSince := now.Add(-5 * time.Minute)
	alert.RootCauseMonitorID = &rcID
	alert.RootCauseMonitorName = &rcName
	alert.RootCauseDownSince = &rcDownSince

	event = buildAlertEvent("created", binding, alert, nil, now)
	if event.Alert.RootCauseMonitorID == nil || *event.Alert.RootCauseMonitorID != rcID.String() {
		t.Fatalf("root cause id = %v, want %s", event.Alert.RootCauseMonitorID, rcID)
	}
	if event.Alert.RootCauseMonitorName == nil || *event.Alert.RootCauseMonitorName != rcName {
		t.Fatalf("root cause name = %v, want %s", event.Alert.RootCauseMonitorName, rcName)
	}
	if event.Alert.RootCauseDownSince == nil || !event.Alert.RootCauseDownSince.Equal(rcDownSince) {
		t.Fatalf("root cause down since = %v, want %s", event.Alert.RootCauseDownSince, rcDownSince)
	}
}

// A follow-up keeps the alert's own status and carries the hint.
func TestBuildAlertEvent_SuspectedCause(t *testing.T) {
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	binding := policyBinding{MonitorID: uuid.New(), TenantID: uuid.New(), MonitorName: "api"}
	sc := &notifications.SuspectedCause{TriageID: "t", Key: "dns_failure", Title: "DNS resolution failure", Confidence: "high"}
	for _, status := range []string{"active", "acknowledged"} {
		alert := &alertRecord{ID: uuid.New(), Status: status, TriggeredAt: now.Add(-time.Hour), SuspectedCause: sc}
		event := buildAlertEvent("suspected_cause", binding, alert, nil, now)
		if event.Type != "suspected_cause" || event.Alert.Status != status || event.Alert.SuspectedCause != sc {
			t.Fatalf("status %s: got type=%q status=%q cause=%v", status, event.Type, event.Alert.Status, event.Alert.SuspectedCause)
		}
	}
	if event := buildAlertEvent("created", binding, &alertRecord{ID: uuid.New()}, nil, now); event.Alert.SuspectedCause != nil {
		t.Fatal("a page must not carry a suspected cause")
	}
}

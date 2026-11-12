package alerter

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/notifications"
)

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

// openAlertEventDue must agree with the created/reminder predicates of
// claimNotificationTx: created only before any send and once the escalation
// delay has passed; reminder only on an unresolved slot whose last send is at
// least one interval old, and never with reminders off.
func TestOpenAlertEventDue(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := 10 * time.Minute
	sent := func(event string, ago time.Duration) *notificationState {
		return &notificationState{LastEventType: event, LastSentAt: now.Add(-ago)}
	}
	tests := []struct {
		name     string
		state    *notificationState
		alertAge time.Duration
		delay    time.Duration
		interval time.Duration
		want     string
	}{
		{"first send, no delay", nil, 0, 0, interval, "created"},
		{"first send, delay not reached", nil, 4 * time.Minute, 5 * time.Minute, interval, ""},
		{"first send, delay reached", nil, 5 * time.Minute, 5 * time.Minute, interval, "created"},
		{"reminder before interval", sent("created", 9*time.Minute), time.Hour, 0, interval, ""},
		{"reminder at interval", sent("created", 10*time.Minute), time.Hour, 0, interval, "reminder"},
		{"reminder after a reminder", sent("reminder", 11*time.Minute), time.Hour, 0, interval, "reminder"},
		{"reminders off", sent("created", time.Hour), time.Hour, 0, 0, ""},
		{"resolved slot never reminds", sent("resolved", time.Hour), time.Hour, 0, interval, ""},
		{"sent slot never re-creates", sent("created", time.Minute), time.Hour, 0, interval, ""},
	}
	for _, tt := range tests {
		if got := openAlertEventDue(tt.state, tt.alertAge, tt.delay, now, tt.interval); got != tt.want {
			t.Errorf("%s: openAlertEventDue = %q, want %q", tt.name, got, tt.want)
		}
	}
}

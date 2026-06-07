package alerter

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
)

func TestUniquePolicyIDs_DedupPreservesOrder(t *testing.T) {
	policyA := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	policyB := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	policyC := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	bindings := []policyBinding{
		{PolicyID: policyA},
		{PolicyID: policyB},
		{PolicyID: policyA},
		{PolicyID: policyC},
		{PolicyID: policyB},
	}

	ids := uniquePolicyIDs(bindings)
	if len(ids) != 3 {
		t.Fatalf("expected 3 unique policy IDs, got %d", len(ids))
	}
	if ids[0] != policyA || ids[1] != policyB || ids[2] != policyC {
		t.Fatalf("unexpected policy order: %v", ids)
	}
}

func TestEvaluateGroupFailures_NoMembers(t *testing.T) {
	alert := newTestAlerter()
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	binding := policyBinding{FailureWindowSeconds: 300}

	count, lastError, detail := alert.evaluateGroupFailures(now, binding, nil, nil)
	if count != 0 {
		t.Fatalf("expected 0 failures, got %d", count)
	}
	if lastError != nil {
		t.Fatalf("expected nil lastError, got %v", *lastError)
	}
	if detail != nil {
		t.Fatalf("expected nil detail, got %#v", detail)
	}
}

func TestEvaluateGroupFailures_WindowAndLimit(t *testing.T) {
	alert := newTestAlerter()
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	binding := policyBinding{FailureWindowSeconds: 600}

	memberA := groupMember{ID: uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"), Name: "api-a"}
	memberB := groupMember{ID: uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"), Name: "api-b"}
	memberC := groupMember{ID: uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc"), Name: "api-c"}

	results := map[uuid.UUID]checkSummary{
		memberA.ID: {Status: "failure", Error: ptrString("timeout"), CreatedAt: now.Add(-2 * time.Minute)},
		memberB.ID: {Status: "error", Error: nil, CreatedAt: now.Add(-4 * time.Minute)},
		memberC.ID: {Status: "failure", Error: ptrString("bad gateway"), CreatedAt: now.Add(-9 * time.Minute)},
	}

	count, lastError, detail := alert.evaluateGroupFailures(now, binding, []groupMember{memberA, memberB, memberC}, results)
	if count != 3 {
		t.Fatalf("expected 3 failures, got %d", count)
	}
	if lastError == nil || *lastError != "timeout" {
		t.Fatalf("expected lastError timeout, got %v", lastError)
	}
	if detail == nil {
		t.Fatalf("expected group detail, got nil")
	}
	if len(detail.Failures) != 2 {
		t.Fatalf("expected 2 failures in detail, got %d", len(detail.Failures))
	}
	if detail.Failures[0].Name != "api-a" || detail.Failures[1].Name != "api-b" {
		t.Fatalf("unexpected failure ordering: %#v", detail.Failures)
	}
	if detail.ExtraCount != 1 {
		t.Fatalf("expected extraCount 1, got %d", detail.ExtraCount)
	}
}

func TestEvaluateGroupFailures_LastErrorFallback(t *testing.T) {
	alert := newTestAlerter()
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	binding := policyBinding{FailureWindowSeconds: 300}

	member := groupMember{ID: uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd"), Name: "api-d"}
	results := map[uuid.UUID]checkSummary{
		member.ID: {Status: "error", Error: nil, CreatedAt: now.Add(-1 * time.Minute)},
	}

	count, lastError, _ := alert.evaluateGroupFailures(now, binding, []groupMember{member}, results)
	if count != 1 {
		t.Fatalf("expected 1 failure, got %d", count)
	}
	if lastError == nil || *lastError != "api-d reported error" {
		t.Fatalf("expected fallback lastError, got %v", lastError)
	}
}

func TestGroupRecovered(t *testing.T) {
	memberA := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	memberB := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	staleFailure := time.Date(2026, 2, 4, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		results  map[uuid.UUID]checkSummary
		expected bool
	}{
		{"no_results", map[uuid.UUID]checkSummary{}, true},
		{"all_success", map[uuid.UUID]checkSummary{
			memberA: {Status: "success"},
			memberB: {Status: "success"},
		}, true},
		{"one_member_still_failing", map[uuid.UUID]checkSummary{
			memberA: {Status: "success"},
			memberB: {Status: "failure", CreatedAt: staleFailure},
		}, false},
		{"error_counts_as_failing", map[uuid.UUID]checkSummary{
			memberA: {Status: "error"},
		}, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := groupRecovered(test.results); got != test.expected {
				t.Fatalf("groupRecovered() = %v, want %v", got, test.expected)
			}
		})
	}
}

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

func newTestAlerter() *Alerter {
	return &Alerter{
		config: &config.AlerterConfig{
			AlertGroupWindowSeconds: 300,
			AlertGroupMaxChildren:   2,
		},
	}
}

func ptrString(value string) *string {
	return &value
}

package alerter

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/notifications"
)

func TestBuildAlertEvent_Status(t *testing.T) {
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	resolvedAt := now.Add(-1 * time.Minute)

	binding := alertSubject{
		MonitorID:   uuid.MustParse("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"),
		TenantID:    uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff"),
		MonitorName: "api-e",
	}

	alert := &alertRecord{
		ID:           uuid.MustParse("99999999-9999-9999-9999-999999999999"),
		MonitorID:    binding.MonitorID,
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
	if event.Alert.MonitorID != binding.MonitorID.String() || event.TenantID != binding.TenantID.String() {
		t.Fatalf("event subject = monitor %s tenant %s, want the binding's", event.Alert.MonitorID, event.TenantID)
	}
	// Monitor alerts carry no "Alert rule" name; only channel test sends do.
	if event.Alert.PolicyName != "" {
		t.Fatalf("PolicyName = %q, want empty", event.Alert.PolicyName)
	}
}

func TestBuildAlertEvent_RootCause(t *testing.T) {
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	binding := alertSubject{
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
	binding := alertSubject{MonitorID: uuid.New(), TenantID: uuid.New(), MonitorName: "api"}
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

// openAlertEventDue must agree with the created/relapsed/reminder predicates
// of claimNotificationTx: created only before any send and once the
// escalation delay has passed; relapsed only on a slot last paged in an
// earlier episode of an alert that has since reopened, whether it heard the
// recovery or not, once the delay has passed again; reminder only on an
// unresolved slot paged this episode whose last send is at least one
// interval old, and never with reminders off.
func TestOpenAlertEventDue(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	interval := 10 * time.Minute
	sent := func(event string, ago time.Duration) *notificationState {
		return &notificationState{LastEventType: event, LastSentAt: now.Add(-ago)}
	}
	// pagedIn is a slot last paged in episode paged; the alert is in episode 1.
	pagedIn := func(event string, ago time.Duration, paged int) *notificationState {
		st := sent(event, ago)
		st.PagedRelapse = paged
		return st
	}
	tests := []struct {
		name     string
		state    *notificationState
		alertAge time.Duration
		delay    time.Duration
		interval time.Duration
		want     string
		relapse  int // the alert's episode
	}{
		{"first send, no delay", nil, 0, 0, interval, "created", 0},
		{"first send, delay not reached", nil, 4 * time.Minute, 5 * time.Minute, interval, "", 0},
		{"first send, delay reached", nil, 5 * time.Minute, 5 * time.Minute, interval, "created", 0},
		{"reminder before interval", sent("created", 9*time.Minute), time.Hour, 0, interval, "", 0},
		{"reminder at interval", sent("created", 10*time.Minute), time.Hour, 0, interval, "reminder", 0},
		{"reminder after a reminder", sent("reminder", 11*time.Minute), time.Hour, 0, interval, "reminder", 0},
		{"reminders off", sent("created", time.Hour), time.Hour, 0, 0, "", 0},
		{"resolved slot never reminds", sent("resolved", time.Hour), time.Hour, 0, interval, "", 0},
		{"heard the recovery, then the alert reopened: relapse notice", pagedIn("resolved", time.Hour, 0), time.Hour, 0, interval, "relapsed", 1},
		{"missed the recovery, then the alert reopened: relapse notice too", pagedIn("reminder", time.Hour, 0), time.Hour, 0, interval, "relapsed", 1},
		{"relapse notice waits for the delay from the reopen", pagedIn("resolved", time.Hour, 0), 4 * time.Minute, 5 * time.Minute, interval, "", 1},
		{"relapsed slot reminds like a page", pagedIn("relapsed", 11*time.Minute, 1), time.Hour, 0, interval, "reminder", 1},
		{"relapsed slot never re-sends the notice", pagedIn("relapsed", time.Minute, 1), time.Hour, 0, interval, "", 1},
		{"sent slot never re-creates", sent("created", time.Minute), time.Hour, 0, interval, "", 0},
	}
	for _, tt := range tests {
		if got := openAlertEventDue(tt.state, tt.alertAge, tt.delay, now, tt.interval, dispatchGate{RelapseCount: tt.relapse}); got != tt.want {
			t.Errorf("%s: openAlertEventDue = %q, want %q", tt.name, got, tt.want)
		}
	}
}

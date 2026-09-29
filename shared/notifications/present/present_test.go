package present

import (
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
)

func sample() notifications.AlertEvent {
	opened := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	lastErr := "dial tcp: i/o timeout"
	return notifications.AlertEvent{
		Type:      EventCreated,
		TenantID:  "tenant-1",
		Timestamp: opened,
		Alert: notifications.AlertDetails{
			ID:           "a-1",
			MonitorID:    "3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234",
			MonitorName:  "API health",
			PolicyName:   "Critical",
			Status:       "active",
			TriggeredAt:  opened,
			FailureCount: 3,
			LastError:    &lastErr,
		},
	}
}

func TestBuildCreated(t *testing.T) {
	m := Build(sample(), "", "https://probara.example.com/")
	if m.Title != "Alert Triggered: API health" || m.StatusWord != "DOWN" || m.Tone != ToneDown {
		t.Fatalf("got title=%q status=%q tone=%q", m.Title, m.StatusWord, m.Tone)
	}
	if m.Severity() != "critical" {
		t.Errorf("Severity = %q, want critical", m.Severity())
	}
	if m.ActionURL != "https://probara.example.com/monitors/3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234" {
		t.Errorf("ActionURL = %q", m.ActionURL)
	}
	if m.Duration() != "" {
		t.Errorf("a fresh alert has no duration yet, got %q", m.Duration())
	}
}

func TestBuildAcknowledged(t *testing.T) {
	ev := sample()
	ev.Timestamp = ev.Alert.TriggeredAt.Add(7 * time.Minute)
	m := Build(ev, EventAcknowledged, "")
	if m.Label != "Alert Acknowledged" || m.StatusWord != "ACKNOWLEDGED" || m.Tone != ToneInfo {
		t.Fatalf("got label=%q status=%q tone=%q", m.Label, m.StatusWord, m.Tone)
	}
	if !strings.Contains(m.Summary, "7m") {
		t.Errorf("summary should say how long until ack: %q", m.Summary)
	}
	if m.ActionURL != "" {
		t.Errorf("no base URL must mean no link, got %q", m.ActionURL)
	}
}

func TestBuildResolvedDropsBlastRadius(t *testing.T) {
	ev := sample()
	ev.Alert.ImpactedMonitors = []notifications.ImpactedMonitor{{ID: "b", Name: "checkout"}}
	ev.Alert.ImpactedCount = 4
	firing := Build(ev, EventCreated, "")
	if got := firing.ImpactedSummary(); got != "checkout and 3 more" {
		t.Errorf("ImpactedSummary = %q", got)
	}

	resolvedAt := ev.Alert.TriggeredAt.Add(90 * time.Minute)
	ev.Alert.ResolvedAt = &resolvedAt
	ev.Alert.Status = "resolved"
	m := Build(ev, EventResolved, "")
	if m.Tone != ToneUp || m.ImpactedSummary() != "" {
		t.Fatalf("resolved: tone=%q impacted=%q", m.Tone, m.ImpactedSummary())
	}
	if m.Duration() != "1h 30m" {
		t.Errorf("Duration = %q, want 1h 30m", m.Duration())
	}
}

func TestBuildMeshEdge(t *testing.T) {
	ev := sample()
	src, dst := "eu-west", "us-east"
	ev.Alert.Kind = notifications.KindMeshEdge
	ev.Alert.MonitorID = emptyUUID
	ev.Alert.SourceLocationName = &src
	ev.Alert.TargetLocationName = &dst
	m := Build(ev, EventCreated, "https://probara.example.com")
	if m.Label != "Mesh Path Down" || m.StatusWord != "PATH DOWN" {
		t.Fatalf("label=%q status=%q", m.Label, m.StatusWord)
	}
	if m.ActionURL != "https://probara.example.com/mesh" {
		t.Errorf("ActionURL = %q", m.ActionURL)
	}
}

func TestFactsOrderAndShort(t *testing.T) {
	ev := sample()
	rc := "Postgres prod"
	ev.Alert.RootCauseMonitorName = &rc
	ev.Alert.FailingLocations = []notifications.FailingLocation{{ID: "1", Name: "eu-west"}}
	m := Build(ev, EventCreated, "https://probara.example.com")

	var labels []string
	for _, f := range m.Facts() {
		labels = append(labels, f.Label)
	}
	got := strings.Join(labels, ",")
	if got != "Alert rule,Triggered,Failed checks,Likely caused by,Failing locations" {
		t.Errorf("facts = %s", got)
	}

	short := m.Short()
	if !strings.HasPrefix(short, "DOWN · Alert Triggered: API health\n") || !strings.HasSuffix(short, "/monitors/3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234") {
		t.Errorf("Short = %q", short)
	}
}

func TestHumanDuration(t *testing.T) {
	for in, want := range map[time.Duration]string{
		45 * time.Second:              "45s",
		4*time.Minute + 5*time.Second: "4m 5s",
		time.Hour + 4*time.Minute:     "1h 4m",
		26 * time.Hour:                "1d 2h",
	} {
		if got := HumanDuration(in); got != want {
			t.Errorf("HumanDuration(%s) = %q, want %q", in, got, want)
		}
	}
}

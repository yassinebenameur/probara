package present

import (
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
)

func floatPtr(v float64) *float64 { return &v }

func domainExpirySample(daysLeft float64) notifications.AlertEvent {
	ev := sample()
	ev.Alert.Kind = notifications.KindDomainExpiry
	ev.Alert.MonitorName = "example.com registration"
	ev.Alert.FailureCount = 0
	ev.Alert.LastError = nil
	ev.Alert.MetricValue = floatPtr(daysLeft)
	ev.Alert.ThresholdValue = floatPtr(30)
	return ev
}

func TestBuildDomainExpiry(t *testing.T) {
	m := Build(domainExpirySample(12), EventCreated, "https://probara.example.com")
	if m.Label != "Domain Expiring" || m.StatusWord != "EXPIRING SOON" || m.Tone != ToneWarn {
		t.Fatalf("label=%q status=%q tone=%q", m.Label, m.StatusWord, m.Tone)
	}
	if m.Summary != "The domain registration expires in 12 days, inside the 30-day warning window." {
		t.Errorf("Summary = %q", m.Summary)
	}
	if got := m.MetricLine(); got != "Registration left: 12 days (warns under 30 days)" {
		t.Errorf("MetricLine = %q", got)
	}
	if m.ActionURL != "https://probara.example.com/monitors/3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234" {
		t.Errorf("ActionURL = %q", m.ActionURL)
	}

	if got := Build(domainExpirySample(12), EventReminder, "").Label; got != "Domain Still Expiring" {
		t.Errorf("reminder label = %q", got)
	}

	expired := Build(domainExpirySample(-3), EventReminder, "")
	if expired.StatusWord != "DOMAIN EXPIRED" || expired.Tone != ToneDown {
		t.Errorf("expired: status=%q tone=%q", expired.StatusWord, expired.Tone)
	}
	if !strings.HasPrefix(expired.Summary, "The domain registration expired 3 days ago.") {
		t.Errorf("expired summary = %q", expired.Summary)
	}
	if got := expired.MetricLine(); got != "Registration expired: 3 days ago (warns under 30 days)" {
		t.Errorf("expired MetricLine = %q", got)
	}

	ev := domainExpirySample(400)
	ev.Alert.Status = "resolved"
	resolved := Build(ev, EventResolved, "")
	if resolved.Label != "Domain Renewed" || resolved.StatusWord != "DOMAIN RENEWED" || resolved.Tone != ToneUp {
		t.Errorf("resolved: label=%q status=%q tone=%q", resolved.Label, resolved.StatusWord, resolved.Tone)
	}
	if resolved.Summary != "The domain registration has been renewed." {
		t.Errorf("resolved summary = %q", resolved.Summary)
	}
}

func locationOfflineSample() notifications.AlertEvent {
	ev := sample()
	loc := "eu-west"
	ev.Alert.Kind = notifications.KindLocationOffline
	ev.Alert.MonitorID = emptyUUID
	ev.Alert.MonitorName = ""
	ev.Alert.FailureCount = 0
	ev.Alert.LastError = nil
	ev.Alert.SourceLocationName = &loc
	return ev
}

func TestBuildLocationOffline(t *testing.T) {
	m := Build(locationOfflineSample(), EventCreated, "https://probara.example.com")
	if m.Title != "Location Offline: eu-west" || m.StatusWord != "OFFLINE" || m.Tone != ToneDown {
		t.Fatalf("title=%q status=%q tone=%q", m.Title, m.StatusWord, m.Tone)
	}
	if !strings.HasPrefix(m.Summary, "Location eu-west has stopped reporting") {
		t.Errorf("Summary = %q", m.Summary)
	}
	if m.Metric != nil {
		t.Errorf("no metric expected, got %+v", m.Metric)
	}
	if len(m.Rows) == 0 || m.Rows[0] != (Row{Label: "Location", Value: "eu-west"}) {
		t.Errorf("Rows = %+v", m.Rows)
	}
	if m.ActionURL != "https://probara.example.com/locations" {
		t.Errorf("ActionURL = %q", m.ActionURL)
	}

	ev := locationOfflineSample()
	ev.Timestamp = ev.Alert.TriggeredAt.Add(10 * time.Minute)
	reminder := Build(ev, EventReminder, "")
	if reminder.Label != "Location Still Offline" || reminder.StatusWord != "STILL OFFLINE" {
		t.Errorf("reminder: label=%q status=%q", reminder.Label, reminder.StatusWord)
	}
	if reminder.Summary != "Location eu-west is still not reporting, 10m after it went silent." {
		t.Errorf("reminder summary = %q", reminder.Summary)
	}

	ev = locationOfflineSample()
	resolvedAt := ev.Alert.TriggeredAt.Add(25 * time.Minute)
	ev.Alert.ResolvedAt = &resolvedAt
	ev.Alert.Status = "resolved"
	resolved := Build(ev, EventResolved, "")
	if resolved.Label != "Location Back Online" || resolved.StatusWord != "BACK ONLINE" || resolved.Tone != ToneUp {
		t.Errorf("resolved: label=%q status=%q tone=%q", resolved.Label, resolved.StatusWord, resolved.Tone)
	}
	if resolved.Summary != "Location eu-west is reporting again after 25m offline." {
		t.Errorf("resolved summary = %q", resolved.Summary)
	}
}

// An open location_offline alert says when the location went quiet and what
// that strands; the closed one does not repeat it.
func TestBuildLocationOfflineFacts(t *testing.T) {
	ev := locationOfflineSample()
	seen := ev.Alert.TriggeredAt.Add(-3 * time.Minute)
	ev.Alert.LocationLastSeenAt = &seen
	cases := []struct {
		total, only int
		want        string
	}{
		{0, 0, "none run from it"},
		{12, 0, "12, all also run from another location"},
		{12, 5, "12, 5 with no other location"},
		{4, 4, "4, none with another location"},
	}
	for _, tc := range cases {
		ev.Alert.LocationMonitorCount, ev.Alert.LocationOnlyMonitorCount = tc.total, tc.only
		m := Build(ev, EventCreated, "")
		want := []Row{
			{Label: "Location", Value: "eu-west"},
			{Label: "Last heartbeat", Value: HumanTime(seen)},
			{Label: "Monitors", Value: tc.want},
		}
		for i, row := range want {
			if i >= len(m.Rows) || m.Rows[i] != row {
				t.Fatalf("%d/%d: Rows = %+v, want prefix %+v", tc.total, tc.only, m.Rows, want)
			}
		}
	}

	resolvedAt := ev.Alert.TriggeredAt.Add(time.Hour)
	ev.Alert.ResolvedAt, ev.Alert.Status = &resolvedAt, "resolved"
	for _, row := range Build(ev, EventResolved, "").Rows {
		if row.Label == "Monitors" || row.Label == "Last heartbeat" {
			t.Fatalf("a closed alert repeats %q", row.Label)
		}
	}
}

// A location_offline alert closed because the location was disabled or
// deleted must not claim the location is back.
func TestBuildLocationOfflineClosedWithoutRecovery(t *testing.T) {
	for reason, want := range map[string]string{
		notifications.ResolveReasonLocationDisabled: "Location eu-west was disabled, so it is no longer expected to report.",
		notifications.ResolveReasonLocationDeleted:  "Location eu-west was deleted.",
		notifications.ResolveReasonManual:           "An operator closed the alert for location eu-west. If it is still not reporting, a new alert opens.",
	} {
		ev := locationOfflineSample()
		resolvedAt := ev.Alert.TriggeredAt.Add(time.Hour)
		ev.Alert.ResolvedAt, ev.Alert.Status, ev.Alert.ResolveReason = &resolvedAt, "resolved", reason
		m := Build(ev, EventResolved, "")
		if m.Label != "Location Alert Closed" || m.StatusWord != "CLOSED" || m.Tone != ToneInfo || m.Summary != want {
			t.Errorf("%s: label=%q status=%q tone=%q summary=%q", reason, m.Label, m.StatusWord, m.Tone, m.Summary)
		}
	}
	ev := locationOfflineSample()
	resolvedAt := ev.Alert.TriggeredAt.Add(time.Hour)
	ev.Alert.ResolvedAt, ev.Alert.Status, ev.Alert.ResolveReason = &resolvedAt, "resolved", notifications.ResolveReasonRecovered
	if m := Build(ev, EventResolved, ""); m.Label != "Location Back Online" || m.StatusWord != "BACK ONLINE" {
		t.Errorf("recovered: label=%q status=%q", m.Label, m.StatusWord)
	}
}

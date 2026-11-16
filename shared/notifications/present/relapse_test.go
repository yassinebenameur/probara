package present

import (
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
)

// relapseSample is sample() reopened for the second time, 4 minutes after it
// last recovered.
func relapseSample() notifications.AlertEvent {
	ev := sample()
	recovered := ev.Alert.TriggeredAt.Add(30 * time.Minute)
	reopened := recovered.Add(4 * time.Minute)
	ev.Timestamp = reopened
	ev.Alert.RelapseCount = 2
	ev.Alert.RelapseNotice = true
	ev.Alert.ReopenedAt = &reopened
	ev.Alert.LastRecoveredAt = &recovered
	return ev
}

// A relapse page has its own wording on every channel: the label, the status
// word and the summary say it relapsed, how soon after recovering, and which
// relapse it is; the rows count the relapses.
func TestBuildRelapsePage(t *testing.T) {
	m := Build(relapseSample(), EventCreated, "")
	if m.Title != "Alert Relapsed: API health" || m.StatusWord != "RELAPSED" || m.Tone != ToneDown {
		t.Fatalf("title=%q status=%q tone=%q", m.Title, m.StatusWord, m.Tone)
	}
	want := "Relapsed — down again 4m after recovering, relapse #2. Stopped responding — 3 checks failed in a row."
	if m.Summary != want {
		t.Fatalf("Summary = %q, want %q", m.Summary, want)
	}
	if !hasRow(m.Rows, "Relapses", "2") || !hasRow(m.Rows, "Reopened", "24 May 2026, 12:34 UTC") {
		t.Fatalf("rows = %+v", m.Rows)
	}
	if !strings.Contains(m.Short(), "RELAPSED · Alert Relapsed: API health") {
		t.Fatalf("Short = %q", m.Short())
	}
}

// Without the recovery time the gap is not guessed.
func TestBuildRelapsePageWithoutRecoveryTime(t *testing.T) {
	ev := relapseSample()
	ev.Alert.LastRecoveredAt = nil
	if m := Build(ev, EventCreated, ""); !strings.HasPrefix(m.Summary, "Relapsed — down again shortly after recovering, relapse #2.") {
		t.Fatalf("Summary = %q", m.Summary)
	}
}

// Reminders and the resolve of a relapsed alert keep their usual wording and
// gain the relapse rows; a first-episode page is untouched.
func TestRelapseOnlyChangesThePage(t *testing.T) {
	reminder := Build(relapseSample(), EventReminder, "")
	if reminder.Label != "Alert Still Active" || reminder.StatusWord != "STILL DOWN" || strings.Contains(reminder.Summary, "Relapsed") {
		t.Fatalf("reminder: label=%q status=%q summary=%q", reminder.Label, reminder.StatusWord, reminder.Summary)
	}
	if !hasRow(reminder.Rows, "Relapses", "2") {
		t.Fatalf("reminder rows = %+v", reminder.Rows)
	}
	ev := relapseSample()
	ev.Alert.Status = "resolved"
	resolved := Build(ev, EventResolved, "")
	if resolved.Label != "Alert Resolved" || resolved.StatusWord != "RECOVERED" || !hasRow(resolved.Rows, "Relapses", "2") {
		t.Fatalf("resolved: label=%q status=%q rows=%+v", resolved.Label, resolved.StatusWord, resolved.Rows)
	}
	first := Build(sample(), EventCreated, "")
	if first.Label != "Alert Triggered" || hasRow(first.Rows, "Relapses", "") {
		t.Fatalf("first page: label=%q rows=%+v", first.Label, first.Rows)
	}
	// A page during a relapse that is not the relapse notice (an escalation
	// route's first page, a channel that never heard the alert) is an
	// ordinary page, with the relapse count as a fact.
	tier := relapseSample()
	tier.Alert.RelapseNotice = false
	page := Build(tier, EventCreated, "")
	if page.Label != "Alert Triggered" || page.StatusWord != "DOWN" || strings.Contains(page.Summary, "Relapsed") ||
		!hasRow(page.Rows, "Relapses", "2") {
		t.Fatalf("ordinary page of a relapsed alert: label=%q status=%q summary=%q rows=%+v",
			page.Label, page.StatusWord, page.Summary, page.Rows)
	}
}

// Every kind that can reopen gets a relapse page in its own terms and tone.
func TestBuildRelapsePageEveryKind(t *testing.T) {
	src, dst, loc := "eu-west", "us-east", "ap-south"
	cpu := "system.cpu.utilization"
	for _, tc := range []struct {
		kind  string
		set   func(*notifications.AlertDetails)
		label string
		again string
		tone  Tone
	}{
		{notifications.KindLatencyAnomaly, nil, "Latency Degraded Again", "degraded again", ToneWarn},
		{notifications.KindHostMetric, func(d *notifications.AlertDetails) { d.MetricName = &cpu }, "", "breaching again", ToneWarn},
		{notifications.KindTLSExpiry, nil, "TLS Certificate Expiring Again", "back in its warning window", ToneWarn},
		{notifications.KindMeshEdge, func(d *notifications.AlertDetails) { d.SourceLocationName, d.TargetLocationName = &src, &dst },
			"Mesh Path Down Again", "failing again", ToneDown},
		{notifications.KindLocationOffline, func(d *notifications.AlertDetails) { d.SourceLocationName = &loc },
			"Location Offline Again", "offline again", ToneDown},
	} {
		ev := relapseSample()
		ev.Alert.Kind = tc.kind
		if tc.set != nil {
			tc.set(&ev.Alert)
		}
		m := Build(ev, EventCreated, "")
		label := tc.label
		if label == "" {
			label = Build(func() notifications.AlertEvent { e := ev; e.Alert.RelapseNotice = false; return e }(), EventCreated, "").Label + " Again"
		}
		if m.Label != label || m.StatusWord != "RELAPSED" || m.Tone != tc.tone {
			t.Errorf("%s: label=%q status=%q tone=%q, want %q", tc.kind, m.Label, m.StatusWord, m.Tone, label)
		}
		if !strings.HasPrefix(m.Summary, "Relapsed — "+tc.again+" 4m after recovering, relapse #2.") {
			t.Errorf("%s: summary=%q", tc.kind, m.Summary)
		}
	}
}

// A relapse page is a page: it carries the acknowledge link like any other.
func TestRelapsePageCarriesAckLink(t *testing.T) {
	s := &recordingSigner{}
	m := BuildWith(relapseSample(), EventCreated, linkedOptions(s))
	if m.AckURL != "https://probara.example.com/alert-action#tok" || s.calls != 1 {
		t.Fatalf("AckURL=%q calls=%d", m.AckURL, s.calls)
	}
}

func hasRow(rows []Row, label, value string) bool {
	for _, r := range rows {
		if r.Label == label && (value == "" || r.Value == value) {
			return true
		}
	}
	return false
}

// The relapse lead composes with each kind's own sentence: a certificate
// alert about an intermediate keeps naming it, and a domain whose expiry
// cannot be read says so rather than "back in its warning window".
func TestRelapsePageComposesWithKindWording(t *testing.T) {
	ev := relapseSample()
	ev.Alert.Kind = notifications.KindTLSExpiry
	lastErr := notifications.FormatTLSExpiryError(notifications.TLSExpiryCertificate{Subject: "CN=R3,O=Example", Intermediate: true}, 9, 14)
	ev.Alert.LastError = &lastErr
	ev.Alert.MetricValue, ev.Alert.ThresholdValue = floatPtr(9), floatPtr(14)
	m := Build(ev, EventCreated, "")
	want := "Relapsed — back in its warning window 4m after recovering, relapse #2. An intermediate certificate (R3) expires in 9 days, inside the 14-day warning window."
	if m.Summary != want || m.StatusWord != "RELAPSED" {
		t.Fatalf("tls: status=%q summary=%q, want %q", m.StatusWord, m.Summary, want)
	}

	dom := relapseSample()
	dom.Alert.Kind = notifications.KindDomainExpiry
	dom.Alert.MetricValue = nil
	if m := Build(dom, EventCreated, ""); !strings.HasPrefix(m.Summary, "Relapsed — unreadable again 4m after recovering, relapse #2.") {
		t.Fatalf("domain expiry unknown: summary=%q", m.Summary)
	}
}

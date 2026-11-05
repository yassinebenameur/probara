package present

import (
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
)

func relapseSample(n int) notifications.AlertEvent {
	event := sample()
	reopened := event.Alert.TriggeredAt.Add(10 * time.Minute)
	event.Alert.RelapseCount = n
	event.Alert.ReopenedAt = &reopened
	event.Timestamp = reopened
	return event
}

// A relapse's page is a "created" event with RelapseCount > 0 and reads
// "Down again (relapse n)"; every event of a relapsed alert lists the relapse.
func TestBuildRelapsePage(t *testing.T) {
	m := Build(relapseSample(2), EventCreated, "")
	if m.Label != "Down Again" || m.StatusWord != "DOWN AGAIN" || m.Tone != ToneDown {
		t.Fatalf("label/word/tone = %q/%q/%q", m.Label, m.StatusWord, m.Tone)
	}
	if want := "Down again (relapse 2) — 3 checks failed in a row."; m.Summary != want {
		t.Fatalf("summary = %q, want %q", m.Summary, want)
	}
	if m.Title != "Down Again: API health" {
		t.Fatalf("title = %q", m.Title)
	}
	rows := map[string]string{}
	for _, r := range m.Rows {
		rows[r.Label] = r.Value
	}
	if rows["Relapses"] != "2" || rows["Reopened"] != "24 May 2026, 12:10 UTC" {
		t.Fatalf("relapse rows = %v", rows)
	}

	// A reminder or resolve of a relapsed alert keeps its usual wording but
	// still carries the relapse facts.
	reminder := Build(relapseSample(1), EventReminder, "")
	if reminder.Label != "Alert Still Active" || reminder.StatusWord != "STILL DOWN" {
		t.Fatalf("reminder label/word = %q/%q", reminder.Label, reminder.StatusWord)
	}
	resolvedEvent := relapseSample(1)
	resolvedAt := resolvedEvent.Timestamp.Add(time.Minute)
	resolvedEvent.Alert.Status, resolvedEvent.Alert.ResolvedAt = "resolved", &resolvedAt
	resolved := Build(resolvedEvent, EventResolved, "")
	if resolved.Label != "Alert Resolved" {
		t.Fatalf("resolved label = %q", resolved.Label)
	}
	found := false
	for _, r := range resolved.Rows {
		found = found || (r.Label == "Relapses" && r.Value == "1")
	}
	if !found {
		t.Fatalf("resolved rows lack the relapse count: %v", resolved.Rows)
	}

	// Episode 0 is unchanged.
	if m := Build(sample(), EventCreated, ""); m.Label != "Alert Triggered" || m.StatusWord != "DOWN" {
		t.Fatalf("first page label/word = %q/%q", m.Label, m.StatusWord)
	}
	for _, r := range Build(sample(), EventCreated, "").Rows {
		if r.Label == "Relapses" || r.Label == "Reopened" {
			t.Fatalf("a never-relapsed alert carries relapse row %q", r.Label)
		}
	}
}

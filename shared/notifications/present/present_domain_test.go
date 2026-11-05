package present

import (
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications"
)

func TestBuildDomainExpiry(t *testing.T) {
	ev := sample()
	days, threshold := 12.0, 30.0
	ev.Alert.Kind = notifications.KindDomainExpiry
	ev.Alert.MonitorName = "example.com"
	ev.Alert.FailureCount = 0
	ev.Alert.MetricValue, ev.Alert.ThresholdValue = &days, &threshold

	m := Build(ev, EventCreated, "https://probara.example.com")
	if m.Label != "Domain Expiring" || m.StatusWord != "EXPIRING SOON" || m.Tone != ToneWarn {
		t.Fatalf("label=%q status=%q tone=%q", m.Label, m.StatusWord, m.Tone)
	}
	if m.Summary != "The domain registration expires in 12 days, inside the 30-day warning window." {
		t.Errorf("Summary = %q", m.Summary)
	}
	if m.Metric == nil || m.Metric.Label != "Registration left" || m.Metric.Value != "12 days" || m.Metric.Note != "warns under 30 days" {
		t.Errorf("Metric = %+v", m.Metric)
	}
	if m.ActionURL != "https://probara.example.com/monitors/"+ev.Alert.MonitorID {
		t.Errorf("ActionURL = %q", m.ActionURL)
	}
	if r := Build(ev, EventReminder, ""); r.Label != "Domain Still Expiring" {
		t.Errorf("reminder label = %q", r.Label)
	}

	expired := -3.0
	ev.Alert.MetricValue = &expired
	x := Build(ev, EventCreated, "")
	if x.StatusWord != "DOMAIN EXPIRED" || x.Tone != ToneDown ||
		x.Summary != "The domain registration expired 3 days ago. Renew it before the registry releases the name." ||
		x.Metric == nil || x.Metric.Label != "Registration expired" || x.Metric.Value != "3 days ago" {
		t.Errorf("expired: status=%q tone=%q summary=%q metric=%+v", x.StatusWord, x.Tone, x.Summary, x.Metric)
	}

	ev.Alert.Status = "resolved"
	r := Build(ev, EventResolved, "")
	if r.Label != "Domain Renewed" || r.StatusWord != "DOMAIN RENEWED" || r.Tone != ToneUp ||
		r.Summary != "The domain registration has been renewed." {
		t.Errorf("resolved label=%q status=%q tone=%q summary=%q", r.Label, r.StatusWord, r.Tone, r.Summary)
	}
}

package present

import (
	"strings"
	"testing"
)

// A domain_expiry alert without a value is "expiry unknown": the alerter
// could not determine the registration date. It warns, never reads as an
// outage, and resolves with neutral wording rather than "renewed".
func TestBuildDomainExpiryUnknown(t *testing.T) {
	ev := domainExpirySample(0)
	ev.Alert.MetricValue = nil
	reason := "domain: expiry unknown: the registry publishes no expiration date"
	ev.Alert.LastError = &reason

	m := Build(ev, EventCreated, "")
	if m.Label != "Domain Expiry Unknown" || m.StatusWord != "EXPIRY UNKNOWN" || m.Tone != ToneWarn {
		t.Fatalf("label=%q status=%q tone=%q", m.Label, m.StatusWord, m.Tone)
	}
	if !strings.HasPrefix(m.Summary, "The registration expiry date could not be determined") {
		t.Errorf("Summary = %q", m.Summary)
	}
	if m.Metric != nil {
		t.Errorf("Metric = %+v, want none", m.Metric)
	}
	if m.LastError != reason {
		t.Errorf("LastError = %q", m.LastError)
	}
	if got := Build(ev, EventReminder, "").Label; got != "Domain Expiry Unknown" {
		t.Errorf("reminder label = %q", got)
	}

	ev.Alert.Status = "resolved"
	resolved := Build(ev, EventResolved, "")
	if resolved.Label != "Domain Expiry Alert Resolved" || resolved.StatusWord != "RESOLVED" || resolved.Tone != ToneUp {
		t.Errorf("resolved: label=%q status=%q tone=%q", resolved.Label, resolved.StatusWord, resolved.Tone)
	}
	if strings.Contains(strings.ToLower(resolved.Summary), "renewed") {
		t.Errorf("resolved summary claims a renewal: %q", resolved.Summary)
	}
}

package present

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// recordingSigner mints "tok" and remembers what it was asked for.
type recordingSigner struct {
	calls           int
	action, channel string
	tenant, alert   string
	issuedAt        time.Time
	err             error
}

func (s *recordingSigner) ActionToken(action, tenantID, alertID, channelID string, issuedAt time.Time) (string, error) {
	s.calls++
	s.action, s.tenant, s.alert, s.channel, s.issuedAt = action, tenantID, alertID, channelID, issuedAt
	if s.err != nil {
		return "", s.err
	}
	return "tok", nil
}

func linkedOptions(s LinkSigner) Options {
	return Options{AppBaseURL: "https://probara.example.com/", Links: s, ChannelID: "c-1"}
}

func TestAckLinkOnPages(t *testing.T) {
	for _, eventType := range []string{EventCreated, EventReminder} {
		s := &recordingSigner{}
		ev := sample()
		m := BuildWith(ev, eventType, linkedOptions(s))
		if m.AckURL != "https://probara.example.com/alert-action#tok" || m.AckLabel != "Acknowledge" {
			t.Fatalf("%s: AckURL=%q AckLabel=%q", eventType, m.AckURL, m.AckLabel)
		}
		if s.action != ActionAcknowledge || s.tenant != ev.TenantID || s.alert != ev.Alert.ID || s.channel != "c-1" || !s.issuedAt.Equal(ev.Timestamp) {
			t.Fatalf("%s: signer asked for %+v", eventType, s)
		}
		// The deep link is unchanged beside it.
		if m.ActionURL == "" {
			t.Fatalf("%s: lost the deep link", eventType)
		}
	}
}

func TestNoAckLink(t *testing.T) {
	resolved := sample()
	resolved.Alert.Status = "resolved"
	acked := sample()
	acked.Alert.Status = "acknowledged"
	type tc struct {
		name  string
		event func() Message
	}
	s := &recordingSigner{}
	for _, c := range []tc{
		{"resolved", func() Message { return BuildWith(resolved, EventResolved, linkedOptions(s)) }},
		{"resolved status on a reminder", func() Message { return BuildWith(resolved, EventReminder, linkedOptions(s)) }},
		{"acknowledged event", func() Message { return BuildWith(sample(), EventAcknowledged, linkedOptions(s)) }},
		{"reminder of an acknowledged alert", func() Message { return BuildWith(acked, EventReminder, linkedOptions(s)) }},
		{"suspected cause", func() Message { return BuildWith(sample(), EventSuspectedCause, linkedOptions(s)) }},
		{"no signer", func() Message {
			return BuildWith(sample(), EventCreated, Options{AppBaseURL: "https://probara.example.com", ChannelID: "c-1"})
		}},
		{"no base URL", func() Message { return BuildWith(sample(), EventCreated, Options{Links: s, ChannelID: "c-1"}) }},
		{"no channel", func() Message {
			return BuildWith(sample(), EventCreated, Options{AppBaseURL: "https://probara.example.com", Links: s})
		}},
		{"plain Build", func() Message { return Build(sample(), EventCreated, "https://probara.example.com") }},
	} {
		if m := c.event(); m.AckURL != "" || m.AckLabel != "" {
			t.Errorf("%s: AckURL=%q, want none", c.name, m.AckURL)
		}
	}
	if s.calls != 0 {
		t.Fatalf("signer called %d times for messages that carry no link", s.calls)
	}

	// A signer that cannot mint (no key, a test alert's ID) means no link,
	// not a broken one.
	failing := &recordingSigner{err: errors.New("no key")}
	if m := BuildWith(sample(), EventCreated, linkedOptions(failing)); m.AckURL != "" {
		t.Fatalf("failing signer: AckURL=%q", m.AckURL)
	}
}

func TestShortCarriesBothLinks(t *testing.T) {
	m := BuildWith(sample(), EventCreated, linkedOptions(&recordingSigner{}))
	short := m.Short()
	if !strings.HasSuffix(short, "\n"+m.ActionURL+"\nAcknowledge: "+m.AckURL) {
		t.Fatalf("Short() = %q", short)
	}
}

// ShortWithin clips the text, never a link, and drops the deep link before
// the acknowledge link when even the links do not fit.
func TestShortWithin(t *testing.T) {
	ev := sample()
	ev.Alert.MonitorName = strings.Repeat("long name ", 100)
	m := BuildWith(ev, EventCreated, linkedOptions(&recordingSigner{}))
	ack := "Acknowledge: " + m.AckURL
	links := "\n" + m.ActionURL + "\n" + ack

	out := m.ShortWithin(200)
	if utf8.RuneCountInString(out) > 200 || !strings.HasSuffix(out, links) || !strings.Contains(out, "…") {
		t.Fatalf("ShortWithin(200) = %q", out)
	}
	tight := utf8.RuneCountInString(links) + 5
	out = m.ShortWithin(tight)
	if utf8.RuneCountInString(out) > tight || strings.Contains(out, m.ActionURL) || !strings.HasSuffix(out, "\n"+ack) {
		t.Fatalf("ShortWithin(%d) = %q, want the deep link dropped and the ack link kept", tight, out)
	}
	if got := m.ShortWithin(0); got != m.Short() {
		t.Fatalf("ShortWithin(0) must be unbounded")
	}
}

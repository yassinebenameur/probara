package pagesubscribers

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/maintenance"
)

// testKeys is the signing key set every test in the package shares; it lives
// outside the integration files so the unit-tagged tests compile without them.
type testKeys struct{}

var testKey = bytes.Repeat([]byte{9}, 32)

func (testKeys) CurrentKey() ([]byte, int, error) { return testKey, 1, nil }
func (testKeys) KeyForVersion(v int) ([]byte, error) {
	if v != 1 {
		return nil, errors.New("no such key")
	}
	return testKey, nil
}

// Hostile input in the address never reaches a header: only a bare
// dot-atom address with a dotted domain is accepted.
func TestNormalizeAddress(t *testing.T) {
	ok := map[string]string{
		"ops@example.com":                  "ops@example.com",
		"  Ops.Team+status@Example.co.uk ": "ops.team+status@example.co.uk",
		"o'brien@example.com":              "o'brien@example.com",
	}
	for in, want := range ok {
		got, err := NormalizeAddress(in)
		if err != nil || got != want {
			t.Errorf("NormalizeAddress(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"",
		"plain",
		"a@localhost",
		`"attacker@evil.example>"@corp.example`,
		"ops@example.com\r\nBcc: victim@example.com",
		"ops@example.com\nSubject: hi",
		"Ops <ops@example.com>",
		"ops@example.com, other@example.com",
		"ops@exa mple.com",
		"ops..team@example.com",
		".ops@example.com",
		"opś@example.com",
		"ops@ëxample.com",
		"ops@[127.0.0.1]",
		strings.Repeat("a", 250) + "@example.com",
	}
	for _, in := range bad {
		if got, err := NormalizeAddress(in); err == nil {
			t.Errorf("NormalizeAddress(%q) = %q, want an error", in, got)
		} else if !errors.As(err, new(Invalid)) {
			t.Errorf("NormalizeAddress(%q) error %v is not Invalid", in, err)
		}
	}
}

func TestNoticeMessage(t *testing.T) {
	n := notice{
		Kind: "incident_published", PageTitle: "Acme\r\nStatus", PageURL: "https://status.example.com/public/status/acme",
		Accent: "red;}body{x", Label: "Incident", Headline: "API <outage>", Body: "Line one\nLine two",
		Facts: []fact{{"Status", "Investigating"}, {"Affected", "API, Web"}},
	}
	msg, err := noticeMessage(n, "ops@example.com", "https://status.example.com/public/status/acme/subscription?action=unsubscribe#tok", "https://app.example.com/api/v1/public/status-page-subscriptions/unsubscribe?token=tok")
	if err != nil {
		t.Fatal(err)
	}
	if err := msg.Validate(); err != nil {
		t.Fatalf("message does not validate: %v", err)
	}
	if msg.Subject != "[Acme Status] Incident: API <outage>" {
		t.Fatalf("subject = %q", msg.Subject)
	}
	if msg.Headers["List-Unsubscribe"] != "<https://app.example.com/api/v1/public/status-page-subscriptions/unsubscribe?token=tok>" ||
		msg.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("headers = %v", msg.Headers)
	}
	for _, want := range []string{"Line one", "Affected: API, Web", "Unsubscribe: https://status.example.com/public/status/acme/subscription?action=unsubscribe#tok", "Current status: https://status.example.com"} {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, msg.Text)
		}
	}
	if strings.Contains(msg.HTML, "<outage>") || !strings.Contains(msg.HTML, "API &lt;outage&gt;") {
		t.Error("HTML part does not escape the headline")
	}
	if strings.Contains(msg.HTML, "red;}") || !strings.Contains(msg.HTML, defaultAccent) {
		t.Error("a non-hex accent reached the style")
	}
}

func TestConfirmMessage(t *testing.T) {
	msg, err := confirmMessage(confirmView{PageTitle: "Acme", Recipient: "ops@example.com", Confirm: "https://s.example.com/public/status/acme/subscription?action=confirm#tok", Accent: "#123456"})
	if err != nil {
		t.Fatal(err)
	}
	if err := msg.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(msg.Headers) != 0 || !strings.Contains(msg.Text, "#tok") || !strings.Contains(msg.HTML, "#123456") {
		t.Fatalf("confirmation = %+v", msg)
	}
}

func TestCSVCellDefusesFormulas(t *testing.T) {
	for in, want := range map[string]string{"=cmd@example.com": "'=cmd@example.com", "+a@example.com": "'+a@example.com", "-a@example.com": "'-a@example.com", "a@example.com": "a@example.com"} {
		if got := csvCell(in); got != want {
			t.Errorf("csvCell(%q) = %q", in, got)
		}
	}
}

func TestRepeatText(t *testing.T) {
	r := maintenance.Rule{Weekdays: []time.Weekday{time.Monday, time.Wednesday}, StartMinute: 120, Duration: 90 * time.Minute, Timezone: "Europe/Paris"}
	if got := repeatText(r); got != "Mon, Wed at 02:00 (Europe/Paris), 1 h 30 min each" {
		t.Fatalf("repeatText = %q", got)
	}
	r.Weekdays = []time.Weekday{0, 1, 2, 3, 4, 5, 6}
	r.Duration = 30 * time.Minute
	r.Until = "2027-01-31"
	if got := repeatText(r); got != "Every day at 02:00 (Europe/Paris), 30 min each, until 2027-01-31" {
		t.Fatalf("repeatText = %q", got)
	}
}

package present

import (
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications"
)

func tlsExpirySample(daysLeft float64, lastError string) notifications.AlertEvent {
	ev := sample()
	ev.Alert.Kind = notifications.KindTLSExpiry
	ev.Alert.MonitorName = "imap.example.com"
	ev.Alert.FailureCount = 0
	ev.Alert.LastError = &lastError
	ev.Alert.MetricValue = floatPtr(daysLeft)
	ev.Alert.ThresholdValue = floatPtr(14)
	return ev
}

// The summary names an intermediate that expires first; the server's own
// certificate keeps the existing wording.
func TestBuildTLSExpiryNamesTheCertificate(t *testing.T) {
	cases := []struct {
		name, lastError, event string
		days                   float64
		want                   string
	}{
		{"intermediate", "tls: intermediate certificate CN=Example Issuing CA 2,O=Example expires on 2026-12-01 (in 9d, < 14d)", EventCreated, 9,
			"An intermediate certificate (Example Issuing CA 2) expires in 9 days, inside the 14-day warning window."},
		{"intermediate reminder", "tls: intermediate certificate CN=Example Issuing CA 2,O=Example expires on 2026-12-01 (in 2d, < 14d)", EventReminder, 2,
			"An intermediate certificate (Example Issuing CA 2) expires in 2 days, inside the 14-day warning window."},
		{"expired intermediate", "tls: intermediate certificate CN=R3 expired on 2026-11-01 (2d ago, threshold 14d)", EventReminder, -2,
			"An intermediate certificate (R3) has expired."},
		{"intermediate without a CN", "tls: intermediate certificate O=Example expires (in 9d, < 14d)", EventCreated, 9,
			"An intermediate certificate (O=Example) expires in 9 days, inside the 14-day warning window."},
		{"leaf", "tls: certificate CN=imap.example.com expires on 2026-12-01 (in 9d, < 14d)", EventCreated, 9,
			"The TLS certificate expires in 9 days, inside the 14-day warning window."},
		{"alert from before certificates were named", "tls: expires in 9d (< 14d)", EventCreated, 9,
			"The TLS certificate expires in 9 days, inside the 14-day warning window."},
		{"resolved intermediate", "tls: intermediate certificate CN=R3 expires on 2026-12-01 (in 9d, < 14d)", EventResolved, 9,
			"The TLS certificate has been renewed."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := Build(tlsExpirySample(c.days, c.lastError), c.event, "")
			if m.Summary != c.want {
				t.Fatalf("Summary = %q, want %q", m.Summary, c.want)
			}
			if m.LastError != c.lastError {
				t.Fatalf("LastError = %q", m.LastError)
			}
		})
	}
}

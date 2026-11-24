package alerter

import (
	"testing"
	"time"
)

// Expiry wording: the leaf or an intermediate, with its date, or how long
// ago it expired.
func TestTLSExpiryMessage(t *testing.T) {
	at := time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		cert certExpiry
		want string
	}{
		{certExpiry{days: 5, notAfter: at, subject: "CN=svc", leaf: true}, "tls: certificate CN=svc expires on 2026-12-01 (in 5d, < 14d)"},
		{certExpiry{days: 5, notAfter: at, subject: "CN=R3", leaf: false}, "tls: intermediate certificate CN=R3 expires on 2026-12-01 (in 5d, < 14d)"},
		{certExpiry{days: -2, notAfter: at, subject: "CN=R3", leaf: false}, "tls: intermediate certificate CN=R3 expired on 2026-12-01 (2d ago, threshold 14d)"},
		{certExpiry{days: 3, leaf: true}, "tls: certificate expires (in 3d, < 14d)"},
	}
	for _, tc := range cases {
		if got := tlsExpiryMessage(tc.cert, 14); got != tc.want {
			t.Errorf("tlsExpiryMessage(%+v) = %q, want %q", tc.cert, got, tc.want)
		}
	}
}

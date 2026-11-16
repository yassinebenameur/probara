package notifications

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

type tlsExpiryFixture struct {
	Cases []struct {
		Text         string `json:"text"`
		OK           bool   `json:"ok"`
		Intermediate bool   `json:"intermediate"`
		Subject      string `json:"subject"`
		CommonName   string `json:"common_name"`
		NotAfter     string `json:"not_after"`
	} `json:"cases"`
}

// The fixture the web twin (tlsExpiryCertificate) also reads.
func TestParseTLSExpiryErrorFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/tls_expiry_errors.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx tlsExpiryFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	for _, c := range fx.Cases {
		cert, ok := ParseTLSExpiryError(c.Text)
		if ok != c.OK {
			t.Errorf("%q: ok = %v, want %v", c.Text, ok, c.OK)
			continue
		}
		if !ok {
			continue
		}
		notAfter := ""
		if !cert.NotAfter.IsZero() {
			notAfter = cert.NotAfter.Format("2006-01-02")
		}
		if cert.Intermediate != c.Intermediate || cert.Subject != c.Subject || notAfter != c.NotAfter || CommonName(cert.Subject) != c.CommonName {
			t.Errorf("%q: got %+v (cn %q), want %+v", c.Text, cert, CommonName(cert.Subject), c)
		}
	}
}

// What the alerter writes, the readers get back.
func TestFormatTLSExpiryErrorRoundTrip(t *testing.T) {
	at := time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		cert TLSExpiryCertificate
		days int
		want string
	}{
		{TLSExpiryCertificate{Subject: "CN=svc", NotAfter: at}, 5, "tls: certificate CN=svc expires on 2026-12-01 (in 5d, < 14d)"},
		{TLSExpiryCertificate{Subject: "CN=R3", Intermediate: true, NotAfter: at}, 5, "tls: intermediate certificate CN=R3 expires on 2026-12-01 (in 5d, < 14d)"},
		{TLSExpiryCertificate{Subject: "CN=R3", Intermediate: true, NotAfter: at}, -2, "tls: intermediate certificate CN=R3 expired on 2026-12-01 (2d ago, threshold 14d)"},
		{TLSExpiryCertificate{}, 3, "tls: certificate expires (in 3d, < 14d)"},
	}
	for _, c := range cases {
		got := FormatTLSExpiryError(c.cert, c.days, 14)
		if got != c.want {
			t.Errorf("FormatTLSExpiryError(%+v) = %q, want %q", c.cert, got, c.want)
		}
		back, ok := ParseTLSExpiryError(got)
		if !ok || back.Subject != c.cert.Subject || back.Intermediate != c.cert.Intermediate || !back.NotAfter.Equal(c.cert.NotAfter.Truncate(24*time.Hour)) {
			t.Errorf("round trip of %q = %+v, %v", got, back, ok)
		}
	}
}

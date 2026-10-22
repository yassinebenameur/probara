package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// pinFixtures are the metrics_data documents both readers must agree on
// (TestSQLExprMatchesExtract). want names the envelope Extract should pick:
// "" means no certificate.
var pinFixtures = []struct {
	name string
	raw  string
	want string
}{
	{name: "http only", raw: `{"http":{"final_url":"https://a.example","tls":{"not_after":"2027-01-01T00:00:00Z","days_until_expiry":30}}}`, want: "http"},
	{name: "sip only", raw: `{"sip":{"method":"OPTIONS","tls":{"not_after":"2027-02-01T00:00:00Z","days_until_expiry":60,"server_name":"sbc.example.com"}}}`, want: "sip"},
	{name: "both prefers http", raw: `{"http":{"tls":{"not_after":"2027-01-01T00:00:00Z","days_until_expiry":30}},"sip":{"tls":{"not_after":"2027-02-01T00:00:00Z","days_until_expiry":60}}}`, want: "http"},
	{name: "neither", raw: `{"sip":{"method":"OPTIONS","transport":"udp"}}`},
	{name: "http without tls falls through to sip", raw: `{"http":{"final_url":"http://a.example"},"sip":{"tls":{"days_until_expiry":5}}}`, want: "sip"},
	{name: "json null is absent", raw: `{"http":{"tls":null},"sip":{"tls":{"days_until_expiry":7}}}`, want: "sip"},
	{name: "string is absent", raw: `{"http":{"tls":"expired"}}`},
	{name: "array is absent", raw: `{"sip":{"tls":[1,2]}}`},
	{name: "envelope is a string", raw: `{"http":"tls"}`},
	{name: "envelope is an array", raw: `{"http":[{"tls":{}}],"sip":{"tls":{"days_until_expiry":1}}}`, want: "sip"},
	{name: "empty tls object counts", raw: `{"http":{"tls":{}}}`, want: "http"},
	{name: "top-level array", raw: `[{"http":{"tls":{}}}]`},
	{name: "top-level scalar", raw: `42`},
	{name: "top-level null", raw: `null`},
	{name: "empty object", raw: `{}`},
	{name: "unrelated envelope", raw: `{"tcp":{"tls":{"days_until_expiry":3}}}`},
}

func TestExtractFixtures(t *testing.T) {
	for _, tt := range pinFixtures {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractRaw(json.RawMessage(tt.raw))
			if tt.want == "" {
				if got != nil {
					t.Fatalf("ExtractRaw = %s, want nil", got)
				}
				if Extract(json.RawMessage(tt.raw)) != nil {
					t.Fatal("Extract != nil, want nil")
				}
				return
			}
			var doc map[string]json.RawMessage
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tt.raw), &doc); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			if err := json.Unmarshal(doc[tt.want], &envelope); err != nil {
				t.Fatalf("fixture %s envelope: %v", tt.want, err)
			}
			if !jsonEqual(t, got, envelope["tls"]) {
				t.Fatalf("ExtractRaw = %s, want %s.tls = %s", got, tt.want, envelope["tls"])
			}
		})
	}
}

func TestExtractDecodes(t *testing.T) {
	info := Extract(json.RawMessage(`{"sip":{"tls":{"not_after":"2027-02-01T00:00:00Z","days_until_expiry":60,"server_name":"sbc.example.com"}}}`))
	if info == nil || info.NotAfter != "2027-02-01T00:00:00Z" || info.DaysUntilExpiry == nil || *info.DaysUntilExpiry != 60 || info.ServerName != "sbc.example.com" {
		t.Fatalf("Extract = %+v", info)
	}
	for _, raw := range []string{"", "   ", "not json", `{"http":{"tls":{"days_until_expiry":"soon"}}}`} {
		if got := Extract(json.RawMessage(raw)); got != nil {
			t.Errorf("Extract(%q) = %+v, want nil", raw, got)
		}
	}
}

func TestSQLExprCoversEveryPath(t *testing.T) {
	expr := SQLExpr("cr.metrics_data")
	if !strings.HasPrefix(expr, "COALESCE(") {
		t.Fatalf("SQLExpr = %s", expr)
	}
	for _, path := range MetricsPaths {
		if !strings.Contains(expr, "cr.metrics_data #> '{"+strings.Join(path, ",")+"}'") {
			t.Errorf("SQLExpr misses path %v: %s", path, expr)
		}
	}
}

func TestFromConnectionState(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	cert := selfSignedCert(t, now.Add(-time.Hour), now.Add(10*24*time.Hour))

	if FromConnectionState(tls.ConnectionState{}, "sbc.example.com", now) != nil {
		t.Fatal("no peer certificate must give nil")
	}

	info := FromConnectionState(tls.ConnectionState{
		Version:          tls.VersionTLS13,
		CipherSuite:      tls.TLS_AES_128_GCM_SHA256,
		PeerCertificates: []*x509.Certificate{cert},
	}, "sbc.example.com", now)
	if info == nil {
		t.Fatal("FromConnectionState = nil")
	}
	if info.DaysUntilExpiry == nil || *info.DaysUntilExpiry != 10 {
		t.Fatalf("days_until_expiry = %v, want 10", info.DaysUntilExpiry)
	}
	if info.Version != "TLS 1.3" || info.CipherSuite != "TLS_AES_128_GCM_SHA256" || info.ServerName != "sbc.example.com" {
		t.Fatalf("session fields = %+v", info)
	}
	if info.NotAfter != now.Add(10*24*time.Hour).Format(time.RFC3339) || info.NotBefore != now.Add(-time.Hour).Format(time.RFC3339) {
		t.Fatalf("validity = %s .. %s", info.NotBefore, info.NotAfter)
	}
	if len(info.DNSNames) != 1 || info.DNSNames[0] != "sbc.example.com" || len(info.IPAddresses) != 1 || info.IPAddresses[0] != "192.0.2.10" {
		t.Fatalf("names = %v %v", info.DNSNames, info.IPAddresses)
	}
	if info.PeerCertificates != 1 || info.VerifiedChains != 0 {
		t.Fatalf("counts = %d peer, %d chains", info.PeerCertificates, info.VerifiedChains)
	}

	// Truncation toward zero: 9 days 23 hours left reports 9.
	info = FromConnectionState(tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}, "", now.Add(time.Hour))
	if *info.DaysUntilExpiry != 9 {
		t.Fatalf("days_until_expiry an hour later = %d, want 9", *info.DaysUntilExpiry)
	}

	// The JSON keys are the stored contract shared with http.tls.
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"not_after"`, `"not_before"`, `"days_until_expiry"`, `"subject"`, `"issuer"`, `"serial_number"`, `"dns_names"`, `"ip_addresses"`, `"peer_certificates"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("marshalled Info lacks %s: %s", key, raw)
		}
	}
}

func selfSignedCert(t *testing.T, notBefore, notAfter time.Time) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "sbc.example.com"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		DNSNames:     []string{"sbc.example.com"},
		IPAddresses:  []net.IP{net.ParseIP("192.0.2.10")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb interface{}
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("decode %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	ra, _ := json.Marshal(va)
	rb, _ := json.Marshal(vb)
	return string(ra) == string(rb)
}

package worker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/tlscert"
)

type tcpTestCert struct {
	cert *x509.Certificate
	der  []byte
	key  *ecdsa.PrivateKey
}

func issueTCPTestCert(t *testing.T, cn string, notAfter time.Time, parent *tcpTestCert, isCA bool) *tcpTestCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              notAfter,
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}
	if isCA {
		tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature
	} else {
		tmpl.DNSNames = []string{cn}
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	signer, signerKey := tmpl, key
	if parent != nil {
		signer, signerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &tcpTestCert{cert: cert, der: der, key: key}
}

// startTLSListener serves a TLS handshake presenting chain (leaf first) and
// closes each connection.
func startTLSListener(t *testing.T, chain ...*tcpTestCert) (string, int) {
	t.Helper()
	cert := tls.Certificate{PrivateKey: chain[0].key}
	for _, c := range chain {
		cert.Certificate = append(cert.Certificate, c.der)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = conn.(*tls.Conn).Handshake()
				conn.Close()
			}()
		}
	}()
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	return host, port
}

func errText(msg *string) string {
	if msg == nil {
		return ""
	}
	return *msg
}

// A TLS-wrapped TCP check verifies under tls_server_name, records the
// certificate where shared/tlscert finds it (tcp.tls) with the chain's
// earliest expiry, and a certificate inside tls_min_days_valid still passes
// (the alerter raises tls_expiry).
func TestTCPChecker_TLSRecordsCertificateAndChain(t *testing.T) {
	day := 24 * time.Hour
	root := issueTCPTestCert(t, "Test Root", time.Now().Add(3650*day), nil, true)
	inter := issueTCPTestCert(t, "Test Intermediate", time.Now().Add(12*day+time.Hour), root, true)
	leaf := issueTCPTestCert(t, "imap.example.test", time.Now().Add(200*day+time.Hour), inter, false)
	host, port := startTLSListener(t, leaf, inter)

	checker := NewTCPChecker(false, nil)
	checker.rootCAs = x509.NewCertPool()
	checker.rootCAs.AddCert(root.cert)
	res := checker.Check(context.Background(), tcpConfig(t, map[string]any{
		"host": host, "port": port, "use_tls": true,
		"tls_server_name": "imap.example.test", "tls_min_days_valid": 14,
	}), 5)
	if res.Status != "success" {
		t.Fatalf("status = %q (%s), want success", res.Status, errText(res.ErrorMessage))
	}
	info := tlscert.Extract(res.MetricsData)
	if info == nil {
		t.Fatalf("no certificate at a tlscert path: %s", res.MetricsData)
	}
	if info.ServerName != "imap.example.test" || info.DaysUntilExpiry == nil || *info.DaysUntilExpiry != 200 || info.VerifiedChains != 1 {
		t.Fatalf("leaf = server %q days %v chains %d, want imap.example.test / 200 / verified", info.ServerName, info.DaysUntilExpiry, info.VerifiedChains)
	}
	if _, days, subject := info.Expiry(); days == nil || *days != 12 || subject != "CN=Test Intermediate" {
		t.Fatalf("Expiry() = %v %q, want the intermediate's 12 days", days, subject)
	}
}

// Verification is on by default: the system store does not know the test
// root, and the wrong server name fails even with the right one.
func TestTCPChecker_TLSVerificationFailures(t *testing.T) {
	day := 24 * time.Hour
	root := issueTCPTestCert(t, "Test Root", time.Now().Add(3650*day), nil, true)
	leaf := issueTCPTestCert(t, "imap.example.test", time.Now().Add(200*day), root, false)
	host, port := startTLSListener(t, leaf)

	res := NewTCPChecker(false, nil).Check(context.Background(), tcpConfig(t, map[string]any{
		"host": host, "port": port, "use_tls": true, "tls_server_name": "imap.example.test",
	}), 5)
	if res.Status != "failure" {
		t.Fatalf("unknown root: status = %q (%s), want failure", res.Status, errText(res.ErrorMessage))
	}

	checker := NewTCPChecker(false, nil)
	checker.rootCAs = x509.NewCertPool()
	checker.rootCAs.AddCert(root.cert)
	res = checker.Check(context.Background(), tcpConfig(t, map[string]any{
		"host": host, "port": port, "use_tls": true, "tls_server_name": "other.example.test",
	}), 5)
	if res.Status != "failure" {
		t.Fatalf("name mismatch: status = %q (%s), want failure", res.Status, errText(res.ErrorMessage))
	}
}

func TestTCPChecker_TLSMinDaysValidRules(t *testing.T) {
	// Refused without use_tls: the threshold could never fire.
	res := NewTCPChecker(false, nil).Check(context.Background(), tcpConfig(t, map[string]any{
		"host": "127.0.0.1", "port": 9, "tls_min_days_valid": 14,
	}), 2)
	if res.Status != "error" {
		t.Fatalf("status = %q, want error for tls_min_days_valid without use_tls", res.Status)
	}

	// Under skip-verify a self-signed certificate is still recorded, and its
	// chain is the leaf alone.
	day := 24 * time.Hour
	self := issueTCPTestCert(t, "lab.example.test", time.Now().Add(5*day+time.Hour), nil, false)
	host, port := startTLSListener(t, self)
	res = NewTCPChecker(false, nil).Check(context.Background(), tcpConfig(t, map[string]any{
		"host": host, "port": port, "use_tls": true, "tls_skip_verify": true, "tls_min_days_valid": 14,
	}), 5)
	if res.Status != "success" {
		t.Fatalf("status = %q (%s), want success (expiry is the alerter's)", res.Status, errText(res.ErrorMessage))
	}
	var env tcpMetricsEnvelope
	if err := json.Unmarshal(res.MetricsData, &env); err != nil || env.TCP == nil || env.TCP.TLS == nil ||
		*env.TCP.TLS.DaysUntilExpiry != 5 || *env.TCP.TLS.ChainMinDays != 5 || env.TCP.TLS.ServerName != host {
		t.Fatalf("metrics = %s (%v)", res.MetricsData, err)
	}
}

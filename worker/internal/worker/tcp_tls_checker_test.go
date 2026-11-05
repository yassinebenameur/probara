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
	"encoding/pem"
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

// startTLSListener serves a TLS handshake with leaf+intermediate and closes.
func startTLSListener(t *testing.T, leaf, inter *tcpTestCert) (string, int) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{
		Certificate: [][]byte{leaf.der, inter.der},
		PrivateKey:  leaf.key,
	}}})
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

// A TLS-wrapped TCP check verifies against tls_ca_pem under tls_server_name,
// records the certificate where shared/tlscert finds it (tcp.tls), and
// captures the chain's earliest expiry; a certificate inside
// tls_min_days_valid still passes (the alerter raises tls_expiry).
func TestTCPChecker_TLSRecordsCertificateAndChain(t *testing.T) {
	day := 24 * time.Hour
	root := issueTCPTestCert(t, "Test Root", time.Now().Add(3650*day), nil, true)
	inter := issueTCPTestCert(t, "Test Intermediate", time.Now().Add(12*day+time.Hour), root, true)
	leaf := issueTCPTestCert(t, "smtp.example.test", time.Now().Add(200*day+time.Hour), inter, false)
	host, port := startTLSListener(t, leaf, inter)
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.der}))

	res := NewTCPChecker(false, nil).Check(context.Background(), tcpConfig(t, map[string]any{
		"host": host, "port": port, "use_tls": true,
		"tls_server_name": "smtp.example.test", "tls_ca_pem": caPEM,
		"tls_min_days_valid": 14, "tls_check_chain": true,
	}), 5)
	if res.Status != "success" {
		t.Fatalf("status = %q (%s), want success", res.Status, derefErr(res.ErrorMessage))
	}
	info := tlscert.Extract(res.MetricsData)
	if info == nil {
		t.Fatalf("no certificate at a tlscert path: %s", res.MetricsData)
	}
	if info.ServerName != "smtp.example.test" || info.DaysUntilExpiry == nil || *info.DaysUntilExpiry != 200 {
		t.Fatalf("leaf = server %q days %v, want smtp.example.test / 200", info.ServerName, info.DaysUntilExpiry)
	}
	if info.ChainMinDays == nil || *info.ChainMinDays != 12 || len(info.Chain) != 2 {
		t.Fatalf("chain = days %v, %d certs; want 12 days over leaf+intermediate", info.ChainMinDays, len(info.Chain))
	}
	if _, days := info.Expiry(true); *days != 12 {
		t.Fatalf("Expiry(chain) days = %d, want 12", *days)
	}
}

func TestTCPChecker_TLSServerNameMismatchFails(t *testing.T) {
	day := 24 * time.Hour
	root := issueTCPTestCert(t, "Test Root", time.Now().Add(3650*day), nil, true)
	inter := issueTCPTestCert(t, "Test Intermediate", time.Now().Add(365*day), root, true)
	leaf := issueTCPTestCert(t, "smtp.example.test", time.Now().Add(200*day), inter, false)
	host, port := startTLSListener(t, leaf, inter)
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.der}))

	res := NewTCPChecker(false, nil).Check(context.Background(), tcpConfig(t, map[string]any{
		"host": host, "port": port, "use_tls": true, "tls_server_name": "other.example.test", "tls_ca_pem": caPEM,
	}), 5)
	if res.Status != "failure" {
		t.Fatalf("status = %q (%s), want failure for a name mismatch", res.Status, derefErr(res.ErrorMessage))
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

	// Under skip-verify a self-signed certificate is still recorded.
	day := 24 * time.Hour
	self := issueTCPTestCert(t, "lab.example.test", time.Now().Add(5*day+time.Hour), nil, false)
	host, port := startTLSListener(t, self, self)
	res = NewTCPChecker(false, nil).Check(context.Background(), tcpConfig(t, map[string]any{
		"host": host, "port": port, "use_tls": true, "tls_skip_verify": true, "tls_min_days_valid": 14,
	}), 5)
	if res.Status != "success" {
		t.Fatalf("status = %q (%s), want success (expiry is the alerter's)", res.Status, derefErr(res.ErrorMessage))
	}
	var env tcpMetricsEnvelope
	if err := json.Unmarshal(res.MetricsData, &env); err != nil || env.TCP == nil || env.TCP.TLS == nil || *env.TCP.TLS.DaysUntilExpiry != 5 {
		t.Fatalf("metrics = %s (%v)", res.MetricsData, err)
	}
}

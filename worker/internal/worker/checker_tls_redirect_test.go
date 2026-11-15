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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testCA issues leaf certificates for TLS tests that need distinct names
// trusted through tls_ca_pem.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  string
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate (CA): %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate (CA): %v", err)
	}
	return &testCA{cert: cert, key: key, pem: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
}

// leaf issues a server certificate valid only for the given DNS names and
// IP addresses.
func (ca *testCA) leaf(t *testing.T, serial int64, dnsNames []string, ips []net.IP) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("CreateCertificate (leaf): %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func startTLSServerWithCert(t *testing.T, cert tls.Certificate, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func serverPort(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	return port
}

func httpTLSServerName(t *testing.T, result CheckResult) string {
	t.Helper()
	var env struct {
		HTTP struct {
			TLS *struct {
				ServerName string `json:"server_name"`
			} `json:"tls"`
		} `json:"http"`
	}
	if err := json.Unmarshal(result.MetricsData, &env); err != nil || env.HTTP.TLS == nil {
		t.Fatalf("metrics_data %s: want http.tls (err %v)", result.MetricsData, err)
	}
	return env.HTTP.TLS.ServerName
}

// A redirect from one HTTPS host to another is verified against the second
// host's own name, not the first URL's. Two names that both reach loopback
// without DNS stand in for the two hosts: "localhost" and the IP literal
// 127.0.0.1, each with a certificate valid for that name only.
func TestHTTPChecker_CrossHostHTTPSRedirectVerifiesEachHop(t *testing.T) {
	ca := newTestCA(t)
	loopbackIP := []net.IP{net.ParseIP("127.0.0.1")}

	target := startTLSServerWithCert(t, ca.leaf(t, 2, nil, loopbackIP), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	targetURL := "https://127.0.0.1:" + serverPort(t, target) + "/landing"

	impostor := startTLSServerWithCert(t, ca.leaf(t, 3, []string{"other.example"}, nil), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	impostorURL := "https://127.0.0.1:" + serverPort(t, impostor) + "/landing"

	redirector := func(to string) string {
		srv := startTLSServerWithCert(t, ca.leaf(t, 4, []string{"localhost"}, nil), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, to, http.StatusFound)
		}))
		return "https://localhost:" + serverPort(t, srv) + "/start"
	}

	checker := NewHTTPChecker(1024, false, nil)
	run := func(t *testing.T, cfg map[string]any) CheckResult {
		t.Helper()
		cfg["method"] = "GET"
		cfg["tls_ca_pem"] = ca.pem
		raw, _ := json.Marshal(cfg)
		return checker.Check(context.Background(), raw, 5)
	}

	t.Run("redirect to a host with its own valid certificate succeeds", func(t *testing.T) {
		result := run(t, map[string]any{"url": redirector(targetURL)})
		if result.Status != "success" {
			t.Fatalf("status = %q (%s), want success", result.Status, derefErr(result.ErrorMessage))
		}
		if got := httpTLSServerName(t, result); got != "127.0.0.1" {
			t.Errorf("recorded server_name = %q, want the final hop's host 127.0.0.1", got)
		}
	})

	t.Run("through the dial guard, which dials the resolved IP", func(t *testing.T) {
		// The guard resolves localhost and dials an IP; the transport still
		// verifies each hop against its URL host.
		_, v4, _ := net.ParseCIDR("127.0.0.0/8")
		_, v6, _ := net.ParseCIDR("::1/128")
		guarded := NewHTTPChecker(1024, true, []*net.IPNet{v4, v6})
		raw, _ := json.Marshal(map[string]any{"url": redirector(targetURL), "method": "GET", "tls_ca_pem": ca.pem})
		result := guarded.Check(context.Background(), raw, 5)
		if result.Status != "success" {
			t.Fatalf("status = %q (%s), want success", result.Status, derefErr(result.ErrorMessage))
		}
	})

	t.Run("redirect to a host whose certificate names someone else fails", func(t *testing.T) {
		result := run(t, map[string]any{"url": redirector(impostorURL)})
		if result.Status == "success" {
			t.Fatal("status = success, want a verification failure for a certificate valid for other.example")
		}
		if msg := derefErr(result.ErrorMessage); !strings.Contains(msg, "certificate") {
			t.Errorf("error = %q, want a certificate verification error", msg)
		}
	})

	t.Run("explicit tls_server_name still pins every connection", func(t *testing.T) {
		// The redirector's certificate is valid for localhost only; reached
		// by IP it verifies only because the pin says which name to check.
		localhostOnly := redirector(targetURL)
		byIP := strings.Replace(localhostOnly, "https://localhost:", "https://127.0.0.1:", 1)
		cfg := map[string]any{"url": byIP, "follow_redirects": false, "expected_status": 302}

		unpinned := run(t, cfg)
		if unpinned.Status == "success" {
			t.Fatal("unpinned: status = success, want a verification failure (certificate is for localhost)")
		}

		cfg["tls_server_name"] = "localhost"
		pinned := run(t, cfg)
		if pinned.Status != "success" {
			t.Fatalf("pinned: status = %q (%s), want success", pinned.Status, derefErr(pinned.ErrorMessage))
		}
		if got := httpTLSServerName(t, pinned); got != "localhost" {
			t.Errorf("recorded server_name = %q, want the pinned localhost", got)
		}

		// The pin applies to the redirect hop too: the target's certificate
		// does not name localhost, so following the redirect fails.
		cfg["follow_redirects"] = true
		delete(cfg, "expected_status")
		pinnedFollow := run(t, cfg)
		if pinnedFollow.Status == "success" {
			t.Fatal("pinned redirect: status = success, want the pinned name to fail on the 127.0.0.1-only hop")
		}
	})
}

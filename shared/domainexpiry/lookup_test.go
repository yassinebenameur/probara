package domainexpiry

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/netguard"
)

// The lookup tests run against local fakes only: an RDAP server over TLS and
// WHOIS servers on loopback, reached through a dialer that maps the test
// hostnames onto them and then dials through a real netguard.Dialer.

// testCert is a self-signed certificate (acting as its own root) for the
// test RDAP hostnames.
func testCert(t *testing.T, names ...string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: names[0]},
		DNSNames:              names,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

const (
	rdapHost      = "rdap.registry.test"
	registrarRDAP = "rdap.registrar.test"
	whoisRoot     = "whois.root.test"
	whoisRegistry = "whois.registry.test"
	whoisReg      = "whois.registrar.test"
	whoisThird    = "whois.third.test"
)

type fakeNet struct {
	t       *testing.T
	pool    *x509.CertPool
	mu      sync.Mutex
	routes  map[string]string // "host:port" → "127.0.0.1:port"
	dialed  []string
	queries map[string][]string // WHOIS host → queries received
}

func newFakeNet(t *testing.T) *fakeNet {
	return &fakeNet{t: t, routes: map[string]string{}, queries: map[string][]string{}}
}

// rdap starts a TLS RDAP server answering for rdapHost and registrarRDAP.
func (n *fakeNet) rdap(handler http.HandlerFunc) {
	cert, pool := testCert(n.t, rdapHost, registrarRDAP)
	n.pool = pool
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	n.t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	n.routes[rdapHost+":443"] = addr
	n.routes[registrarRDAP+":443"] = addr
}

// whois starts a WHOIS server for host answering through respond.
func (n *fakeNet) whois(host string, respond func(query string) string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		n.t.Fatal(err)
	}
	n.t.Cleanup(func() { _ = ln.Close() })
	n.routes[host+":43"] = ln.Addr().String()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, _ := bufio.NewReader(conn).ReadString('\n')
				query := strings.TrimSpace(line)
				n.mu.Lock()
				n.queries[host] = append(n.queries[host], query)
				n.mu.Unlock()
				_, _ = conn.Write([]byte(respond(query)))
			}()
		}
	}()
}

func (n *fakeNet) queriesTo(host string) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.queries[host]...)
}

// client returns a Client whose connections go through a netguard.Dialer
// blocking private addresses; allowLoopback admits the fakes the way an
// operator's HTTP_ALLOWED_CIDRS would.
func (n *fakeNet) client(dir *Directory, allowLoopback bool) *Client {
	policy := netguard.Policy{BlockPrivate: true}
	if allowLoopback {
		_, loopback, _ := net.ParseCIDR("127.0.0.0/8")
		policy.AllowedCIDRs = []*net.IPNet{loopback}
	}
	guard := &netguard.Dialer{Policy: policy, Timeout: 5 * time.Second}
	return &Client{
		Directory: dir,
		RootCAs:   n.pool,
		WHOISRoot: whoisRoot,
		Now:       func() time.Time { return parseNow },
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			n.mu.Lock()
			n.dialed = append(n.dialed, address)
			target, ok := n.routes[address]
			n.mu.Unlock()
			if !ok {
				// Anything else is a literal the guard judges, or
				// unreachable.
				host, _, _ := net.SplitHostPort(address)
				if net.ParseIP(host) == nil {
					return nil, errors.New("no route to " + address)
				}
				target = address
			}
			return guard.DialContext(ctx, network, target)
		},
	}
}

func (n *fakeNet) dialedTo(address string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, d := range n.dialed {
		if d == address {
			return true
		}
	}
	return false
}

func lookupCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func rdapDir() *Directory {
	return newDirectoryWith(map[string][]string{"com": {"https://" + rdapHost + "/com/"}})
}

func TestLookupRDAP(t *testing.T) {
	n := newFakeNet(t)
	body := fixture(t, "rdap-example-com.json")
	n.rdap(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/com/domain/example.com" || !strings.Contains(r.Header.Get("Accept"), "application/rdap+json") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/rdap+json")
		_, _ = w.Write(body)
	})
	info := n.client(rdapDir(), true).Lookup(lookupCtx(t), "example.com")
	if info.Source != SourceRDAP || info.ExpiresAt != "2027-08-13T04:00:00Z" || info.UnknownReason != "" {
		t.Fatalf("info = %+v", info)
	}
	if info.Server != "https://"+rdapHost || info.Registrar != "RESERVED-Internet Assigned Numbers Authority" {
		t.Fatalf("server %q registrar %q", info.Server, info.Registrar)
	}
}

func TestLookupRDAPNotFoundIsFinal(t *testing.T) {
	n := newFakeNet(t)
	n.rdap(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	n.whois(whoisRoot, func(string) string { return "whois: " + whoisRegistry + "\n" })
	info := n.client(rdapDir(), true).Lookup(lookupCtx(t), "example.com")
	if !info.IsNotRegistered() || info.Source != SourceRDAP || info.UnknownReason != "" {
		t.Fatalf("info = %+v", info)
	}
	if q := n.queriesTo(whoisRoot); len(q) != 0 {
		t.Fatalf("WHOIS asked after an RDAP 404: %v", q)
	}
}

func TestLookupRDAPRedirects(t *testing.T) {
	body := fixture(t, "rdap-example-com.json")
	tests := []struct {
		name     string
		location string
		follow   bool
	}{
		{name: "registrar hostname", location: "https://" + registrarRDAP + "/domain/example.com", follow: true},
		{name: "loopback literal", location: "https://127.0.0.1/domain/example.com"},
		{name: "metadata literal", location: "https://169.254.169.254/latest"},
		{name: "plain http", location: "http://" + registrarRDAP + "/domain/example.com"},
		{name: "other port", location: "https://" + registrarRDAP + ":8443/domain/example.com"},
		{name: "credentials", location: "https://user:pw@" + registrarRDAP + "/domain/example.com"},
		{name: "endless", location: "https://" + rdapHost + "/com/domain/example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := newFakeNet(t)
			n.rdap(func(w http.ResponseWriter, r *http.Request) {
				if r.Host == registrarRDAP && tt.follow {
					_, _ = w.Write(body)
					return
				}
				http.Redirect(w, r, tt.location, http.StatusFound)
			})
			info := n.client(rdapDir(), true).Lookup(lookupCtx(t), "example.com")
			if tt.follow {
				if info.ExpiresAt == "" || info.Server != "https://"+registrarRDAP {
					t.Fatalf("info = %+v", info)
				}
				return
			}
			if info.ExpiresAt != "" || !strings.Contains(info.UnknownReason, "redirect refused") {
				t.Fatalf("info = %+v", info)
			}
			if n.dialedTo("127.0.0.1:443") || n.dialedTo("169.254.169.254:443") {
				t.Fatal("dialed a redirect target that should have been refused")
			}
		})
	}
}

func TestLookupRDAPResponseLimit(t *testing.T) {
	n := newFakeNet(t)
	n.rdap(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"objectClassName":"domain","pad":"` + strings.Repeat("x", maxRDAPBytes) + `"}`))
	})
	info := n.client(rdapDir(), true).Lookup(lookupCtx(t), "example.com")
	if !strings.Contains(info.UnknownReason, "response exceeds 1 MiB") {
		t.Fatalf("info = %+v", info)
	}
}

func TestLookupRDAPTimeoutLeavesWHOISTime(t *testing.T) {
	n := newFakeNet(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	n.rdap(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	n.whois(whoisRoot, func(string) string { return "whois: " + whoisRegistry + "\n" })
	n.whois(whoisRegistry, func(string) string { return string(fixture(t, "whois-gtld-example-com.txt")) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	info := n.client(rdapDir(), true).Lookup(ctx, "example.com")
	if info.Source != SourceWHOIS || info.ExpiresAt == "" || info.RDAPError != "timed out" {
		t.Fatalf("info = %+v", info)
	}
}

func TestLookupWHOISFallbackAndCache(t *testing.T) {
	n := newFakeNet(t)
	n.whois(whoisRoot, func(q string) string {
		return "% IANA WHOIS server\ndomain: " + strings.ToUpper(q) + "\nwhois: " + whoisRegistry + "\n"
	})
	n.whois(whoisRegistry, func(string) string { return string(fixture(t, "whois-se.txt")) })
	client := n.client(newDirectoryWith(nil), true)

	for i := 0; i < 2; i++ {
		info := client.Lookup(lookupCtx(t), "example.se")
		if info.Source != SourceWHOIS || info.ExpiresAt != "2031-03-09T00:00:00Z" || info.Server != whoisRegistry {
			t.Fatalf("lookup %d: info = %+v", i, info)
		}
		if info.RDAPError != errNoRDAP.Error() || info.UnknownReason != "" {
			t.Fatalf("lookup %d: rdap error %q, unknown %q", i, info.RDAPError, info.UnknownReason)
		}
	}
	if q := n.queriesTo(whoisRoot); len(q) != 1 || q[0] != "se" {
		t.Fatalf("root queries = %v, want one query for the TLD", q)
	}
	if q := n.queriesTo(whoisRegistry); len(q) != 2 || q[0] != "example.se" {
		t.Fatalf("registry queries = %v", q)
	}
}

func TestLookupWHOISRegistrarReferralIsBounded(t *testing.T) {
	n := newFakeNet(t)
	n.whois(whoisRoot, func(string) string { return "whois: " + whoisRegistry + "\n" })
	n.whois(whoisRegistry, func(string) string {
		return "Domain Name: EXAMPLE.SE\nRegistrar: Thin Registrar\nRegistrar WHOIS Server: " + whoisReg + "\n"
	})
	n.whois(whoisReg, func(string) string {
		return "Domain Name: EXAMPLE.SE\nRegistrar WHOIS Server: " + whoisThird + "\nCreation Date: 2001-01-01\n"
	})
	n.whois(whoisThird, func(string) string {
		return "Domain Name: EXAMPLE.SE\nRegistrar Registration Expiration Date: 2030-01-01T00:00:00Z\n"
	})
	info := n.client(newDirectoryWith(nil), true).Lookup(lookupCtx(t), "example.se")
	if len(n.queriesTo(whoisReg)) != 1 {
		t.Fatalf("registrar not asked: %v", n.queriesTo(whoisReg))
	}
	if q := n.queriesTo(whoisThird); len(q) != 0 {
		t.Fatalf("followed a second referral: %v", q)
	}
	if info.ExpiresAt != "" || info.Registrar != "Thin Registrar" || info.UnknownReason != "the registry publishes no expiration date" {
		t.Fatalf("info = %+v", info)
	}
}

func TestLookupWHOISRegistrarReferralSuppliesExpiry(t *testing.T) {
	n := newFakeNet(t)
	n.whois(whoisRoot, func(string) string { return "whois: " + whoisRegistry + "\n" })
	n.whois(whoisRegistry, func(string) string {
		return "Domain Name: EXAMPLE.SE\nRegistrar: Thin Registrar\nRegistrar WHOIS Server: " + whoisReg + "\n"
	})
	n.whois(whoisReg, func(string) string {
		return "Domain Name: EXAMPLE.SE\nRegistrar Registration Expiration Date: 2030-01-01T00:00:00Z\n"
	})
	info := n.client(newDirectoryWith(nil), true).Lookup(lookupCtx(t), "example.se")
	if info.ExpiresAt != "2030-01-01T00:00:00Z" || info.Server != whoisReg || info.Registrar != "Thin Registrar" {
		t.Fatalf("info = %+v", info)
	}
}

func TestLookupRDAPWithoutExpiryAsksWHOIS(t *testing.T) {
	n := newFakeNet(t)
	noExpiry := fixture(t, "rdap-no-expiry.json")
	n.rdap(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(noExpiry) })
	n.whois(whoisRoot, func(string) string { return "whois: " + whoisRegistry + "\n" })

	// WHOIS has the date: it wins, keeping RDAP's registrar.
	n.whois(whoisRegistry, func(string) string {
		return "Domain Name: example.com\nRegistry Expiry Date: 2028-05-01T00:00:00Z\n"
	})
	info := n.client(rdapDir(), true).Lookup(lookupCtx(t), "example.com")
	if info.Source != SourceWHOIS || info.ExpiresAt != "2028-05-01T00:00:00Z" || info.Registrar != "Nominet UK" {
		t.Fatalf("info = %+v", info)
	}
}

func TestLookupNoExpiryAnywhereIsUnknown(t *testing.T) {
	n := newFakeNet(t)
	noExpiry := fixture(t, "rdap-no-expiry.json")
	n.rdap(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(noExpiry) })
	n.whois(whoisRoot, func(string) string { return "whois: " + whoisRegistry + "\n" })
	n.whois(whoisRegistry, func(string) string { return string(fixture(t, "whois-uk-no-expiry.txt")) })
	info := n.client(rdapDir(), true).Lookup(lookupCtx(t), "example.com")
	if info.Source != SourceRDAP || !info.IsRegistered() || info.ExpiresAt != "" ||
		info.UnknownReason != "the registry publishes no expiration date" {
		t.Fatalf("info = %+v", info)
	}
}

func TestLookupUnsupportedTLD(t *testing.T) {
	n := newFakeNet(t)
	n.whois(whoisRoot, func(q string) string { return "% IANA WHOIS server\ndomain: " + q + "\nstatus: ACTIVE\n" })
	client := n.client(newDirectoryWith(nil), true)
	for i := 0; i < 2; i++ {
		info := client.Lookup(lookupCtx(t), "example.zz")
		if info.Registered != nil || info.Source != "" || info.UnknownReason != "no RDAP or WHOIS service is known for .zz" {
			t.Fatalf("lookup %d: info = %+v", i, info)
		}
	}
	if q := n.queriesTo(whoisRoot); len(q) != 1 {
		t.Fatalf("root asked %d times; the empty answer must be cached", len(q))
	}
}

func TestLookupRefusesPrivateDestinations(t *testing.T) {
	// The same fakes, but the policy does not admit loopback: every
	// connection is refused by the guard, and nothing is reached.
	n := newFakeNet(t)
	n.rdap(func(w http.ResponseWriter, r *http.Request) { t.Error("RDAP server reached") })
	n.whois(whoisRoot, func(string) string { t.Error("WHOIS server reached"); return "" })
	info := n.client(rdapDir(), false).Lookup(lookupCtx(t), "example.com")
	if info.Registered != nil || info.ExpiresAt != "" ||
		info.UnknownReason != "rdap: blocked by the worker network policy; whois: blocked by the worker network policy" {
		t.Fatalf("info = %+v", info)
	}
}

func TestLookupIgnoresLiteralWHOISReferral(t *testing.T) {
	n := newFakeNet(t)
	n.whois(whoisRoot, func(string) string { return "whois: 127.0.0.1\nrefer: 10.0.0.5\n" })
	info := n.client(newDirectoryWith(nil), true).Lookup(lookupCtx(t), "example.zz")
	if info.UnknownReason != "no RDAP or WHOIS service is known for .zz" {
		t.Fatalf("info = %+v", info)
	}
	for _, addr := range []string{"127.0.0.1:43", "10.0.0.5:43"} {
		if n.dialedTo(addr) {
			t.Fatalf("followed a literal referral to %s", addr)
		}
	}
}

func TestLookupErrorsNameNoAddress(t *testing.T) {
	n := newFakeNet(t)
	n.rdap(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
	info := n.client(rdapDir(), true).Lookup(lookupCtx(t), "example.com")
	if !strings.HasPrefix(info.UnknownReason, "rdap: rate limited by the registry (HTTP 429); whois: ") {
		t.Fatalf("reason = %q", info.UnknownReason)
	}
	if strings.Contains(info.UnknownReason, "127.0.0.1") || strings.Contains(info.UnknownReason, fmt.Sprint(n.routes[rdapHost+":443"])) {
		t.Fatalf("reason leaks an address: %q", info.UnknownReason)
	}
}

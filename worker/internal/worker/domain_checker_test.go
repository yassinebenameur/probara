package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/domainexpiry"
)

var domainTestNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

const rdapExampleCom = `{
  "objectClassName": "domain",
  "ldhName": "EXAMPLE.COM",
  "status": ["client delete prohibited", "client transfer prohibited"],
  "events": [
    {"eventAction": "registration", "eventDate": "1995-08-14T04:00:00Z"},
    {"eventAction": "expiration", "eventDate": "2027-08-13T04:00:00Z"},
    {"eventAction": "last changed", "eventDate": "2026-08-14T07:01:44Z"}
  ],
  "entities": [
    {"objectClassName": "entity", "roles": ["registrar"],
     "vcardArray": ["vcard", [["version", {}, "text", "4.0"], ["fn", {}, "text", "RESERVED-Internet Assigned Numbers Authority"]]]}
  ],
  "nameservers": [{"objectClassName": "nameserver", "ldhName": "A.IANA-SERVERS.NET"}]
}`

// rdapServer serves a bootstrap registry and one RDAP base under the same
// TLS listener. domains maps a domain to its response body (404 when absent).
func rdapServer(t *testing.T, tlds []string, domains map[string]string, status int) (*httptest.Server, *int32) {
	t.Helper()
	var bootstrapHits int32
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/dns.json":
			atomic.AddInt32(&bootstrapHits, 1)
			quoted := make([]string, len(tlds))
			for i, tld := range tlds {
				quoted[i] = fmt.Sprintf("%q", tld)
			}
			fmt.Fprintf(w, `{"version":"1.0","services":[[[%s],["%s/rdap/"]]]}`, strings.Join(quoted, ","), srv.URL)
		case strings.HasPrefix(r.URL.Path, "/rdap/domain/"):
			if status != 0 {
				w.WriteHeader(status)
				return
			}
			body, ok := domains[strings.TrimPrefix(r.URL.Path, "/rdap/domain/")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/rdap+json")
			_, _ = w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &bootstrapHits
}

// whoisServer answers WHOIS queries from responses keyed by the query
// line; every host the checker dials maps to it.
func whoisServer(t *testing.T, responses map[string]string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				line, _ := bufio.NewReader(conn).ReadString('\n')
				_, _ = conn.Write([]byte(responses[strings.TrimSpace(line)]))
			}(conn)
		}
	}()
	return ln.Addr().String()
}

func testDomainChecker(srv *httptest.Server, whoisAddr string) *DomainChecker {
	c := NewDomainChecker(false, nil)
	c.now = func() time.Time { return domainTestNow }
	if srv != nil {
		c.bootstrap = &rdapBootstrap{url: srv.URL + "/dns.json"}
		pool := srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
		c.rootCAs = pool
	} else {
		c.bootstrap = &rdapBootstrap{url: "https://127.0.0.1:1/dns.json"}
	}
	if whoisAddr != "" {
		c.whoisAddr = func(string) string { return whoisAddr }
	} else {
		c.whoisAddr = func(string) string { return "127.0.0.1:1" }
	}
	return c
}

func checkDomain(t *testing.T, c *DomainChecker, config string) (CheckResult, *domainexpiry.Info) {
	t.Helper()
	res := c.Check(context.Background(), json.RawMessage(config), 5)
	return res, domainexpiry.Extract(res.MetricsData)
}

func TestDomainCheckerRDAP(t *testing.T) {
	srv, hits := rdapServer(t, []string{"com"}, map[string]string{"example.com": rdapExampleCom}, 0)
	c := testDomainChecker(srv, "")

	res, info := checkDomain(t, c, `{"domain":"Example.COM.","min_days_valid":30}`)
	if res.Status != "success" || res.ErrorMessage != nil {
		t.Fatalf("status = %s (%v), want success", res.Status, res.ErrorMessage)
	}
	if info == nil {
		t.Fatalf("no domain metrics: %s", res.MetricsData)
	}
	if info.Domain != "example.com" || info.Source != "rdap" || !info.Registered {
		t.Fatalf("info = %+v", info)
	}
	if info.ExpiresAt != "2027-08-13T04:00:00Z" || info.DaysUntilExpiry == nil || *info.DaysUntilExpiry != 280 {
		t.Fatalf("expiry = %q %v", info.ExpiresAt, info.DaysUntilExpiry)
	}
	if info.Registrar != "RESERVED-Internet Assigned Numbers Authority" {
		t.Fatalf("registrar = %q", info.Registrar)
	}
	if info.RegisteredAt != "1995-08-14T04:00:00Z" || info.UpdatedAt != "2026-08-14T07:01:44Z" {
		t.Fatalf("dates = %q %q", info.RegisteredAt, info.UpdatedAt)
	}
	if len(info.Statuses) != 2 || len(info.Nameservers) != 1 || info.Nameservers[0] != "a.iana-servers.net" {
		t.Fatalf("statuses/nameservers = %v %v", info.Statuses, info.Nameservers)
	}
	if res.LatencyMs == nil {
		t.Fatal("latency missing")
	}

	// The bootstrap registry is cached in process.
	_, _ = checkDomain(t, c, `{"domain":"example.com"}`)
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("bootstrap fetched %d times, want 1", got)
	}
}

func TestDomainCheckerVerdicts(t *testing.T) {
	expired := strings.Replace(rdapExampleCom, "2027-08-13T04:00:00Z", "2026-11-02T04:00:00Z", 1)
	redemption := strings.Replace(rdapExampleCom, `"client delete prohibited"`, `"redemption period"`, 1)
	srv, _ := rdapServer(t, []string{"com"}, map[string]string{
		"expired.com":    expired,
		"redemption.com": redemption,
	}, 0)
	c := testDomainChecker(srv, "")

	tests := []struct {
		domain, status, message string
	}{
		{"expired.com", "failure", "domain: expired 3 days ago (2026-11-02)"},
		{"redemption.com", "failure", "domain: expired and redemption period"},
		{"free.com", "failure", "domain: free.com is not registered (rdap: not found)"},
	}
	for _, tt := range tests {
		t.Run(tt.domain, func(t *testing.T) {
			res, info := checkDomain(t, c, fmt.Sprintf(`{"domain":%q,"whois_fallback":false}`, tt.domain))
			if res.Status != tt.status || res.ErrorMessage == nil || *res.ErrorMessage != tt.message {
				t.Fatalf("got %s %v, want %s %q", res.Status, res.ErrorMessage, tt.status, tt.message)
			}
			if info == nil {
				t.Fatal("a registry answer is always recorded")
			}
		})
	}
}

func TestDomainCheckerRDAPErrorWithoutFallback(t *testing.T) {
	srv, _ := rdapServer(t, []string{"com"}, nil, http.StatusTooManyRequests)
	c := testDomainChecker(srv, "")
	res, info := checkDomain(t, c, `{"domain":"example.com","whois_fallback":false}`)
	if res.Status != "error" || *res.ErrorMessage != "domain: rdap: HTTP 429" || info != nil {
		t.Fatalf("got %s %v %v", res.Status, *res.ErrorMessage, info)
	}

	// No RDAP service for the TLD.
	res, _ = checkDomain(t, c, `{"domain":"example.zz","whois_fallback":false}`)
	if res.Status != "error" || *res.ErrorMessage != "domain: rdap: no RDAP service for .zz" {
		t.Fatalf("got %s %v", res.Status, *res.ErrorMessage)
	}
}

const whoisVerisign = `   Domain Name: EXAMPLE.NET
   Registry Domain ID: 4743345_DOMAIN_NET-VRSN
   Registrar WHOIS Server: whois.iana.org
   Updated Date: 2026-08-14T07:02:07Z
   Creation Date: 1995-08-14T04:00:00Z
   Registry Expiry Date: 2026-11-20T04:00:00Z
   Registrar: RESERVED-Internet Assigned Numbers Authority
   Domain Status: clientDeleteProhibited https://icann.org/epp#clientDeleteProhibited
   Domain Status: clientTransferProhibited https://icann.org/epp#clientTransferProhibited
   Name Server: A.IANA-SERVERS.NET
>>> Last update of whois database: 2026-11-05T11:00:00Z <<<
`

func TestDomainCheckerWHOISFallback(t *testing.T) {
	addr := whoisServer(t, map[string]string{
		"net":         "% IANA WHOIS server\n\nrefer:        whois.verisign-grs.com\n\ndomain:       NET\n",
		"example.net": whoisVerisign,
		"free.net":    "No match for \"FREE.NET\".\n>>> Last update of whois database <<<\n",
		"zz":          "% IANA WHOIS server\n% This query returned 0 objects.\n",
	})
	// RDAP knows only .com, so .net falls back to WHOIS.
	srv, _ := rdapServer(t, []string{"com"}, nil, 0)
	c := testDomainChecker(srv, addr)

	res, info := checkDomain(t, c, `{"domain":"example.net"}`)
	if res.Status != "success" {
		t.Fatalf("status = %s (%v)", res.Status, res.ErrorMessage)
	}
	if info == nil || info.Source != "whois" || info.Server != "whois.verisign-grs.com" || !info.Registered {
		t.Fatalf("info = %+v", info)
	}
	if info.ExpiresAt != "2026-11-20T04:00:00Z" || *info.DaysUntilExpiry != 14 {
		t.Fatalf("expiry = %q %v", info.ExpiresAt, *info.DaysUntilExpiry)
	}
	if info.RDAPError != "no RDAP service for .net" {
		t.Fatalf("rdap_error = %q", info.RDAPError)
	}
	if info.Registrar != "RESERVED-Internet Assigned Numbers Authority" ||
		strings.Join(info.Statuses, ",") != "clientDeleteProhibited,clientTransferProhibited" ||
		strings.Join(info.Nameservers, ",") != "a.iana-servers.net" {
		t.Fatalf("details = %+v", info)
	}

	res, info = checkDomain(t, c, `{"domain":"free.net"}`)
	if res.Status != "failure" || *res.ErrorMessage != "domain: free.net is not registered (whois: not found)" || info.Registered {
		t.Fatalf("got %s %v %+v", res.Status, *res.ErrorMessage, info)
	}

	res, _ = checkDomain(t, c, `{"domain":"example.zz"}`)
	if res.Status != "error" || *res.ErrorMessage != "domain: rdap: no RDAP service for .zz; whois: no WHOIS server for .zz" {
		t.Fatalf("got %s %v", res.Status, *res.ErrorMessage)
	}
}

func TestDomainCheckerNetworkPolicy(t *testing.T) {
	// The bootstrap fetch itself goes through the guard: a loopback test
	// server is refused under the default private-IP block.
	srv, _ := rdapServer(t, []string{"com"}, map[string]string{"example.com": rdapExampleCom}, 0)
	c := testDomainChecker(srv, "")
	c.blockPrivateIPs = true
	res, _ := checkDomain(t, c, `{"domain":"example.com","whois_fallback":false}`)
	if res.Status != "error" || *res.ErrorMessage != "domain: rdap: bootstrap registry: blocked by the worker network policy" {
		t.Fatalf("got %s %v", res.Status, *res.ErrorMessage)
	}

	// The same for rdap_server_override and WHOIS.
	res, _ = checkDomain(t, c, `{"domain":"example.com","rdap_server_override":"`+srv.URL+`/rdap"}`)
	if res.Status != "error" || *res.ErrorMessage != "domain: rdap: blocked by the worker network policy; whois: blocked by the worker network policy" {
		t.Fatalf("got %s %v", res.Status, *res.ErrorMessage)
	}
}

func TestDomainCheckerOverrideAndInvalidConfig(t *testing.T) {
	srv, hits := rdapServer(t, []string{"com"}, map[string]string{"example.com": rdapExampleCom}, 0)
	c := testDomainChecker(srv, "")
	res, info := checkDomain(t, c, `{"domain":"example.com","rdap_server_override":"`+srv.URL+`/rdap/"}`)
	if res.Status != "success" || info == nil || info.Server != srv.URL {
		t.Fatalf("got %s %v %+v", res.Status, res.ErrorMessage, info)
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Fatal("an override must not consult the bootstrap registry")
	}

	for _, cfg := range []string{`{}`, `{"domain":"https://example.com"}`, `{"domain":"localhost"}`, `{"domain":"10.0.0.1"}`} {
		res := c.Check(context.Background(), json.RawMessage(cfg), 5)
		if res.Status != "error" || res.MetricsData != nil {
			t.Fatalf("%s: got %s", cfg, res.Status)
		}
	}
}

// A registry that publishes no expiry over RDAP (and none over WHOIS) is
// registered and passes, with a note instead of a date.
func TestDomainCheckerNoExpiryPublished(t *testing.T) {
	noExpiry := `{"objectClassName":"domain","ldhName":"example.com","status":["active"],"events":[{"eventAction":"last changed","eventDate":"2026-01-01T00:00:00Z"}]}`
	srv, _ := rdapServer(t, []string{"com"}, map[string]string{"example.com": noExpiry}, 0)
	addr := whoisServer(t, map[string]string{
		"com":         "refer: whois.example-registry.test\n",
		"example.com": "Domain: example.com\nStatus: connect\n",
	})
	c := testDomainChecker(srv, addr)
	res, info := checkDomain(t, c, `{"domain":"example.com","min_days_valid":30}`)
	if res.Status != "success" || info == nil || info.ExpiresAt != "" || info.Source != "rdap" || info.Note == "" {
		t.Fatalf("got %s %v %+v", res.Status, res.ErrorMessage, info)
	}
}

func TestParseWHOISFormats(t *testing.T) {
	tests := []struct {
		name, text, expires, registrar string
		registered                     bool
	}{
		{"nominet", "    Domain name:\n        example.co.uk\n    Registrar:\n        Example Ltd\n    Relevant dates:\n        Registered on: 26-Aug-1996\n        Expiry date:  26-Aug-2027\n", "2027-08-26T00:00:00Z", "", true},
		{"ru paid-till", "domain:        EXAMPLE.RU\nregistrar:     RU-CENTER-RU\npaid-till:     2027-03-01T21:00:00Z\n", "2027-03-01T21:00:00Z", "RU-CENTER-RU", true},
		{"dotted date", "Domain Name: example.kr\nExpiration Date: 2027. 05. 01.\nExpiry: 2027.05.01\n", "2027-05-01T00:00:00Z", "", true},
		{"annotated date", "Domain Name: example.jp\nExpires on: 2027/04/30 (JST)\n", "2027-04-30T00:00:00Z", "", true},
		{"not found", "%% NOT FOUND\n", "", "", false},
		{"available", "Domain: free.be\nStatus: AVAILABLE\n", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := parseWHOIS(tt.text, "example.test", domainTestNow)
			if err != nil {
				t.Fatal(err)
			}
			if info.Registered != tt.registered || info.ExpiresAt != tt.expires {
				t.Fatalf("got registered=%v expires=%q", info.Registered, info.ExpiresAt)
			}
			if tt.registrar != "" && info.Registrar != tt.registrar {
				t.Fatalf("registrar = %q", info.Registrar)
			}
		})
	}
	if _, err := parseWHOIS("Terms of use: lorem ipsum\n", "example.test", domainTestNow); err == nil {
		t.Fatal("an unrecognized answer must be an error, not a verdict")
	}
}

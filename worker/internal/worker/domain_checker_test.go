package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/domainexpiry"
	"github.com/yassinebenameur/probara/shared/netguard"
)

// The checker tests replace the registry lookup with a fake: no test here
// touches the network (the lookup itself is tested in shared/domainexpiry).

var domainNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func fakeDomainChecker(answer func(name string) *domainexpiry.Info) (*DomainChecker, *[]string) {
	var asked []string
	c := &DomainChecker{
		now: func() time.Time { return domainNow },
		lookup: func(_ context.Context, name string, _ time.Duration) *domainexpiry.Info {
			asked = append(asked, name)
			return answer(name)
		},
	}
	return c, &asked
}

func registeredInfo(name, expiresAt string, statuses ...string) *domainexpiry.Info {
	yes := true
	info := &domainexpiry.Info{Domain: name, Registered: &yes, Source: domainexpiry.SourceRDAP, Registrar: "Example Registrar", Statuses: statuses}
	if expiresAt != "" {
		at, _ := time.Parse(time.RFC3339, expiresAt)
		info.SetExpiry(at, domainNow)
	}
	return info
}

func checkDomain(t *testing.T, c *DomainChecker, config string) (CheckResult, *domainexpiry.Info) {
	t.Helper()
	res := c.Check(context.Background(), json.RawMessage(config), 30)
	return res, domainexpiry.Extract(res.MetricsData)
}

func TestDomainCheckerVerdicts(t *testing.T) {
	no := false
	tests := []struct {
		name       string
		info       *domainexpiry.Info
		wantStatus string
		wantMsg    string
	}{
		{name: "far from expiry", info: registeredInfo("example.com", "2027-08-13T04:00:00Z"), wantStatus: "success"},
		// Inside the warning window the check still passes: that is the
		// alerter's domain_expiry alert, not an outage.
		{name: "inside the window", info: registeredInfo("example.com", "2026-11-20T00:00:00Z"), wantStatus: "success"},
		{name: "expired today", info: registeredInfo("example.com", "2026-11-15T06:00:00Z"), wantStatus: "failure", wantMsg: "domain: registration expired today (2026-11-15)"},
		{name: "expired days ago", info: registeredInfo("example.com", "2026-11-12T00:00:00Z"), wantStatus: "failure", wantMsg: "domain: registration expired 3 days ago (2026-11-12)"},
		{name: "redemption", info: registeredInfo("example.com", "2027-01-01T00:00:00Z", "redemption period"), wantStatus: "failure", wantMsg: "domain: registration is in redemption period"},
		{name: "pending delete epp", info: registeredInfo("example.com", "", "pendingDelete"), wantStatus: "failure", wantMsg: "domain: registration is in pendingDelete"},
		{name: "not registered", info: &domainexpiry.Info{Domain: "example.com", Registered: &no, Source: "rdap"}, wantStatus: "failure", wantMsg: "domain: example.com is not registered (rdap)"},
		// Undetermined expiry never fails the check.
		{name: "no date published", info: func() *domainexpiry.Info {
			i := registeredInfo("example.de", "")
			i.UnknownReason = "the registry publishes no expiration date"
			return i
		}(), wantStatus: "success"},
		{name: "lookups failed", info: &domainexpiry.Info{Domain: "example.com", UnknownReason: "rdap: timed out; whois: connection failed"}, wantStatus: "success"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := fakeDomainChecker(func(string) *domainexpiry.Info { return tt.info })
			res, info := checkDomain(t, c, `{"domain":"example.com"}`)
			if res.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", res.Status, tt.wantStatus)
			}
			var msg string
			if res.ErrorMessage != nil {
				msg = *res.ErrorMessage
			}
			if msg != tt.wantMsg {
				t.Fatalf("message = %q, want %q", msg, tt.wantMsg)
			}
			if res.LatencyMs == nil {
				t.Fatal("no latency")
			}
			// The snapshot is recorded on every lookup outcome.
			if info == nil || info.Domain != tt.info.Domain || info.UnknownReason != tt.info.UnknownReason || info.ExpiresAt != tt.info.ExpiresAt {
				t.Fatalf("metrics = %s", res.MetricsData)
			}
		})
	}
}

func TestDomainCheckerRecordsRegistrarAndSource(t *testing.T) {
	c, _ := fakeDomainChecker(func(name string) *domainexpiry.Info { return registeredInfo(name, "2027-08-13T04:00:00Z") })
	res, _ := checkDomain(t, c, `{"domain":"example.com"}`)
	var envelope struct {
		Domain map[string]any `json:"domain"`
	}
	if err := json.Unmarshal(res.MetricsData, &envelope); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"expires_at": "2027-08-13T04:00:00Z", "days_until_expiry": float64(270),
		"registrar": "Example Registrar", "source": "rdap", "registered": true,
	} {
		if envelope.Domain[key] != want {
			t.Errorf("%s = %v, want %v", key, envelope.Domain[key], want)
		}
	}
}

func TestDomainCheckerLooksUpTheRegistrableDomain(t *testing.T) {
	c, asked := fakeDomainChecker(func(name string) *domainexpiry.Info { return registeredInfo(name, "2027-08-13T04:00:00Z") })
	for _, cfg := range []string{`{"domain":"www.shop.example.co.uk"}`, `{"domain":"Bücher.DE."}`} {
		if res, _ := checkDomain(t, c, cfg); res.Status != "success" {
			t.Fatalf("%s: %+v", cfg, res)
		}
	}
	if strings.Join(*asked, ",") != "example.co.uk,xn--bcher-kva.de" {
		t.Fatalf("looked up %v", *asked)
	}
}

func TestDomainCheckerRefusesBadConfigWithoutLookup(t *testing.T) {
	c, asked := fakeDomainChecker(func(string) *domainexpiry.Info { t.Fatal("lookup called"); return nil })
	for cfg, want := range map[string]string{
		`not json`:                   "invalid domain config",
		`{"domain":""}`:              "domain is required",
		`{"domain":"127.0.0.1"}`:     "not an IP address",
		`{"domain":"localhost"}`:     "public suffix",
		`{"domain":"example.com/x"}`: "bare domain name",
	} {
		res := c.Check(context.Background(), json.RawMessage(cfg), 30)
		if res.Status != "error" || res.ErrorMessage == nil || !strings.Contains(*res.ErrorMessage, want) || res.MetricsData != nil {
			t.Errorf("%s: %+v", cfg, res)
		}
	}
	if res := c.Check(context.Background(), json.RawMessage(`{"domain":"example.com"}`), 0); res.Status != "error" {
		t.Errorf("zero timeout: %+v", res)
	}
	if len(*asked) != 0 {
		t.Fatalf("looked up %v", *asked)
	}
}

func TestDomainCheckerPassesTheDeadline(t *testing.T) {
	c := &DomainChecker{now: time.Now}
	c.lookup = func(ctx context.Context, name string, timeout time.Duration) *domainexpiry.Info {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > timeout || timeout != 7*time.Second {
			t.Errorf("deadline %v ok=%v timeout %v", time.Until(deadline), ok, timeout)
		}
		return &domainexpiry.Info{Domain: name, UnknownReason: "x"}
	}
	c.Check(context.Background(), json.RawMessage(`{"domain":"example.com"}`), 7)
}

// Domain lookups refuse private destinations even on a worker whose own
// policy is open (HTTP_BLOCK_PRIVATE_IPS=false): every server is chosen by
// bootstrap data or a remote referral. HTTP_ALLOWED_CIDRS still admits.
func TestDomainDialGuardRefusesPrivateDestinations(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	ctx := context.Background()

	open := NewDomainChecker(false, nil)
	for _, addr := range []string{ln.Addr().String(), "10.0.0.1:43", "169.254.169.254:443", "[::1]:43"} {
		_, err := domainDialGuard(open.allowedCIDRs, time.Second).DialContext(ctx, "tcp", addr)
		if !errors.Is(err, netguard.ErrBlocked) {
			t.Errorf("dial %s: %v, want blocked", addr, err)
		}
	}

	_, loopback, _ := net.ParseCIDR("127.0.0.0/8")
	allowed := NewDomainChecker(false, []*net.IPNet{loopback})
	conn, err := domainDialGuard(allowed.allowedCIDRs, time.Second).DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("allowed CIDR refused: %v", err)
	}
	conn.Close()
}

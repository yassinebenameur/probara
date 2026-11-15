package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/yassinebenameur/probara/shared/netguard"
)

func TestBuildDNSResolver_EmptyUsesSystemDefault(t *testing.T) {
	r, err := buildDNSResolver("", newDialGuard(true, nil, time.Second))
	if err != nil {
		t.Fatalf("buildDNSResolver: %v", err)
	}
	if r != net.DefaultResolver {
		t.Fatal("want net.DefaultResolver")
	}
}

func TestBuildDNSResolver_CustomNameserver(t *testing.T) {
	r, err := buildDNSResolver("10.0.0.2", newDialGuard(false, nil, time.Second))
	if err != nil {
		t.Fatalf("buildDNSResolver: %v", err)
	}
	if r == net.DefaultResolver || !r.PreferGo || r.Dial == nil {
		t.Fatal("want a custom Go resolver with a pinned dial func")
	}
}

// A custom nameserver is an operator-supplied address: its connections go
// through dialGuard like any target.
func TestBuildDNSResolver_NameserverThroughDialGuard(t *testing.T) {
	r, err := buildDNSResolver("10.0.0.53", newDialGuard(true, nil, time.Second))
	if err != nil {
		t.Fatalf("buildDNSResolver: %v", err)
	}
	_, err = r.Dial(context.Background(), "udp", "ignored:53")
	if !errors.Is(err, netguard.ErrBlocked) {
		t.Fatalf("Dial() error = %v, want ssrf_blocked for a private nameserver", err)
	}
}

// startFakeDNSServer answers every A query over UDP on loopback with
// 192.0.2.10 and counts the queries it saw.
func startFakeDNSServer(t *testing.T) (addr string, hits *atomic.Int32) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	t.Cleanup(func() { pc.Close() })
	hits = &atomic.Int32{}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			hits.Add(1)
			var p dnsmessage.Parser
			hdr, err := p.Start(buf[:n])
			if err != nil {
				continue
			}
			q, err := p.Question()
			if err != nil {
				continue
			}
			b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: hdr.ID, Response: true, Authoritative: true, RecursionDesired: hdr.RecursionDesired})
			b.EnableCompression()
			_ = b.StartQuestions()
			_ = b.Question(q)
			_ = b.StartAnswers()
			if q.Type == dnsmessage.TypeA {
				_ = b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60}, dnsmessage.AResource{A: [4]byte{192, 0, 2, 10}})
			}
			resp, err := b.Finish()
			if err != nil {
				continue
			}
			_, _ = pc.WriteTo(resp, from)
		}
	}()
	return pc.LocalAddr().String(), hits
}

// The DNS checker's custom nameserver honours the worker's policy: a
// loopback nameserver is refused (as an error, before any query reaches it)
// when private destinations are blocked, and answers when blocking is off or
// its range is allow-listed.
func TestDNSChecker_NameserverSSRFPolicy(t *testing.T) {
	nameserver, hits := startFakeDNSServer(t)
	config, _ := json.Marshal(map[string]any{"host": "probe.example", "record_type": "A", "nameserver": nameserver, "expected_answers": []string{"192.0.2.10"}})

	blocked := NewDNSChecker(true, nil).Check(context.Background(), config, 2)
	if blocked.Status != "error" || !isSSRFBlocked(errors.New(derefErr(blocked.ErrorMessage))) {
		t.Fatalf("blocked: status %q (%s), want error carrying ssrf_blocked", blocked.Status, derefErr(blocked.ErrorMessage))
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("nameserver saw %d queries through a blocking policy, want 0", n)
	}

	_, loopback, _ := net.ParseCIDR("127.0.0.0/8")
	for name, checker := range map[string]*DNSChecker{
		"blocking off": NewDNSChecker(false, nil),
		"allow-listed": NewDNSChecker(true, []*net.IPNet{loopback}),
	} {
		result := checker.Check(context.Background(), config, 2)
		if result.Status != "success" {
			t.Errorf("%s: status %q (%s), want success", name, result.Status, derefErr(result.ErrorMessage))
		}
		if msg := derefErr(result.ErrorMessage); strings.Contains(msg, "ssrf_blocked") {
			t.Errorf("%s: error %q, want no policy refusal", name, msg)
		}
	}
}

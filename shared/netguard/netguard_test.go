package netguard

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestPolicyAllows(t *testing.T) {
	_, office, _ := net.ParseCIDR("10.20.0.0/16")
	block := Policy{BlockPrivate: true, AllowedCIDRs: []*net.IPNet{office}}

	cases := []struct {
		ip     string
		policy Policy
		want   bool
	}{
		{"8.8.8.8", block, true},
		{"169.254.169.254", block, false}, // cloud metadata
		{"127.0.0.1", block, false},
		{"::1", block, false},
		{"10.1.2.3", block, false},
		{"10.20.9.9", block, true}, // allow-listed
		{"fd00::1", block, false},
		{"0.0.0.0", block, false},
		{"169.254.169.254", Policy{}, true}, // zero value allows everything
	}
	for _, tc := range cases {
		if got := tc.policy.Allows(net.ParseIP(tc.ip)); got != tc.want {
			t.Errorf("Allows(%s) with block=%v = %v, want %v", tc.ip, tc.policy.BlockPrivate, got, tc.want)
		}
	}
}

func TestDialerRefusesBlockedAddressWithSentinel(t *testing.T) {
	d := &Dialer{Policy: Policy{BlockPrivate: true}, Timeout: time.Second}
	_, err := d.DialContext(context.Background(), "tcp", "127.0.0.1:9")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
	if err.Error() != "ssrf_blocked: ip 127.0.0.1 not allowed" {
		t.Fatalf("message = %q; worker check results rely on the ssrf_blocked prefix", err.Error())
	}
}

func mustCIDRs(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatalf("ParseCIDR(%q): %v", c, err)
		}
		out = append(out, n)
	}
	return out
}

// serverChosenReason runs AllowsServerChosen and returns "" for allowed or the
// refusal's reason, failing the test on an error of any other shape.
func serverChosenReason(t *testing.T, p Policy, dst *net.UDPAddr, peer net.IP) string {
	t.Helper()
	err := p.AllowsServerChosen(dst, peer)
	if err == nil {
		return ""
	}
	var sce *ServerChosenError
	if !errors.As(err, &sce) || !errors.Is(err, ErrBlocked) {
		t.Fatalf("AllowsServerChosen(%v, %v) = %v (%T), want a *ServerChosenError wrapping ErrBlocked", dst, peer, err, err)
	}
	return sce.Reason
}

func TestAllowsServerChosen(t *testing.T) {
	open := Policy{}
	openListed := Policy{AllowedCIDRs: mustCIDRs(t, "10.20.0.0/16", "2001:db8::/32")}
	block := Policy{BlockPrivate: true}
	blockListed := Policy{BlockPrivate: true, AllowedCIDRs: mustCIDRs(t,
		"10.20.0.0/16", "100.102.14.0/24", "127.0.0.0/8", "2001:db8::/32")}

	const (
		public   = "8.8.8.8"
		public6  = "2001:4860:4860::8888"
		private  = "10.0.0.2"
		private6 = "fd00::2"
		loop     = "127.0.0.1"
	)

	cases := []struct {
		name   string
		policy Policy
		dst    string
		port   int
		peer   string // "" is a nil peer
		want   string // "" is allowed
	}{
		// Rule 1: shape. The port check runs before any address rule.
		{"port 0", open, "8.8.4.4", 0, public, ReasonPort},
		{"port 53 (DNS)", open, "8.8.4.4", 53, public, ReasonPort},
		{"port 1023", open, "8.8.4.4", 1023, public, ReasonPort},
		{"port 1024", open, "8.8.4.4", 1024, public, ""},
		{"port 65535", open, "8.8.4.4", 65535, public, ""},
		{"rtcp after rtp 65535", open, "8.8.4.4", 65536, public, ReasonPort},
		{"negative port", open, "8.8.4.4", -1, public, ReasonPort},
		{"port before scope", open, "10.0.0.5", 53, public, ReasonPort},
		{"port before same-as-peer", open, public, 123, public, ReasonPort},

		// Rule 3: the peer's own address, any policy.
		{"same as public peer", open, public, 40000, public, ""},
		{"same as public peer, block", block, public, 40000, public, ""},
		{"same as private peer, block", block, private, 40000, private, ""},
		{"same as loopback peer, block", block, loop, 40000, loop, ""},
		{"same as peer, v4-mapped", open, "::ffff:8.8.8.8", 40000, public, ""},
		{"same as peer, IPv6", open, public6, 40000, public6, ""},
		{"same as NAT64 peer", open, "64:ff9b::a00:1", 40000, "64:ff9b::a00:1", ""},

		// Rule 4: loopback only behind a loopback peer, never rescued by
		// AllowedCIDRs.
		{"loopback behind public", open, "127.0.0.1", 40000, public, ReasonLoopback},
		{"loopback behind private", open, "127.0.0.1", 40000, private, ReasonLoopback},
		{"loopback behind nil peer", open, "127.0.0.1", 40000, "", ReasonLoopback},
		{"loopback listed behind public", blockListed, "127.0.0.2", 40000, public, ReasonLoopback},
		{"::1 behind public", open, "::1", 40000, public6, ReasonLoopback},
		{"NAT64 loopback behind public", open, "64:ff9b::7f00:1", 40000, public, ReasonLoopback},
		{"loopback behind loopback", open, "127.0.0.2", 40000, loop, ""},
		{"::1 behind 127.0.0.1", open, "::1", 40000, loop, ""},
		{"127.0.0.1 behind ::1", open, "127.0.0.1", 40000, "::1", ""},
		{"loopback behind loopback, block", block, "127.0.0.2", 40000, loop, ReasonBlockedByPolicy},
		{"loopback behind loopback, block listed", blockListed, "127.0.0.2", 40000, loop, ""},

		// Rule 5: the worker policy.
		{"public, block", block, "8.8.4.4", 40000, public, ""},
		{"public IPv6, block", block, "2606:4700::1111", 40000, public6, ""},
		{"private behind private, block", block, "10.0.0.5", 40000, private, ReasonBlockedByPolicy},
		{"private behind public, block", block, "10.0.0.5", 40000, public, ReasonBlockedByPolicy},
		{"ULA behind ULA, block", block, "fd00::5", 40000, private6, ReasonBlockedByPolicy},
		{"listed behind public, block", blockListed, "10.20.1.1", 40000, public, ""},
		{"listed behind private, block", blockListed, "10.20.1.1", 40000, private, ""},
		{"on-prem CGNAT, block", block, "100.102.14.7", 40000, "100.102.14.1", ReasonBlockedByPolicy},
		{"on-prem CGNAT listed, block", blockListed, "100.102.14.7", 40000, "100.102.14.1", ""},
		{"documentation, block", block, "2001:db8::1", 40000, private6, ReasonBlockedByPolicy},
		{"documentation listed, block", blockListed, "2001:db8::1", 40000, public6, ""},
		{"Teredo, block", block, "2001:0:4136:e378::1", 40000, private6, ReasonBlockedByPolicy},
		{"NAT64 local-use, block", block, "64:ff9b:1::808:808", 40000, private6, ReasonBlockedByPolicy},
		{"NAT64 of private, block", block, "64:ff9b::a00:1", 40000, public, ReasonBlockedByPolicy},
		{"NAT64 of public, block", block, "64:ff9b::808:404", 40000, public, ""},
		{"NAT64 of listed, block", blockListed, "64:ff9b::a14:101", 40000, public, ""},
		{"6to4 of private, block", block, "2002:a00:1::1", 40000, public, ReasonBlockedByPolicy},
		{"6to4 of public, block", block, "2002:808:404::1", 40000, public, ""},

		// Rule 6: the scope rule, which holds with BlockPrivate false.
		{"10/8 behind public", open, "10.0.0.5", 40000, public, ReasonPrivateBehindPublic},
		{"172.16/12 behind public", open, "172.16.0.1", 40000, public, ReasonPrivateBehindPublic},
		{"192.168/16 behind public", open, "192.168.1.1", 40000, public, ReasonPrivateBehindPublic},
		{"CGNAT behind public", open, "100.64.0.1", 40000, public, ReasonPrivateBehindPublic},
		{"IETF protocol block behind public", open, "192.0.0.9", 40000, public, ReasonPrivateBehindPublic},
		{"TEST-NET-1 behind public", open, "192.0.2.1", 40000, public, ReasonPrivateBehindPublic},
		{"TEST-NET-2 behind public", open, "198.51.100.7", 40000, public, ReasonPrivateBehindPublic},
		{"TEST-NET-3 behind public", open, "203.0.113.1", 40000, public, ReasonPrivateBehindPublic},
		{"ULA behind public", open, "fd00::5", 40000, public6, ReasonPrivateBehindPublic},
		{"fc00::/8 behind public", open, "fc00::5", 40000, public6, ReasonPrivateBehindPublic},
		{"v4-mapped private behind public", open, "::ffff:10.0.0.5", 40000, public, ReasonPrivateBehindPublic},
		{"private behind nil peer", open, "10.0.0.5", 40000, "", ReasonPrivateBehindPublic},
		{"private behind a documentation peer", open, "10.0.0.5", 40000, "2001:db8::1", ReasonPrivateBehindPublic},
		{"private behind a NAT64 peer", open, "10.0.0.5", 40000, "64:ff9b::a00:1", ReasonPrivateBehindPublic},
		{"NAT64 of private behind public", open, "64:ff9b::a00:1", 40000, public, ReasonPrivateBehindPublic},
		{"NAT64 of private behind public IPv6", open, "64:ff9b::a00:1", 40000, public6, ReasonPrivateBehindPublic},
		{"NAT64 of CGNAT behind public", open, "64:ff9b::6440:1", 40000, public, ReasonPrivateBehindPublic},
		{"NAT64 local-use of private behind public", open, "64:ff9b:1::a00:1", 40000, public, ReasonPrivateBehindPublic},
		{"NAT64 local-use of public behind public", open, "64:ff9b:1::808:808", 40000, public, ReasonPrivateBehindPublic},
		{"6to4 of private behind public", open, "2002:0a00:0001::1", 40000, public, ReasonPrivateBehindPublic},
		{"6to4 of TEST-NET-2 behind public", open, "2002:c633:6407::1", 40000, public, ReasonPrivateBehindPublic},
		{"Teredo behind public", open, "2001:0:4136:e378:8000:63bf:3fff:fdd2", 40000, public6, ReasonPrivateBehindPublic},
		{"documentation behind public", open, "2001:db8::1", 40000, public6, ReasonPrivateBehindPublic},
		{"listed behind public", openListed, "10.20.0.1", 40000, public, ""},
		{"unlisted next to listed behind public", openListed, "10.21.0.1", 40000, public, ReasonPrivateBehindPublic},
		{"NAT64 of listed behind public", openListed, "64:ff9b::a14:1", 40000, public, ""},
		{"listed documentation behind public", openListed, "2001:db8::1", 40000, public6, ""},
		{"on-prem CGNAT behind on-prem peer", open, "100.102.14.7", 40000, "100.102.14.1", ""},
		{"private behind private", open, "10.0.0.5", 40000, "192.168.1.1", ""},
		{"ULA behind private IPv4", open, "fd00::5", 40000, private, ""},
		{"NAT64 of private behind private", open, "64:ff9b::a00:1", 40000, private, ""},
		{"6to4 of private behind private", open, "2002:a00:1::1", 40000, private, ""},
		{"NAT64 local-use behind private", open, "64:ff9b:1::a00:1", 40000, private6, ""},
		{"Teredo behind private", open, "2001:0:4136:e378::1", 40000, private6, ""},
		{"documentation behind private", open, "2001:db8::1", 40000, private6, ""},

		// Rule 7: otherwise allowed.
		{"public behind public", open, "8.8.4.4", 40000, public, ""},
		{"public IPv6 behind public", open, "2606:4700::1111", 40000, public6, ""},
		{"public behind private", open, "8.8.4.4", 40000, private, ""},
		{"public behind nil peer", open, "8.8.4.4", 40000, "", ""},
		{"NAT64 of public behind public", open, "64:ff9b::808:404", 40000, public, ""},
		{"6to4 of public behind public", open, "2002:808:404::1", 40000, public, ""},
		{"v4-mapped public behind public", open, "::ffff:8.8.4.4", 40000, public, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.dst)
			if ip == nil {
				t.Fatalf("bad test address %q", tc.dst)
			}
			var peer net.IP
			if tc.peer != "" {
				if peer = net.ParseIP(tc.peer); peer == nil {
					t.Fatalf("bad test peer %q", tc.peer)
				}
			}
			got := serverChosenReason(t, tc.policy, &net.UDPAddr{IP: ip, Port: tc.port}, peer)
			if got != tc.want {
				t.Errorf("AllowsServerChosen(%s:%d, peer %q) = %q, want %q", tc.dst, tc.port, tc.peer, got, tc.want)
			}
		})
	}
}

// TestAllowsServerChosenHardRefusals pins rule 2: these destinations are
// refused under every policy, even one that lists every address, and even when
// the peer itself sits at the same address.
func TestAllowsServerChosenHardRefusals(t *testing.T) {
	everything := mustCIDRs(t, "0.0.0.0/0", "::/0")
	policies := map[string]Policy{
		"open":           {},
		"open, all":      {AllowedCIDRs: everything},
		"block":          {BlockPrivate: true},
		"block, all":     {BlockPrivate: true, AllowedCIDRs: everything},
		"block, partial": {BlockPrivate: true, AllowedCIDRs: mustCIDRs(t, "169.254.0.0/16", "224.0.0.0/4", "0.0.0.0/8")},
	}
	cases := []struct {
		dst  string
		want string
	}{
		{"0.0.0.0", ReasonUnspecified},
		{"0.1.2.3", ReasonUnspecified},
		{"0.255.255.255", ReasonUnspecified},
		{"::", ReasonUnspecified},
		{"::ffff:0.0.0.0", ReasonUnspecified},
		{"::ffff:0.1.2.3", ReasonUnspecified},
		{"224.0.0.1", ReasonMulticast},
		{"239.255.255.250", ReasonMulticast},
		{"ff02::1", ReasonMulticast},
		{"ff0e::fb", ReasonMulticast},
		{"240.0.0.1", ReasonMulticast},
		{"255.255.255.255", ReasonMulticast},
		{"169.254.169.254", ReasonLinkLocal},
		{"169.254.0.1", ReasonLinkLocal},
		{"::ffff:169.254.169.254", ReasonLinkLocal},
		{"fe80::1", ReasonLinkLocal},
		{"febf::1", ReasonLinkLocal},
		// Judged on the embedded IPv4 address.
		{"64:ff9b::", ReasonUnspecified},
		{"64:ff9b::1:203", ReasonUnspecified},
		{"64:ff9b::e000:1", ReasonMulticast},
		{"64:ff9b::ffff:ffff", ReasonMulticast},
		{"64:ff9b::a9fe:a9fe", ReasonLinkLocal},
		{"64:ff9b:1::a9fe:a9fe", ReasonLinkLocal},
		{"2002:a9fe:a9fe::1", ReasonLinkLocal},
		{"2002:0001:0203::1", ReasonUnspecified},
		{"2002:e000:0001::1", ReasonMulticast},
	}
	peers := []string{"8.8.8.8", "10.0.0.2", "127.0.0.1", "2001:4860:4860::8888"}
	for name, p := range policies {
		for _, tc := range cases {
			ip := net.ParseIP(tc.dst)
			if ip == nil {
				t.Fatalf("bad test address %q", tc.dst)
			}
			dst := &net.UDPAddr{IP: ip, Port: 40000}
			for _, peer := range append(peers[:len(peers):len(peers)], tc.dst) { // the peer itself too
				if got := serverChosenReason(t, p, dst, net.ParseIP(peer)); got != tc.want {
					t.Errorf("%s: AllowsServerChosen(%s, peer %s) = %q, want %q", name, tc.dst, peer, got, tc.want)
				}
			}
		}
	}
}

func TestAllowsServerChosenMissingAddress(t *testing.T) {
	cases := map[string]*net.UDPAddr{
		"nil addr":         nil,
		"nil ip":           {Port: 40000},
		"empty ip":         {IP: net.IP{}, Port: 40000},
		"truncated ip":     {IP: net.IP{10, 0, 0}, Port: 40000},
		"overlong ip":      {IP: make(net.IP, 17), Port: 40000},
		"nil ip, bad port": {Port: 53},
	}
	for name, dst := range cases {
		for _, p := range []Policy{{}, {BlockPrivate: true}} {
			if got := serverChosenReason(t, p, dst, net.ParseIP("8.8.8.8")); got != ReasonUnspecified {
				t.Errorf("%s (block=%v) = %q, want %q", name, p.BlockPrivate, got, ReasonUnspecified)
			}
		}
	}
}

// TestServerChosenErrorCarriesNoAddress pins that a refusal's text never names
// the destination, the peer or the port: it reaches check results and every
// notification channel.
func TestServerChosenErrorCarriesNoAddress(t *testing.T) {
	dst := &net.UDPAddr{IP: net.ParseIP("10.11.12.13"), Port: 40404}
	err := Policy{}.AllowsServerChosen(dst, net.ParseIP("8.8.8.8"))
	if err == nil {
		t.Fatal("want a refusal")
	}
	if got, want := err.Error(), "ssrf_blocked: server-chosen destination refused (private_behind_public)"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
	for _, leak := range []string{"10.11", "12.13", "40404", "8.8"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("message %q leaks %q", err.Error(), leak)
		}
	}
}

// TestServerChosenRangesStayOutOfSharedTable pins that the extra ranges only
// AllowsServerChosen consults did not leak into Allows, which every checker
// and notification plugin dials through.
func TestServerChosenRangesStayOutOfSharedTable(t *testing.T) {
	block := Policy{BlockPrivate: true}
	for _, ip := range []string{"0.1.2.3", "2001:db8::1", "2001::1", "64:ff9b:1::1", "64:ff9b::a00:1", "2002:a00:1::1"} {
		if !block.Allows(net.ParseIP(ip)) {
			t.Errorf("Allows(%s) = false; the server-chosen ranges must not change the shared policy", ip)
		}
		if IsPrivateOrReserved(net.ParseIP(ip)) {
			t.Errorf("IsPrivateOrReserved(%s) = true; the server-chosen ranges must not change the shared table", ip)
		}
	}
}

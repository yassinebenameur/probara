package clientip

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func mustResolver(t *testing.T, raw string) *Resolver {
	t.Helper()
	trusted, err := ParseTrustedProxies(raw)
	if err != nil {
		t.Fatal(err)
	}
	return NewResolver(trusted)
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name    string
		trusted string
		remote  string
		xff     []string
		realIP  string
		want    string
	}{
		{name: "no trust, no headers", remote: "203.0.113.7:5555", want: "203.0.113.7"},
		{name: "no trust ignores spoofed XFF", remote: "203.0.113.7:5555", xff: []string{"1.2.3.4"}, want: "203.0.113.7"},
		{name: "no trust ignores spoofed X-Real-IP", remote: "203.0.113.7:5555", realIP: "1.2.3.4", want: "203.0.113.7"},
		{name: "untrusted peer with XFF is ignored", trusted: "10.0.0.0/8", remote: "203.0.113.7:5555", xff: []string{"1.2.3.4, 10.0.0.5"}, want: "203.0.113.7"},
		{name: "untrusted peer with X-Real-IP is ignored", trusted: "10.0.0.0/8", remote: "203.0.113.7:5555", realIP: "1.2.3.4", want: "203.0.113.7"},
		{name: "trusted peer, single hop", trusted: "10.0.0.0/8", remote: "10.0.0.2:80", xff: []string{"198.51.100.9"}, want: "198.51.100.9"},
		{
			// The client prepended a forged entry; our proxy appended the real
			// peer it saw. Right-to-left stops at the first untrusted hop.
			name: "client-forged leftmost entry is skipped", trusted: "10.0.0.0/8", remote: "10.0.0.2:80",
			xff: []string{"6.6.6.6, 198.51.100.9"}, want: "198.51.100.9",
		},
		{name: "multiple trusted hops", trusted: "10.0.0.0/8,192.168.1.1", remote: "10.0.0.2:80", xff: []string{"198.51.100.9, 192.168.1.1, 10.1.1.1"}, want: "198.51.100.9"},
		{name: "XFF across header lines", trusted: "10.0.0.0/8", remote: "10.0.0.2:80", xff: []string{"6.6.6.6", "198.51.100.9, 10.1.1.1"}, want: "198.51.100.9"},
		{name: "every hop trusted yields leftmost", trusted: "10.0.0.0/8", remote: "10.0.0.2:80", xff: []string{"10.9.9.9, 10.1.1.1"}, want: "10.9.9.9"},
		{name: "garbage hop stops the walk", trusted: "10.0.0.0/8", remote: "10.0.0.2:80", xff: []string{"198.51.100.9, nonsense, 10.1.1.1"}, want: "10.1.1.1"},
		{name: "garbage nearest hop falls back to peer", trusted: "10.0.0.0/8", remote: "10.0.0.2:80", xff: []string{"198.51.100.9, nonsense"}, want: "10.0.0.2"},
		{name: "hop with port", trusted: "10.0.0.0/8", remote: "10.0.0.2:80", xff: []string{"198.51.100.9:4711"}, want: "198.51.100.9"},
		{name: "trusted peer, X-Real-IP only", trusted: "10.0.0.0/8", remote: "10.0.0.2:80", realIP: "198.51.100.9", want: "198.51.100.9"},
		{name: "XFF wins over X-Real-IP", trusted: "10.0.0.0/8", remote: "10.0.0.2:80", xff: []string{"198.51.100.9"}, realIP: "6.6.6.6", want: "198.51.100.9"},
		{name: "invalid X-Real-IP falls back to peer", trusted: "10.0.0.0/8", remote: "10.0.0.2:80", realIP: "nope", want: "10.0.0.2"},
		{name: "trusted peer, no headers", trusted: "10.0.0.0/8", remote: "10.0.0.2:80", want: "10.0.0.2"},
		{name: "bare trusted IP", trusted: "10.0.0.2", remote: "10.0.0.2:80", xff: []string{"198.51.100.9"}, want: "198.51.100.9"},
		{name: "bare trusted IP does not trust neighbour", trusted: "10.0.0.2", remote: "10.0.0.3:80", xff: []string{"198.51.100.9"}, want: "10.0.0.3"},
		{name: "IPv6 peer and client", trusted: "fd00::/8", remote: "[fd00::1]:443", xff: []string{"2001:db8::7"}, want: "2001:db8::7"},
		{name: "IPv4-mapped peer matches IPv4 CIDR", trusted: "10.0.0.0/8", remote: "[::ffff:10.0.0.2]:80", xff: []string{"198.51.100.9"}, want: "198.51.100.9"},
		{name: "bracketed IPv6 hop", trusted: "10.0.0.0/8", remote: "10.0.0.2:80", xff: []string{"[2001:db8::7]:1234"}, want: "2001:db8::7"},
		{name: "untrusted IPv6 peer ignores XFF", trusted: "fd00::/8", remote: "[2001:db8::99]:443", xff: []string{"2001:db8::7"}, want: "2001:db8::99"},
		{name: "IPv6 multi-hop chain", trusted: "fd00::/8", remote: "[fd00::1]:443", xff: []string{"2001:db8::6, 2001:db8::7, fd00::2"}, want: "2001:db8::7"},
		{name: "IPv6 peer with zone", trusted: "fe80::/10", remote: "[fe80::1%eth0]:443", xff: []string{"2001:db8::7"}, want: "2001:db8::7"},
		{name: "malformed XFF from untrusted peer", remote: "203.0.113.7:5555", xff: []string{"<script>, ,,"}, want: "203.0.113.7"},
		{name: "empty XFF entries from trusted peer", trusted: "10.0.0.0/8", remote: "10.0.0.2:80", xff: []string{" , "}, want: "10.0.0.2"},
		{name: "XFF hop that is a hostname", trusted: "10.0.0.0/8", remote: "10.0.0.2:80", xff: []string{"evil.example"}, want: "10.0.0.2"},
		{name: "remote without port", remote: "203.0.113.7", want: "203.0.113.7"},
		{name: "non-IP remote returned as is", remote: "pipe", want: "pipe"},
		{name: "empty remote", remote: "", xff: []string{"1.2.3.4"}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			for _, v := range tc.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			if tc.realIP != "" {
				req.Header.Set("X-Real-IP", tc.realIP)
			}
			if got := mustResolver(t, tc.trusted).Resolve(req); got != tc.want {
				t.Fatalf("Resolve = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTrustAllYieldsLeftmost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:1"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if got := NewResolver(TrustAll()).Resolve(req); got != "1.2.3.4" {
		t.Fatalf("got %q", got)
	}
}

func TestNilResolverTrustsNothing(t *testing.T) {
	var r *Resolver
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:80"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := r.Resolve(req); got != "10.0.0.2" {
		t.Fatalf("got %q", got)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := ParseTrustedProxies(" 10.0.0.0/8 , ,192.168.1.7,fd00::/8, 10.1.2.3/8")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "192.168.1.7/32", "fd00::/8", "10.0.0.0/8"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Fatalf("entry %d = %s, want %s", i, got[i], want[i])
		}
	}
	if empty, err := ParseTrustedProxies(""); err != nil || len(empty) != 0 {
		t.Fatalf("empty input: %v %v", empty, err)
	}
	for _, bad := range []string{"10.0.0.0/33", "not-an-ip", "10.0.0.0/8,host.example", "::ffff:10.0.0.0/104"} {
		if _, err := ParseTrustedProxies(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestMiddlewareAndFromRequest(t *testing.T) {
	r := mustResolver(t, "10.0.0.0/8")
	var seen string
	h := r.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		seen = FromRequest(req)
		if req.RemoteAddr != "10.0.0.2:80" {
			t.Errorf("RemoteAddr rewritten to %q", req.RemoteAddr)
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:80"
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "198.51.100.9" {
		t.Fatalf("FromRequest inside middleware = %q", seen)
	}

	// Outside the middleware no header is believed.
	if got := FromRequest(req); got != "10.0.0.2" {
		t.Fatalf("FromRequest outside middleware = %q", got)
	}
}

// An address keys a per-address limit only when it names one client: a
// trusted proxy forwarded it, or it is a public TCP peer. A private,
// loopback, link-local or shared-space peer we cannot see through does not.
func TestAttributable(t *testing.T) {
	cases := []struct {
		name, trusted, remote, xff string
		want                       bool
	}{
		{name: "public peer", remote: "203.0.113.7:1", want: true},
		{name: "public IPv6 peer", remote: "[2001:db8::7]:1", want: true},
		{name: "private peer, no trust (frontend proxy, ingress pod)", remote: "10.0.0.5:1", want: false},
		{name: "loopback peer", remote: "127.0.0.1:1", want: false},
		{name: "IPv6 ULA peer", remote: "[fd00::5]:1", want: false},
		{name: "link-local peer", remote: "[fe80::1]:1", want: false},
		{name: "shared address space peer", remote: "100.64.1.2:1", want: false},
		{name: "private peer ignores XFF it is not trusted for", remote: "10.0.0.5:1", xff: "203.0.113.7", want: false},
		{name: "trusted proxy forwards a public client", trusted: "10.0.0.0/8", remote: "10.0.0.5:1", xff: "203.0.113.7", want: true},
		{name: "trusted proxy forwards a private client", trusted: "10.0.0.0/8", remote: "10.0.0.5:1", xff: "192.168.1.20", want: true},
		{name: "trusted proxy without a header", trusted: "10.0.0.0/8", remote: "10.0.0.5:1", want: false},
		{name: "trusted proxy with an unreadable hop", trusted: "10.0.0.0/8", remote: "10.0.0.5:1", xff: "nonsense", want: false},
		{name: "non-IP remote", remote: "pipe", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got bool
			h := mustResolver(t, tc.trusted).Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
				got = Attributable(req)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Fatalf("Attributable = %v, want %v", got, tc.want)
			}
		})
	}
}

// BucketKey applies the Attributable rule to the resolver's own answer and
// groups IPv6 by /64, for limiters outside a router that runs Middleware.
func TestBucketKey(t *testing.T) {
	r := NewResolver([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	cases := []struct {
		remote, xff, want string
	}{
		{"203.0.113.7:1", "", "203.0.113.7"},
		{"[2001:db8:1:2::1]:1", "", "2001:db8:1:2::/64"},
		{"[2001:db8:1:2:ffff::9]:1", "", "2001:db8:1:2::/64"},
		{"[::ffff:203.0.113.8]:1", "", "203.0.113.8"},
		// An untrusted private peer pools every visitor behind it.
		{"172.16.0.5:1", "", ""},
		{"127.0.0.1:1", "", ""},
		// A trusted proxy without a forwarding header is not the client.
		{"10.1.2.3:1", "", ""},
		// Forwarded by a trusted proxy: attributable, private or not.
		{"10.1.2.3:1", "198.51.100.9", "198.51.100.9"},
		{"10.1.2.3:1", "192.168.1.20", "192.168.1.20"},
		// A forwarding header from an untrusted peer is ignored.
		{"203.0.113.7:1", "198.51.100.9", "203.0.113.7"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := r.BucketKey(req); got != c.want {
			t.Errorf("%s via %q: BucketKey = %q, want %q", c.remote, c.xff, got, c.want)
		}
	}
	if BucketAddress("not-an-ip") != "not-an-ip" {
		t.Fatal("unparseable address changed")
	}
}

// Package clientip resolves the address of the client behind an HTTP request.
//
// Forwarding headers are client-settable, so they are believed only when the
// TCP peer is a trusted proxy (TRUSTED_PROXY_CIDRS, parsed once by
// shared/config). The default, an empty trust list, means RemoteAddr only:
// no header can move the resolved address.
//
// With trusted proxies, X-Forwarded-For is walked right to left. Each proxy
// appends the address it received the connection from, so the rightmost
// entries are written by our own proxies and everything to the left of the
// first untrusted hop may be forged. The first untrusted address from the
// right is the client. X-Real-IP is consulted only when the trusted peer sent
// no X-Forwarded-For.
//
// Every IP-keyed control (audit log IP, session IP, rate limits) must go
// through this package; a handler that reads X-Forwarded-For itself reopens
// the spoofing hole.
package clientip

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ParseTrustedProxies parses a comma-separated list of CIDRs or bare IPs
// (a bare IP trusts exactly that address). Empty input yields an empty list.
func ParseTrustedProxies(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "/") {
			p, err := netip.ParsePrefix(part)
			if err != nil {
				return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", part, err)
			}
			if p.Addr().Is4In6() {
				// ::ffff:10.0.0.0/104 would never match: peers are unmapped.
				return nil, fmt.Errorf("invalid trusted proxy CIDR %q: use the IPv4 form", part)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy address %q: %w", part, err)
		}
		a = normalize(a)
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// TrustAll is the trust list that believes every peer: the resolved address
// is then the leftmost X-Forwarded-For entry. Only for the deprecated
// STATUS_PAGE_TRUSTED_PROXY=true, which had exactly that behavior.
func TrustAll() []netip.Prefix {
	return []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
}

// Resolver resolves client addresses against a fixed trusted-proxy list. A
// nil *Resolver trusts nothing.
type Resolver struct {
	trusted []netip.Prefix
}

// NewResolver returns a resolver that believes forwarding headers only from
// peers inside trusted.
func NewResolver(trusted []netip.Prefix) *Resolver {
	return &Resolver{trusted: append([]netip.Prefix(nil), trusted...)}
}

// Trusts reports whether addr is a trusted proxy.
func (r *Resolver) Trusts(addr netip.Addr) bool {
	if r == nil || !addr.IsValid() {
		return false
	}
	addr = normalize(addr)
	for _, p := range r.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// Resolve returns the client address for req, without a port. When
// RemoteAddr is not an IP (in-process requests, tests) it is returned as is.
func (r *Resolver) Resolve(req *http.Request) string {
	ip, _ := r.resolve(req)
	return ip
}

// resolve is Resolve plus whether the address came from a trusted proxy's
// forwarding header rather than being the TCP peer itself.
func (r *Resolver) resolve(req *http.Request) (string, bool) {
	peer, ok := parseHostPort(req.RemoteAddr)
	if !ok {
		return remoteHost(req.RemoteAddr), false
	}
	if !r.Trusts(peer) {
		return peer.String(), false
	}

	if hops := forwardedFor(req.Header); len(hops) > 0 {
		client := peer
		forwarded := false
		for i := len(hops) - 1; i >= 0; i-- {
			hop, ok := parseHostPort(hops[i])
			if !ok {
				// A hop we cannot read ends the verifiable chain: the
				// nearest address we could check stands in for the client.
				break
			}
			client = hop
			forwarded = true
			if !r.Trusts(hop) {
				break
			}
		}
		return client.String(), forwarded
	}

	if xri, ok := parseHostPort(strings.TrimSpace(req.Header.Get("X-Real-IP"))); ok {
		return xri.String(), true
	}
	return peer.String(), false
}

// Middleware resolves the client address once per request and stores it in
// the request context for FromRequest/FromContext/Attributable. RemoteAddr is
// left as the TCP peer.
func (r *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ip, forwarded := r.resolve(req)
		ctx := context.WithValue(req.Context(), ctxKey{}, resolved{ip: ip, forwarded: forwarded})
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

type ctxKey struct{}

// resolved is what Middleware stores: the address and whether a trusted
// proxy's forwarding header supplied it.
type resolved struct {
	ip        string
	forwarded bool
}

// WithClientIP returns ctx carrying ip as the resolved client address, as if
// it were the TCP peer (not forwarded).
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxKey{}, resolved{ip: ip})
}

// FromContext returns the address stored by Middleware or WithClientIP.
func FromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxKey{}).(resolved)
	return v.ip, ok
}

// Attributable reports whether the resolved client address identifies one
// client well enough to key a per-address limit on. It does when a trusted
// proxy forwarded it, or when it is the TCP peer and a public address. A
// loopback, private (RFC 1918, fc00::/7), link-local or shared (100.64/10)
// peer that is not a trusted proxy is usually a hop we cannot see through
// -- the frontend's /api proxy, an ingress pod, a NAT gateway -- and keying
// on it would pool every visitor behind it into one bucket.
func Attributable(req *http.Request) bool {
	v, ok := req.Context().Value(ctxKey{}).(resolved)
	if !ok {
		v = resolved{ip: remoteHost(req.RemoteAddr)}
	}
	return v.attributable()
}

func (v resolved) attributable() bool {
	if v.forwarded {
		return true
	}
	a, err := netip.ParseAddr(v.ip)
	if err != nil {
		return false
	}
	a = normalize(a)
	return !(a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsUnspecified() || sharedAddressSpace.Contains(a))
}

// BucketKey is the key a per-client limit should use for req: the resolved
// address under the Attributable rule, IPv6 grouped by /64 (BucketAddress).
// "" when the address is not attributable, so callers fall back to a limit
// that does not pool every visitor behind one hop into one bucket.
func (r *Resolver) BucketKey(req *http.Request) string {
	ip, forwarded := r.resolve(req)
	if !(resolved{ip: ip, forwarded: forwarded}).attributable() {
		return ""
	}
	return BucketAddress(ip)
}

// BucketAddress groups IPv6 clients by /64: one host is usually handed a
// whole /64, so per-address buckets would let it rotate to a fresh one on
// every request. IPv4 addresses are returned as they are.
func BucketAddress(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	a = a.WithZone("").Unmap()
	if a.Is6() {
		if p, err := a.Prefix(64); err == nil {
			return p.String()
		}
	}
	return a.String()
}

// sharedAddressSpace is RFC 6598 carrier-grade NAT space, also used by some
// cluster networks: an address in it is shared by design.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

// FromRequest returns the resolved client address for a request that passed
// through Middleware. Outside it, it falls back to the RemoteAddr host and
// never to a forwarding header.
func FromRequest(req *http.Request) string {
	if ip, ok := FromContext(req.Context()); ok {
		return ip
	}
	return remoteHost(req.RemoteAddr)
}

// forwardedFor flattens every X-Forwarded-For header line, in order.
func forwardedFor(h http.Header) []string {
	var hops []string
	for _, line := range h.Values("X-Forwarded-For") {
		for _, hop := range strings.Split(line, ",") {
			hops = append(hops, strings.TrimSpace(hop))
		}
	}
	return hops
}

// parseHostPort reads "ip", "ip:port", "[v6]" or "[v6]:port".
func parseHostPort(s string) (netip.Addr, bool) {
	if s == "" {
		return netip.Addr{}, false
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return normalize(a), true
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		if a, err := netip.ParseAddr(host); err == nil {
			return normalize(a), true
		}
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		if a, err := netip.ParseAddr(s[1 : len(s)-1]); err == nil {
			return normalize(a), true
		}
	}
	return netip.Addr{}, false
}

func remoteHost(remoteAddr string) string {
	if a, ok := parseHostPort(remoteAddr); ok {
		return a.String()
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

// normalize drops the zone and unmaps IPv4-in-IPv6, so ::ffff:10.0.0.1 from a
// dual-stack listener matches 10.0.0.0/8.
func normalize(a netip.Addr) netip.Addr {
	return a.WithZone("").Unmap()
}

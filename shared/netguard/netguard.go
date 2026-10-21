// Package netguard is the outbound-connection (SSRF) policy shared by every
// component that dials an operator-supplied address: the worker's checkers
// and the notification plugins (webhook, chat, paging and SMS channels).
//
// A Policy either allows everything (the zero value, BlockPrivate == false) or
// refuses loopback, unspecified, multicast and private/reserved addresses,
// with AllowedCIDRs as the escape hatch for internal targets an operator
// trusts. The check runs on the resolved IP at dial time, so a hostname that
// resolves to a private address is refused the same as a literal one, and
// every redirect hop is covered because each hop dials through the guard.
//
// Policy.AllowsServerChosen is the stricter check for an address a remote
// server chose rather than the operator (the media address in a SIP SDP
// answer): a few destinations are refused whatever the policy says, and a
// private address named by a public peer is refused even by an open policy.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// ErrBlocked is wrapped by every refusal, so callers can tell a policy
// rejection (permanent) from a network failure (transient) with errors.Is.
// Its text keeps the historical "ssrf_blocked:" prefix check results carry.
var ErrBlocked = errors.New("ssrf_blocked")

// privateCIDRs are the ranges refused when a Policy blocks private addresses.
var privateCIDRs = mustParseCIDRs(
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"127.0.0.0/8",
	"169.254.0.0/16",  // link-local + cloud metadata
	"100.64.0.0/10",   // CGNAT
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"224.0.0.0/4",     // multicast
	"240.0.0.0/4",     // reserved
	"::1/128",
	"fc00::/7",  // unique local
	"fe80::/10", // link-local
)

// Ranges only AllowsServerChosen consults. Those not in privateCIDRs stay out
// of it on purpose: adding them there would change what every checker and
// notification plugin may dial, which is a separate decision.
var (
	thisNetwork = mustParseCIDRs("0.0.0.0/8")[0] // RFC 1122; Linux delivers 0.0.0.0 to the local host
	classE      = mustParseCIDRs("240.0.0.0/4")[0]

	// Translation prefixes whose addresses embed an IPv4 address, which is
	// judged as well (the stricter answer wins): an IPv6-only worker behind
	// NAT64 reaches 10.0.0.1 through 64:ff9b::a00:1.
	nat64WellKnown = mustParseCIDRs("64:ff9b::/96")[0]   // RFC 6052
	nat64LocalUse  = mustParseCIDRs("64:ff9b:1::/48")[0] // RFC 8215
	sixToFour      = mustParseCIDRs("2002::/16")[0]      // RFC 3056

	// serverChosenReservedCIDRs count as private or reserved for the scope
	// rule, on top of privateCIDRs.
	serverChosenReservedCIDRs = []*net.IPNet{
		nat64LocalUse,                      // meaningful inside one site only
		mustParseCIDRs("2001::/32")[0],     // Teredo: the real endpoint is hidden in the address
		mustParseCIDRs("2001:db8::/32")[0], // documentation
	}
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(fmt.Sprintf("netguard: bad CIDR %q: %v", c, err))
		}
		out = append(out, n)
	}
	return out
}

// IsPrivateOrReserved reports whether ip falls in a private, loopback,
// link-local (including cloud metadata), CGNAT, documentation, multicast or
// reserved range.
func IsPrivateOrReserved(ip net.IP) bool {
	for _, n := range privateCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Policy decides which resolved addresses an outbound connection may reach.
type Policy struct {
	// BlockPrivate refuses loopback, unspecified, multicast and
	// private/reserved addresses. False allows every address.
	BlockPrivate bool
	// AllowedCIDRs are always allowed, even when BlockPrivate is set.
	AllowedCIDRs []*net.IPNet
}

// Allows reports whether the policy permits connecting to ip.
func (p Policy) Allows(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if !p.BlockPrivate {
		return true
	}
	if inCIDRs(p.AllowedCIDRs, ip) {
		return true
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	return !IsPrivateOrReserved(ip)
}

// Reasons AllowsServerChosen gives for a refusal. They are coarse on purpose:
// they reach check results and metric labels, and never name the address.
const (
	ReasonPort                = "port"                  // not 1024-65535: keeps traffic off DNS, NTP, SNMP, syslog
	ReasonUnspecified         = "unspecified"           // no address, ::, or 0.0.0.0/8 ("this network")
	ReasonMulticast           = "multicast"             // multicast, or 240.0.0.0/4 including broadcast
	ReasonLinkLocal           = "link_local"            // 169.254.0.0/16 (cloud metadata) and fe80::/10
	ReasonLoopback            = "loopback"              // loopback named by a peer that is not loopback itself
	ReasonBlockedByPolicy     = "blocked_by_policy"     // refused by BlockPrivate (AllowedCIDRs did not admit it)
	ReasonPrivateBehindPublic = "private_behind_public" // private or reserved, named by a public peer
)

// ServerChosenError is AllowsServerChosen's refusal. It wraps ErrBlocked and
// its text carries the reason only, never the address.
type ServerChosenError struct {
	Reason string
}

func (e *ServerChosenError) Error() string {
	return fmt.Sprintf("%s: server-chosen destination refused (%s)", ErrBlocked, e.Reason)
}

func (e *ServerChosenError) Unwrap() error { return ErrBlocked }

// AllowsServerChosen vets a UDP destination that a remote server chose rather
// than the operator, such as the media address in a SIP SDP answer, where
// peer is the server's own address (the one the worker dialled, already
// vetted by Allows). It returns nil or a *ServerChosenError.
//
// The far end picks such an address, so Allows alone is not enough: an open
// policy (BlockPrivate false, the worker's default) would let any carrier aim
// the probe's traffic into the worker's own network. In order, the first
// match deciding:
//
//  1. The port must be 1024-65535 and the IP present. Callers check a derived
//     port (RTCP's RTP+1) through here too, so RTP port 65535 gets no RTCP.
//  2. Unspecified and 0.0.0.0/8, multicast and 240.0.0.0/4, and link-local
//     are refused whatever the policy and AllowedCIDRs say.
//  3. The peer's own address is allowed.
//  4. Loopback is allowed only when the peer is loopback (dev and tests).
//  5. The policy: an address Allows refuses is refused. With BlockPrivate
//     set, serverChosenReservedCIDRs are refused too unless AllowedCIDRs
//     lists them.
//  6. The scope rule, whatever BlockPrivate says: a private or reserved
//     address named by a public peer is refused unless AllowedCIDRs lists
//     it. A private peer naming a private address (an on-prem estate) passes.
//
// A NAT64 or 6to4 address is judged on its embedded IPv4 address as well, and
// is refused when either answer refuses. Rule 3 compares the address as
// given. The peer counts as private only by the shared range table, so an
// unusual peer address errs towards public, the stricter reading; a nil peer
// is public and matches nothing.
func (p Policy) AllowsServerChosen(dst *net.UDPAddr, peer net.IP) error {
	if dst == nil || (len(dst.IP) != net.IPv4len && len(dst.IP) != net.IPv6len) {
		return &ServerChosenError{Reason: ReasonUnspecified}
	}
	if dst.Port < 1024 || dst.Port > 65535 {
		return &ServerChosenError{Reason: ReasonPort}
	}
	ips := []net.IP{dst.IP}
	if v4 := embeddedIPv4(dst.IP); v4 != nil {
		ips = append(ips, v4)
	}
	for _, ip := range ips {
		if reason := hardRefusal(ip); reason != "" {
			return &ServerChosenError{Reason: reason}
		}
	}
	if dst.IP.Equal(peer) {
		return nil
	}
	for _, ip := range ips {
		if reason := p.serverChosenRefusal(ip, peer); reason != "" {
			return &ServerChosenError{Reason: reason}
		}
	}
	return nil
}

// hardRefusal is rule 2 of AllowsServerChosen: destinations no policy admits.
func hardRefusal(ip net.IP) string {
	switch {
	case ip.IsUnspecified() || thisNetwork.Contains(ip):
		return ReasonUnspecified
	case ip.IsMulticast() || classE.Contains(ip):
		return ReasonMulticast
	case ip.IsLinkLocalUnicast():
		return ReasonLinkLocal
	}
	return ""
}

// serverChosenRefusal is rules 4-6 of AllowsServerChosen for one address.
func (p Policy) serverChosenRefusal(ip, peer net.IP) string {
	if ip.IsLoopback() && !peer.IsLoopback() {
		return ReasonLoopback
	}
	listed := inCIDRs(p.AllowedCIDRs, ip)
	extra := inCIDRs(serverChosenReservedCIDRs, ip)
	if !p.Allows(ip) || (p.BlockPrivate && extra && !listed) {
		return ReasonBlockedByPolicy
	}
	if (IsPrivateOrReserved(ip) || extra) && !IsPrivateOrReserved(peer) && !listed {
		return ReasonPrivateBehindPublic
	}
	return ""
}

// embeddedIPv4 returns the IPv4 address inside a NAT64 or 6to4 address, or
// nil. The well-known NAT64 prefix only exists as a /96 (RFC 6052 §2.1), so
// the IPv4 is the last 32 bits. A local-use prefix's length is the operator's
// choice, so where its IPv4 sits cannot be known; it is read as a /96 (the
// common choice), and the whole block is reserved for the scope rule anyway.
func embeddedIPv4(ip net.IP) net.IP {
	if ip.To4() != nil {
		return nil
	}
	switch {
	case nat64WellKnown.Contains(ip), nat64LocalUse.Contains(ip):
		return net.IPv4(ip[12], ip[13], ip[14], ip[15])
	case sixToFour.Contains(ip):
		return net.IPv4(ip[2], ip[3], ip[4], ip[5])
	}
	return nil
}

func inCIDRs(cidrs []*net.IPNet, ip net.IP) bool {
	for _, cidr := range cidrs {
		if cidr != nil && cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// Dialer is a TCP dialer that enforces a Policy. It implements DialContext
// (and thereby the Mongo driver's ContextDialer).
type Dialer struct {
	Policy  Policy
	Timeout time.Duration
}

// DialContext dials address, resolving hostnames and validating every
// candidate IP against the policy first. Only an allowed IP is ever dialled,
// so DNS rebinding between check and connect is not possible.
func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: d.Timeout, KeepAlive: 30 * time.Second}

	if !d.Policy.BlockPrivate {
		return dialer.DialContext(ctx, network, address)
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	if ip := net.ParseIP(host); ip != nil {
		if !d.Policy.Allows(ip) {
			return nil, fmt.Errorf("%w: ip %s not allowed", ErrBlocked, host)
		}
		return dialer.DialContext(ctx, network, address)
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, ipAddr := range ips {
		ipStr := ipAddr.IP.String()
		if !d.Policy.Allows(ipAddr.IP) {
			lastErr = fmt.Errorf("%w: resolved %s -> %s", ErrBlocked, host, ipStr)
			continue
		}

		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ipStr, port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no resolved IPs")
	}
	return nil, lastErr
}

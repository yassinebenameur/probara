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
	for _, cidr := range p.AllowedCIDRs {
		if cidr != nil && cidr.Contains(ip) {
			return true
		}
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	return !IsPrivateOrReserved(ip)
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

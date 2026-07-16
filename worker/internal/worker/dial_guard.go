package worker

import (
	"context"
	"fmt"
	"net"
	"time"
)

// dialGuard is a TCP dialer that enforces the worker's private-IP policy. It
// is shared by checkers that hand connection setup to a client library
// (Redis, Postgres, MongoDB) so they get the same SSRF protection as the
// HTTP and gRPC checkers. It implements DialContext (and thereby the Mongo
// driver's ContextDialer).
type dialGuard struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
	timeout         time.Duration
}

func newDialGuard(blockPrivateIPs bool, allowedCIDRs []*net.IPNet, timeout time.Duration) *dialGuard {
	return &dialGuard{
		blockPrivateIPs: blockPrivateIPs,
		allowedCIDRs:    allowedCIDRs,
		timeout:         timeout,
	}
}

// DialContext dials address, resolving hostnames and validating every
// candidate IP against the allow/block policy first.
func (g *dialGuard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: g.timeout, KeepAlive: 30 * time.Second}

	if !g.blockPrivateIPs {
		return dialer.DialContext(ctx, network, address)
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	if ip := net.ParseIP(host); ip != nil {
		if !g.isIPAllowed(ip) {
			return nil, fmt.Errorf("ssrf_blocked: ip %s not allowed", host)
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
		if !g.isIPAllowed(ipAddr.IP) {
			lastErr = fmt.Errorf("ssrf_blocked: resolved %s -> %s", host, ipStr)
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

func (g *dialGuard) isIPAllowed(ip net.IP) bool {
	if ip == nil {
		return false
	}

	for _, cidr := range g.allowedCIDRs {
		if cidr != nil && cidr.Contains(ip) {
			return true
		}
	}

	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	return !isPrivateOrReservedIP(ip)
}

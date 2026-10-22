package models

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ValidateNameserver checks a monitor's custom DNS server: a host or IP,
// optionally with a port ("10.0.0.2", "resolver.internal:5353",
// "[fd00::2]:53", a bare IPv6 literal). Blank means the system resolver. The
// DNS and SIP (RFC 3263) configs share it, so both accept one grammar.
func ValidateNameserver(nameserver string) error {
	ns := strings.TrimSpace(nameserver)
	if ns == "" {
		return nil
	}
	nsHost := ns
	if h, p, err := net.SplitHostPort(ns); err == nil {
		nsHost = h
		if port, err := strconv.Atoi(p); err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("nameserver port must be between 1 and 65535")
		}
	}
	if net.ParseIP(nsHost) == nil && !IsValidHostname(nsHost) {
		return fmt.Errorf("nameserver must be a valid IP address or hostname (host or host:port)")
	}
	return nil
}

// NormalizeNameserverAddr turns a nameserver value (host, host:port, bare
// IPv6 literal) into a dialable host:port with a default port of 53.
func NormalizeNameserverAddr(nameserver string) (string, error) {
	addr := strings.TrimSpace(nameserver)
	if _, _, err := net.SplitHostPort(addr); err != nil {
		if ip := net.ParseIP(addr); ip != nil && ip.To4() == nil {
			// Bare IPv6 literal — bracket it before appending the port.
			addr = "[" + addr + "]"
		}
		addr = addr + ":53"
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return "", err
	}
	return addr, nil
}

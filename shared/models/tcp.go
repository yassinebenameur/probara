package models

import (
	"fmt"
	"net"
	"strings"
)

// TCPMonitorConfig represents configuration for raw TCP connect monitors. It
// validates that a host:port accepts a connection (route/SG/listener
// reachability), optionally completing a TLS handshake.
type TCPMonitorConfig struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	UseTLS        *bool  `json:"use_tls,omitempty"`         // perform a TLS handshake after connecting
	TLSSkipVerify *bool  `json:"tls_skip_verify,omitempty"` // accept self-signed / mismatched certs
}

// Validate checks a TCP config.
func (c TCPMonitorConfig) Validate() error {
	host := strings.TrimSpace(c.Host)
	if host == "" {
		return fmt.Errorf("host is required")
	}
	if net.ParseIP(host) == nil && !IsValidHostname(host) {
		return fmt.Errorf("host must be a valid IP address or hostname")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

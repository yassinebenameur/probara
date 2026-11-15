package models

import (
	"fmt"
	"net"
	"strings"
)

// TCPMonitorConfig represents configuration for raw TCP connect monitors. It
// validates that a host:port accepts a connection (route/SG/listener
// reachability), optionally completing a TLS handshake on a TLS-wrapped port
// (443, 993, 5671, 6697, …) and recording its certificate for tls_expiry.
type TCPMonitorConfig struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	UseTLS        *bool  `json:"use_tls,omitempty"`         // perform a TLS handshake after connecting
	TLSSkipVerify *bool  `json:"tls_skip_verify,omitempty"` // accept self-signed / mismatched certs
	// TLSMinDaysValid opens a tls_expiry alert when a certificate of the
	// presented chain has fewer days left (shared/tlscert). The check itself
	// still passes; it requires use_tls.
	TLSMinDaysValid *int `json:"tls_min_days_valid,omitempty"`
	// TLSServerName overrides the SNI and verification name (default: host).
	TLSServerName *string `json:"tls_server_name,omitempty"`
}

// TLSEnabled reports whether the check completes a TLS handshake.
func (c TCPMonitorConfig) TLSEnabled() bool {
	return c.UseTLS != nil && *c.UseTLS
}

// ServerName is the TLS identity the handshake asks for and verifies:
// tls_server_name when set, else the host.
func (c TCPMonitorConfig) ServerName() string {
	if c.TLSServerName != nil {
		if name := strings.TrimSpace(*c.TLSServerName); name != "" {
			return name
		}
	}
	return strings.TrimSpace(c.Host)
}

// Validate checks a TCP config. The worker runs it on every job too, so a
// rule added here must hold for configs saved before it existed: those carry
// only host, port, use_tls and tls_skip_verify, which keep their old rules.
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
	// The certificate fields only mean something after a handshake: refuse
	// them on a plain connect rather than store a threshold that never fires.
	if c.TLSMinDaysValid != nil {
		if *c.TLSMinDaysValid < 0 {
			return fmt.Errorf("tls_min_days_valid must be 0 or greater")
		}
		if !c.TLSEnabled() {
			return fmt.Errorf("tls_min_days_valid requires use_tls")
		}
	}
	if c.TLSServerName != nil {
		name := strings.TrimSpace(*c.TLSServerName)
		if name == "" {
			return fmt.Errorf("tls_server_name cannot be empty")
		}
		if !c.TLSEnabled() {
			return fmt.Errorf("tls_server_name requires use_tls")
		}
		if !isHostOrIP(strings.TrimSuffix(name, ".")) {
			return fmt.Errorf("tls_server_name must be a valid hostname or IP address")
		}
	}
	return nil
}

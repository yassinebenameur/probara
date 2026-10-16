package models

import (
	"fmt"
	"net"
	"strings"
)

// SIPMonitorConfig is the configuration of a `sip` monitor. It is the one
// definition of the config: the API validator and the worker's SIP checker
// both unmarshal into it and call Validate, so a rule added here holds on
// both sides of the queue.
type SIPMonitorConfig struct {
	Host           string `json:"host"`                      // SIP server hostname/IP
	Port           int    `json:"port"`                      // Default 5060 (5061 for TLS)
	Transport      string `json:"transport"`                 // "udp", "tcp", or "tls" (default: udp)
	Method         string `json:"method,omitempty"`          // "options" (default) or "register"
	Username       string `json:"username,omitempty"`        // AOR user + digest auth username
	Password       string `json:"password,omitempty"`        // Digest auth secret (encrypted at rest)
	Domain         string `json:"domain,omitempty"`          // SIP domain for the AOR/request URI (default: host)
	ExpectedStatus *int   `json:"expected_status,omitempty"` // Expected final SIP response (default: 200)
	TLSSkipVerify  bool   `json:"tls_skip_verify,omitempty"` // Skip certificate verification (lab/self-signed)
	TLSServerName  string `json:"tls_server_name,omitempty"` // SNI/verification name override
}

// Validate checks the config. The worker calls it too, so it must accept a
// stored config with decrypted secrets and the API calls it on configs whose
// password may be the "***" mask (which counts as set).
func (c SIPMonitorConfig) Validate() error {
	// Every string field ends up in, or next to, a SIP header line. A CR or
	// LF would let a field inject its own header lines (or a whole second
	// message on a stream transport), and a NUL truncates parsers on the far
	// end. Reject them before anything else looks at the values.
	for _, f := range []struct{ name, value string }{
		{"host", c.Host},
		{"transport", c.Transport},
		{"method", c.Method},
		{"username", c.Username},
		{"password", c.Password},
		{"domain", c.Domain},
		{"tls_server_name", c.TLSServerName},
	} {
		if strings.ContainsAny(f.value, "\r\n\x00") {
			return fmt.Errorf("%s must not contain CR, LF or NUL characters", f.name)
		}
	}

	if c.Host == "" {
		return fmt.Errorf("host is required")
	}
	if !isHostOrIP(c.Host) {
		return fmt.Errorf("host must be a valid IP address or hostname")
	}

	// Port 0 means the transport default (5060, or 5061 for TLS).
	if c.Port != 0 && (c.Port < 1 || c.Port > 65535) {
		return fmt.Errorf("port must be between 1 and 65535")
	}

	switch strings.ToLower(c.Transport) {
	case "", "udp", "tcp", "tls":
	default:
		return fmt.Errorf("transport must be 'udp', 'tcp', or 'tls'")
	}

	switch strings.ToLower(c.Method) {
	case "", "options", "register":
	default:
		return fmt.Errorf("method must be 'options' or 'register'")
	}

	// Digest credentials go together: a password without a username (or vice
	// versa) is a config mistake, not a server-side condition.
	if (strings.TrimSpace(c.Username) == "") != (c.Password == "") {
		return fmt.Errorf("username and password must be provided together")
	}

	// SIP response codes are 100-699.
	if c.ExpectedStatus != nil && (*c.ExpectedStatus < 100 || *c.ExpectedStatus > 699) {
		return fmt.Errorf("expected_status must be between 100 and 699")
	}

	// The checker trims the override before using it as the SNI and
	// verification name, so validate what it will actually use. An IP is
	// accepted: crypto/tls verifies it against the certificate's IP SANs.
	if name := strings.TrimSpace(c.TLSServerName); name != "" && !isHostOrIP(name) {
		return fmt.Errorf("tls_server_name must be a valid hostname or IP address")
	}

	return nil
}

func isHostOrIP(s string) bool {
	return net.ParseIP(s) != nil || IsValidHostname(s)
}

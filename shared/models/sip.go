package models

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

// SIPMonitorConfig is the configuration of a `sip` monitor. It is the one
// definition of the config: the API validator and the worker's SIP checker
// both unmarshal into it and call Validate, so a rule added here holds on
// both sides of the queue.
type SIPMonitorConfig struct {
	Host           string `json:"host"`                      // SIP server hostname/IP; the RFC 3263 domain under resolution rfc3263
	Port           int    `json:"port"`                      // 0: 5060 (5061 for TLS), or from SRV under rfc3263
	Transport      string `json:"transport"`                 // "udp", "tcp", or "tls"; "" is udp, or SRV-discovered under rfc3263
	Method         string `json:"method,omitempty"`          // "options" (default), "register" or "invite" (a test call: Call)
	Username       string `json:"username,omitempty"`        // AOR user + digest auth username
	Password       string `json:"password,omitempty"`        // Digest auth secret (encrypted at rest)
	Domain         string `json:"domain,omitempty"`          // SIP domain for the AOR/request URI (default: host)
	ExpectedStatus *int   `json:"expected_status,omitempty"` // Expected final SIP response (default: 200)
	TLSSkipVerify  bool   `json:"tls_skip_verify,omitempty"` // Skip certificate verification (lab/self-signed)
	TLSServerName  string `json:"tls_server_name,omitempty"` // SNI/verification name override
	// TLSMinDaysValid opens a tls_expiry alert when the server certificate
	// has fewer days left (alerter tls_expiry.go); the check itself passes.
	TLSMinDaysValid *int `json:"tls_min_days_valid,omitempty"`
	// Resolution picks how host becomes addresses: "" or "a" dials host's
	// A/AAAA records at the configured or default port, exactly as before
	// the field existed; "rfc3263" locates the server like a SIP client
	// (RFC 3263): SRV records when no port is set (and no transport, every
	// transport's), with failover between the targets. Opt-in, because the
	// form always sent port and transport and an implicit SRV lookup would
	// silently re-target existing monitors.
	Resolution string `json:"resolution,omitempty"`
	// Nameserver pins the DNS server for rfc3263 lookups (host[:port], the
	// DNS monitor's grammar). Blank uses the worker's resolver.
	Nameserver string `json:"nameserver,omitempty"`
	// Call configures the test call; set if and only if method is "invite"
	// (sip_call.go).
	Call *SIPCallConfig `json:"call,omitempty"`
}

// SIP monitor methods.
const (
	SIPMethodOptions  = "options"
	SIPMethodRegister = "register"
	SIPMethodInvite   = "invite"
)

// IsCall reports a test-call (INVITE) config: one whose every check places a
// real, possibly billed, call. Either marker counts, so a payload that
// somehow carries only one of them is still treated as a call by everything
// that gates or de-duplicates calls (the API's tenant-admin gate, the
// worker's at-most-once delivery). Both fields are non-secret, so a masked or
// still-encrypted config answers the same.
func (c SIPMonitorConfig) IsCall() bool {
	return strings.EqualFold(strings.TrimSpace(c.Method), SIPMethodInvite) || c.Call != nil
}

// IsSIPCallConfig reports whether a monitor of monitorType with this config
// places calls (SIPMonitorConfig.IsCall). Anything that is not a parseable
// sip config is not a call.
func IsSIPCallConfig(monitorType string, config json.RawMessage) bool {
	if monitorType != "sip" || len(config) == 0 {
		return false
	}
	var cfg SIPMonitorConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return false
	}
	return cfg.IsCall()
}

// SIP address resolution modes.
const (
	SIPResolutionA       = "a"
	SIPResolutionRFC3263 = "rfc3263"
)

// ResolutionMode is the effective resolution mode: "a" or "rfc3263".
func (c SIPMonitorConfig) ResolutionMode() string {
	if strings.EqualFold(strings.TrimSpace(c.Resolution), SIPResolutionRFC3263) {
		return SIPResolutionRFC3263
	}
	return SIPResolutionA
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
		{"resolution", c.Resolution},
		{"nameserver", c.Nameserver},
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

	switch strings.ToLower(c.Resolution) {
	case "", SIPResolutionA, SIPResolutionRFC3263:
	default:
		return fmt.Errorf("resolution must be 'a' or 'rfc3263'")
	}
	if strings.TrimSpace(c.Nameserver) != "" {
		// Only rfc3263 does its own lookups; "a" mode resolves like every
		// other monitor type, through the worker's resolver.
		if c.ResolutionMode() != SIPResolutionRFC3263 {
			return fmt.Errorf("nameserver requires resolution rfc3263")
		}
		if err := ValidateNameserver(c.Nameserver); err != nil {
			return err
		}
	}

	switch strings.ToLower(c.Method) {
	case "", SIPMethodOptions, SIPMethodRegister, SIPMethodInvite:
	default:
		return fmt.Errorf("method must be 'options', 'register' or 'invite'")
	}
	isInvite := strings.EqualFold(c.Method, SIPMethodInvite)
	if isInvite && c.Call == nil {
		return fmt.Errorf("call is required when method is invite")
	}
	if !isInvite && c.Call != nil {
		return fmt.Errorf("call is only allowed when method is invite")
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

	if c.Call != nil {
		if err := c.Call.validate(c.ExpectedStatus); err != nil {
			return err
		}
	}

	// The checker trims the override before using it as the SNI and
	// verification name, so validate what it will actually use. An IP is
	// accepted: crypto/tls verifies it against the certificate's IP SANs.
	if name := strings.TrimSpace(c.TLSServerName); name != "" && !isHostOrIP(name) {
		return fmt.Errorf("tls_server_name must be a valid hostname or IP address")
	}

	// The certificate is read from the TLS handshake, so the threshold needs
	// an explicit TLS transport: on UDP/TCP no certificate is ever seen and
	// the alert could never fire.
	if c.TLSMinDaysValid != nil {
		if *c.TLSMinDaysValid < 0 {
			return fmt.Errorf("tls_min_days_valid must be 0 or greater")
		}
		if !strings.EqualFold(c.Transport, "tls") {
			return fmt.Errorf("tls_min_days_valid requires transport tls (set transport tls)")
		}
	}

	return nil
}

func isHostOrIP(s string) bool {
	return net.ParseIP(s) != nil || IsValidHostname(s)
}

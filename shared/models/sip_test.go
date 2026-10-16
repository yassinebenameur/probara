package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSIPMonitorConfigValidate(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		errContains string // empty: must be valid
	}{
		{name: "minimal options monitor", config: `{"host":"sip.example.com"}`},
		{name: "ipv4 host", config: `{"host":"192.0.2.10"}`},
		{name: "ipv6 host", config: `{"host":"2001:db8::10"}`},
		{name: "explicit port", config: `{"host":"sip.example.com","port":5080}`},
		{name: "tls transport", config: `{"host":"sip.example.com","transport":"tls","tls_skip_verify":true}`},
		{name: "transport and method are case-insensitive", config: `{"host":"sip.example.com","transport":"TCP","method":"OPTIONS"}`},
		{name: "register with credentials", config: `{"host":"sip.example.com","method":"register","username":"agent","password":"secret","domain":"example.com"}`},
		{name: "register without credentials is allowed", config: `{"host":"sip.example.com","method":"register"}`},
		{name: "masked password preserved on update", config: `{"host":"sip.example.com","method":"register","username":"agent","password":"***"}`},
		{name: "expected status lower bound", config: `{"host":"sip.example.com","expected_status":100}`},
		{name: "expected status upper bound", config: `{"host":"sip.example.com","expected_status":699}`},
		{name: "tls server name hostname", config: `{"host":"192.0.2.10","transport":"tls","tls_server_name":"sbc.example.com"}`},
		{name: "tls server name ip", config: `{"host":"sbc.example.com","transport":"tls","tls_server_name":"192.0.2.10"}`},
		{name: "tls server name is trimmed like the checker does", config: `{"host":"sip.example.com","tls_server_name":" sbc.example.com "}`},
		{name: "blank tls server name means default", config: `{"host":"sip.example.com","tls_server_name":"  "}`},
		{name: "tls min days valid over tls", config: `{"host":"sip.example.com","transport":"tls","tls_min_days_valid":14}`},
		{name: "tls min days valid zero", config: `{"host":"sip.example.com","transport":"TLS","tls_min_days_valid":0}`},
		{name: "tls min days valid with skip verify", config: `{"host":"sip.example.com","transport":"tls","tls_skip_verify":true,"tls_min_days_valid":7}`},

		{name: "missing host", config: `{"transport":"udp"}`, errContains: "host is required"},
		{name: "invalid host", config: `{"host":"sip example.com"}`, errContains: "host must be a valid IP address or hostname"},
		{name: "host with scheme", config: `{"host":"sip:sip.example.com"}`, errContains: "host must be a valid IP address or hostname"},
		{name: "port too large", config: `{"host":"sip.example.com","port":65536}`, errContains: "port must be between 1 and 65535"},
		{name: "negative port", config: `{"host":"sip.example.com","port":-1}`, errContains: "port must be between 1 and 65535"},
		{name: "invalid transport", config: `{"host":"sip.example.com","transport":"sctp"}`, errContains: "transport must be"},
		// INVITE is not accepted until the call flow exists.
		{name: "invalid method", config: `{"host":"sip.example.com","method":"invite"}`, errContains: "method must be"},
		{name: "password without username", config: `{"host":"sip.example.com","password":"secret"}`, errContains: "username and password must be provided together"},
		{name: "username without password", config: `{"host":"sip.example.com","username":"agent"}`, errContains: "username and password must be provided together"},
		{name: "blank username with password", config: `{"host":"sip.example.com","username":"  ","password":"secret"}`, errContains: "username and password must be provided together"},
		{name: "expected status below range", config: `{"host":"sip.example.com","expected_status":42}`, errContains: "expected_status must be between 100 and 699"},
		{name: "expected status above range", config: `{"host":"sip.example.com","expected_status":700}`, errContains: "expected_status must be between 100 and 699"},
		{name: "tls min days valid negative", config: `{"host":"sip.example.com","transport":"tls","tls_min_days_valid":-1}`, errContains: "tls_min_days_valid must be 0 or greater"},
		{name: "tls min days valid over udp", config: `{"host":"sip.example.com","transport":"udp","tls_min_days_valid":14}`, errContains: "tls_min_days_valid requires transport tls"},
		{name: "tls min days valid over tcp", config: `{"host":"sip.example.com","transport":"tcp","tls_min_days_valid":14}`, errContains: "tls_min_days_valid requires transport tls"},
		{name: "tls min days valid needs an explicit transport", config: `{"host":"sip.example.com","port":5061,"tls_min_days_valid":14}`, errContains: "set transport tls"},
		{name: "tls server name invalid", config: `{"host":"sip.example.com","transport":"tls","tls_server_name":"bad name"}`, errContains: "tls_server_name must be a valid hostname or IP address"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg SIPMonitorConfig
			if err := json.Unmarshal([]byte(tt.config), &cfg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			err := cfg.Validate()
			if tt.errContains == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.errContains) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.errContains)
			}
		})
	}
}

// Every string field reaches a SIP header line (or the digest computation),
// so CR, LF and NUL are refused in each one, whatever the method.
func TestSIPMonitorConfigValidateRejectsInjectionCharacters(t *testing.T) {
	base := func() SIPMonitorConfig {
		return SIPMonitorConfig{
			Host:          "sip.example.com",
			Transport:     "tls",
			Method:        "register",
			Username:      "agent",
			Password:      "secret",
			Domain:        "example.com",
			TLSServerName: "sbc.example.com",
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("base config invalid: %v", err)
	}

	fields := map[string]func(*SIPMonitorConfig, string){
		"host":            func(c *SIPMonitorConfig, v string) { c.Host += v },
		"transport":       func(c *SIPMonitorConfig, v string) { c.Transport += v },
		"method":          func(c *SIPMonitorConfig, v string) { c.Method += v },
		"username":        func(c *SIPMonitorConfig, v string) { c.Username += v },
		"password":        func(c *SIPMonitorConfig, v string) { c.Password += v },
		"domain":          func(c *SIPMonitorConfig, v string) { c.Domain += v },
		"tls_server_name": func(c *SIPMonitorConfig, v string) { c.TLSServerName += v },
	}
	for field, set := range fields {
		for _, bad := range []string{"\r", "\n", "\x00", "\r\nX-Injected: 1"} {
			cfg := base()
			set(&cfg, bad)
			err := cfg.Validate()
			want := field + " must not contain CR, LF or NUL characters"
			if err == nil || err.Error() != want {
				t.Errorf("%s + %q: Validate() = %v, want %q", field, bad, err, want)
			}
		}
	}
}

func TestIsValidHostname(t *testing.T) {
	valid := []string{"a", "example.com", "sip-1.example.com", "xn--bcher-kva.example", "192.0.2.1", strings.Repeat("a", 63) + ".com"}
	for _, h := range valid {
		if !IsValidHostname(h) {
			t.Errorf("IsValidHostname(%q) = false, want true", h)
		}
	}
	invalid := []string{"", "-a.com", "a-.com", "a..com", "example.com.", "a b", "a_b.com", "2001:db8::1", strings.Repeat("a", 64) + ".com", strings.Repeat("a.", 127) + "aa"}
	for _, h := range invalid {
		if IsValidHostname(h) {
			t.Errorf("IsValidHostname(%q) = true, want false", h)
		}
	}
}

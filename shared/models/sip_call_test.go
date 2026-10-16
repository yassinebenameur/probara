package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSIPCallConfigValidate(t *testing.T) {
	const base = `"host":"trunk.example.com","method":"invite"`
	tests := []struct {
		name        string
		config      string
		errContains string // empty: must be valid
	}{
		{name: "number", config: `{` + base + `,"call":{"number":"+33123456789"}}`},
		{name: "extension with letters and marks", config: `{` + base + `,"call":{"number":"ivr-test_1.a~*#"}}`},
		{name: "target uri", config: `{` + base + `,"call":{"target_uri":"sip:+33123456789@carrier.example.com;user=phone"}}`},
		{name: "sips target uri with port", config: `{` + base + `,"transport":"tls","call":{"target_uri":"sips:echo@carrier.example.com:5061"}}`},
		{name: "ipv6 target uri", config: `{` + base + `,"call":{"target_uri":"sip:100@[2001:db8::1]:5060"}}`},
		{name: "method is case-insensitive", config: `{"host":"trunk.example.com","method":"INVITE","call":{"number":"100"}}`},
		{name: "caller id", config: `{` + base + `,"call":{"number":"100","from_user":"+15550100","from_display":"Probara probe","from_domain":"probes.example.com"}}`},
		{name: "answer mode with hold and thresholds", config: `{` + base + `,"call":{"number":"100","mode":"answer","hold_ms":10000,"warn_pdd_ms":3000,"max_pdd_ms":5000,"warn_answer_ms":8000,"max_answer_ms":12000}}`},
		{name: "ring mode with pdd thresholds", config: `{` + base + `,"call":{"number":"100","mode":"ring","warn_pdd_ms":3000,"max_pdd_ms":5000}}`},
		{name: "busy line expected", config: `{` + base + `,"expected_status":486,"call":{"number":"100"}}`},
		{name: "sdp address and probe header", config: `{` + base + `,"call":{"number":"100","sdp_address":"198.51.100.7","probe_header":true}}`},
		{name: "null media is absent", config: `{` + base + `,"call":{"number":"100","media":null}}`},
		{name: "digest credentials", config: `{` + base + `,"username":"trunk","password":"***","call":{"number":"100"}}`},

		{name: "no callee", config: `{` + base + `,"call":{}}`, errContains: "call.number or call.target_uri is required"},
		{name: "both callees", config: `{` + base + `,"call":{"number":"100","target_uri":"sip:100@example.com"}}`, errContains: "mutually exclusive"},
		{name: "number with space", config: `{` + base + `,"call":{"number":"1 00"}}`, errContains: "call.number must be"},
		{name: "number with at sign", config: `{` + base + `,"call":{"number":"100@evil.example.com"}}`, errContains: "call.number must be"},
		{name: "number with semicolon", config: `{` + base + `,"call":{"number":"100;maddr=10.0.0.1"}}`, errContains: "call.number must be"},
		{name: "number too long", config: `{` + base + `,"call":{"number":"` + strings.Repeat("1", 65) + `"}}`, errContains: "call.number must be"},
		{name: "target uri http", config: `{` + base + `,"call":{"target_uri":"http://example.com"}}`, errContains: "must start with sip: or sips:"},
		{name: "target uri without user", config: `{` + base + `,"call":{"target_uri":"sip:example.com"}}`, errContains: "must have a user part"},
		{name: "target uri with headers", config: `{` + base + `,"call":{"target_uri":"sip:100@example.com?Route=%3Csip:evil%3E"}}`, errContains: "without headers"},
		{name: "target uri with maddr", config: `{` + base + `,"call":{"target_uri":"sip:100@example.com;maddr=10.0.0.1"}}`, errContains: "maddr"},
		{name: "target uri bad host", config: `{` + base + `,"call":{"target_uri":"sip:100@bad_host"}}`, errContains: "host must be a valid hostname or IP address"},
		{name: "target uri bad port", config: `{` + base + `,"call":{"target_uri":"sip:100@example.com:0"}}`, errContains: "port must be between 1 and 65535"},
		{name: "target uri too long", config: `{` + base + `,"call":{"target_uri":"sip:100@` + strings.Repeat("a.", 260) + `com"}}`, errContains: "at most 512 bytes"},
		{name: "from user bad charset", config: `{` + base + `,"call":{"number":"100","from_user":"a b"}}`, errContains: "call.from_user must be"},
		{name: "from display with quote", config: `{` + base + `,"call":{"number":"100","from_display":"a\"b"}}`, errContains: "call.from_display must be printable"},
		{name: "from display with backslash", config: `{` + base + `,"call":{"number":"100","from_display":"a\\b"}}`, errContains: "call.from_display must be printable"},
		{name: "from display too long", config: `{` + base + `,"call":{"number":"100","from_display":"` + strings.Repeat("a", 65) + `"}}`, errContains: "at most 64 characters"},
		{name: "from domain bad", config: `{` + base + `,"call":{"number":"100","from_domain":"bad domain"}}`, errContains: "call.from_domain must be"},
		{name: "sdp address bad", config: `{` + base + `,"call":{"number":"100","sdp_address":"not an ip"}}`, errContains: "call.sdp_address must be"},
		{name: "unknown mode", config: `{` + base + `,"call":{"number":"100","mode":"voicemail"}}`, errContains: "call.mode must be"},
		{name: "hold too long", config: `{` + base + `,"call":{"number":"100","hold_ms":10001}}`, errContains: "call.hold_ms must be between 0 and 10000"},
		{name: "negative hold", config: `{` + base + `,"call":{"number":"100","hold_ms":-1}}`, errContains: "call.hold_ms must be between 0 and 10000"},
		{name: "hold in ring mode", config: `{` + base + `,"call":{"number":"100","mode":"ring","hold_ms":1000}}`, errContains: "not allowed in ring mode"},
		{name: "answer thresholds in ring mode", config: `{` + base + `,"call":{"number":"100","mode":"ring","max_answer_ms":5000}}`, errContains: "not allowed in ring mode"},
		{name: "zero pdd threshold", config: `{` + base + `,"call":{"number":"100","warn_pdd_ms":0}}`, errContains: "call.warn_pdd_ms must be greater than 0"},
		{name: "warn not below max", config: `{` + base + `,"call":{"number":"100","warn_answer_ms":5000,"max_answer_ms":5000}}`, errContains: "call.warn_answer_ms must be less than call.max_answer_ms"},
		{name: "provisional expected status", config: `{` + base + `,"expected_status":180,"call":{"number":"100"}}`, errContains: "final response (200-699)"},
		{name: "expected status in ring mode", config: `{` + base + `,"expected_status":200,"call":{"number":"100","mode":"ring"}}`, errContains: "expected_status is not allowed in ring mode"},
		{name: "media reserved", config: `{` + base + `,"call":{"number":"100","media":{"mos":true}}}`, errContains: "media analysis is not available yet"},
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

// The call's string fields reach the INVITE's Request-URI, From and SDP, so
// CR, LF and NUL are refused in each.
func TestSIPCallConfigValidateRejectsInjectionCharacters(t *testing.T) {
	fields := map[string]func(*SIPCallConfig, string){
		"call.number":       func(c *SIPCallConfig, v string) { c.Number += v },
		"call.from_user":    func(c *SIPCallConfig, v string) { c.FromUser += v },
		"call.from_display": func(c *SIPCallConfig, v string) { c.FromDisplay += v },
		"call.from_domain":  func(c *SIPCallConfig, v string) { c.FromDomain += v },
		"call.mode":         func(c *SIPCallConfig, v string) { c.Mode += v },
		"call.sdp_address":  func(c *SIPCallConfig, v string) { c.SDPAddress += v },
	}
	for field, set := range fields {
		for _, bad := range []string{"\r", "\n", "\x00", "\r\nX-Injected: 1"} {
			call := &SIPCallConfig{Number: "100", FromUser: "probe", FromDisplay: "Probe", FromDomain: "example.com", Mode: "answer", SDPAddress: "192.0.2.1"}
			set(call, bad)
			cfg := SIPMonitorConfig{Host: "trunk.example.com", Method: "invite", Call: call}
			want := field + " must not contain CR, LF or NUL characters"
			if err := cfg.Validate(); err == nil || err.Error() != want {
				t.Errorf("%s + %q: Validate() = %v, want %q", field, bad, err, want)
			}
		}
	}
	cfg := SIPMonitorConfig{Host: "trunk.example.com", Method: "invite", Call: &SIPCallConfig{TargetURI: "sip:100@example.com\r\nX: 1"}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "call.target_uri must not contain CR, LF or NUL") {
		t.Errorf("target_uri with CRLF: Validate() = %v", err)
	}
}

func TestSIPMonitorConfigValidateSchedule(t *testing.T) {
	call := func(hold int) SIPMonitorConfig {
		return SIPMonitorConfig{Host: "trunk.example.com", Method: "invite", Call: &SIPCallConfig{Number: "100", HoldMs: hold}}
	}
	tests := []struct {
		name              string
		cfg               SIPMonitorConfig
		interval, timeout int
		errContains       string
	}{
		{name: "options has no extra rules", cfg: SIPMonitorConfig{Host: "x.example.com"}, interval: 10, timeout: 60},
		{name: "call at the bounds", cfg: call(0), interval: 60, timeout: 20},
		{name: "call with hold that fits", cfg: call(10000), interval: 300, timeout: 12},
		{name: "interval below the floor", cfg: call(0), interval: 59, timeout: 10, errContains: "interval_seconds must be at least 60"},
		{name: "timeout above the cap", cfg: call(0), interval: 60, timeout: 21, errContains: "timeout_seconds must be at most 20"},
		{name: "hold leaves no answer budget", cfg: call(9000), interval: 60, timeout: 10, errContains: "must fit in timeout_seconds"},
		{name: "method alone marks a call", cfg: SIPMonitorConfig{Host: "x.example.com", Method: "INVITE"}, interval: 30, timeout: 10, errContains: "interval_seconds must be at least 60"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.ValidateSchedule(tt.interval, tt.timeout)
			if tt.errContains == "" {
				if err != nil {
					t.Fatalf("ValidateSchedule() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.errContains) {
				t.Fatalf("ValidateSchedule() = %v, want error containing %q", err, tt.errContains)
			}
		})
	}
}

func TestIsSIPCallConfig(t *testing.T) {
	cases := []struct {
		typ, config string
		want        bool
	}{
		{"sip", `{"host":"x","method":"invite","call":{"number":"1"}}`, true},
		{"sip", `{"host":"x","method":"Invite"}`, true},
		{"sip", `{"host":"x","call":{"number":"1"}}`, true},
		{"sip", `{"host":"x","method":"options"}`, false},
		{"sip", `{"host":"x","password":"enc:v1:…"}`, false},
		{"sip", `not json`, false},
		{"http", `{"method":"invite"}`, false},
		{"sip", ``, false},
	}
	for _, c := range cases {
		if got := IsSIPCallConfig(c.typ, json.RawMessage(c.config)); got != c.want {
			t.Errorf("IsSIPCallConfig(%s, %s) = %v, want %v", c.typ, c.config, got, c.want)
		}
	}
}

func TestParseSIPCallURI(t *testing.T) {
	u, err := ParseSIPCallURI("SIPS:+331234:pw;isub=1@[2001:db8::1]:5061;user=phone;transport=tcp")
	if err != nil {
		t.Fatalf("ParseSIPCallURI() error = %v", err)
	}
	if u.Scheme != "sips" || u.User != "+331234" || u.Host != "2001:db8::1" || u.Port != 5061 || u.Params != ";user=phone;transport=tcp" {
		t.Fatalf("ParseSIPCallURI() = %+v", u)
	}
	if _, err := ParseSIPCallURI("sip:100@example.com:5060:1"); err == nil {
		t.Fatal("ParseSIPCallURI(two ports) = nil, want error")
	}
	if _, err := ParseSIPCallURI("sip:100@[192.0.2.1]"); err == nil {
		t.Fatal("ParseSIPCallURI(bracketed IPv4) = nil, want error")
	}
}

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
		{name: "target uri escaped hash", config: `{` + base + `,"call":{"target_uri":"sip:ivr%23@carrier.example.com"}}`},
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
		{name: "target uri phone-context user param", config: `{` + base + `,"call":{"target_uri":"sip:0123;phone-context=+44@carrier.example.com"}}`, errContains: "must not carry ;parameters or a :password"},
		{name: "target uri password", config: `{` + base + `,"call":{"target_uri":"sip:+33123:+449999@carrier.example.com"}}`, errContains: "must not carry ;parameters or a :password"},
		{name: "target uri escaped semicolon", config: `{` + base + `,"call":{"target_uri":"sip:0123%3Bphone-context=+44@carrier.example.com"}}`, errContains: "user part must be 1-64 characters"},
		{name: "target uri raw hash", config: `{` + base + `,"call":{"target_uri":"sip:100#@carrier.example.com"}}`, errContains: "user part must be 1-64 characters"},
		{name: "target uri bad escape", config: `{` + base + `,"call":{"target_uri":"sip:100%2@carrier.example.com"}}`, errContains: "user part must be 1-64 characters"},
		{name: "sips target uri over udp", config: `{` + base + `,"transport":"udp","call":{"target_uri":"sips:echo@carrier.example.com"}}`, errContains: "a sips: URI requires transport tls"},
		{name: "sips target uri with transport unset", config: `{` + base + `,"call":{"target_uri":"sips:echo@carrier.example.com"}}`, errContains: "a sips: URI requires transport tls"},
		{name: "sips target uri under rfc3263 auto transport", config: `{` + base + `,"resolution":"rfc3263","call":{"target_uri":"sips:echo@carrier.example.com"}}`, errContains: "a sips: URI requires transport tls"},
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
		{name: "media", config: `{` + base + `,"call":{"number":"100","hold_ms":3000,"media":{"expect":"audio","max_loss_pct":2}}}`},
		{name: "media with a 2xx expected status", config: `{` + base + `,"expected_status":200,"call":{"number":"100","hold_ms":3000,"media":{"expect":"echo"}}}`},
		{name: "media unknown key", config: `{` + base + `,"call":{"number":"100","hold_ms":3000,"media":{"expect":"audio","mos":true}}}`, errContains: `call.media: unknown key "mos"`},
		{name: "media in ring mode", config: `{` + base + `,"call":{"number":"100","mode":"ring","media":{"expect":"none"}}}`, errContains: "call.media requires answer mode"},
		{name: "media with a short hold", config: `{` + base + `,"call":{"number":"100","hold_ms":2999,"media":{"expect":"audio"}}}`, errContains: "call.hold_ms must be at least 3000 when call.media is set"},
		{name: "media without a hold", config: `{` + base + `,"call":{"number":"100","media":{"expect":"none"}}}`, errContains: "call.hold_ms must be at least 3000"},
		{name: "media two way", config: `{` + base + `,"call":{"number":"100","hold_ms":6000,"media":{"expect":"two_way"}}}`},
		{name: "media two way with the media floor only", config: `{` + base + `,"call":{"number":"100","hold_ms":3000,"media":{"expect":"two_way"}}}`, errContains: "call.hold_ms must be at least 6000 when call.media.expect is two_way"},
		{name: "media with a busy expected status", config: `{` + base + `,"expected_status":486,"call":{"number":"100","hold_ms":3000,"media":{"expect":"audio"}}}`, errContains: "expected_status must be unset or 2xx"},
		{name: "domain", config: `{` + base + `,"domain":"carrier.example.com","call":{"number":"100"}}`},
		{name: "domain ip", config: `{` + base + `,"domain":"203.0.113.9","call":{"number":"100"}}`},
		{name: "domain with maddr", config: `{` + base + `,"domain":"carrier.example.com;maddr=203.0.113.9","call":{"number":"100"}}`, errContains: "domain must be a valid hostname or IP address"},
		{name: "domain with headers", config: `{` + base + `,"domain":"carrier.example.com?X=1","call":{"number":"100"}}`, errContains: "domain must be a valid hostname"},
		{name: "domain with a second name-addr", config: `{` + base + `,"domain":"carrier.example.com>, <sip:x@y","call":{"number":"100"}}`, errContains: "domain must be a valid hostname"},
		{name: "domain with a space", config: `{` + base + `,"domain":"carrier example.com","call":{"number":"100"}}`, errContains: "domain must be a valid hostname"},
		{name: "domain with a quote", config: `{` + base + `,"domain":"carrier\".example.com","call":{"number":"100"}}`, errContains: "domain must be a valid hostname"},
		{name: "domain with a port", config: `{` + base + `,"domain":"carrier.example.com:5060","call":{"number":"100"}}`, errContains: "domain must be a valid hostname"},
		// OPTIONS/REGISTER keep accepting what they always stored.
		{name: "options domain is not a host", config: `{"host":"trunk.example.com","domain":"carrier.example.com:5060"}`},
		{name: "register domain is not a host", config: `{"host":"trunk.example.com","method":"register","domain":"carrier.example.com;x=y"}`},
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
		// Case variants: the struct decode keeps the last spelling, the
		// stored form may keep another; any spelling of a call counts.
		{"sip", caseVariantCall, true},
		{"sip", `{"host":"x","method":"options","METHOD":" invite "}`, true},
		{"sip", `{"host":"x","Call":{"number":"1"},"call":null}`, true},
		{"sip", `{"host":"x","Call":null}`, false},
		{"sip", `{"host":42,"Method":"invite"}`, true}, // struct decode fails, the key scan still sees it
	}
	for _, c := range cases {
		if got := IsSIPCallConfig(c.typ, json.RawMessage(c.config)); got != c.want {
			t.Errorf("IsSIPCallConfig(%s, %s) = %v, want %v", c.typ, c.config, got, c.want)
		}
	}
}

// caseVariantCall decodes straight into SIPMonitorConfig as an OPTIONS probe
// (the later "Method"/"Call" win), but reads back as a test call once stored
// through a map, as the secrets transform stores configs.
const caseVariantCall = `{"host":"trunk.example.com","method":"invite","call":{"number":"+33123"},"Method":"options","Call":null}`

func TestCaseVariantKeysRoundTripToACall(t *testing.T) {
	var direct SIPMonitorConfig
	if err := json.Unmarshal([]byte(caseVariantCall), &direct); err != nil {
		t.Fatal(err)
	}
	if direct.IsCall() {
		t.Fatalf("direct decode = %+v; the premise is that it reads as a probe", direct)
	}
	var asMap map[string]any
	if err := json.Unmarshal([]byte(caseVariantCall), &asMap); err != nil {
		t.Fatal(err)
	}
	stored, _ := json.Marshal(asMap)
	var readBack SIPMonitorConfig
	if err := json.Unmarshal(stored, &readBack); err != nil {
		t.Fatal(err)
	}
	if !readBack.IsCall() {
		t.Fatalf("stored form %s reads back as %+v; the premise is that it reads as a call", stored, readBack)
	}
	// So the API refuses it up front, and every gate treats it as a call.
	if _, err := ParseSIPMonitorConfig(json.RawMessage(caseVariantCall)); err == nil || !strings.Contains(err.Error(), `config key "Method" must be spelled "method"`) &&
		!strings.Contains(err.Error(), `config key "Call" must be spelled "call"`) {
		t.Fatalf("ParseSIPMonitorConfig() = %v, want a case-variant key refused", err)
	}
	if err := ValidateSIPSchedule(json.RawMessage(caseVariantCall), 30, 10); err == nil || !strings.Contains(err.Error(), "at least 60") {
		t.Fatalf("ValidateSIPSchedule() = %v, want the call floor", err)
	}
}

func TestParseSIPMonitorConfig(t *testing.T) {
	for _, tc := range []struct {
		config, wantErr string
	}{
		{`{"host":"x.example.com","method":"options","x_unknown":1}`, ""},
		{`{"host":"x.example.com","method":"invite","call":{"number":"1"}}`, ""},
		{`{"host":"x.example.com","Host":"y.example.com"}`, `config key "Host" must be spelled "host"`},
		{`{"host":"x.example.com","TLS_SERVER_NAME":"y"}`, `config key "TLS_SERVER_NAME" must be spelled "tls_server_name"`},
		{`{"host":"x.example.com","method":"invite","call":{"number":"1","Number":"2"}}`, `call: config key "Number" must be spelled "number"`},
		{`{"host":42}`, "cannot unmarshal"},
	} {
		_, err := ParseSIPMonitorConfig(json.RawMessage(tc.config))
		if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("ParseSIPMonitorConfig(%s) = %v, want %q", tc.config, err, tc.wantErr)
		}
	}
}

func TestParseSIPCallURI(t *testing.T) {
	u, err := ParseSIPCallURI("SIPS:%2B331234@[2001:db8::1]:5061;user=phone;transport=tcp")
	if err != nil {
		t.Fatalf("ParseSIPCallURI() error = %v", err)
	}
	if u.Scheme != "sips" || u.User != "%2B331234" || u.DecodedUser != "+331234" || u.Host != "2001:db8::1" || u.Port != 5061 || u.Params != ";user=phone;transport=tcp" {
		t.Fatalf("ParseSIPCallURI() = %+v", u)
	}
	for _, raw := range []string{
		"sip:100@example.com:5060:1", // two ports
		"sip:100@[192.0.2.1]",        // bracketed IPv4
		// The userinfo is sent as written, so what the allow-list matches
		// (DecodedUser) must be all of it: no user parameters, no password.
		"sip:0123;phone-context=+44@trunk.example.com",
		"sip:+33123:+449999@trunk.example.com",
		"sip:%3Bx@trunk.example.com", // an escaped ';' decodes outside the charset
	} {
		if _, err := ParseSIPCallURI(raw); err == nil {
			t.Errorf("ParseSIPCallURI(%q) = nil, want error", raw)
		}
	}
}

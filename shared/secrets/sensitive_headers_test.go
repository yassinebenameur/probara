package secrets

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestIsSensitiveHeaderName_SharedFixture pins the Go twin against the
// fixture web/lib/monitor-form-http.test.ts also reads, so the form's
// default-secret rule and the API's write-path normalization cannot drift.
func TestIsSensitiveHeaderName_SharedFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/sensitive_header_names.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture struct {
		Sensitive []string `json:"sensitive"`
		Plain     []string `json:"plain"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if len(fixture.Sensitive) == 0 || len(fixture.Plain) == 0 {
		t.Fatal("fixture is empty")
	}
	for _, name := range fixture.Sensitive {
		if !IsSensitiveHeaderName(name) {
			t.Errorf("IsSensitiveHeaderName(%q) = false, want true", name)
		}
	}
	for _, name := range fixture.Plain {
		if IsSensitiveHeaderName(name) {
			t.Errorf("IsSensitiveHeaderName(%q) = true, want false", name)
		}
	}
}

func TestNormalizeSensitiveHeaders_HTTP(t *testing.T) {
	in := json.RawMessage(`{"url":"https://api.example.test","method":"GET",
		"headers":{"Accept":"application/json","Authorization":"Bearer plain","X-Api-Key":"k1"},
		"secret_headers":{"authorization":"enc:v1:old","X-Other":"enc:v1:other"}}`)
	out, changed, err := NormalizeSensitiveHeaders("http", in)
	if err != nil || !changed {
		t.Fatalf("NormalizeSensitiveHeaders: changed=%v err=%v", changed, err)
	}
	var cfg struct {
		Headers       map[string]string `json:"headers"`
		SecretHeaders map[string]string `json:"secret_headers"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cfg.Headers) != 1 || cfg.Headers["Accept"] != "application/json" {
		t.Fatalf("plain headers = %v, want only Accept", cfg.Headers)
	}
	want := map[string]string{"Authorization": "Bearer plain", "X-Api-Key": "k1", "X-Other": "enc:v1:other"}
	if len(cfg.SecretHeaders) != len(want) {
		t.Fatalf("secret headers = %v, want %v (a moved header replaces its case-insensitive twin)", cfg.SecretHeaders, want)
	}
	for k, v := range want {
		if cfg.SecretHeaders[k] != v {
			t.Fatalf("secret_headers[%q] = %q, want %q", k, cfg.SecretHeaders[k], v)
		}
	}

	// Nothing sensitive: untouched, byte for byte.
	plain := json.RawMessage(`{"url":"https://x.test","method":"GET","headers":{"Accept":"*/*"}}`)
	out, changed, err = NormalizeSensitiveHeaders("http", plain)
	if err != nil || changed || string(out) != string(plain) {
		t.Fatalf("plain config rewritten: changed=%v err=%v out=%s", changed, err, out)
	}

	// Other types pass through even with sensitive-looking keys.
	ws := json.RawMessage(`{"url":"wss://x.test","headers":{"Authorization":"x"}}`)
	if out, changed, _ := NormalizeSensitiveHeaders("websocket", ws); changed || string(out) != string(ws) {
		t.Fatalf("websocket config rewritten: %s", out)
	}
}

func TestNormalizeSensitiveHeaders_SyntheticAPI(t *testing.T) {
	in := json.RawMessage(`{"variables":{"login_authorization":"taken"},"steps":[
		{"id":"login","request":{"method":"POST","url":"https://x.test/login",
			"headers":{"Authorization":"Bearer literal","X-Trace":"abc","X-Api-Key":"{{api_key}}"}}},
		{"id":"me","request":{"method":"GET","url":"https://x.test/me","headers":{"Cookie":"sid=1"}}}]}`)
	out, changed, err := NormalizeSensitiveHeaders("synthetic_api", in)
	if err != nil || !changed {
		t.Fatalf("NormalizeSensitiveHeaders: changed=%v err=%v", changed, err)
	}
	var cfg struct {
		SecretVariables map[string]string `json:"secret_variables"`
		Steps           []struct {
			Request struct {
				Headers map[string]string `json:"headers"`
			} `json:"request"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// login_authorization is taken by a plain variable, so the generated
	// name gets a suffix.
	if got := cfg.Steps[0].Request.Headers["Authorization"]; got != "{{login_authorization_2}}" {
		t.Fatalf("login Authorization = %q", got)
	}
	if cfg.SecretVariables["login_authorization_2"] != "Bearer literal" {
		t.Fatalf("secret_variables = %v", cfg.SecretVariables)
	}
	if got := cfg.Steps[0].Request.Headers["X-Api-Key"]; got != "{{api_key}}" {
		t.Fatalf("a templated sensitive header must stay as written, got %q", got)
	}
	if got := cfg.Steps[0].Request.Headers["X-Trace"]; got != "abc" {
		t.Fatalf("plain header changed: %q", got)
	}
	if got := cfg.Steps[1].Request.Headers["Cookie"]; got != "{{me_cookie}}" || cfg.SecretVariables["me_cookie"] != "sid=1" {
		t.Fatalf("me Cookie = %q, secret_variables = %v", got, cfg.SecretVariables)
	}

	// Idempotent: a second pass finds only references.
	again, changed, err := NormalizeSensitiveHeaders("synthetic_api", out)
	if err != nil || changed || string(again) != string(out) {
		t.Fatalf("second pass changed the config: changed=%v err=%v", changed, err)
	}
}

func TestHasSecretValues(t *testing.T) {
	cases := []struct {
		monitorType string
		raw         string
		want        bool
	}{
		{"http", `{"url":"https://x.test","method":"GET","headers":{"Accept":"*/*"}}`, false},
		{"http", `{"url":"https://x.test","secret_headers":{"Authorization":"enc"}}`, true},
		{"http", `{"url":"https://x.test","basic_auth_username":"u","basic_auth_password":"p"}`, true},
		{"grpc", `{"host":"x","metadata":{"a":"b"}}`, false},
		{"grpc", `{"host":"x","secret_metadata":{"a":"b"}}`, true},
		{"synthetic_api", `{"variables":{"a":"b"},"steps":[]}`, false},
		{"synthetic_browser", `{"secret_variables":{"a":"b"},"steps":[]}`, true},
		{"ping", `{"host":"secret_headers"}`, false},
		{"redis", `{"host":"x"}`, false},
		{"redis", `{"host":"x","password":"p"}`, true},
		{"http", ``, false},
		// An escaped key cannot hide a secret from the fast path.
		{"http", `{"basic_auth_p\u0061ssword":"p"}`, true},
	}
	for _, tc := range cases {
		if got := HasSecretValues(tc.monitorType, json.RawMessage(tc.raw)); got != tc.want {
			t.Errorf("HasSecretValues(%s, %s) = %v, want %v", tc.monitorType, tc.raw, got, tc.want)
		}
	}
}

// TestRequestSecretFieldsLifecycle runs the write-only contract over each
// field this registry gained for request credentials: encrypt on write, mask
// on read, "***" keeps, "" clears, a map is edited per key.
func TestRequestSecretFieldsLifecycle(t *testing.T) {
	enc := monitorTestEncryptor(t)
	cases := []struct {
		monitorType string
		create      string
		update      string // round-trips the masked read, rotating/clearing parts
		wantPlain   map[string]string
		wantAbsent  []string
	}{
		{
			monitorType: "http",
			create:      `{"url":"https://x.test","method":"GET","basic_auth_username":"ops","basic_auth_password":"pw1","secret_headers":{"Authorization":"Bearer a","X-Api-Key":"k"}}`,
			update:      `{"url":"https://x.test","method":"GET","basic_auth_username":"ops","basic_auth_password":"***","secret_headers":{"Authorization":"***","X-Api-Key":""}}`,
			wantPlain:   map[string]string{"basic_auth_password": "pw1", "secret_headers.Authorization": "Bearer a"},
			wantAbsent:  []string{"secret_headers.X-Api-Key"},
		},
		{
			monitorType: "http",
			create:      `{"url":"https://x.test","method":"GET","basic_auth_username":"ops","basic_auth_password":"pw1"}`,
			update:      `{"url":"https://x.test","method":"GET","basic_auth_password":""}`,
			wantAbsent:  []string{"basic_auth_password"},
		},
		{
			monitorType: "grpc",
			create:      `{"host":"g.test","metadata":{"x-tenant":"t1"},"secret_metadata":{"authorization":"Bearer g"}}`,
			update:      `{"host":"g.test","metadata":{"x-tenant":"t2"}}`,
			wantPlain:   map[string]string{"secret_metadata.authorization": "Bearer g"},
		},
		{
			monitorType: "synthetic_api",
			create:      `{"steps":[],"secret_variables":{"token":"t0","old":"o"}}`,
			update:      `{"steps":[],"secret_variables":{"token":"***","new":"n"}}`,
			wantPlain:   map[string]string{"secret_variables.token": "t0", "secret_variables.new": "n"},
			wantAbsent:  []string{"secret_variables.old"},
		},
		{
			monitorType: "synthetic_browser",
			create:      `{"start_url":"https://x.test","steps":[],"secret_variables":{"password":"pw"}}`,
			update:      `{"start_url":"https://x.test","steps":[],"secret_variables":{}}`,
			wantAbsent:  []string{"secret_variables.password"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.monitorType, func(t *testing.T) {
			created, err := MergeMonitorConfigSecrets(tc.monitorType, json.RawMessage(tc.create), nil)
			if err != nil {
				t.Fatalf("merge create: %v", err)
			}
			stored, err := EncryptMonitorConfig(enc, tc.monitorType, created)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			masked, err := MaskMonitorConfig(tc.monitorType, stored)
			if err != nil {
				t.Fatalf("mask: %v", err)
			}
			for _, leak := range []string{"pw1", "Bearer a", "Bearer g", "t0", `"pw"`, `\"alg\"`} {
				if strings.Contains(string(masked), leak) {
					t.Fatalf("masked read leaks %q: %s", leak, masked)
				}
			}

			merged, err := MergeMonitorConfigSecrets(tc.monitorType, json.RawMessage(tc.update), stored)
			if err != nil {
				t.Fatalf("merge update: %v", err)
			}
			restored, err := EncryptMonitorConfig(enc, tc.monitorType, merged)
			if err != nil {
				t.Fatalf("encrypt update: %v", err)
			}
			plain, err := DecryptMonitorConfig(enc, tc.monitorType, restored)
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}
			var cfg map[string]any
			if err := json.Unmarshal(plain, &cfg); err != nil {
				t.Fatalf("decode: %v", err)
			}
			for path, want := range tc.wantPlain {
				if got := lookupPath(cfg, path); got != want {
					t.Fatalf("%s = %v, want %q (config %s)", path, got, want, plain)
				}
			}
			for _, path := range tc.wantAbsent {
				if got := lookupPath(cfg, path); got != nil {
					t.Fatalf("%s = %v, want it cleared", path, got)
				}
			}
		})
	}
}

func lookupPath(cfg map[string]any, path string) any {
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			inner, ok := cfg[path[:i]].(map[string]any)
			if !ok {
				return nil
			}
			return inner[path[i+1:]]
		}
	}
	return cfg[path]
}

func TestNormalizeSensitiveHeaders_GRPCMetadata(t *testing.T) {
	in := json.RawMessage(`{"host":"g.test","metadata":{"x-tenant":"t1","authorization":"Bearer g","X-Api-Key":"k"},"secret_metadata":{"Authorization":"enc-old"}}`)
	out, changed, err := NormalizeSensitiveHeaders("grpc", in)
	if err != nil || !changed {
		t.Fatalf("NormalizeSensitiveHeaders: changed=%v err=%v", changed, err)
	}
	var cfg struct {
		Metadata       map[string]string `json:"metadata"`
		SecretMetadata map[string]string `json:"secret_metadata"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cfg.Metadata) != 1 || cfg.Metadata["x-tenant"] != "t1" {
		t.Fatalf("metadata = %v, want only x-tenant", cfg.Metadata)
	}
	if len(cfg.SecretMetadata) != 2 || cfg.SecretMetadata["authorization"] != "Bearer g" || cfg.SecretMetadata["X-Api-Key"] != "k" {
		t.Fatalf("secret_metadata = %v, want the moved keys replacing their case-insensitive twin", cfg.SecretMetadata)
	}
	again, changed, _ := NormalizeSensitiveHeaders("grpc", out)
	if changed || string(again) != string(out) {
		t.Fatalf("second pass changed the config: %s", again)
	}
}

func TestMoveSensitiveVariables(t *testing.T) {
	in := json.RawMessage(`{"start_url":"https://x.test","variables":{"username":"u","password":"pw","api_token":"","x":"1"},"secret_variables":{"session_secret":"enc"}}`)
	out, changed, err := MoveSensitiveVariables("synthetic_browser", in)
	if err != nil || !changed {
		t.Fatalf("MoveSensitiveVariables: changed=%v err=%v", changed, err)
	}
	var cfg struct {
		Variables       map[string]string `json:"variables"`
		SecretVariables map[string]string `json:"secret_variables"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// An empty credential-named value has nothing to protect and stays.
	if len(cfg.Variables) != 3 || cfg.Variables["username"] != "u" || cfg.Variables["x"] != "1" || cfg.Variables["api_token"] != "" {
		t.Fatalf("variables = %v", cfg.Variables)
	}
	if cfg.SecretVariables["password"] != "pw" || cfg.SecretVariables["session_secret"] != "enc" {
		t.Fatalf("secret_variables = %v", cfg.SecretVariables)
	}

	// A name already secret keeps its secret value; the plain copy goes.
	both := json.RawMessage(`{"steps":[],"variables":{"password":"plain"},"secret_variables":{"password":"enc"}}`)
	out, changed, _ = MoveSensitiveVariables("synthetic_api", both)
	if !changed || strings.Contains(string(out), "plain") || !strings.Contains(string(out), `"password":"enc"`) {
		t.Fatalf("collision handling: changed=%v out=%s", changed, out)
	}

	if _, changed, _ := MoveSensitiveVariables("synthetic_api", out); changed {
		t.Fatal("second pass changed the config")
	}
	if _, changed, _ := MoveSensitiveVariables("http", in); changed {
		t.Fatal("non-synthetic type changed")
	}
}

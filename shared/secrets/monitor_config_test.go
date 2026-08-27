package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func monitorTestEncryptor(t *testing.T) *AESGCMEncryptor {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	t.Setenv("PROBARA_SECRETS_KEY", base64.StdEncoding.EncodeToString(key))
	kp, err := NewEnvKeyProvider()
	if err != nil {
		t.Fatalf("NewEnvKeyProvider: %v", err)
	}
	return NewAESGCMEncryptor(kp)
}

func configMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return m
}

func TestEnvKeyProvider_Rotation(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	rand.Read(key1)
	rand.Read(key2)
	t.Setenv("PROBARA_SECRETS_KEY", base64.StdEncoding.EncodeToString(key1))

	// Single key: v1 is current.
	kp, err := NewEnvKeyProvider()
	if err != nil {
		t.Fatalf("NewEnvKeyProvider: %v", err)
	}
	encV1 := NewAESGCMEncryptor(kp)
	ct, err := encV1.Encrypt("secret")
	if err != nil {
		t.Fatalf("encrypt v1: %v", err)
	}
	if v, ok := EnvelopeVersion(ct); !ok || v != 1 {
		t.Fatalf("envelope version = %d (%v), want 1", v, ok)
	}

	// Add v2: new writes use v2, old v1 envelopes still decrypt.
	t.Setenv("PROBARA_SECRETS_KEY_V2", base64.StdEncoding.EncodeToString(key2))
	kp2, err := NewEnvKeyProvider()
	if err != nil {
		t.Fatalf("NewEnvKeyProvider with v2: %v", err)
	}
	encV2 := NewAESGCMEncryptor(kp2)

	plain, err := encV2.Decrypt(ct)
	if err != nil || plain != "secret" {
		t.Fatalf("v1 envelope with rotated provider: %q, %v", plain, err)
	}
	ct2, err := encV2.Encrypt("secret")
	if err != nil {
		t.Fatalf("encrypt v2: %v", err)
	}
	if v, _ := EnvelopeVersion(ct2); v != 2 {
		t.Fatalf("new envelope version = %d, want 2", v)
	}

	// Retire v1 entirely: v2 still works, v1 envelopes fail loudly.
	t.Setenv("PROBARA_SECRETS_KEY", "")
	kp3, err := NewEnvKeyProvider()
	if err != nil {
		t.Fatalf("NewEnvKeyProvider v2-only: %v", err)
	}
	encV2Only := NewAESGCMEncryptor(kp3)
	if _, err := encV2Only.Decrypt(ct2); err != nil {
		t.Fatalf("v2 envelope with v2-only provider: %v", err)
	}
	if _, err := encV2Only.Decrypt(ct); err == nil {
		t.Fatal("v1 envelope should fail after v1 is retired")
	}
}

func TestEncryptDecryptMonitorConfig_RoundTrip(t *testing.T) {
	enc := monitorTestEncryptor(t)
	raw := json.RawMessage(`{"host":"db.internal","port":5432,"username":"probe","password":"s3cret"}`)

	encrypted, err := EncryptMonitorConfig(enc, "postgres", raw)
	if err != nil {
		t.Fatalf("EncryptMonitorConfig: %v", err)
	}
	m := configMap(t, encrypted)
	pw, _ := m["password"].(string)
	if !LooksLikeEnvelope(pw) {
		t.Fatalf("password not encrypted: %q", pw)
	}
	if m["host"] != "db.internal" {
		t.Fatalf("non-secret field changed: %v", m["host"])
	}

	decrypted, err := DecryptMonitorConfig(enc, "postgres", encrypted)
	if err != nil {
		t.Fatalf("DecryptMonitorConfig: %v", err)
	}
	m = configMap(t, decrypted)
	if m["password"] != "s3cret" {
		t.Fatalf("password round-trip failed: %v", m["password"])
	}
}

func TestEncryptMonitorConfig_NoSecretTypePassthrough(t *testing.T) {
	enc := monitorTestEncryptor(t)
	raw := json.RawMessage(`{"url":"https://example.com","method":"GET"}`)

	out, err := EncryptMonitorConfig(enc, "http", raw)
	if err != nil {
		t.Fatalf("EncryptMonitorConfig: %v", err)
	}
	if string(out) != string(raw) {
		t.Fatalf("config for type without secrets was modified: %s", out)
	}
}

func TestEncryptMonitorConfig_EnvelopePassthrough(t *testing.T) {
	enc := monitorTestEncryptor(t)
	first, err := EncryptMonitorConfig(enc, "redis", json.RawMessage(`{"host":"r","password":"pw"}`))
	if err != nil {
		t.Fatalf("first encrypt: %v", err)
	}
	second, err := EncryptMonitorConfig(enc, "redis", first)
	if err != nil {
		t.Fatalf("second encrypt: %v", err)
	}
	if configMap(t, first)["password"] != configMap(t, second)["password"] {
		t.Fatal("already-encrypted value was re-encrypted")
	}
}

func TestMaskMonitorConfig(t *testing.T) {
	raw := json.RawMessage(`{"host":"r","password":"pw","port":6379}`)
	masked, err := MaskMonitorConfig("redis", raw)
	if err != nil {
		t.Fatalf("MaskMonitorConfig: %v", err)
	}
	m := configMap(t, masked)
	if m["password"] != MaskedSecret {
		t.Fatalf("password not masked: %v", m["password"])
	}
	if m["host"] != "r" || m["port"].(float64) != 6379 {
		t.Fatal("non-secret fields changed")
	}

	// Empty secrets stay empty so the UI can tell "no password" from "has one".
	masked, err = MaskMonitorConfig("redis", json.RawMessage(`{"host":"r","password":""}`))
	if err != nil {
		t.Fatalf("MaskMonitorConfig: %v", err)
	}
	if configMap(t, masked)["password"] != "" {
		t.Fatal("empty password should not be masked")
	}
}

func TestMergeMonitorConfigSecrets(t *testing.T) {
	existing := json.RawMessage(`{"host":"old","password":"cipher-blob"}`)

	cases := []struct {
		name     string
		incoming string
		wantPW   any
	}{
		{"masked placeholder keeps existing", `{"host":"new","password":"***"}`, "cipher-blob"},
		// Clearing a secret has exactly one spelling: the empty string. An
		// absent field keeps the stored secret, so a client that submits a
		// config it read back (never containing plaintext secrets) cannot
		// silently destroy the credential.
		{"empty clears", `{"host":"new","password":""}`, nil},
		{"missing keeps existing", `{"host":"new"}`, "cipher-blob"},
		{"new value wins", `{"host":"new","password":"fresh"}`, "fresh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged, err := MergeMonitorConfigSecrets("postgres", json.RawMessage(tc.incoming), existing)
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			m := configMap(t, merged)
			if m["password"] != tc.wantPW {
				t.Fatalf("password = %v, want %v", m["password"], tc.wantPW)
			}
			if m["host"] != "new" {
				t.Fatalf("host = %v, want new", m["host"])
			}
		})
	}

	// Clearing is impossible via placeholder; absent existing secret stays absent.
	merged, err := MergeMonitorConfigSecrets("postgres", json.RawMessage(`{"host":"h","password":"***"}`), json.RawMessage(`{"host":"old"}`))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, ok := configMap(t, merged)["password"]; ok {
		t.Fatal("placeholder with no stored secret should drop the field")
	}
}

// TestMergeMonitorConfigSecrets_ScalarContract pins the full write-only
// contract for a scalar secret field, on update and on create: absent and
// "***" keep the stored secret, "" clears it, anything else replaces it. On
// create there is nothing to keep, so both "keep" spellings drop the field.
func TestMergeMonitorConfigSecrets_ScalarContract(t *testing.T) {
	const stored = `{"host":"old","port":5432,"password":"stored-cipher"}`

	cases := []struct {
		name     string
		incoming string
		existing string
		wantPW   any
		wantAbs  bool
	}{
		{name: "update/absent keeps stored", incoming: `{"host":"new","port":5432}`, existing: stored, wantPW: "stored-cipher"},
		{name: "update/masked keeps stored", incoming: `{"host":"new","password":"***"}`, existing: stored, wantPW: "stored-cipher"},
		{name: "update/empty clears", incoming: `{"host":"new","password":""}`, existing: stored, wantAbs: true},
		{name: "update/new value replaces", incoming: `{"host":"new","password":"rotated"}`, existing: stored, wantPW: "rotated"},

		{name: "create/absent stays absent", incoming: `{"host":"new","port":5432}`, existing: "", wantAbs: true},
		{name: "create/masked stays absent", incoming: `{"host":"new","password":"***"}`, existing: "", wantAbs: true},
		{name: "create/empty stays absent", incoming: `{"host":"new","password":""}`, existing: "", wantAbs: true},
		{name: "create/new value kept", incoming: `{"host":"new","password":"fresh"}`, existing: "", wantPW: "fresh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var existing json.RawMessage
			if tc.existing != "" {
				existing = json.RawMessage(tc.existing)
			}
			merged, err := MergeMonitorConfigSecrets("postgres", json.RawMessage(tc.incoming), existing)
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			m := configMap(t, merged)
			got, present := m["password"]
			if tc.wantAbs {
				if present {
					t.Fatalf("password = %v, want absent", got)
				}
				return
			}
			if got != tc.wantPW {
				t.Fatalf("password = %v, want %v", got, tc.wantPW)
			}
			if m["host"] != "new" {
				t.Fatalf("host = %v, want new (non-secret fields must pass through)", m["host"])
			}
		})
	}
}

// TestMergeMonitorConfigSecrets_MapContract pins the same contract for a map
// secret field. The whole-field rules mirror the scalar ones; per-key rules
// only apply when the field is present, because editing a submitted map is
// explicit intent.
func TestMergeMonitorConfigSecrets_MapContract(t *testing.T) {
	const stored = `{"url":"wss://old.test/socket","headers":{"Authorization":"stored-auth","Cookie":"stored-cookie"}}`

	cases := []struct {
		name        string
		incoming    string
		existing    string
		wantHeaders map[string]string
		wantAbsent  bool
	}{
		{
			name:        "update/field absent keeps stored map",
			incoming:    `{"url":"wss://new.test/socket"}`,
			existing:    stored,
			wantHeaders: map[string]string{"Authorization": "stored-auth", "Cookie": "stored-cookie"},
		},
		{
			name:        "update/all keys masked keeps stored values",
			incoming:    `{"url":"wss://new.test/socket","headers":{"Authorization":"***","Cookie":"***"}}`,
			existing:    stored,
			wantHeaders: map[string]string{"Authorization": "stored-auth", "Cookie": "stored-cookie"},
		},
		{
			name:       "update/empty values clear the whole field",
			incoming:   `{"url":"wss://new.test/socket","headers":{"Authorization":"","Cookie":""}}`,
			existing:   stored,
			wantAbsent: true,
		},
		{
			name:        "update/new values replace",
			incoming:    `{"url":"wss://new.test/socket","headers":{"Authorization":"rotated","Cookie":"fresh"}}`,
			existing:    stored,
			wantHeaders: map[string]string{"Authorization": "rotated", "Cookie": "fresh"},
		},
		{
			name:        "update/present map still drops omitted keys",
			incoming:    `{"url":"wss://new.test/socket","headers":{"Authorization":"***"}}`,
			existing:    stored,
			wantHeaders: map[string]string{"Authorization": "stored-auth"},
		},
		{
			name:       "update/empty object clears the field",
			incoming:   `{"url":"wss://new.test/socket","headers":{}}`,
			existing:   stored,
			wantAbsent: true,
		},

		{name: "create/field absent stays absent", incoming: `{"url":"wss://new.test/socket"}`, existing: "", wantAbsent: true},
		{name: "create/masked keys stay absent", incoming: `{"url":"wss://new.test/socket","headers":{"Authorization":"***"}}`, existing: "", wantAbsent: true},
		{name: "create/empty values stay absent", incoming: `{"url":"wss://new.test/socket","headers":{"Authorization":""}}`, existing: "", wantAbsent: true},
		{
			name:        "create/new values kept",
			incoming:    `{"url":"wss://new.test/socket","headers":{"Authorization":"Bearer t"}}`,
			existing:    "",
			wantHeaders: map[string]string{"Authorization": "Bearer t"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var existing json.RawMessage
			if tc.existing != "" {
				existing = json.RawMessage(tc.existing)
			}
			merged, err := MergeMonitorConfigSecrets("websocket", json.RawMessage(tc.incoming), existing)
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			m := configMap(t, merged)
			raw, present := m["headers"]
			if tc.wantAbsent {
				if present {
					t.Fatalf("headers = %v, want absent", raw)
				}
				return
			}
			if !present {
				t.Fatalf("headers absent, want %v", tc.wantHeaders)
			}
			got, ok := stringMap(raw)
			if !ok {
				t.Fatalf("headers not a string map: %v", raw)
			}
			if len(got) != len(tc.wantHeaders) {
				t.Fatalf("headers = %v, want %v", got, tc.wantHeaders)
			}
			for key, want := range tc.wantHeaders {
				if got[key] != want {
					t.Fatalf("header %q = %q, want %q", key, got[key], want)
				}
			}
			if m["url"] != "wss://new.test/socket" {
				t.Fatalf("url = %v, want the submitted value", m["url"])
			}
		})
	}
}

// TestMergeMonitorConfigSecrets_TagEditKeepsPostgresPassword reproduces the
// production incident: a postgres monitor edited only to change tags submitted
// a config without a `password` key at all (not "***"), which used to drop the
// stored credential and break every subsequent check with a SASL auth failure.
// `password` is optional for the type (connection_string is the alternative),
// so validation could not catch it.
func TestMergeMonitorConfigSecrets_TagEditKeepsPostgresPassword(t *testing.T) {
	existing := json.RawMessage(`{"host":"db.internal","port":5432,"database":"app","username":"probe","ssl_mode":"require","password":"v1:stored-ciphertext"}`)
	incoming := json.RawMessage(`{"host":"db.internal","port":5432,"database":"app","username":"probe","ssl_mode":"require"}`)

	merged, err := MergeMonitorConfigSecrets("postgres", incoming, existing)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	m := configMap(t, merged)
	if m["password"] != "v1:stored-ciphertext" {
		t.Fatalf("password = %v, want the stored ciphertext preserved", m["password"])
	}
	for field, want := range map[string]any{
		"host":     "db.internal",
		"database": "app",
		"username": "probe",
		"ssl_mode": "require",
	} {
		if m[field] != want {
			t.Fatalf("%s = %v, want %v", field, m[field], want)
		}
	}
}

func TestWebSocketHeaderSecretLifecycle(t *testing.T) {
	enc := monitorTestEncryptor(t)
	raw := json.RawMessage(`{"url":"wss://example.test/socket","headers":{"Authorization":"Bearer top-secret","Cookie":"session=abc","Origin":"https://app.test"}}`)

	if !HasMonitorSecrets("websocket") {
		t.Fatal("websocket must be registered as carrying monitor secrets")
	}

	encrypted, err := EncryptMonitorConfig(enc, "websocket", raw)
	if err != nil {
		t.Fatalf("EncryptMonitorConfig: %v", err)
	}
	encryptedHeaders := configMap(t, encrypted)["headers"].(map[string]any)
	for name, plaintext := range map[string]string{
		"Authorization": "Bearer top-secret",
		"Cookie":        "session=abc",
		"Origin":        "https://app.test",
	} {
		ciphertext, ok := encryptedHeaders[name].(string)
		if !ok || !LooksLikeEnvelope(ciphertext) {
			t.Fatalf("header %q was not encrypted: %v", name, encryptedHeaders[name])
		}
		if ciphertext == plaintext {
			t.Fatalf("header %q remained plaintext", name)
		}
	}

	masked, err := MaskMonitorConfig("websocket", encrypted)
	if err != nil {
		t.Fatalf("MaskMonitorConfig: %v", err)
	}
	maskedHeaders := configMap(t, masked)["headers"].(map[string]any)
	for name := range encryptedHeaders {
		if maskedHeaders[name] != MaskedSecret {
			t.Fatalf("header %q = %v, want masked", name, maskedHeaders[name])
		}
	}

	// Per-key placeholders preserve existing ciphertext. Header matching is
	// case-insensitive because HTTP header names are case-insensitive.
	merged, err := MergeMonitorConfigSecrets(
		"websocket",
		json.RawMessage(`{"url":"wss://new.example.test/socket","headers":{"authorization":"***","Cookie":"rotated","X-Removed":"***"}}`),
		encrypted,
	)
	if err != nil {
		t.Fatalf("MergeMonitorConfigSecrets: %v", err)
	}
	mergedHeaders := configMap(t, merged)["headers"].(map[string]any)
	if mergedHeaders["authorization"] != encryptedHeaders["Authorization"] {
		t.Fatal("Authorization placeholder did not preserve stored ciphertext")
	}
	if mergedHeaders["Cookie"] != "rotated" {
		t.Fatalf("replacement header = %v, want rotated", mergedHeaders["Cookie"])
	}
	if _, ok := mergedHeaders["Origin"]; ok {
		t.Fatal("omitted header should be removed")
	}
	if _, ok := mergedHeaders["X-Removed"]; ok {
		t.Fatal("orphaned placeholder should be removed")
	}

	reencrypted, err := EncryptMonitorConfig(enc, "websocket", merged)
	if err != nil {
		t.Fatalf("EncryptMonitorConfig after merge: %v", err)
	}
	decrypted, err := DecryptMonitorConfig(enc, "websocket", reencrypted)
	if err != nil {
		t.Fatalf("DecryptMonitorConfig: %v", err)
	}
	plainHeaders := configMap(t, decrypted)["headers"].(map[string]any)
	if plainHeaders["authorization"] != "Bearer top-secret" || plainHeaders["Cookie"] != "rotated" {
		t.Fatalf("decrypted headers = %v", plainHeaders)
	}
}

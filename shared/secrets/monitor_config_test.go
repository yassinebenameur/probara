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
		// Updates replace the whole config: empty or absent secrets clear,
		// only the explicit placeholder preserves.
		{"empty clears", `{"host":"new","password":""}`, nil},
		{"missing clears", `{"host":"new"}`, nil},
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

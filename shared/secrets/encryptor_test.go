package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

func testEncryptor(t *testing.T) *AESGCMEncryptor {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("gen key: %v", err)
	}
	t.Setenv("PROBARA_SECRETS_KEY", base64.StdEncoding.EncodeToString(key))
	kp, err := NewEnvKeyProvider()
	if err != nil {
		t.Fatalf("NewEnvKeyProvider: %v", err)
	}
	return NewAESGCMEncryptor(kp)
}

func TestAESGCM_RoundTrip(t *testing.T) {
	enc := testEncryptor(t)
	cipher, err := enc.Encrypt("hunter2")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !LooksLikeEnvelope(cipher) {
		t.Fatalf("expected envelope, got %q", cipher)
	}
	plain, err := enc.Decrypt(cipher)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if plain != "hunter2" {
		t.Errorf("plain = %q, want hunter2", plain)
	}
}

func TestAESGCM_DistinctCiphertexts(t *testing.T) {
	enc := testEncryptor(t)
	a, _ := enc.Encrypt("same")
	b, _ := enc.Encrypt("same")
	if a == b {
		t.Fatal("expected distinct ciphertexts due to random nonce")
	}
}

func TestAESGCM_DecryptPlaintextPassthrough(t *testing.T) {
	enc := testEncryptor(t)
	out, err := enc.Decrypt("not-an-envelope")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if out != "not-an-envelope" {
		t.Errorf("out = %q, want passthrough", out)
	}
}

func TestNoOpEncryptor_RefusesEnvelope(t *testing.T) {
	enc := testEncryptor(t)
	cipher, _ := enc.Encrypt("x")

	_, err := NoOpEncryptor{}.Decrypt(cipher)
	if err == nil {
		t.Fatal("expected error decrypting envelope with NoOpEncryptor")
	}
	got, err := NoOpEncryptor{}.Decrypt("plain")
	if err != nil || got != "plain" {
		t.Fatalf("plain passthrough failed: out=%q err=%v", got, err)
	}
}

func TestEnvKeyProvider_MissingVar(t *testing.T) {
	os.Unsetenv("PROBARA_SECRETS_KEY")
	if _, err := NewEnvKeyProvider(); err != ErrKeyNotConfigured {
		t.Fatalf("err = %v, want ErrKeyNotConfigured", err)
	}
}

func TestEnvKeyProvider_WrongLength(t *testing.T) {
	t.Setenv("PROBARA_SECRETS_KEY", base64.StdEncoding.EncodeToString([]byte("short")))
	if _, err := NewEnvKeyProvider(); err == nil {
		t.Fatal("expected length error")
	}
}

func TestEncryptConfig_MaskAndDecrypt(t *testing.T) {
	enc := testEncryptor(t)
	manifest := plugin.Manifest{
		Type: "demo",
		Fields: []plugin.Field{
			{Key: "url", Type: plugin.FieldTypeSecret, Secret: true},
			{Key: "label", Type: plugin.FieldTypeString},
		},
	}
	in := map[string]any{"url": "https://hook", "label": "ops"}

	encrypted, err := EncryptConfig(enc, manifest, in)
	if err != nil {
		t.Fatalf("EncryptConfig: %v", err)
	}
	if encrypted["label"] != "ops" {
		t.Errorf("label changed: %v", encrypted["label"])
	}
	if !LooksLikeEnvelope(encrypted["url"].(string)) {
		t.Errorf("url not encrypted: %v", encrypted["url"])
	}
	// Mutation of input is forbidden.
	if in["url"] != "https://hook" {
		t.Errorf("input mutated: %v", in["url"])
	}

	masked := MaskConfig(manifest, encrypted)
	if masked["url"] != MaskedSecret {
		t.Errorf("mask failed: %v", masked["url"])
	}

	decrypted, err := DecryptConfig(enc, manifest, encrypted)
	if err != nil {
		t.Fatalf("DecryptConfig: %v", err)
	}
	if decrypted["url"] != "https://hook" {
		t.Errorf("roundtrip failed: %v", decrypted["url"])
	}
}

func TestEncryptConfig_AlreadyEncryptedPassthrough(t *testing.T) {
	enc := testEncryptor(t)
	manifest := plugin.Manifest{
		Type:   "demo",
		Fields: []plugin.Field{{Key: "url", Secret: true}},
	}
	cipher, _ := enc.Encrypt("https://hook")

	out, err := EncryptConfig(enc, manifest, map[string]any{"url": cipher})
	if err != nil {
		t.Fatalf("EncryptConfig: %v", err)
	}
	if out["url"] != cipher {
		t.Errorf("re-encrypted already-encrypted value")
	}
}

func TestMergePreserveSecrets(t *testing.T) {
	manifest := plugin.Manifest{
		Type: "demo",
		Fields: []plugin.Field{
			{Key: "url", Secret: true},
			{Key: "label", Type: plugin.FieldTypeString},
		},
	}
	existing := map[string]any{"url": "old-cipher", "label": "old"}

	t.Run("empty secret preserves existing", func(t *testing.T) {
		incoming := map[string]any{"url": "", "label": "new"}
		out := MergePreserveSecrets(manifest, incoming, existing)
		if out["url"] != "old-cipher" {
			t.Errorf("url not preserved: %v", out["url"])
		}
		if out["label"] != "new" {
			t.Errorf("label = %v, want new", out["label"])
		}
	})

	t.Run("masked sentinel preserves existing", func(t *testing.T) {
		incoming := map[string]any{"url": MaskedSecret, "label": "new"}
		out := MergePreserveSecrets(manifest, incoming, existing)
		if out["url"] != "old-cipher" {
			t.Errorf("masked sentinel not converted: %v", out["url"])
		}
	})

	t.Run("supplied secret wins", func(t *testing.T) {
		incoming := map[string]any{"url": "new-plaintext"}
		out := MergePreserveSecrets(manifest, incoming, existing)
		if out["url"] != "new-plaintext" {
			t.Errorf("user value not respected: %v", out["url"])
		}
	})
}

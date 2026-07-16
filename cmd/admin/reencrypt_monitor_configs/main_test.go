package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/shared/secrets"
)

func TestReencryptConfigWebSocketHeaders(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	t.Setenv("PROBARA_SECRETS_KEY", base64.StdEncoding.EncodeToString(key))
	provider, err := secrets.NewEnvKeyProvider()
	if err != nil {
		t.Fatalf("NewEnvKeyProvider: %v", err)
	}
	encryptor := secrets.NewAESGCMEncryptor(provider)

	raw := json.RawMessage(`{"url":"wss://example.test/socket","headers":{"Authorization":"Bearer legacy-plaintext","Cookie":"session=legacy"}}`)
	updated, changed, err := reencryptConfig(encryptor, "websocket", raw, 1)
	if err != nil {
		t.Fatalf("reencryptConfig: %v", err)
	}
	if !changed {
		t.Fatal("legacy plaintext WebSocket headers were not migrated")
	}

	var cfg map[string]any
	if err := json.Unmarshal(updated, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	headers := cfg["headers"].(map[string]any)
	for name, want := range map[string]string{
		"Authorization": "Bearer legacy-plaintext",
		"Cookie":        "session=legacy",
	} {
		ciphertext := headers[name].(string)
		if !secrets.LooksLikeEnvelope(ciphertext) {
			t.Fatalf("header %q was not encrypted: %q", name, ciphertext)
		}
		plain, err := encryptor.Decrypt(ciphertext)
		if err != nil || plain != want {
			t.Fatalf("header %q decrypts to %q (err %v), want %q", name, plain, err, want)
		}
	}

	unchanged, changed, err := reencryptConfig(encryptor, "websocket", updated, 1)
	if err != nil {
		t.Fatalf("second reencryptConfig: %v", err)
	}
	if changed || string(unchanged) != string(updated) {
		t.Fatal("current-version WebSocket headers should be left unchanged")
	}
}

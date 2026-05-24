// Package secrets provides at-rest encryption for sensitive plugin config
// values (webhook URLs, API tokens, HMAC keys, …). Ciphertexts are stored as
// a small JSON envelope so the key version travels with the value and
// rotation is possible without re-encrypting on a hot path.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	algAESGCM = "aes-256-gcm"
	keyBytes  = 32
)

// Encryptor encrypts and decrypts short secret strings. Implementations must
// be safe to use concurrently.
type Encryptor interface {
	// Encrypt returns a self-describing envelope (JSON) containing the
	// ciphertext, nonce, key version, and algorithm.
	Encrypt(plaintext string) (string, error)
	// Decrypt accepts either an envelope string (produced by Encrypt) or a
	// raw plaintext value (legacy, pre-encryption rows) and returns the
	// plaintext. Returning the input as-is for non-envelope strings is
	// deliberate — see [LooksLikeEnvelope].
	Decrypt(value string) (string, error)
}

// envelope is the on-disk shape of an encrypted secret.
type envelope struct {
	V     int    `json:"v"`
	Alg   string `json:"alg"`
	CT    string `json:"ct"`
	Nonce string `json:"nonce"`
}

// LooksLikeEnvelope reports whether a value appears to be a ciphertext
// envelope (JSON object with the algorithm marker). Used to tolerate mixed
// plaintext/ciphertext rows during the migration window.
func LooksLikeEnvelope(value string) bool {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "{") || !strings.Contains(trimmed, `"alg"`) {
		return false
	}
	var env envelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return false
	}
	return env.Alg != "" && env.CT != ""
}

// AESGCMEncryptor performs AES-256-GCM with a key supplied by a KeyProvider.
type AESGCMEncryptor struct {
	keys KeyProvider
}

// NewAESGCMEncryptor returns an encryptor backed by the given key provider.
func NewAESGCMEncryptor(keys KeyProvider) *AESGCMEncryptor {
	return &AESGCMEncryptor{keys: keys}
}

// Encrypt produces an envelope JSON string. Returns an error if the current
// key is unavailable or random nonce generation fails.
func (e *AESGCMEncryptor) Encrypt(plaintext string) (string, error) {
	key, version, err := e.keys.CurrentKey()
	if err != nil {
		return "", fmt.Errorf("secrets: load current key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("secrets: build cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("secrets: build gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("secrets: generate nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, []byte(plaintext), nil)

	env := envelope{
		V:     version,
		Alg:   algAESGCM,
		CT:    base64.StdEncoding.EncodeToString(ct),
		Nonce: base64.StdEncoding.EncodeToString(nonce),
	}
	b, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("secrets: marshal envelope: %w", err)
	}
	return string(b), nil
}

// Decrypt opens an envelope. If the input does not look like an envelope it
// is returned unchanged — this lets the alerter handle legacy plaintext rows
// during the one-shot migration window.
func (e *AESGCMEncryptor) Decrypt(value string) (string, error) {
	if !LooksLikeEnvelope(value) {
		return value, nil
	}
	var env envelope
	if err := json.Unmarshal([]byte(value), &env); err != nil {
		return "", fmt.Errorf("secrets: decode envelope: %w", err)
	}
	if env.Alg != algAESGCM {
		return "", fmt.Errorf("secrets: unsupported alg %q", env.Alg)
	}
	key, err := e.keys.KeyForVersion(env.V)
	if err != nil {
		return "", fmt.Errorf("secrets: load key v%d: %w", env.V, err)
	}
	ct, err := base64.StdEncoding.DecodeString(env.CT)
	if err != nil {
		return "", fmt.Errorf("secrets: decode ciphertext: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return "", fmt.Errorf("secrets: decode nonce: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("secrets: build cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("secrets: build gcm: %w", err)
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("secrets: gcm open: %w", err)
	}
	return string(plain), nil
}

// NoOpEncryptor is a passthrough used in dev/test environments where no
// encryption key is configured. Encrypt returns plaintext unchanged; Decrypt
// is also a passthrough. Refuses to decrypt actual envelopes so we don't
// silently confuse missing-key bugs with passthrough behavior.
type NoOpEncryptor struct{}

// Encrypt returns the plaintext unchanged.
func (NoOpEncryptor) Encrypt(plaintext string) (string, error) { return plaintext, nil }

// Decrypt returns the value unchanged unless it looks like an envelope, in
// which case it returns an error — encrypted data is unreadable without a key.
func (NoOpEncryptor) Decrypt(value string) (string, error) {
	if LooksLikeEnvelope(value) {
		return "", errors.New("secrets: cannot decrypt envelope with NoOpEncryptor (PROBARA_SECRETS_KEY not configured?)")
	}
	return value, nil
}

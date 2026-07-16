// Package locationauth authenticates messages emitted by private-location
// workers and derives a separate key for monitor-config encryption in transit.
package locationauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/yassinebenameur/probara/shared/secrets"
)

const credentialBytes = 32

// SignJSON signs the canonical JSON representation produced by encoding/json.
// Callers must clear the signature field before passing the value.
func SignJSON(credential string, value any) (string, error) {
	key, err := decodeCredential(credential)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("location auth: marshal signed payload: %w", err)
	}
	mac := hmac.New(sha256.New, signingKey(key))
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// VerifyJSON validates a signature in constant time. The caller must clear the
// signature field in value before passing it.
func VerifyJSON(credential, signature string, value any) error {
	if signature == "" {
		return errors.New("location auth: missing signature")
	}
	expected, err := SignJSON(credential, value)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) != 1 {
		return errors.New("location auth: invalid signature")
	}
	return nil
}

// ConfigEncryptor derives a key separated from the HMAC signing key. This
// lets a remote worker decrypt only jobs addressed to its location without
// receiving the platform-wide PROBARA_SECRETS_KEY.
func ConfigEncryptor(credential string) (secrets.Encryptor, error) {
	key, err := decodeCredential(credential)
	if err != nil {
		return nil, err
	}
	return secrets.NewAESGCMEncryptor(staticKeyProvider{key: derive(key, "probara/location/config/v1")}), nil
}

func decodeCredential(credential string) ([]byte, error) {
	key, err := base64.RawURLEncoding.DecodeString(credential)
	if err != nil {
		return nil, fmt.Errorf("location auth: decode credential: %w", err)
	}
	if len(key) != credentialBytes {
		return nil, fmt.Errorf("location auth: credential must decode to %d bytes", credentialBytes)
	}
	return key, nil
}

func signingKey(key []byte) []byte { return derive(key, "probara/location/signing/v1") }

func derive(key []byte, purpose string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(purpose))
	return mac.Sum(nil)
}

type staticKeyProvider struct{ key []byte }

func (p staticKeyProvider) CurrentKey() ([]byte, int, error) { return p.key, 1, nil }
func (p staticKeyProvider) KeyForVersion(v int) ([]byte, error) {
	if v != 1 {
		return nil, fmt.Errorf("location auth: unsupported config key version %d", v)
	}
	return p.key, nil
}

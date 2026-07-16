package secrets

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

// KeyProvider supplies symmetric keys for AESGCMEncryptor. Implementations
// should be safe for concurrent use. The interface intentionally exposes key
// versions so rotation can roll out new ciphertexts without invalidating
// in-flight reads.
type KeyProvider interface {
	// CurrentKey returns the key to use for new Encrypt calls along with its
	// integer version stamped into envelopes.
	CurrentKey() ([]byte, int, error)
	// KeyForVersion returns the key for a previously-issued envelope. Should
	// return a clear error for unknown versions.
	KeyForVersion(v int) ([]byte, error)
}

// ErrKeyNotConfigured is returned when no encryption key is available. The
// binary's main should treat this as a hard failure in production and as a
// "fall back to NoOp" signal in dev.
var ErrKeyNotConfigured = errors.New("secrets: PROBARA_SECRETS_KEY not set")

// EnvKeyProvider loads versioned keys from the environment:
//
//	PROBARA_SECRETS_KEY     — version 1
//	PROBARA_SECRETS_KEY_V2  — version 2
//	PROBARA_SECRETS_KEY_V3  — version 3, …
//
// The highest version present is used for new ciphertexts; lower versions
// stay available for decrypting envelopes written before a rotation.
//
// Rotation flow: add PROBARA_SECRETS_KEY_V<n+1> alongside the old key(s) and
// restart → new writes use the new key while old rows still decrypt → run
// `go run ./cmd/admin/reencrypt_monitor_configs` (and the channels variant)
// → remove the old key once nothing references it.
type EnvKeyProvider struct {
	keys    map[int][]byte
	current int
}

// NewEnvKeyProvider reads PROBARA_SECRETS_KEY and any PROBARA_SECRETS_KEY_V<n>
// variables (each 32 raw bytes, base64-encoded). Returns ErrKeyNotConfigured
// when no key variable is set at all.
func NewEnvKeyProvider() (*EnvKeyProvider, error) {
	keys := make(map[int][]byte)

	decode := func(name, raw string) ([]byte, error) {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("secrets: decode %s: %w", name, err)
		}
		if len(key) != keyBytes {
			return nil, fmt.Errorf("secrets: %s must be %d bytes, got %d", name, keyBytes, len(key))
		}
		return key, nil
	}

	if raw := os.Getenv("PROBARA_SECRETS_KEY"); raw != "" {
		key, err := decode("PROBARA_SECRETS_KEY", raw)
		if err != nil {
			return nil, err
		}
		keys[1] = key
	}

	// Versions don't need to be contiguous: a retired v1 can be removed while
	// v2 and v3 remain.
	current := 0
	for v := 2; v <= 100; v++ {
		name := fmt.Sprintf("PROBARA_SECRETS_KEY_V%d", v)
		raw := os.Getenv(name)
		if raw == "" {
			continue
		}
		key, err := decode(name, raw)
		if err != nil {
			return nil, err
		}
		keys[v] = key
	}

	for v := range keys {
		if v > current {
			current = v
		}
	}
	if current == 0 {
		return nil, ErrKeyNotConfigured
	}

	return &EnvKeyProvider{keys: keys, current: current}, nil
}

// CurrentKey returns the active key and its version.
func (p *EnvKeyProvider) CurrentKey() ([]byte, int, error) {
	return p.keys[p.current], p.current, nil
}

// KeyForVersion returns the key for a given envelope version.
func (p *EnvKeyProvider) KeyForVersion(v int) ([]byte, error) {
	key, ok := p.keys[v]
	if !ok {
		return nil, fmt.Errorf("secrets: no key for version %d", v)
	}
	return key, nil
}

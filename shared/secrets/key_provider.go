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

// EnvKeyProvider loads a single key from the PROBARA_SECRETS_KEY environment
// variable. Key rotation can be added later without breaking the interface
// by extending this to read multiple keys (e.g. PROBARA_SECRETS_KEY_V2).
type EnvKeyProvider struct {
	keys    map[int][]byte
	current int
}

// NewEnvKeyProvider reads PROBARA_SECRETS_KEY (32 raw bytes, base64-encoded).
// Returns ErrKeyNotConfigured if the variable is unset.
func NewEnvKeyProvider() (*EnvKeyProvider, error) {
	raw := os.Getenv("PROBARA_SECRETS_KEY")
	if raw == "" {
		return nil, ErrKeyNotConfigured
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("secrets: decode PROBARA_SECRETS_KEY: %w", err)
	}
	if len(key) != keyBytes {
		return nil, fmt.Errorf("secrets: PROBARA_SECRETS_KEY must be %d bytes, got %d", keyBytes, len(key))
	}
	return &EnvKeyProvider{
		keys:    map[int][]byte{1: key},
		current: 1,
	}, nil
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

package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/yassinebenameur/probara/shared/db"
)

const (
	// DefaultCost is the default bcrypt cost for hashing API keys
	DefaultCost = 12
)

// HashAPIKeyResult contains both the bcrypt hash and the SHA256 prefix for fast lookup
type HashAPIKeyResult struct {
	BcryptHash string
	KeyPrefix  string
}

// HashAPIKey hashes an API key using bcrypt and computes SHA256 prefix for fast lookup
func HashAPIKey(key string) (*HashAPIKeyResult, error) {
	if key == "" {
		return nil, fmt.Errorf("API key cannot be empty")
	}

	// Generate bcrypt hash
	bcryptHash, err := bcrypt.GenerateFromPassword([]byte(key), DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash API key: %w", err)
	}

	// Compute SHA256 prefix for fast lookup
	sha256Hash := sha256.Sum256([]byte(key))
	keyPrefix := hex.EncodeToString(sha256Hash[:])

	return &HashAPIKeyResult{
		BcryptHash: string(bcryptHash),
		KeyPrefix:  keyPrefix,
	}, nil
}

// CompareAPIKey compares a plaintext API key with a hash using constant-time comparison
func CompareAPIKey(hashedKey, plainKey string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(hashedKey), []byte(plainKey))
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, nil
		}
		return false, fmt.Errorf("failed to compare API key: %w", err)
	}
	return true, nil
}

// ExtractAPIKey extracts the API key from the Authorization header
// Expects format: "Bearer <key>"
func ExtractAPIKey(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", fmt.Errorf("missing Authorization header")
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return "", fmt.Errorf("invalid Authorization header format, expected: Bearer <token>")
	}

	key := strings.TrimSpace(parts[1])
	if key == "" {
		return "", fmt.Errorf("API key cannot be empty")
	}

	return key, nil
}

// ValidateAPIKey validates an API key and returns the tenant ID
// Uses key_prefix for fast direct lookup, then verifies with bcrypt
func ValidateAPIKey(ctx context.Context, dbClient *db.Client, key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("API key cannot be empty")
	}

	// Compute SHA256 prefix for fast lookup
	sha256Hash := sha256.Sum256([]byte(key))
	keyPrefix := hex.EncodeToString(sha256Hash[:])

	// Direct lookup by key_prefix (single row query)
	query := `
		SELECT tenant_id, key_hash
		FROM api_keys
		WHERE key_prefix = $1 AND revoked_at IS NULL
		LIMIT 1
	`

	var tenantID string
	var storedHash string

	err := dbClient.QueryRowContext(ctx, query, keyPrefix).Scan(&tenantID, &storedHash)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("invalid API key")
		}
		return "", fmt.Errorf("failed to query API key: %w", err)
	}

	// Verify the key matches the stored bcrypt hash
	match, err := CompareAPIKey(storedHash, key)
	if err != nil {
		return "", fmt.Errorf("failed to compare API key: %w", err)
	}

	if !match {
		return "", fmt.Errorf("invalid API key")
	}

	return tenantID, nil
}

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
	"time"

	"github.com/lib/pq"
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

// APIKeyIdentity is the resolved identity of a validated API key.
type APIKeyIdentity struct {
	KeyID     string
	TenantID  string
	Name      string
	Scope     string // ScopeRead or ScopeWrite
	ExpiresAt *time.Time
}

// ValidateAPIKey validates an API key and returns its identity (tenant,
// scope, expiry). Uses key_prefix for fast direct lookup, then verifies with
// bcrypt. Expired keys are rejected at the SQL level.
func ValidateAPIKey(ctx context.Context, dbClient *db.Client, key string) (*APIKeyIdentity, error) {
	if key == "" {
		return nil, fmt.Errorf("API key cannot be empty")
	}

	// Compute SHA256 prefix for fast lookup
	sha256Hash := sha256.Sum256([]byte(key))
	keyPrefix := hex.EncodeToString(sha256Hash[:])

	query := `
		SELECT id, tenant_id, name, key_hash, scope, expires_at
		FROM api_keys
		WHERE key_prefix = $1 AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
		LIMIT 1
	`

	identity := &APIKeyIdentity{}
	var storedHash string

	err := dbClient.QueryRowContext(ctx, query, keyPrefix).
		Scan(&identity.KeyID, &identity.TenantID, &identity.Name, &storedHash, &identity.Scope, &identity.ExpiresAt)
	if isMissingColumn(err) {
		// Deploy-ordering tolerance: this binary may run before the scope
		// migration (post-upgrade job). Legacy keys behave as full-access.
		legacy := `
			SELECT id, tenant_id, name, key_hash
			FROM api_keys
			WHERE key_prefix = $1 AND revoked_at IS NULL
			LIMIT 1
		`
		err = dbClient.QueryRowContext(ctx, legacy, keyPrefix).
			Scan(&identity.KeyID, &identity.TenantID, &identity.Name, &storedHash)
		identity.Scope = ScopeWrite
	}
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("invalid API key")
		}
		return nil, fmt.Errorf("failed to query API key: %w", err)
	}

	// Verify the key matches the stored bcrypt hash
	match, err := CompareAPIKey(storedHash, key)
	if err != nil {
		return nil, fmt.Errorf("failed to compare API key: %w", err)
	}

	if !match {
		return nil, fmt.Errorf("invalid API key")
	}

	return identity, nil
}

// isMissingColumn reports Postgres undefined_column (42703).
func isMissingColumn(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	return pqErr.Code == "42703"
}

package adminauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// One-time account tokens (admin_user_tokens, migration 000119). The plain
// token travels once, in a link; only its SHA-256 is stored. A token is
// single use (consumed by one UPDATE ... WHERE used_at IS NULL), expires,
// and issuing another of the same purpose for the same user supersedes the
// unused ones, so only the latest link works.
const (
	PurposeInvite        = "invite"
	PurposePasswordReset = "password_reset"
	PurposeEmailChange   = "email_change"
)

// Token lifetimes per purpose.
const (
	InviteTTL        = 7 * 24 * time.Hour
	PasswordResetTTL = time.Hour
	EmailChangeTTL   = 24 * time.Hour
)

// tokenRetention keeps used and expired rows this long (for support and
// audit correlation), then the next issue prunes them.
const tokenRetention = 30 * 24 * time.Hour

var (
	// ErrTokenInvalid is every unusable token: unknown, used, expired,
	// superseded, or for an account that no longer qualifies. One error so
	// the public routes never say which.
	ErrTokenInvalid = errors.New("this link is invalid or has expired")
	// ErrTokensUnavailable: migration 000119 has not run yet.
	ErrTokensUnavailable = errors.New("account links are not available until the database is migrated")
)

// IssuedToken is a freshly minted token; Token is the only copy of the
// plain value.
type IssuedToken struct {
	Token     string
	ExpiresAt time.Time
}

// TokenRecord is a stored token's subject.
type TokenRecord struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Purpose   string
	NewEmail  *string
	ExpiresAt time.Time
}

func tokenTTL(purpose string) time.Duration {
	switch purpose {
	case PurposeInvite:
		return InviteTTL
	case PurposeEmailChange:
		return EmailChangeTTL
	default:
		return PasswordResetTTL
	}
}

// newAccountToken returns a 256-bit URL-safe token and its stored hash.
func newAccountToken() (plain, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(raw)
	return plain, hashAccountToken(plain), nil
}

func hashAccountToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// issueToken mints a token for userID inside tx: it supersedes the user's
// unused tokens of the same purpose and prunes rows past tokenRetention.
func issueToken(ctx context.Context, tx *sql.Tx, userID uuid.UUID, purpose string, newEmail *string, createdBy *uuid.UUID) (*IssuedToken, error) {
	plain, hash, err := newAccountToken()
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_user_tokens SET used_at = NOW()
		WHERE admin_user_id = $1 AND purpose = $2 AND used_at IS NULL
	`, userID, purpose); err != nil {
		return nil, mapTokenSchema(err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM admin_user_tokens
		WHERE admin_user_id = $1
		  AND COALESCE(used_at, expires_at) < NOW() - make_interval(secs => $2)
	`, userID, tokenRetention.Seconds()); err != nil {
		return nil, mapTokenSchema(err)
	}
	var expiresAt time.Time
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO admin_user_tokens (admin_user_id, purpose, token_hash, new_email, expires_at, created_by)
		VALUES ($1, $2, $3, $4, NOW() + make_interval(secs => $5), $6)
		RETURNING expires_at
	`, userID, purpose, hash, newEmail, tokenTTL(purpose).Seconds(), createdBy).Scan(&expiresAt); err != nil {
		return nil, mapTokenSchema(err)
	}
	return &IssuedToken{Token: plain, ExpiresAt: expiresAt}, nil
}

// consumeToken marks a live token used and returns its record, in tx. The
// single UPDATE is the single-use guarantee: of two concurrent redemptions
// exactly one gets the row.
func consumeToken(ctx context.Context, tx *sql.Tx, plain, purpose string) (*TokenRecord, error) {
	if plain == "" {
		return nil, ErrTokenInvalid
	}
	rec := TokenRecord{Purpose: purpose}
	err := tx.QueryRowContext(ctx, `
		UPDATE admin_user_tokens SET used_at = NOW()
		WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > NOW()
		RETURNING id, admin_user_id, new_email, expires_at
	`, hashAccountToken(plain), purpose).Scan(&rec.ID, &rec.UserID, &rec.NewEmail, &rec.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenInvalid
	}
	if err != nil {
		return nil, mapTokenSchema(err)
	}
	return &rec, nil
}

// InspectToken reads a live token without consuming it (the accept-invite
// page shows whose account it sets up).
func (s *Service) InspectToken(ctx context.Context, plain, purpose string) (*TokenRecord, error) {
	if plain == "" {
		return nil, ErrTokenInvalid
	}
	rec := TokenRecord{Purpose: purpose}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, admin_user_id, new_email, expires_at
		FROM admin_user_tokens
		WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > NOW()
	`, hashAccountToken(plain), purpose).Scan(&rec.ID, &rec.UserID, &rec.NewEmail, &rec.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenInvalid
	}
	if err != nil {
		return nil, mapTokenSchema(err)
	}
	return &rec, nil
}

// mapTokenSchema turns "migration 000119 not applied" into
// ErrTokensUnavailable.
func mapTokenSchema(err error) error {
	if isMissingSchema(err) {
		return ErrTokensUnavailable
	}
	return err
}

// withTx runs fn in a transaction.
func (s *Service) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

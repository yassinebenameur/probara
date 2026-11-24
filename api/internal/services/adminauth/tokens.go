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
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/db/pgerr"
)

// Single-use account tokens (admin_account_tokens, migration 000123): invite
// and password-reset links, email-change confirmations, and the pending
// second step of a two-factor sign-in. Single use and early revocation need
// server state, so a token is 256 random bits whose SHA-256 is stored, rather
// than a signed stateless token: the plain value exists once (in a link, or a
// sign-in response) and a database leak yields nothing usable.
//
//   - Consumed by one conditional UPDATE, so of two concurrent redemptions
//     exactly one wins.
//   - Bound to its purpose: the lookup names it, so an invite token never
//     resets a password and a reset token never confirms an email.
//   - Issuing a link supersedes the user's open links of the same purpose
//     (only the newest invite or reset link works). Sign-in challenges are
//     not superseded: each is one sign-in, possibly on another device.
//   - Every credential reset retires every open token of the user
//     (RetireAccountTokens), so a link or a half-finished sign-in started
//     under the old password dies with it.
const (
	PurposeInvite        = "invite"
	PurposePasswordReset = "password_reset"
	PurposeEmailChange   = "email_change"
	PurposeMFAChallenge  = "mfa_challenge"
)

// Token lifetimes.
const (
	InviteTTL        = 7 * 24 * time.Hour
	PasswordResetTTL = time.Hour
	EmailChangeTTL   = 24 * time.Hour
	MFAChallengeTTL  = 5 * time.Minute
)

var (
	// ErrTokenInvalid is every unusable link token: unknown, used, expired,
	// superseded, of another purpose, or for an account that no longer
	// qualifies. One error, so the public routes never say which.
	ErrTokenInvalid = errors.New("this link is invalid or has expired")
	// ErrTokensUnavailable: migration 000123 has not run yet.
	ErrTokensUnavailable = errors.New("account links are not available until the database is migrated")
)

// IssuedToken is a freshly minted token. Token is the only copy of the
// plain value.
type IssuedToken struct {
	Token     string
	ExpiresAt time.Time
}

// tokenRecord is a stored token's subject.
type tokenRecord struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	NewEmail  *string
	ExpiresAt time.Time
}

func tokenTTL(purpose string) time.Duration {
	switch purpose {
	case PurposeInvite:
		return InviteTTL
	case PurposeEmailChange:
		return EmailChangeTTL
	case PurposeMFAChallenge:
		return MFAChallengeTTL
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

// issueToken mints a token for userID in q (a transaction, so a superseded
// link and its successor change together). createdBy is the superadmin who
// asked, nil when the user did.
func issueToken(ctx context.Context, q SessionQuerier, userID uuid.UUID, purpose string, newEmail *string, createdBy *uuid.UUID) (*IssuedToken, error) {
	plain, hash, err := newAccountToken()
	if err != nil {
		return nil, err
	}
	if purpose != PurposeMFAChallenge {
		if _, err := q.ExecContext(ctx, `
			UPDATE admin_account_tokens SET revoked_at = NOW()
			WHERE admin_user_id = $1 AND purpose = $2 AND used_at IS NULL AND revoked_at IS NULL
		`, userID, purpose); err != nil {
			return nil, mapTokenSchema(err)
		}
	}
	var expiresAt time.Time
	if err := q.QueryRowContext(ctx, `
		INSERT INTO admin_account_tokens (admin_user_id, purpose, token_hash, new_email, expires_at, created_by)
		VALUES ($1, $2, $3, $4, NOW() + make_interval(secs => $5), $6)
		RETURNING expires_at
	`, userID, purpose, hash, newEmail, tokenTTL(purpose).Seconds(), createdBy).Scan(&expiresAt); err != nil {
		return nil, mapTokenSchema(err)
	}
	return &IssuedToken{Token: plain, ExpiresAt: expiresAt}, nil
}

// openTokenWhere matches a live token of a purpose by hash ($1, $2).
const openTokenWhere = `token_hash = $1 AND purpose = $2 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > NOW()`

// consumeToken marks a live token of purpose used and returns it, in q. The
// one conditional UPDATE is the single-use guarantee.
func consumeToken(ctx context.Context, q SessionQuerier, plain, purpose string) (*tokenRecord, error) {
	if plain == "" {
		return nil, ErrTokenInvalid
	}
	var rec tokenRecord
	err := q.QueryRowContext(ctx, `
		UPDATE admin_account_tokens SET used_at = NOW()
		WHERE `+openTokenWhere+`
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

// peekToken reads a live token without consuming it.
func peekToken(ctx context.Context, q SessionQuerier, plain, purpose string) (*tokenRecord, error) {
	if plain == "" {
		return nil, ErrTokenInvalid
	}
	var rec tokenRecord
	err := q.QueryRowContext(ctx, `
		SELECT id, admin_user_id, new_email, expires_at
		FROM admin_account_tokens
		WHERE `+openTokenWhere,
		hashAccountToken(plain), purpose).Scan(&rec.ID, &rec.UserID, &rec.NewEmail, &rec.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenInvalid
	}
	if err != nil {
		return nil, mapTokenSchema(err)
	}
	return &rec, nil
}

// allTokenPurposes is what a credential reset retires: every open link and
// sign-in challenge. An invite or reset link was for the old password; a
// pending email change may have been started by whoever held it, and could
// otherwise still move the account to their mailbox; a sign-in challenge
// proved the old password.
var allTokenPurposes = []string{PurposeInvite, PurposePasswordReset, PurposeEmailChange, PurposeMFAChallenge}

// retireTokens revokes userID's open tokens of the given purposes, in q.
func retireTokens(ctx context.Context, q SessionQuerier, userID uuid.UUID, purposes ...string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE admin_account_tokens SET revoked_at = NOW()
		WHERE admin_user_id = $1 AND purpose = ANY($2) AND used_at IS NULL AND revoked_at IS NULL
	`, userID, pq.Array(purposes))
	return err
}

// RetireAccountTokens revokes every open link and sign-in challenge of
// userID. User management calls it when a superadmin sets a password. Before
// migration 000123 there is nothing to retire. In a transaction, a
// missing-schema error aborts it: callers holding a transaction use
// retireTokensTolerant.
func RetireAccountTokens(ctx context.Context, q SessionQuerier, userID uuid.UUID) error {
	if err := retireTokens(ctx, q, userID, allTokenPurposes...); err != nil && !pgerr.IsMissingSchema(err) {
		return fmt.Errorf("failed to retire account links: %w", err)
	}
	return nil
}

// retireTokensTolerant is retireTokens inside tx, under a savepoint so a
// database without the table (before migration 000123) leaves tx usable.
func retireTokensTolerant(ctx context.Context, tx *sql.Tx, userID uuid.UUID, purposes ...string) error {
	return tolerateMissingSchema(ctx, tx, func() error {
		return retireTokens(ctx, tx, userID, purposes...)
	})
}

// tolerateMissingSchema runs fn under a savepoint of tx. A missing-schema
// error (an API newer than its database, before the migration job ran) rolls
// back to the savepoint and is swallowed; in Postgres any failed statement
// otherwise aborts the whole transaction.
func tolerateMissingSchema(ctx context.Context, tx *sql.Tx, fn func() error) error {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT adminauth_optional`); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if !pgerr.IsMissingSchema(err) {
			return err
		}
		_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT adminauth_optional`)
		return err
	}
	_, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT adminauth_optional`)
	return err
}

// mapTokenSchema turns "migration 000123 not applied" into
// ErrTokensUnavailable.
func mapTokenSchema(err error) error {
	if pgerr.IsMissingSchema(err) {
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

// linkPurposes are the links mailed to the account's address. When the
// address changes they are retired: one already sent to the old, possibly
// compromised mailbox must not keep working.
var linkPurposes = []string{PurposeInvite, PurposePasswordReset}

// RetireMailedLinksTx revokes userID's open invite and reset links inside
// tx (under a savepoint, so a database before migration 000123 leaves tx
// usable). User management calls it when a superadmin changes the address.
func RetireMailedLinksTx(ctx context.Context, tx *sql.Tx, userID uuid.UUID) error {
	return retireTokensTolerant(ctx, tx, userID, linkPurposes...)
}

// RevokeMCPAccess ends every MCP OAuth connection of userID: its grants
// (which every access and refresh token is checked against) and the
// authorization codes not exchanged yet, which would otherwise mint a new
// grant. A credential reset calls it, because whoever held the old password
// may have connected an MCP client that would keep acting as the user.
// Workspace API keys are not the user's credentials and stay. Before
// migration 000096 there is nothing to revoke.
func RevokeMCPAccess(ctx context.Context, q SessionQuerier, userID uuid.UUID) (int, error) {
	var revoked int
	err := q.QueryRowContext(ctx, `
		WITH grants AS (
			UPDATE mcp_oauth_grants SET revoked_at = NOW()
			WHERE admin_user_id = $1 AND revoked_at IS NULL
			RETURNING 1
		), codes AS (
			DELETE FROM mcp_oauth_codes WHERE admin_user_id = $1 AND grant_id IS NULL
			RETURNING 1
		)
		SELECT COUNT(*) FROM grants
	`, userID).Scan(&revoked)
	if pgerr.IsMissingSchema(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to revoke MCP connections: %w", err)
	}
	return revoked, nil
}

// revokeMCPAccessTolerant is RevokeMCPAccess inside tx, under a savepoint.
func revokeMCPAccessTolerant(ctx context.Context, tx *sql.Tx, userID uuid.UUID) error {
	return tolerateMissingSchema(ctx, tx, func() error {
		_, err := tx.ExecContext(ctx, `
			WITH grants AS (
				UPDATE mcp_oauth_grants SET revoked_at = NOW()
				WHERE admin_user_id = $1 AND revoked_at IS NULL
				RETURNING 1
			)
			DELETE FROM mcp_oauth_codes WHERE admin_user_id = $1 AND grant_id IS NULL
		`, userID)
		return err
	})
}

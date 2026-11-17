package adminauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db/pgerr"
)

// Session families (migration 000118). A family is one sign-in, password or
// SSO: the refresh token rotates inside it (admin_sessions, one row per
// token), and the access JWT carries the family id as its `sid` claim.
// admin_session_families.revoked_at is the single authority for "is this
// sign-in still valid": the refresh path and the auth middleware
// (SessionActive) both read it, so revoking that one row ends the refresh
// chain and every access JWT of the family on its next request, on every API
// replica, with no cache to wait out.
//
// Every query here tolerates a database that predates the migration
// (42P01/42703, the post-upgrade migration job): sessions then work as
// before, without families, and access tokens carry no sid.

// Auth methods recorded on a family.
const (
	AuthMethodPassword = "password"
	AuthMethodOIDC     = "oidc"
)

// Revoke reasons recorded on a family.
const (
	RevokeReasonLogout        = "logout"
	RevokeReasonUser          = "user"
	RevokeReasonAdmin         = "admin"
	RevokeReasonPasswordReset = "password_reset"
	RevokeReasonRefreshReuse  = "refresh_reuse"
)

// refreshReuseGrace is how long a rotated refresh token is still accepted
// after rotation. Concurrent refreshes race: when the access token expires,
// parallel 401 retries and other browser tabs all POST /refresh with the same
// cookie, only one rotation can win, and without a reuse interval the losers
// would be treated as stolen. Past it, presenting a rotated token is reuse
// (RefreshSession). The grace never outlives the family: a revoked sign-in
// accepts none of its tokens.
const refreshReuseGrace = 60 * time.Second

// maxUserAgentLength bounds the stored user agent; the list shows a summary.
const maxUserAgentLength = 512

var (
	// ErrInvalidRefreshToken: unknown, expired, or of a revoked session.
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
	// ErrRefreshTokenReused matches a *RefreshReuse.
	ErrRefreshTokenReused = errors.New("refresh token reused")
	// ErrSessionNotFound: not one of the user's live sessions.
	ErrSessionNotFound = errors.New("session not found")
)

// RefreshReuse is a refresh token presented again after it was rotated, past
// the grace: the token was copied, so its whole session has been revoked.
type RefreshReuse struct {
	AdminID, FamilyID uuid.UUID
}

func (e *RefreshReuse) Error() string { return ErrRefreshTokenReused.Error() }

// Is lets errors.Is(err, ErrRefreshTokenReused) match.
func (e *RefreshReuse) Is(target error) bool { return target == ErrRefreshTokenReused }

// Refreshed is the session a refresh token continued.
type Refreshed struct {
	AdminID uuid.UUID
	// FamilyID is uuid.Nil before migration 000118.
	FamilyID uuid.UUID
}

// SessionQuerier is the database surface the package-level session helpers
// need (*db.Client and *sql.Tx both have it).
type SessionQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func truncateUserAgent(ua string) string {
	if len(ua) <= maxUserAgentLength {
		return ua
	}
	return ua[:maxUserAgentLength]
}

// CreateSession starts a session family with its first refresh token and
// returns the family id (uuid.Nil before migration 000118).
func (s *Service) CreateSession(ctx context.Context, adminID uuid.UUID, refreshTokenHash string, expiresAt time.Time, ip, userAgent, authMethod string) (uuid.UUID, error) {
	userAgent = truncateUserAgent(userAgent)
	var familyID uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		WITH family AS (
			INSERT INTO admin_session_families (admin_user_id, auth_method, expires_at, ip, user_agent)
			VALUES ($1, $6, $3, $4, $5)
			RETURNING id
		)
		INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at, ip, user_agent, family_id)
		SELECT $1, $2, $3, $4, $5, id FROM family
		RETURNING family_id
	`, adminID, refreshTokenHash, expiresAt, ip, userAgent, authMethod).Scan(&familyID)
	if pgerr.IsMissingSchema(err) {
		familyID = uuid.Nil
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at, ip, user_agent)
			VALUES ($1, $2, $3, $4, $5)
		`, adminID, refreshTokenHash, expiresAt, ip, userAgent)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to create session: %w", err)
	}
	return familyID, nil
}

// RefreshSession rotates a refresh token: the presented one is retired and
// newHash continues its session family, whose last use, expiry, address and
// user agent move forward. ErrInvalidRefreshToken for an unknown or expired
// token or a revoked session; a *RefreshReuse when the token was already
// rotated more than refreshReuseGrace ago, after revoking its whole session.
func (s *Service) RefreshSession(ctx context.Context, oldHash, newHash string, expiresAt time.Time, ip, userAgent string) (*Refreshed, error) {
	userAgent = truncateUserAgent(userAgent)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin refresh: %w", err)
	}
	defer tx.Rollback()

	var (
		out                              Refreshed
		expired, rotated, pastGrace, off bool
	)
	// The family row lock serializes this with concurrent refreshes and with
	// revocation, so a refresh never continues a session revoked under it.
	err = tx.QueryRowContext(ctx, `
		SELECT s.admin_user_id, s.family_id,
		       s.expires_at <= NOW(),
		       s.revoked_at IS NOT NULL,
		       s.revoked_at IS NOT NULL AND s.revoked_at <= NOW() - make_interval(secs => $2),
		       f.revoked_at IS NOT NULL OR f.expires_at <= NOW()
		FROM admin_sessions s
		JOIN admin_session_families f ON f.id = s.family_id
		WHERE s.refresh_token_hash = $1
		LIMIT 1
		FOR UPDATE OF f
	`, oldHash, refreshReuseGrace.Seconds()).Scan(&out.AdminID, &out.FamilyID, &expired, &rotated, &pastGrace, &off)
	switch {
	case pgerr.IsMissingSchema(err):
		_ = tx.Rollback()
		return s.refreshWithoutFamilies(ctx, oldHash, newHash, expiresAt, ip, userAgent)
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrInvalidRefreshToken
	case err != nil:
		return nil, fmt.Errorf("failed to look up refresh token: %w", err)
	}
	if off || expired {
		return nil, ErrInvalidRefreshToken
	}
	if rotated && pastGrace {
		// Only one party can hold the newest token of a family. A rotated
		// one coming back means the token was copied: end the session for
		// both the thief and the user, who signs in again.
		if _, err := revokeFamilyTx(ctx, tx, out.AdminID, out.FamilyID, RevokeReasonRefreshReuse); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("failed to revoke reused session: %w", err)
		}
		return nil, &RefreshReuse{AdminID: out.AdminID, FamilyID: out.FamilyID}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_sessions
		SET revoked_at = NOW(), last_used_at = NOW()
		WHERE refresh_token_hash = $1 AND revoked_at IS NULL
	`, oldHash); err != nil {
		return nil, fmt.Errorf("failed to retire refresh token: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at, ip, user_agent, family_id)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, out.AdminID, newHash, expiresAt, ip, userAgent, out.FamilyID); err != nil {
		return nil, fmt.Errorf("failed to issue refresh token: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_session_families
		SET last_used_at = NOW(), expires_at = GREATEST(expires_at, $2), ip = $3, user_agent = $4
		WHERE id = $1
	`, out.FamilyID, expiresAt, ip, userAgent); err != nil {
		return nil, fmt.Errorf("failed to update session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit refresh: %w", err)
	}
	return &out, nil
}

// refreshWithoutFamilies is RefreshSession for a database that predates
// migration 000118: the previous rotation, without reuse detection.
func (s *Service) refreshWithoutFamilies(ctx context.Context, oldHash, newHash string, expiresAt time.Time, ip, userAgent string) (*Refreshed, error) {
	var adminID uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		SELECT admin_user_id
		FROM admin_sessions
		WHERE refresh_token_hash = $1
		  AND expires_at > NOW()
		  AND (revoked_at IS NULL OR revoked_at > NOW() - make_interval(secs => $2))
		LIMIT 1
	`, oldHash, refreshReuseGrace.Seconds()).Scan(&adminID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidRefreshToken
	}
	if err != nil {
		return nil, fmt.Errorf("failed to look up refresh token: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE admin_sessions
		SET revoked_at = NOW(), last_used_at = NOW()
		WHERE refresh_token_hash = $1 AND revoked_at IS NULL
	`, oldHash); err != nil {
		return nil, fmt.Errorf("failed to retire refresh token: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5)
	`, adminID, newHash, expiresAt, ip, userAgent); err != nil {
		return nil, fmt.Errorf("failed to issue refresh token: %w", err)
	}
	return &Refreshed{AdminID: adminID}, nil
}

// RevokeByRefreshToken ends the session a refresh token belongs to (sign
// out). Unknown tokens are not an error.
func (s *Service) RevokeByRefreshToken(ctx context.Context, refreshTokenHash string) error {
	_, err := s.db.ExecContext(ctx, `
		WITH family AS (
			UPDATE admin_session_families f
			SET revoked_at = NOW(), revoke_reason = $2
			FROM admin_sessions s
			WHERE s.refresh_token_hash = $1 AND f.id = s.family_id AND f.revoked_at IS NULL
			RETURNING f.id
		)
		UPDATE admin_sessions
		SET revoked_at = NOW(), last_used_at = COALESCE(last_used_at, NOW())
		WHERE revoked_at IS NULL
		  AND (refresh_token_hash = $1 OR family_id IN (SELECT id FROM family))
	`, refreshTokenHash, RevokeReasonLogout)
	if pgerr.IsMissingSchema(err) {
		_, err = s.db.ExecContext(ctx, `
			UPDATE admin_sessions
			SET revoked_at = NOW(), last_used_at = NOW()
			WHERE refresh_token_hash = $1 AND revoked_at IS NULL
		`, refreshTokenHash)
	}
	if err != nil {
		return fmt.Errorf("failed to revoke session: %w", err)
	}
	return nil
}

// RevokeSession ends one of adminID's sessions; ErrSessionNotFound when it
// is not theirs or already over.
func (s *Service) RevokeSession(ctx context.Context, adminID, familyID uuid.UUID, reason string) error {
	n, err := revokeFamilyTx(ctx, s.db, adminID, familyID, reason)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func revokeFamilyTx(ctx context.Context, q SessionQuerier, adminID, familyID uuid.UUID, reason string) (int, error) {
	var revoked int
	err := q.QueryRowContext(ctx, `
		WITH family AS (
			UPDATE admin_session_families
			SET revoked_at = NOW(), revoke_reason = $3
			WHERE id = $1 AND admin_user_id = $2 AND revoked_at IS NULL
			RETURNING id
		), tokens AS (
			UPDATE admin_sessions
			SET revoked_at = NOW()
			WHERE family_id IN (SELECT id FROM family) AND revoked_at IS NULL
			RETURNING 1
		)
		SELECT COUNT(*) FROM family
	`, familyID, adminID, reason).Scan(&revoked)
	if pgerr.IsMissingSchema(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to revoke session: %w", err)
	}
	return revoked, nil
}

// RevokeAllSessions ends every session of adminID except keep (uuid.Nil
// keeps none) and returns how many it ended.
func (s *Service) RevokeAllSessions(ctx context.Context, adminID, keep uuid.UUID, reason string) (int, error) {
	return RevokeUserSessions(ctx, s.db, adminID, keep, reason)
}

// RevokeUserSessions ends every session of adminID except keep (uuid.Nil
// keeps none): the families and their refresh tokens, so access tokens stop
// working on their next request. Before migration 000118 it revokes the
// refresh tokens only. Shared with user management, which ends a user's
// sessions when a superadmin sets their password.
func RevokeUserSessions(ctx context.Context, q SessionQuerier, adminID, keep uuid.UUID, reason string) (int, error) {
	var revoked int
	err := q.QueryRowContext(ctx, `
		WITH families AS (
			UPDATE admin_session_families
			SET revoked_at = NOW(), revoke_reason = $3
			WHERE admin_user_id = $1 AND revoked_at IS NULL AND id <> $2
			RETURNING id
		), tokens AS (
			UPDATE admin_sessions
			SET revoked_at = NOW()
			WHERE admin_user_id = $1 AND revoked_at IS NULL
			  AND family_id IS DISTINCT FROM $2
			RETURNING 1
		)
		SELECT COUNT(*) FROM families
	`, adminID, keep, reason).Scan(&revoked)
	if pgerr.IsMissingSchema(err) {
		res, err := q.ExecContext(ctx, `
			UPDATE admin_sessions
			SET revoked_at = NOW(), last_used_at = NOW()
			WHERE admin_user_id = $1 AND revoked_at IS NULL
		`, adminID)
		if err != nil {
			return 0, fmt.Errorf("failed to revoke sessions: %w", err)
		}
		n, _ := res.RowsAffected()
		return int(n), nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to revoke sessions: %w", err)
	}
	return revoked, nil
}

// ListSessions returns adminID's live sessions, most recently used first,
// with current (the caller's own, uuid.Nil for none) marked. Empty before
// migration 000118.
func (s *Service) ListSessions(ctx context.Context, adminID, current uuid.UUID) ([]models.AdminSession, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(auth_method, ''), created_at, last_used_at, expires_at,
		       COALESCE(ip, ''), COALESCE(user_agent, '')
		FROM admin_session_families
		WHERE admin_user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY last_used_at DESC, created_at DESC
	`, adminID)
	if pgerr.IsMissingSchema(err) {
		return []models.AdminSession{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]models.AdminSession, 0)
	for rows.Next() {
		var item models.AdminSession
		if err := rows.Scan(&item.ID, &item.AuthMethod, &item.CreatedAt, &item.LastUsedAt, &item.ExpiresAt, &item.IP, &item.UserAgent); err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}
		item.Client = SummarizeUserAgent(item.UserAgent)
		item.Current = current != uuid.Nil && item.ID == current
		sessions = append(sessions, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate sessions: %w", err)
	}
	return sessions, nil
}

// SessionActive reports whether an access token's session still admits
// requests: it belongs to adminID, is not revoked and has not expired. One
// primary-key lookup, run for every request a session cookie authenticates
// (AuthMiddleware) and by /auth/me. Before migration 000118 every session is
// active: there is nothing to revoke yet, and refusing would sign everyone
// out mid-deploy.
func (s *Service) SessionActive(ctx context.Context, adminID, familyID uuid.UUID) (bool, error) {
	return SessionActive(ctx, s.db, adminID, familyID)
}

// SessionActive is Service.SessionActive for callers holding a querier.
func SessionActive(ctx context.Context, q SessionQuerier, adminID, familyID uuid.UUID) (bool, error) {
	var active bool
	err := q.QueryRowContext(ctx, `
		SELECT revoked_at IS NULL AND expires_at > NOW()
		FROM admin_session_families
		WHERE id = $1 AND admin_user_id = $2
	`, familyID, adminID).Scan(&active)
	switch {
	case pgerr.IsMissingSchema(err):
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("failed to check session: %w", err)
	}
	return active, nil
}

type sessionCtxKey struct{}

// WithSessionID records the session that authenticated a request (the
// access token's sid); the auth middleware sets it.
func WithSessionID(ctx context.Context, familyID uuid.UUID) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, familyID)
}

// SessionIDFromContext returns the session that authenticated the request:
// false for API keys, MCP tokens and access tokens without a sid.
func SessionIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(sessionCtxKey{}).(uuid.UUID)
	return id, ok && id != uuid.Nil
}

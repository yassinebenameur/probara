package adminauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// Session families (migration 000104). A family is one sign-in: the refresh
// token rotates inside it, and the access JWT carries its id as `sid`.
// admin_session_families.revoked_at is the single authority for "is this
// sign-in still valid": the refresh lookup and the auth middleware both read
// it, so revoking that one row ends the refresh chain and every access JWT
// of the family at once, with no race against a concurrent rotation.

// Auth methods recorded on a family.
const (
	AuthMethodPassword = "password"
	AuthMethodOIDC     = "oidc"
)

// Revoke reasons recorded on a family.
const (
	RevokeReasonLogout         = "logout"
	RevokeReasonUser           = "revoked"
	RevokeReasonAdmin          = "admin"
	RevokeReasonPasswordChange = "password_change"
)

// Querier is the database surface the package-level session helpers need.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// CreateSession starts a session family with its first refresh token and
// returns the family id (uuid.Nil before migration 000104).
func (s *Service) CreateSession(ctx context.Context, adminID uuid.UUID, refreshTokenHash string, expiresAt time.Time, ip, userAgent, authMethod string) (uuid.UUID, error) {
	var familyID uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		WITH family AS (
			INSERT INTO admin_session_families (admin_user_id, auth_method)
			VALUES ($1, $6)
			RETURNING id
		)
		INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at, ip, user_agent, family_id)
		SELECT $1, $2, $3, $4, $5, id FROM family
		RETURNING family_id
	`, adminID, refreshTokenHash, expiresAt, ip, userAgent, authMethod).Scan(&familyID)
	if isMissingSchema(err) {
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at, ip, user_agent)
			VALUES ($1, $2, $3, $4, $5)
		`, adminID, refreshTokenHash, expiresAt, ip, userAgent)
		familyID = uuid.Nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to create session: %w", err)
	}
	return familyID, nil
}

// refreshReuseGrace is how long a rotated (revoked) refresh token is still
// accepted after rotation. Concurrent refreshes race: when the access token
// expires, parallel 401 retries and other browser tabs all POST /refresh with
// the same cookie, only one rotation can win, and without a reuse interval
// the losers would be treated as invalid and bounce the user to the login
// page. The trade-off is that a stolen refresh token can also be replayed
// within this window before reuse is rejected. The grace never outlives the
// family: a revoked sign-in rejects every one of its tokens at once.
const refreshReuseGrace = 60 * time.Second

// GetSessionByTokenHash returns the admin and session family of a valid
// refresh token hash. Tokens rotated less than refreshReuseGrace ago are
// still accepted so that concurrent refreshes from the same browser don't
// invalidate the session; a revoked family accepts none.
func (s *Service) GetSessionByTokenHash(ctx context.Context, refreshTokenHash string) (adminID, familyID uuid.UUID, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT s.admin_user_id, s.family_id
		FROM admin_sessions s
		JOIN admin_session_families f ON f.id = s.family_id
		WHERE s.refresh_token_hash = $1
		  AND s.expires_at > NOW()
		  AND (s.revoked_at IS NULL OR s.revoked_at > NOW() - make_interval(secs => $2))
		  AND f.revoked_at IS NULL
		LIMIT 1
	`, refreshTokenHash, refreshReuseGrace.Seconds()).Scan(&adminID, &familyID)
	if isMissingSchema(err) {
		familyID = uuid.Nil
		err = s.db.QueryRowContext(ctx, `
			SELECT admin_user_id
			FROM admin_sessions
			WHERE refresh_token_hash = $1
			  AND expires_at > NOW()
			  AND (revoked_at IS NULL OR revoked_at > NOW() - make_interval(secs => $2))
			LIMIT 1
		`, refreshTokenHash, refreshReuseGrace.Seconds()).Scan(&adminID)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.Nil, uuid.Nil, fmt.Errorf("invalid refresh token")
		}
		return uuid.Nil, uuid.Nil, fmt.Errorf("failed to query refresh token: %w", err)
	}
	return adminID, familyID, nil
}

// RotateSession revokes an existing refresh token hash and issues its
// successor in the same session family.
func (s *Service) RotateSession(ctx context.Context, oldHash, newHash string, adminID, familyID uuid.UUID, expiresAt time.Time, ip, userAgent string) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE admin_sessions
		SET revoked_at = NOW(), last_used_at = NOW()
		WHERE refresh_token_hash = $1 AND revoked_at IS NULL
	`, oldHash); err != nil {
		return fmt.Errorf("failed to revoke old session: %w", err)
	}

	if familyID == uuid.Nil {
		// Pre-migration: no families to continue.
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at, ip, user_agent)
			VALUES ($1, $2, $3, $4, $5)
		`, adminID, newHash, expiresAt, ip, userAgent); err != nil {
			return fmt.Errorf("failed to create session: %w", err)
		}
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at, ip, user_agent, family_id)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, adminID, newHash, expiresAt, ip, userAgent, familyID); err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// RevokeSession ends the sign-in a refresh token belongs to (logout): the
// token's whole family, so the access JWT dies with it.
func (s *Service) RevokeSession(ctx context.Context, refreshTokenHash string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE admin_session_families
		SET revoked_at = NOW(), revoke_reason = $2
		WHERE revoked_at IS NULL
		  AND id = (SELECT family_id FROM admin_sessions WHERE refresh_token_hash = $1 LIMIT 1)
	`, refreshTokenHash, RevokeReasonLogout)
	if err != nil && !isMissingSchema(err) {
		return fmt.Errorf("failed to revoke session family: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE admin_sessions
		SET revoked_at = NOW(), last_used_at = NOW()
		WHERE refresh_token_hash = $1 AND revoked_at IS NULL
	`, refreshTokenHash); err != nil {
		return fmt.Errorf("failed to revoke session: %w", err)
	}
	return nil
}

// RevokeFamily ends one of adminID's sessions. It reports false when the
// family is not adminID's or is already over.
func (s *Service) RevokeFamily(ctx context.Context, adminID, familyID uuid.UUID, reason string) (bool, error) {
	var revoked int
	err := s.db.QueryRowContext(ctx, `
		WITH family AS (
			UPDATE admin_session_families
			SET revoked_at = NOW(), revoke_reason = $3
			WHERE id = $1 AND admin_user_id = $2 AND revoked_at IS NULL
			RETURNING id
		), tokens AS (
			UPDATE admin_sessions
			SET revoked_at = NOW(), last_used_at = NOW()
			WHERE family_id IN (SELECT id FROM family) AND revoked_at IS NULL
			RETURNING 1
		)
		SELECT COUNT(*) FROM family
	`, familyID, adminID, reason).Scan(&revoked)
	if isMissingSchema(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to revoke session: %w", err)
	}
	return revoked > 0, nil
}

// RevokeAllSessions ends every session of adminID except keep (uuid.Nil
// keeps none) and returns how many it ended.
func (s *Service) RevokeAllSessions(ctx context.Context, adminID, keep uuid.UUID, reason string) (int, error) {
	return RevokeUserSessions(ctx, s.db, adminID, keep, reason)
}

// RevokeUserAccess is the superadmin "sign out everywhere": it ends every
// session of adminID and cancels a pending email change, which an intruder
// holding a session may have started and could otherwise still confirm from
// their own mailbox, then use to reset the password.
func (s *Service) RevokeUserAccess(ctx context.Context, adminID uuid.UUID) (int, error) {
	n, err := RevokeUserSessions(ctx, s.db, adminID, uuid.Nil, RevokeReasonAdmin)
	if err != nil {
		return 0, err
	}
	if err := retireTokens(ctx, s.db, adminID, PurposeEmailChange); err != nil && !isMissingSchema(err) {
		return 0, fmt.Errorf("failed to cancel pending email change: %w", err)
	}
	return n, nil
}

// RevokeUserSessions ends every session of adminID except keep (uuid.Nil
// keeps none): families and their refresh tokens, so access JWTs stop
// working on their next request. Before migration 000104 it revokes the
// refresh tokens only. Shared with the user-management service, which ends a
// user's sessions when a superadmin sets their password.
func RevokeUserSessions(ctx context.Context, q Querier, adminID, keep uuid.UUID, reason string) (int, error) {
	var revoked int
	err := q.QueryRowContext(ctx, `
		WITH families AS (
			UPDATE admin_session_families
			SET revoked_at = NOW(), revoke_reason = $3
			WHERE admin_user_id = $1 AND revoked_at IS NULL AND id <> $2
			RETURNING id
		), tokens AS (
			UPDATE admin_sessions
			SET revoked_at = NOW(), last_used_at = NOW()
			WHERE admin_user_id = $1 AND revoked_at IS NULL AND family_id <> $2
			RETURNING 1
		)
		SELECT COUNT(*) FROM families
	`, adminID, keep, reason).Scan(&revoked)
	if isMissingSchema(err) {
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

// ListSessions returns adminID's live sessions, most recently active first.
// current marks the caller's own family. Before migration 000104 the list is
// empty.
func (s *Service) ListSessions(ctx context.Context, adminID, current uuid.UUID) ([]models.AdminSession, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.id, f.auth_method, f.created_at, t.created_at, t.expires_at,
		       COALESCE(t.ip, ''), COALESCE(t.user_agent, '')
		FROM admin_session_families f
		JOIN LATERAL (
			SELECT created_at, expires_at, ip, user_agent
			FROM admin_sessions
			WHERE family_id = f.id
			ORDER BY created_at DESC
			LIMIT 1
		) t ON TRUE
		WHERE f.admin_user_id = $1 AND f.revoked_at IS NULL AND t.expires_at > NOW()
		ORDER BY t.created_at DESC
	`, adminID)
	if isMissingSchema(err) {
		return []models.AdminSession{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]models.AdminSession, 0)
	for rows.Next() {
		var item models.AdminSession
		if err := rows.Scan(&item.ID, &item.AuthMethod, &item.CreatedAt, &item.LastActiveAt, &item.ExpiresAt, &item.IP, &item.UserAgent); err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}
		item.Current = current != uuid.Nil && item.ID == current
		sessions = append(sessions, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate sessions: %w", err)
	}
	return sessions, nil
}

// SessionFamilyActive reports whether an access JWT's session family still
// admits requests: it exists, belongs to adminID and is not revoked. Before
// migration 000104 every family is treated as active (there is nothing to
// revoke yet), so this never locks users out mid-deploy.
func SessionFamilyActive(ctx context.Context, q Querier, adminID, familyID uuid.UUID) (bool, error) {
	var active bool
	err := q.QueryRowContext(ctx, `
		SELECT revoked_at IS NULL
		FROM admin_session_families
		WHERE id = $1 AND admin_user_id = $2
	`, familyID, adminID).Scan(&active)
	switch {
	case isMissingSchema(err):
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("failed to check session: %w", err)
	}
	return active, nil
}

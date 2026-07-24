package adminauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
)

// Service handles admin authentication.
type Service struct {
	db         *db.Client
	bcryptCost int
}

// NewService creates a new admin auth service.
func NewService(dbClient *db.Client, bcryptCost int) *Service {
	return &Service{db: dbClient, bcryptCost: bcryptCost}
}

// Authenticate validates admin credentials.
func (s *Service) Authenticate(ctx context.Context, username, password string) (*models.AdminUser, error) {
	query := `
		SELECT id, username, password_hash, created_at, updated_at, last_login_at, disabled_at
		FROM admin_users
		WHERE username = $1 AND disabled_at IS NULL
		LIMIT 1
	`

	var user models.AdminUser
	var passwordHash sql.NullString

	err := s.db.QueryRowContext(ctx, query, username).Scan(
		&user.ID,
		&user.Username,
		&passwordHash,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
		&user.DisabledAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("invalid credentials")
		}
		return nil, fmt.Errorf("failed to query admin user: %w", err)
	}

	// OIDC-only users have no local password and cannot log in with one.
	if !passwordHash.Valid || passwordHash.String == "" {
		return nil, fmt.Errorf("invalid credentials")
	}

	match, err := auth.ComparePassword(passwordHash.String, password)
	if err != nil {
		return nil, err
	}
	if !match {
		return nil, fmt.Errorf("invalid credentials")
	}

	return &user, nil
}

// GetAdminByID retrieves an admin user by ID, including identity fields.
func (s *Service) GetAdminByID(ctx context.Context, adminID uuid.UUID) (*models.AdminUser, error) {
	query := `
		SELECT id, username, email, platform_role,
		       CASE WHEN external_subject IS NOT NULL THEN 'oidc' ELSE 'password' END,
		       created_at, updated_at, last_login_at, disabled_at
		FROM admin_users
		WHERE id = $1
		LIMIT 1
	`

	var user models.AdminUser
	err := s.db.QueryRowContext(ctx, query, adminID).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PlatformRole,
		&user.AuthMethod,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
		&user.DisabledAt,
	)
	if isMissingSchema(err) {
		// Deploy-ordering tolerance: identity columns may not be migrated yet.
		// Pre-migration every admin is a superadmin.
		legacy := `
			SELECT id, username, created_at, updated_at, last_login_at, disabled_at
			FROM admin_users
			WHERE id = $1
			LIMIT 1
		`
		err = s.db.QueryRowContext(ctx, legacy, adminID).Scan(
			&user.ID,
			&user.Username,
			&user.CreatedAt,
			&user.UpdatedAt,
			&user.LastLoginAt,
			&user.DisabledAt,
		)
		user.PlatformRole = auth.PlatformRoleSuperadmin
		user.AuthMethod = "password"
	}
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("admin user not found")
		}
		return nil, fmt.Errorf("failed to query admin user: %w", err)
	}

	return &user, nil
}

// GetMembershipsForAdmin returns the user's tenant memberships with tenant names.
func (s *Service) GetMembershipsForAdmin(ctx context.Context, adminID uuid.UUID) ([]models.TenantMembership, error) {
	query := `
		SELECT m.tenant_id, t.name, m.role
		FROM tenant_memberships m
		JOIN tenants t ON t.id = m.tenant_id
		WHERE m.admin_user_id = $1
		ORDER BY t.name ASC
	`

	rows, err := s.db.QueryContext(ctx, query, adminID)
	if err != nil {
		if isMissingSchema(err) {
			return []models.TenantMembership{}, nil
		}
		return nil, fmt.Errorf("failed to query memberships: %w", err)
	}
	defer rows.Close()

	memberships := make([]models.TenantMembership, 0)
	for rows.Next() {
		var m models.TenantMembership
		if err := rows.Scan(&m.TenantID, &m.TenantName, &m.Role); err != nil {
			return nil, fmt.Errorf("failed to scan membership: %w", err)
		}
		memberships = append(memberships, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate memberships: %w", err)
	}
	return memberships, nil
}

// isMissingSchema reports whether err is Postgres undefined_table (42P01) or
// undefined_column (42703) — this binary running ahead of the migrations job.
func isMissingSchema(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	return pqErr.Code == "42P01" || pqErr.Code == "42703"
}

// UpdateLastLogin updates the admin user's last login timestamp.
func (s *Service) UpdateLastLogin(ctx context.Context, adminID uuid.UUID) error {
	query := `UPDATE admin_users SET last_login_at = NOW(), updated_at = NOW() WHERE id = $1`
	if _, err := s.db.ExecContext(ctx, query, adminID); err != nil {
		return fmt.Errorf("failed to update last login: %w", err)
	}
	return nil
}

// CreateSession creates a refresh session.
func (s *Service) CreateSession(ctx context.Context, adminID uuid.UUID, refreshTokenHash string, expiresAt time.Time, ip, userAgent string) error {
	query := `
		INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5)
	`

	_, err := s.db.ExecContext(ctx, query, adminID, refreshTokenHash, expiresAt, ip, userAgent)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// refreshReuseGrace is how long a rotated (revoked) refresh token is still
// accepted after rotation. Concurrent refreshes race: when the access token
// expires, parallel 401 retries and other browser tabs all POST /refresh with
// the same cookie, only one rotation can win, and without a reuse interval
// the losers would be treated as invalid and bounce the user to the login
// page. The trade-off is that a stolen refresh token can also be replayed
// within this window before reuse is rejected.
const refreshReuseGrace = 60 * time.Second

// GetSessionByTokenHash returns the admin ID for a valid refresh token hash.
// Tokens rotated less than refreshReuseGrace ago are still accepted so that
// concurrent refreshes from the same browser don't invalidate the session.
func (s *Service) GetSessionByTokenHash(ctx context.Context, refreshTokenHash string) (uuid.UUID, error) {
	query := `
		SELECT admin_user_id
		FROM admin_sessions
		WHERE refresh_token_hash = $1
		  AND expires_at > NOW()
		  AND (revoked_at IS NULL OR revoked_at > NOW() - make_interval(secs => $2))
		LIMIT 1
	`

	var adminID uuid.UUID
	if err := s.db.QueryRowContext(ctx, query, refreshTokenHash, refreshReuseGrace.Seconds()).Scan(&adminID); err != nil {
		if err == sql.ErrNoRows {
			return uuid.UUID{}, fmt.Errorf("invalid refresh token")
		}
		return uuid.UUID{}, fmt.Errorf("failed to query refresh token: %w", err)
	}
	return adminID, nil
}

// RotateSession revokes an existing refresh token hash and creates a new one.
func (s *Service) RotateSession(ctx context.Context, oldHash, newHash string, adminID uuid.UUID, expiresAt time.Time, ip, userAgent string) error {
	query := `
		UPDATE admin_sessions
		SET revoked_at = NOW(), last_used_at = NOW()
		WHERE refresh_token_hash = $1 AND revoked_at IS NULL
	`

	if _, err := s.db.ExecContext(ctx, query, oldHash); err != nil {
		return fmt.Errorf("failed to revoke old session: %w", err)
	}

	return s.CreateSession(ctx, adminID, newHash, expiresAt, ip, userAgent)
}

// RevokeSession revokes a refresh token hash.
func (s *Service) RevokeSession(ctx context.Context, refreshTokenHash string) error {
	query := `
		UPDATE admin_sessions
		SET revoked_at = NOW(), last_used_at = NOW()
		WHERE refresh_token_hash = $1 AND revoked_at IS NULL
	`
	if _, err := s.db.ExecContext(ctx, query, refreshTokenHash); err != nil {
		return fmt.Errorf("failed to revoke session: %w", err)
	}
	return nil
}

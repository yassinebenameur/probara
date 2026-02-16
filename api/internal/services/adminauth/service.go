package adminauth

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

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
	var passwordHash string

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

	match, err := auth.ComparePassword(passwordHash, password)
	if err != nil {
		return nil, err
	}
	if !match {
		return nil, fmt.Errorf("invalid credentials")
	}

	return &user, nil
}

// GetAdminByID retrieves an admin user by ID.
func (s *Service) GetAdminByID(ctx context.Context, adminID uuid.UUID) (*models.AdminUser, error) {
	query := `
		SELECT id, username, created_at, updated_at, last_login_at, disabled_at
		FROM admin_users
		WHERE id = $1
		LIMIT 1
	`

	var user models.AdminUser
	if err := s.db.QueryRowContext(ctx, query, adminID).Scan(
		&user.ID,
		&user.Username,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
		&user.DisabledAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("admin user not found")
		}
		return nil, fmt.Errorf("failed to query admin user: %w", err)
	}

	return &user, nil
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

// GetSessionByTokenHash returns the admin ID for a valid refresh token hash.
func (s *Service) GetSessionByTokenHash(ctx context.Context, refreshTokenHash string) (uuid.UUID, error) {
	query := `
		SELECT admin_user_id
		FROM admin_sessions
		WHERE refresh_token_hash = $1 AND revoked_at IS NULL AND expires_at > NOW()
		LIMIT 1
	`

	var adminID uuid.UUID
	if err := s.db.QueryRowContext(ctx, query, refreshTokenHash).Scan(&adminID); err != nil {
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

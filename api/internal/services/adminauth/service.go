package adminauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/db/pgerr"
)

// ErrUserNotFound is returned by user-targeted actions for an unknown id.
var ErrUserNotFound = errors.New("admin user not found")

// Service handles admin authentication.
type Service struct {
	db         *db.Client
	bcryptCost int
	// clockOffset moves the lockout's database clock; zero except in tests,
	// the one seam they use to let time pass (lockout.go, lockoutNow).
	clockOffset time.Duration

	dummyOnce sync.Once
	dummyHash string
}

// NewService creates a new admin auth service.
func NewService(dbClient *db.Client, bcryptCost int) *Service {
	return &Service{db: dbClient, bcryptCost: bcryptCost}
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
	if pgerr.IsMissingSchema(err) {
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
		if errors.Is(err, sql.ErrNoRows) {
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
		if pgerr.IsMissingSchema(err) {
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

// UpdateLastLogin updates the admin user's last login timestamp.
func (s *Service) UpdateLastLogin(ctx context.Context, adminID uuid.UUID) error {
	query := `UPDATE admin_users SET last_login_at = NOW(), updated_at = NOW() WHERE id = $1`
	if _, err := s.db.ExecContext(ctx, query, adminID); err != nil {
		return fmt.Errorf("failed to update last login: %w", err)
	}
	return nil
}

package adminusers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
)

var (
	ErrUserNotFound          = errors.New("admin user not found")
	ErrUsernameTaken         = errors.New("username already exists")
	ErrEmailTaken            = errors.New("email already exists")
	ErrCannotDeleteSelf      = errors.New("cannot delete your own account")
	ErrCannotDeleteLastUser  = errors.New("cannot delete the last active superadmin")
	ErrCannotDemoteLastAdmin = errors.New("cannot demote the last active superadmin")
	ErrBootstrapClosed       = errors.New("admin bootstrap is disabled because an admin already exists")
)

const (
	defaultTenantID   = "00000000-0000-0000-0000-000000000001"
	defaultTenantName = "Default"
)

type repository interface {
	ListUsers(ctx context.Context, page, pageSize int) ([]models.AdminUser, int, error)
	GetUser(ctx context.Context, userID uuid.UUID) (*models.AdminUser, error)
	CreateUser(ctx context.Context, id uuid.UUID, username string, email, passwordHash *string, platformRole string, memberships []models.MembershipInput, now time.Time) (*models.AdminUser, error)
	CreateFirstUser(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error)
	UpdateUser(ctx context.Context, userID uuid.UUID, username, email, passwordHash, platformRole *string, memberships *[]models.MembershipInput, now time.Time) (*models.AdminUser, error)
	RevokeSessionsByAdminID(ctx context.Context, adminID uuid.UUID) error
	CountActiveAdmins(ctx context.Context) (int, error)
	IsLastActiveSuperadmin(ctx context.Context, userID uuid.UUID) (bool, error)
	DeleteUser(ctx context.Context, userID uuid.UUID) error
	AttachMemberships(ctx context.Context, users []models.AdminUser) error
}

type sqlRepository struct {
	db *db.Client
}

// Service handles admin user management operations.
type Service struct {
	repo       repository
	bcryptCost int
}

// NewService creates a new admin users service.
func NewService(dbClient *db.Client, bcryptCost int) *Service {
	return &Service{
		repo:       &sqlRepository{db: dbClient},
		bcryptCost: bcryptCost,
	}
}

// ListUsers returns paginated admin users with memberships.
func (s *Service) ListUsers(ctx context.Context, page, pageSize int) (*models.AdminUserListResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	items, total, err := s.repo.ListUsers(ctx, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("failed to list admin users: %w", err)
	}

	if err := s.repo.AttachMemberships(ctx, items); err != nil {
		return nil, fmt.Errorf("failed to load memberships: %w", err)
	}

	return &models.AdminUserListResponse{
		Items:    items,
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}, nil
}

// GetUser returns a single admin user by ID with memberships.
func (s *Service) GetUser(ctx context.Context, userID uuid.UUID) (*models.AdminUser, error) {
	user, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get admin user: %w", err)
	}

	users := []models.AdminUser{*user}
	if err := s.repo.AttachMemberships(ctx, users); err != nil {
		return nil, fmt.Errorf("failed to load memberships: %w", err)
	}
	return &users[0], nil
}

// CreateUser creates a new admin user with optional memberships.
// A nil/empty password creates an OIDC-only user.
func (s *Service) CreateUser(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error) {
	if req == nil {
		return nil, fmt.Errorf("create request is required")
	}

	platformRole := req.PlatformRole
	if platformRole == "" {
		platformRole = auth.PlatformRoleMember
	}
	if !auth.ValidPlatformRole(platformRole) {
		return nil, fmt.Errorf("invalid platform role %q", platformRole)
	}
	for _, m := range req.Memberships {
		if !auth.ValidTenantRole(m.Role) {
			return nil, fmt.Errorf("invalid tenant role %q", m.Role)
		}
	}

	var passwordHash *string
	if req.Password != nil && *req.Password != "" {
		hash, err := auth.HashPassword(*req.Password, s.bcryptCost)
		if err != nil {
			return nil, fmt.Errorf("failed to hash password: %w", err)
		}
		passwordHash = &hash
	}

	user, err := s.repo.CreateUser(ctx, uuid.New(), strings.TrimSpace(req.Username), normalizeEmail(req.Email), passwordHash, platformRole, req.Memberships, time.Now())
	if err != nil {
		return nil, mapUserWriteError(err)
	}

	users := []models.AdminUser{*user}
	if err := s.repo.AttachMemberships(ctx, users); err != nil {
		return nil, fmt.Errorf("failed to load memberships: %w", err)
	}
	return &users[0], nil
}

// HasActiveUsers reports whether at least one active admin user exists.
func (s *Service) HasActiveUsers(ctx context.Context) (bool, error) {
	count, err := s.repo.CountActiveAdmins(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to count active admin users: %w", err)
	}
	return count > 0, nil
}

// CreateFirstUser creates the first admin user (as superadmin) only when no
// active admin exists.
func (s *Service) CreateFirstUser(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error) {
	if req == nil {
		return nil, fmt.Errorf("create request is required")
	}
	if req.Password == nil || *req.Password == "" {
		return nil, fmt.Errorf("password is required")
	}

	passwordHash, err := auth.HashPassword(*req.Password, s.bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user, err := s.repo.CreateFirstUser(ctx, uuid.New(), strings.TrimSpace(req.Username), passwordHash, time.Now())
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, ErrBootstrapClosed
		default:
			return nil, mapUserWriteError(err)
		}
	}

	return user, nil
}

// UpdateUser updates an existing admin user, optionally replacing memberships.
func (s *Service) UpdateUser(ctx context.Context, userID uuid.UUID, req *models.UpdateAdminUserRequest) (*models.AdminUser, error) {
	if req == nil {
		return nil, fmt.Errorf("update request is required")
	}

	var username *string
	if req.Username != nil {
		trimmedUsername := strings.TrimSpace(*req.Username)
		username = &trimmedUsername
	}

	var passwordHash *string
	if req.Password != nil {
		hash, err := auth.HashPassword(*req.Password, s.bcryptCost)
		if err != nil {
			return nil, fmt.Errorf("failed to hash password: %w", err)
		}
		passwordHash = &hash
	}

	if req.PlatformRole != nil {
		if !auth.ValidPlatformRole(*req.PlatformRole) {
			return nil, fmt.Errorf("invalid platform role %q", *req.PlatformRole)
		}
		// Demoting the last active superadmin would orphan the install.
		if *req.PlatformRole == auth.PlatformRoleMember {
			isLast, err := s.repo.IsLastActiveSuperadmin(ctx, userID)
			if err != nil {
				return nil, fmt.Errorf("failed to check superadmin count: %w", err)
			}
			if isLast {
				return nil, ErrCannotDemoteLastAdmin
			}
		}
	}

	if req.Memberships != nil {
		for _, m := range *req.Memberships {
			if !auth.ValidTenantRole(m.Role) {
				return nil, fmt.Errorf("invalid tenant role %q", m.Role)
			}
		}
	}

	user, err := s.repo.UpdateUser(ctx, userID, username, normalizeEmail(req.Email), passwordHash, req.PlatformRole, req.Memberships, time.Now())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, mapUserWriteError(err)
	}

	if passwordHash != nil {
		if err := s.repo.RevokeSessionsByAdminID(ctx, userID); err != nil {
			return nil, fmt.Errorf("failed to revoke admin sessions: %w", err)
		}
	}

	users := []models.AdminUser{*user}
	if err := s.repo.AttachMemberships(ctx, users); err != nil {
		return nil, fmt.Errorf("failed to load memberships: %w", err)
	}
	return &users[0], nil
}

// DeleteUser deletes an admin user with safety guardrails.
func (s *Service) DeleteUser(ctx context.Context, actorUserID, targetUserID uuid.UUID) error {
	if actorUserID == targetUserID {
		return ErrCannotDeleteSelf
	}

	isLast, err := s.repo.IsLastActiveSuperadmin(ctx, targetUserID)
	if err != nil {
		return fmt.Errorf("failed to check superadmin count: %w", err)
	}
	if isLast {
		return ErrCannotDeleteLastUser
	}

	if err := s.repo.DeleteUser(ctx, targetUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("failed to delete admin user: %w", err)
	}

	return nil
}

func mapUserWriteError(err error) error {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		if strings.Contains(pqErr.Constraint, "email") {
			return ErrEmailTaken
		}
		return ErrUsernameTaken
	}
	return fmt.Errorf("failed to write admin user: %w", err)
}

func normalizeEmail(email *string) *string {
	if email == nil {
		return nil
	}
	trimmed := strings.ToLower(strings.TrimSpace(*email))
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// --- SQL repository ---

const userColumns = `
	id, username, email, platform_role,
	CASE WHEN external_subject IS NOT NULL THEN 'oidc' ELSE 'password' END,
	created_at, updated_at, last_login_at, disabled_at
`

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanUser(row rowScanner) (*models.AdminUser, error) {
	var user models.AdminUser
	if err := row.Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PlatformRole,
		&user.AuthMethod,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
		&user.DisabledAt,
	); err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *sqlRepository) ListUsers(ctx context.Context, page, pageSize int) ([]models.AdminUser, int, error) {
	offset := (page - 1) * pageSize

	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users`).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT `+userColumns+`
		FROM admin_users
		ORDER BY username ASC
		LIMIT $1 OFFSET $2
	`, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]models.AdminUser, 0, pageSize)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *user)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

func (r *sqlRepository) GetUser(ctx context.Context, userID uuid.UUID) (*models.AdminUser, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT `+userColumns+`
		FROM admin_users
		WHERE id = $1
		LIMIT 1
	`, userID)
	return scanUser(row)
}

func (r *sqlRepository) CreateUser(ctx context.Context, id uuid.UUID, username string, email, passwordHash *string, platformRole string, memberships []models.MembershipInput, now time.Time) (*models.AdminUser, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		INSERT INTO admin_users (id, username, email, password_hash, platform_role, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		RETURNING `+userColumns+`
	`, id, username, email, passwordHash, platformRole, now)

	user, err := scanUser(row)
	if err != nil {
		return nil, err
	}

	if err := insertMembershipsTx(ctx, tx, user.ID, memberships, now); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit user creation: %w", err)
	}
	return user, nil
}

func (r *sqlRepository) CreateFirstUser(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error) {
	query := `
		WITH inserted AS (
			INSERT INTO admin_users (id, username, password_hash, platform_role, created_at, updated_at)
			SELECT $1, $2, $3, 'superadmin', $4, $4
			WHERE NOT EXISTS (
				SELECT 1 FROM admin_users WHERE disabled_at IS NULL
			)
			RETURNING id, username, email, platform_role, external_subject, created_at, updated_at, last_login_at, disabled_at
		),
		seeded_tenant AS (
			INSERT INTO tenants (id, name, created_at, updated_at)
			SELECT $5::uuid, $6, $4, $4
			FROM inserted
			WHERE NOT EXISTS (
				SELECT 1 FROM tenants
			)
			ON CONFLICT (id) DO NOTHING
		)
		SELECT id, username, email, platform_role,
		       CASE WHEN external_subject IS NOT NULL THEN 'oidc' ELSE 'password' END,
		       created_at, updated_at, last_login_at, disabled_at
		FROM inserted
	`

	row := r.db.QueryRowContext(ctx, query, id, username, passwordHash, now, defaultTenantID, defaultTenantName)
	return scanUser(row)
}

func (r *sqlRepository) UpdateUser(ctx context.Context, userID uuid.UUID, username, email, passwordHash, platformRole *string, memberships *[]models.MembershipInput, now time.Time) (*models.AdminUser, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		UPDATE admin_users
		SET username = COALESCE($2, username),
		    email = COALESCE($3, email),
		    password_hash = COALESCE($4, password_hash),
		    platform_role = COALESCE($5, platform_role),
		    updated_at = $6
		WHERE id = $1
		RETURNING `+userColumns+`
	`, userID, username, email, passwordHash, platformRole, now)

	user, err := scanUser(row)
	if err != nil {
		return nil, err
	}

	if memberships != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tenant_memberships WHERE admin_user_id = $1`, userID); err != nil {
			return nil, fmt.Errorf("failed to clear memberships: %w", err)
		}
		if err := insertMembershipsTx(ctx, tx, userID, *memberships, now); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit user update: %w", err)
	}
	return user, nil
}

func insertMembershipsTx(ctx context.Context, tx *sql.Tx, userID uuid.UUID, memberships []models.MembershipInput, now time.Time) error {
	for _, m := range memberships {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tenant_memberships (id, admin_user_id, tenant_id, role, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $5)
		`, uuid.New(), userID, m.TenantID, m.Role, now); err != nil {
			return fmt.Errorf("failed to insert membership for tenant %s: %w", m.TenantID, err)
		}
	}
	return nil
}

func (r *sqlRepository) RevokeSessionsByAdminID(ctx context.Context, adminID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE admin_sessions
		SET revoked_at = NOW(), last_used_at = NOW()
		WHERE admin_user_id = $1 AND revoked_at IS NULL
	`, adminID)
	return err
}

func (r *sqlRepository) CountActiveAdmins(ctx context.Context) (int, error) {
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users WHERE disabled_at IS NULL`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *sqlRepository) IsLastActiveSuperadmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	var targetIsSuperadmin bool
	var otherSuperadmins int
	err := r.db.QueryRowContext(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM admin_users WHERE id = $1 AND platform_role = 'superadmin' AND disabled_at IS NULL),
			(SELECT COUNT(*) FROM admin_users WHERE id <> $1 AND platform_role = 'superadmin' AND disabled_at IS NULL)
	`, userID).Scan(&targetIsSuperadmin, &otherSuperadmins)
	if err != nil {
		return false, err
	}
	return targetIsSuperadmin && otherSuperadmins == 0, nil
}

func (r *sqlRepository) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM admin_users WHERE id = $1`, userID)
	if err != nil {
		return err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

func (r *sqlRepository) AttachMemberships(ctx context.Context, users []models.AdminUser) error {
	if len(users) == 0 {
		return nil
	}

	ids := make([]uuid.UUID, len(users))
	index := make(map[uuid.UUID]int, len(users))
	for i, u := range users {
		ids[i] = u.ID
		index[u.ID] = i
		users[i].Memberships = []models.TenantMembership{}
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT m.admin_user_id, m.tenant_id, t.name, m.role
		FROM tenant_memberships m
		JOIN tenants t ON t.id = m.tenant_id
		WHERE m.admin_user_id = ANY($1)
		ORDER BY t.name ASC
	`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var adminUserID uuid.UUID
		var m models.TenantMembership
		if err := rows.Scan(&adminUserID, &m.TenantID, &m.TenantName, &m.Role); err != nil {
			return err
		}
		if i, ok := index[adminUserID]; ok {
			users[i].Memberships = append(users[i].Memberships, m)
		}
	}
	return rows.Err()
}

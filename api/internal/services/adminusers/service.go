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
	ErrUserNotFound         = errors.New("admin user not found")
	ErrUsernameTaken        = errors.New("username already exists")
	ErrCannotDeleteSelf     = errors.New("cannot delete your own account")
	ErrCannotDeleteLastUser = errors.New("cannot delete the last active admin user")
	ErrBootstrapClosed      = errors.New("admin bootstrap is disabled because an admin already exists")
)

type repository interface {
	ListUsers(ctx context.Context, page, pageSize int) ([]models.AdminUser, int, error)
	GetUser(ctx context.Context, userID uuid.UUID) (*models.AdminUser, error)
	CreateUser(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error)
	CreateFirstUser(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error)
	UpdateUser(ctx context.Context, userID uuid.UUID, username, passwordHash *string, now time.Time) (*models.AdminUser, error)
	RevokeSessionsByAdminID(ctx context.Context, adminID uuid.UUID) error
	CountActiveAdmins(ctx context.Context) (int, error)
	CountOtherActiveAdmins(ctx context.Context, excludeUserID uuid.UUID) (int, error)
	DeleteUser(ctx context.Context, userID uuid.UUID) error
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

// ListUsers returns paginated admin users.
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

	return &models.AdminUserListResponse{
		Items:    items,
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}, nil
}

// GetUser returns a single admin user by ID.
func (s *Service) GetUser(ctx context.Context, userID uuid.UUID) (*models.AdminUser, error) {
	user, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get admin user: %w", err)
	}
	return user, nil
}

// CreateUser creates a new admin user.
func (s *Service) CreateUser(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error) {
	if req == nil {
		return nil, fmt.Errorf("create request is required")
	}

	passwordHash, err := auth.HashPassword(req.Password, s.bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user, err := s.repo.CreateUser(ctx, uuid.New(), strings.TrimSpace(req.Username), passwordHash, time.Now())
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrUsernameTaken
		}
		return nil, fmt.Errorf("failed to create admin user: %w", err)
	}

	return user, nil
}

// HasActiveUsers reports whether at least one active admin user exists.
func (s *Service) HasActiveUsers(ctx context.Context) (bool, error) {
	count, err := s.repo.CountActiveAdmins(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to count active admin users: %w", err)
	}
	return count > 0, nil
}

// CreateFirstUser creates the first admin user only when no active admin exists.
func (s *Service) CreateFirstUser(ctx context.Context, req *models.CreateAdminUserRequest) (*models.AdminUser, error) {
	if req == nil {
		return nil, fmt.Errorf("create request is required")
	}

	passwordHash, err := auth.HashPassword(req.Password, s.bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user, err := s.repo.CreateFirstUser(ctx, uuid.New(), strings.TrimSpace(req.Username), passwordHash, time.Now())
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, ErrBootstrapClosed
		case isUniqueViolation(err):
			return nil, ErrUsernameTaken
		default:
			return nil, fmt.Errorf("failed to create first admin user: %w", err)
		}
	}

	return user, nil
}

// UpdateUser updates an existing admin user.
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

	user, err := s.repo.UpdateUser(ctx, userID, username, passwordHash, time.Now())
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, ErrUserNotFound
		case isUniqueViolation(err):
			return nil, ErrUsernameTaken
		default:
			return nil, fmt.Errorf("failed to update admin user: %w", err)
		}
	}

	if passwordHash != nil {
		if err := s.repo.RevokeSessionsByAdminID(ctx, userID); err != nil {
			return nil, fmt.Errorf("failed to revoke admin sessions: %w", err)
		}
	}

	return user, nil
}

// DeleteUser deletes an admin user with safety guardrails.
func (s *Service) DeleteUser(ctx context.Context, actorUserID, targetUserID uuid.UUID) error {
	if actorUserID == targetUserID {
		return ErrCannotDeleteSelf
	}

	otherCount, err := s.repo.CountOtherActiveAdmins(ctx, targetUserID)
	if err != nil {
		return fmt.Errorf("failed to count other admin users: %w", err)
	}
	if otherCount < 1 {
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

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	return pqErr.Code == "23505"
}

func (r *sqlRepository) ListUsers(ctx context.Context, page, pageSize int) ([]models.AdminUser, int, error) {
	offset := (page - 1) * pageSize

	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users`).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := `
		SELECT id, username, created_at, updated_at, last_login_at, disabled_at
		FROM admin_users
		ORDER BY username ASC
		LIMIT $1 OFFSET $2
	`

	rows, err := r.db.QueryContext(ctx, query, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]models.AdminUser, 0, pageSize)
	for rows.Next() {
		var user models.AdminUser
		if err := rows.Scan(
			&user.ID,
			&user.Username,
			&user.CreatedAt,
			&user.UpdatedAt,
			&user.LastLoginAt,
			&user.DisabledAt,
		); err != nil {
			return nil, 0, err
		}
		items = append(items, user)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

func (r *sqlRepository) GetUser(ctx context.Context, userID uuid.UUID) (*models.AdminUser, error) {
	query := `
		SELECT id, username, created_at, updated_at, last_login_at, disabled_at
		FROM admin_users
		WHERE id = $1
		LIMIT 1
	`

	var user models.AdminUser
	if err := r.db.QueryRowContext(ctx, query, userID).Scan(
		&user.ID,
		&user.Username,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
		&user.DisabledAt,
	); err != nil {
		return nil, err
	}

	return &user, nil
}

func (r *sqlRepository) CreateUser(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error) {
	query := `
		INSERT INTO admin_users (id, username, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		RETURNING id, username, created_at, updated_at, last_login_at, disabled_at
	`

	var user models.AdminUser
	if err := r.db.QueryRowContext(ctx, query, id, username, passwordHash, now).Scan(
		&user.ID,
		&user.Username,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
		&user.DisabledAt,
	); err != nil {
		return nil, err
	}

	return &user, nil
}

func (r *sqlRepository) CreateFirstUser(ctx context.Context, id uuid.UUID, username, passwordHash string, now time.Time) (*models.AdminUser, error) {
	query := `
		WITH inserted AS (
			INSERT INTO admin_users (id, username, password_hash, created_at, updated_at)
			SELECT $1, $2, $3, $4, $4
			WHERE NOT EXISTS (
				SELECT 1 FROM admin_users WHERE disabled_at IS NULL
			)
			RETURNING id, username, created_at, updated_at, last_login_at, disabled_at
		)
		SELECT id, username, created_at, updated_at, last_login_at, disabled_at
		FROM inserted
	`

	var user models.AdminUser
	if err := r.db.QueryRowContext(ctx, query, id, username, passwordHash, now).Scan(
		&user.ID,
		&user.Username,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
		&user.DisabledAt,
	); err != nil {
		return nil, err
	}

	return &user, nil
}

func (r *sqlRepository) UpdateUser(ctx context.Context, userID uuid.UUID, username, passwordHash *string, now time.Time) (*models.AdminUser, error) {
	query := `
		UPDATE admin_users
		SET username = COALESCE($2, username),
		    password_hash = COALESCE($3, password_hash),
		    updated_at = $4
		WHERE id = $1
		RETURNING id, username, created_at, updated_at, last_login_at, disabled_at
	`

	var user models.AdminUser
	if err := r.db.QueryRowContext(ctx, query, userID, username, passwordHash, now).Scan(
		&user.ID,
		&user.Username,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
		&user.DisabledAt,
	); err != nil {
		return nil, err
	}

	return &user, nil
}

func (r *sqlRepository) RevokeSessionsByAdminID(ctx context.Context, adminID uuid.UUID) error {
	query := `
		UPDATE admin_sessions
		SET revoked_at = NOW(), last_used_at = NOW()
		WHERE admin_user_id = $1 AND revoked_at IS NULL
	`

	_, err := r.db.ExecContext(ctx, query, adminID)
	return err
}

func (r *sqlRepository) CountActiveAdmins(ctx context.Context) (int, error) {
	query := `SELECT COUNT(*) FROM admin_users WHERE disabled_at IS NULL`
	var count int
	if err := r.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *sqlRepository) CountOtherActiveAdmins(ctx context.Context, excludeUserID uuid.UUID) (int, error) {
	query := `SELECT COUNT(*) FROM admin_users WHERE id <> $1 AND disabled_at IS NULL`
	var count int
	if err := r.db.QueryRowContext(ctx, query, excludeUserID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *sqlRepository) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	query := `DELETE FROM admin_users WHERE id = $1`
	result, err := r.db.ExecContext(ctx, query, userID)
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

package adminauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// ErrUserNotFound is returned by user-targeted actions for an unknown id.
var ErrUserNotFound = errors.New("admin user not found")

// Service handles admin authentication.
type Service struct {
	db         *db.Client
	bcryptCost int

	dummyOnce sync.Once
	dummyHash string

	// enc seals TOTP secrets (ConfigureEncryption; no-op by default).
	enc secrets.Encryptor
}

// NewService creates a new admin auth service.
func NewService(dbClient *db.Client, bcryptCost int) *Service {
	return &Service{db: dbClient, bcryptCost: bcryptCost}
}

// burnPasswordCompare spends one bcrypt comparison at the configured cost, so
// an unknown or SSO-only username answers as slowly as a wrong password.
func (s *Service) burnPasswordCompare(password string) {
	s.dummyOnce.Do(func() {
		s.dummyHash, _ = auth.HashPassword("not-a-real-password", s.bcryptCost)
	})
	if s.dummyHash != "" {
		_, _ = auth.ComparePassword(s.dummyHash, password)
	}
}

// Authenticate validates admin credentials under the login lockout. A
// refusal is a *LoginFailure matching ErrInvalidCredentials or
// ErrAccountLocked. A correct password on an account with TOTP enabled is
// a *SecondFactorRequired, never a user: the sign-in is not over until
// CompleteSecondFactor. Any other error is internal.
func (s *Service) Authenticate(ctx context.Context, username, password string) (*models.AdminUser, error) {
	const query = `
		SELECT id, username, password_hash, created_at, updated_at, last_login_at, disabled_at, locked_until
		FROM admin_users
		WHERE username = $1 AND disabled_at IS NULL
		LIMIT 1
	`

	var user models.AdminUser
	var passwordHash sql.NullString
	var lockedUntil sql.NullTime
	lockout := true

	err := s.db.QueryRowContext(ctx, query, username).Scan(
		&user.ID,
		&user.Username,
		&passwordHash,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
		&user.DisabledAt,
		&lockedUntil,
	)
	if isMissingSchema(err) {
		// Deploy-ordering tolerance: before migration 000103 there is no
		// lockout state; the in-memory throttles still apply.
		lockout = false
		err = s.db.QueryRowContext(ctx, `
			SELECT id, username, password_hash, created_at, updated_at, last_login_at, disabled_at
			FROM admin_users
			WHERE username = $1 AND disabled_at IS NULL
			LIMIT 1
		`, username).Scan(
			&user.ID,
			&user.Username,
			&passwordHash,
			&user.CreatedAt,
			&user.UpdatedAt,
			&user.LastLoginAt,
			&user.DisabledAt,
		)
	}
	// OIDC-only users have no local password; they get the same treatment
	// as an unknown username.
	noAccount := errors.Is(err, sql.ErrNoRows) || (err == nil && (!passwordHash.Valid || passwordHash.String == ""))
	if err != nil && !noAccount {
		return nil, fmt.Errorf("failed to query admin user: %w", err)
	}
	if noAccount {
		return nil, s.ghostFailure(ctx, username, password)
	}

	if lockout {
		count, until, locked, err := s.reserveAttempt(ctx, user.ID)
		if err != nil && !isMissingSchema(err) {
			return nil, fmt.Errorf("failed to record login attempt: %w", err)
		}
		if locked {
			failure := &LoginFailure{Locked: true, UserID: &user.ID}
			if lockedUntil.Valid {
				failure.RetryAfter = time.Until(lockedUntil.Time)
			}
			return nil, failure
		}

		match, err := auth.ComparePassword(passwordHash.String, password)
		if err != nil {
			return nil, err
		}
		if !match {
			failure := &LoginFailure{UserID: &user.ID, FailedAttempts: count}
			if until != nil && until.After(time.Now()) {
				failure.LockedNow = true
				failure.RetryAfter = time.Until(*until)
			}
			return nil, failure
		}
		if binding, required, err := s.totpRequired(ctx, user.ID, passwordHash.String); err != nil {
			return nil, fmt.Errorf("failed to check second factor: %w", err)
		} else if required {
			// Step one only: give back this attempt's reservation, keep the
			// streak (second-factor failures count on it).
			lockedNow := until != nil && until.After(time.Now())
			if _, err := s.db.ExecContext(ctx, releaseReservationQuery, user.ID, lockedNow); err != nil {
				return nil, fmt.Errorf("failed to release login attempt: %w", err)
			}
			return nil, &SecondFactorRequired{UserID: user.ID, Username: user.Username, Binding: binding}
		}
		if _, err := s.db.ExecContext(ctx, resetFailuresQuery, user.ID); err != nil && !isMissingSchema(err) {
			return nil, fmt.Errorf("failed to reset login failures: %w", err)
		}
		return &user, nil
	}

	match, err := auth.ComparePassword(passwordHash.String, password)
	if err != nil {
		return nil, err
	}
	if !match {
		return nil, &LoginFailure{UserID: &user.ID}
	}
	// No lockout columns means no TOTP columns either (000120 > 000103).
	return &user, nil
}

// ghostFailure refuses a login for a username with no password account
// (unknown, SSO-only or disabled). Its streak is kept in the database and
// counted exactly like a real account's, and it costs the same bcrypt
// comparison, so neither the 401/429 pattern nor the timing tells it from a
// real account, on any replica or after a restart.
func (s *Service) ghostFailure(ctx context.Context, username, password string) error {
	key := ghostKey(username)
	count, until, locked, err := s.reserveGhost(ctx, key)
	switch {
	case isMissingSchema(err):
		// Before migration 000106 unknown usernames are never locked.
		s.burnPasswordCompare(password)
		return &LoginFailure{}
	case err != nil:
		return fmt.Errorf("failed to record login attempt: %w", err)
	case locked:
		return &LoginFailure{Locked: true, RetryAfter: s.ghostLockedFor(ctx, key)}
	}

	s.burnPasswordCompare(password)
	if rand.IntN(ghostPruneEvery) == 0 {
		// Best-effort housekeeping: an error here must not change the answer,
		// which would set unknown names apart from real accounts.
		_, _ = PruneGhostStreaks(ctx, s.db)
	}
	failure := &LoginFailure{FailedAttempts: count}
	if until != nil && until.After(time.Now()) {
		failure.LockedNow = true
		failure.RetryAfter = time.Until(*until)
	}
	return failure
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

// DB exposes the service's database for the package-level session helpers.
func (s *Service) DB() Querier {
	return s.db
}

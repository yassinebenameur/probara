package adminauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db/pgerr"
)

// Password sign-in lockout. The streak and the lock live on the user row
// (migration 000117), so every API replica sees the same state and it
// survives restarts. Fixed policy, no configuration: LockoutThreshold
// consecutive failures lock the account for LockoutDuration; a successful
// sign-in resets the streak, the lock expires on its own, and a superadmin
// can lift it sooner (UnlockUser). SSO sign-ins and API keys never pass here.
//
// The answer never tells an unknown username, a wrong password and a locked
// account apart (Authenticate's callers send one generic 401 for every
// *LoginFailure), and each costs one bcrypt comparison, so the lock itself
// is invisible from outside: an attacker cannot use it to learn that an
// account exists. The operator sees it instead (users list, audit log).
const (
	LockoutThreshold = 10
	LockoutDuration  = 15 * time.Minute
)

// ErrInvalidCredentials is every refused password sign-in. A refusal is
// always a *LoginFailure, which matches it with errors.Is.
var ErrInvalidCredentials = errors.New("invalid credentials")

// LoginFailure describes a refused password sign-in, for audit and logs only;
// the client sees the same generic answer whatever it says.
type LoginFailure struct {
	// UserID is set when the username names a password account.
	UserID *uuid.UUID
	// Locked: refused without comparing the password, because the account is
	// locked until LockedUntil.
	Locked bool
	// LockedNow: this failure completed the streak and locked the account
	// until LockedUntil.
	LockedNow   bool
	LockedUntil time.Time
	// FailedAttempts is the streak after this attempt (0 once it locked).
	FailedAttempts int
}

func (f *LoginFailure) Error() string { return ErrInvalidCredentials.Error() }

// Is lets errors.Is(err, ErrInvalidCredentials) match every refusal.
func (f *LoginFailure) Is(target error) bool { return target == ErrInvalidCredentials }

// countFailureSet records one failed attempt on an unlocked row: the streak
// grows, and the attempt that reaches the threshold locks the account and
// restarts the streak, so another full streak is needed after the lock ends.
// It reads the row's current values, so when concurrent attempts serialize
// on the row lock each sees the previous one's count: they cannot undercount.
// $2 now, $3 threshold, $4 lock seconds.
const countFailureSet = `
	SET failed_login_count = CASE WHEN failed_login_count + 1 >= $3 THEN 0 ELSE failed_login_count + 1 END,
	    locked_until = CASE WHEN failed_login_count + 1 >= $3
	                        THEN $2::timestamptz + make_interval(secs => $4)
	                        ELSE locked_until END
`

// unlockedRow limits a counting UPDATE to a password account that is not
// locked at $2. A locked row matches nothing, so attempts during a lock
// neither extend it nor count toward the next one.
const unlockedRow = `
	disabled_at IS NULL
	AND password_hash IS NOT NULL AND password_hash <> ''
	AND (locked_until IS NULL OR locked_until <= $2)
`

// reserveAttemptQuery counts a sign-in attempt as a failure BEFORE the
// password is compared; a correct password then clears it (clearFailures).
// Reserving first bounds concurrent guessing: parallel requests cannot all
// read "not locked yet" and all be compared, because the one that reaches the
// threshold locks the row for every request after it.
var reserveAttemptQuery = `
	UPDATE admin_users
	` + countFailureSet + `
	WHERE username = $1 AND ` + unlockedRow + `
	RETURNING id, username, password_hash, created_at, updated_at, last_login_at, disabled_at,
	          failed_login_count, locked_until
`

// recordFailureQuery is the same count for a known user, by id.
var recordFailureQuery = `
	UPDATE admin_users
	` + countFailureSet + `
	WHERE id = $1 AND ` + unlockedRow + `
	RETURNING failed_login_count, locked_until
`

// lockedAccountQuery tells a locked password account from no account at all,
// for the audit record of a refused attempt.
const lockedAccountQuery = `
	SELECT id, locked_until
	FROM admin_users
	WHERE username = $1 AND disabled_at IS NULL
	  AND password_hash IS NOT NULL AND password_hash <> ''
	  AND locked_until > $2
`

const clearFailuresQuery = `
	UPDATE admin_users
	SET failed_login_count = 0, locked_until = NULL
	WHERE id = $1 AND (failed_login_count <> 0 OR locked_until IS NOT NULL)
`

// Authenticate checks a password sign-in under the lockout. A refusal is a
// *LoginFailure (errors.Is ErrInvalidCredentials); any other error is
// internal.
//
// Two-factor sign-in builds on this: a password that matches must not clear
// the streak until the second factor passes (move clearFailures there), and
// a failed second factor counts through RecordFailedAttempt, toward the same
// lock.
func (s *Service) Authenticate(ctx context.Context, username, password string) (*models.AdminUser, error) {
	now := s.clock()
	var (
		user         models.AdminUser
		passwordHash string
		count        int
		lockedUntil  sql.NullTime
	)
	err := s.db.QueryRowContext(ctx, reserveAttemptQuery,
		username, now, LockoutThreshold, LockoutDuration.Seconds(),
	).Scan(
		&user.ID, &user.Username, &passwordHash, &user.CreatedAt, &user.UpdatedAt,
		&user.LastLoginAt, &user.DisabledAt, &count, &lockedUntil,
	)
	switch {
	case pgerr.IsMissingSchema(err):
		// Deploy-ordering tolerance: before migration 000117 there is no
		// lockout state to keep.
		return s.authenticateWithoutLockout(ctx, username, password)
	case errors.Is(err, sql.ErrNoRows):
		return nil, s.refuseWithoutAccount(ctx, username, password, now)
	case err != nil:
		return nil, fmt.Errorf("failed to record sign-in attempt: %w", err)
	}

	match, err := auth.ComparePassword(passwordHash, password)
	if err != nil {
		return nil, err
	}
	if !match {
		failure := &LoginFailure{UserID: &user.ID, FailedAttempts: count}
		if lockedUntil.Valid && lockedUntil.Time.After(now) {
			failure.LockedNow = true
			failure.LockedUntil = lockedUntil.Time
		}
		return nil, failure
	}
	if err := s.clearFailures(ctx, user.ID); err != nil {
		return nil, err
	}
	return &user, nil
}

// refuseWithoutAccount answers an attempt that counted nothing: the username
// names no password account (unknown, SSO-only or disabled), or a locked one.
// It costs the same bcrypt comparison as a wrong password.
func (s *Service) refuseWithoutAccount(ctx context.Context, username, password string, now time.Time) error {
	failure := &LoginFailure{}
	var id uuid.UUID
	var until time.Time
	if err := s.db.QueryRowContext(ctx, lockedAccountQuery, username, now).Scan(&id, &until); err == nil {
		failure.UserID = &id
		failure.Locked = true
		failure.LockedUntil = until
	}
	s.burnPasswordCompare(password)
	return failure
}

// RecordFailedAttempt counts a failed sign-in step for a known user (a wrong
// second factor) toward the same lock as wrong passwords. A locked account
// counts nothing; the result says whether this failure locked it.
func (s *Service) RecordFailedAttempt(ctx context.Context, userID uuid.UUID) (*LoginFailure, error) {
	now := s.clock()
	failure := &LoginFailure{UserID: &userID}
	var lockedUntil sql.NullTime
	err := s.db.QueryRowContext(ctx, recordFailureQuery,
		userID, now, LockoutThreshold, LockoutDuration.Seconds(),
	).Scan(&failure.FailedAttempts, &lockedUntil)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		failure.Locked = true
		return failure, nil
	case err != nil:
		return nil, fmt.Errorf("failed to record sign-in attempt: %w", err)
	}
	if lockedUntil.Valid && lockedUntil.Time.After(now) {
		failure.LockedNow = true
		failure.LockedUntil = lockedUntil.Time
	}
	return failure, nil
}

func (s *Service) clearFailures(ctx context.Context, userID uuid.UUID) error {
	if _, err := s.db.ExecContext(ctx, clearFailuresQuery, userID); err != nil && !pgerr.IsMissingSchema(err) {
		return fmt.Errorf("failed to reset sign-in failures: %w", err)
	}
	return nil
}

// UnlockUser lifts a user's lock and clears their failure streak. It reports
// whether the account was locked; ErrUserNotFound for an unknown id.
func (s *Service) UnlockUser(ctx context.Context, userID uuid.UUID) (bool, error) {
	var wasLocked bool
	err := s.db.QueryRowContext(ctx, `
		UPDATE admin_users u
		SET failed_login_count = 0, locked_until = NULL
		FROM (SELECT id, locked_until FROM admin_users WHERE id = $1 FOR UPDATE) prior
		WHERE u.id = prior.id
		RETURNING COALESCE(prior.locked_until > $2, FALSE)
	`, userID, s.clock()).Scan(&wasLocked)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, ErrUserNotFound
	case pgerr.IsMissingSchema(err):
		// Before migration 000117 nothing is ever locked.
		if _, err := s.GetAdminByID(ctx, userID); err != nil {
			return false, ErrUserNotFound
		}
		return false, nil
	case err != nil:
		return false, fmt.Errorf("failed to unlock user: %w", err)
	}
	return wasLocked, nil
}

// authenticateWithoutLockout is the sign-in check for a database that
// predates migration 000117.
func (s *Service) authenticateWithoutLockout(ctx context.Context, username, password string) (*models.AdminUser, error) {
	var user models.AdminUser
	var passwordHash sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, created_at, updated_at, last_login_at, disabled_at
		FROM admin_users
		WHERE username = $1 AND disabled_at IS NULL
		LIMIT 1
	`, username).Scan(
		&user.ID, &user.Username, &passwordHash, &user.CreatedAt, &user.UpdatedAt,
		&user.LastLoginAt, &user.DisabledAt,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed to query admin user: %w", err)
	}
	if err != nil || !passwordHash.Valid || passwordHash.String == "" {
		s.burnPasswordCompare(password)
		return nil, &LoginFailure{}
	}
	match, err := auth.ComparePassword(passwordHash.String, password)
	if err != nil {
		return nil, err
	}
	if !match {
		return nil, &LoginFailure{UserID: &user.ID}
	}
	return &user, nil
}

// burnPasswordCompare spends one bcrypt comparison at the configured cost, so
// a refusal that has no hash to compare answers as slowly as a wrong password.
func (s *Service) burnPasswordCompare(password string) {
	s.dummyOnce.Do(func() {
		s.dummyHash, _ = auth.HashPassword("probara-timing-equalizer", s.bcryptCost)
	})
	if s.dummyHash != "" {
		_, _ = auth.ComparePassword(s.dummyHash, password)
	}
}

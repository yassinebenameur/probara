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
// survives restarts, and every time is the database clock's -- the same clock
// the users list and session expiry read -- so replicas with skewed clocks
// still agree. Fixed policy, no configuration: LockoutThreshold consecutive
// failures lock the account for LockoutDuration; a successful sign-in resets
// the streak, a failure more than LockoutStreakMemory after the previous one
// starts a new streak, the lock expires on its own, and a superadmin can
// lift it sooner (UnlockUser). SSO sign-ins and API keys never pass here.
//
// The answer never tells an unknown username, a wrong password and a locked
// account apart (Authenticate's callers send one generic 401 for every
// *LoginFailure), and each costs one bcrypt comparison, so the lock itself
// is invisible from outside: an attacker cannot use it to learn that an
// account exists. The operator sees it instead (users list, audit log).
const (
	LockoutThreshold = 10
	LockoutDuration  = 15 * time.Minute
	// LockoutStreakMemory forgets a streak after this long without a
	// failure, so occasional typos spread over weeks never add up to a lock.
	LockoutStreakMemory = 24 * time.Hour
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
	// SecondFactor: the password was right and the second factor was not
	// (a two-factor sign-in's second step, or an account-page check that
	// asks for both).
	SecondFactor bool
}

func (f *LoginFailure) Error() string { return ErrInvalidCredentials.Error() }

// Is lets errors.Is(err, ErrInvalidCredentials) match every refusal.
func (f *LoginFailure) Is(target error) bool { return target == ErrInvalidCredentials }

// lockoutNow is the lockout's "now" in SQL: the database clock, moved by
// $2 seconds -- always 0 outside tests (Service.clockOffset, the one seam
// tests use to move time).
const lockoutNow = `(NOW() + make_interval(secs => $2))`

// countFailureSet records one failed attempt on an unlocked row: the streak
// grows (or restarts after LockoutStreakMemory of quiet), and the attempt
// that reaches the threshold locks the account and restarts the streak, so
// another full streak is needed after the lock ends. It reads the row's
// current values, so when concurrent attempts serialize on the row lock each
// sees the previous one's count: they cannot undercount.
// $3 threshold, $4 lock seconds, $5 streak memory seconds.
const countFailureSet = `
	SET failed_login_count = CASE WHEN ` + streakAfterFailure + ` >= $3 THEN 0 ELSE ` + streakAfterFailure + ` END,
	    last_failed_login_at = ` + lockoutNow + `,
	    locked_until = CASE WHEN ` + streakAfterFailure + ` >= $3
	                        THEN ` + lockoutNow + ` + make_interval(secs => $4)
	                        ELSE locked_until END
`

const streakAfterFailure = `(CASE
	WHEN last_failed_login_at IS NULL OR last_failed_login_at <= ` + lockoutNow + ` - make_interval(secs => $5)
	THEN 1 ELSE failed_login_count + 1 END)`

// unlockedRow limits a counting UPDATE to a password account that is not
// locked now. A locked row matches nothing, so attempts during a lock
// neither extend it nor count toward the next one.
const unlockedRow = `
	disabled_at IS NULL
	AND password_hash IS NOT NULL AND password_hash <> ''
	AND (locked_until IS NULL OR locked_until <= ` + lockoutNow + `)
`

// reserveAttemptQuery counts a sign-in attempt as a failure BEFORE the
// password is compared; a correct password then clears it (clearFailures).
// Reserving first bounds concurrent guessing: parallel requests cannot all
// read "not locked yet" and all be compared, because the one that reaches the
// threshold locks the row for every request after it.
const reserveAttemptQuery = `
	UPDATE admin_users
	` + countFailureSet + `
	WHERE username = $1 AND ` + unlockedRow + `
	RETURNING id, username, password_hash, created_at, updated_at, last_login_at, disabled_at,
	          failed_login_count, locked_until, COALESCE(locked_until > ` + lockoutNow + `, FALSE)
`

// recordFailureQuery is the same count for a known user, by id.
const recordFailureQuery = `
	UPDATE admin_users
	` + countFailureSet + `
	WHERE id = $1 AND ` + unlockedRow + `
	RETURNING failed_login_count, locked_until, COALESCE(locked_until > ` + lockoutNow + `, FALSE)
`

// lockedAccountQuery tells a locked password account from no account at all,
// for the audit record of a refused attempt.
const lockedAccountQuery = `
	SELECT id, locked_until
	FROM admin_users
	WHERE username = $1 AND disabled_at IS NULL
	  AND password_hash IS NOT NULL AND password_hash <> ''
	  AND locked_until > ` + lockoutNow

const clearFailuresQuery = `
	UPDATE admin_users
	SET failed_login_count = 0, last_failed_login_at = NULL, locked_until = NULL
	WHERE id = $1 AND (failed_login_count <> 0 OR last_failed_login_at IS NOT NULL OR locked_until IS NOT NULL)
`

// releaseReservationQuery takes back the one failure a reservation counted,
// for a password that turned out right while the check is not over (a second
// factor is still to come) or that only proved the current password on the
// account page. Unlike clearFailures it keeps the rest of the streak, so a
// correct password never washes out failed second-factor codes. A
// reservation that locked the account is never released: its caller refuses.
const releaseReservationQuery = `
	UPDATE admin_users
	SET failed_login_count = GREATEST(failed_login_count - 1, 0)
	WHERE id = $1
`

// offsetSeconds is lockoutNow's $2.
func (s *Service) offsetSeconds() float64 { return s.clockOffset.Seconds() }

func (s *Service) countArgs(key any) []any {
	return []any{key, s.offsetSeconds(), LockoutThreshold, LockoutDuration.Seconds(), LockoutStreakMemory.Seconds()}
}

// Authenticate checks a password sign-in under the lockout. A refusal is a
// *LoginFailure (errors.Is ErrInvalidCredentials); a correct password on an
// account with two-factor on is a *SecondFactorRequired carrying the
// sign-in's challenge (no session yet); any other error is internal.
//
// With two-factor on, a matching password does not clear the streak: the
// reservation is released (the streak stays as it was) and only the second
// step clears it (CompleteSecondFactor), so passwords cannot wash out failed
// codes, which count through RecordFailedAttempt toward the same lock.
func (s *Service) Authenticate(ctx context.Context, username, password string) (*models.AdminUser, error) {
	var (
		user         models.AdminUser
		passwordHash string
		count        int
		lockedUntil  sql.NullTime
		lockedNow    bool
	)
	err := s.db.QueryRowContext(ctx, reserveAttemptQuery, s.countArgs(username)...).Scan(
		&user.ID, &user.Username, &passwordHash, &user.CreatedAt, &user.UpdatedAt,
		&user.LastLoginAt, &user.DisabledAt, &count, &lockedUntil, &lockedNow,
	)
	switch {
	case pgerr.IsMissingSchema(err):
		// Deploy-ordering tolerance: before migration 000117 there is no
		// lockout state to keep.
		return s.authenticateWithoutLockout(ctx, username, password)
	case errors.Is(err, sql.ErrNoRows):
		return nil, s.refuseWithoutAccount(ctx, username, password)
	case err != nil:
		return nil, fmt.Errorf("failed to record sign-in attempt: %w", err)
	}

	match, err := auth.ComparePassword(passwordHash, password)
	if err != nil {
		return nil, err
	}
	if !match {
		failure := &LoginFailure{UserID: &user.ID, FailedAttempts: count}
		if lockedNow {
			failure.LockedNow = true
			failure.LockedUntil = lockedUntil.Time
		}
		return nil, failure
	}
	secondFactor, err := secondFactorEnabled(ctx, s.db, user.ID)
	if err != nil {
		return nil, err
	}
	if secondFactor {
		return nil, s.startSecondFactor(ctx, &user, lockedNow, lockedUntil)
	}
	if err := s.clearFailures(ctx, user.ID); err != nil {
		return nil, err
	}
	return &user, nil
}

// startSecondFactor answers a correct password on a two-factor account. A
// reservation that just completed a streak keeps its lock, and the answer is
// the same refusal as a wrong password: a lockout is never told apart, in
// either step. Otherwise the reservation is released and the sign-in gets a
// challenge for its second step.
func (s *Service) startSecondFactor(ctx context.Context, user *models.AdminUser, lockedNow bool, lockedUntil sql.NullTime) error {
	if lockedNow {
		return &LoginFailure{UserID: &user.ID, LockedNow: true, LockedUntil: lockedUntil.Time}
	}
	if _, err := s.db.ExecContext(ctx, releaseReservationQuery, user.ID); err != nil {
		return fmt.Errorf("failed to release sign-in attempt: %w", err)
	}
	challenge, err := issueToken(ctx, s.db, user.ID, PurposeMFAChallenge, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to start the second sign-in step: %w", err)
	}
	return &SecondFactorRequired{UserID: user.ID, Username: user.Username, Challenge: *challenge}
}

// refuseWithoutAccount answers an attempt that counted nothing: the username
// names no password account (unknown, SSO-only or disabled), or a locked one.
// It costs the same bcrypt comparison as a wrong password.
func (s *Service) refuseWithoutAccount(ctx context.Context, username, password string) error {
	failure := &LoginFailure{}
	var id uuid.UUID
	var until time.Time
	if err := s.db.QueryRowContext(ctx, lockedAccountQuery, username, s.offsetSeconds()).Scan(&id, &until); err == nil {
		failure.UserID = &id
		failure.Locked = true
		failure.LockedUntil = until
	}
	s.burnPasswordCompare(password)
	return failure
}

// RecordFailedAttempt counts a failed sign-in step for a known user (a wrong
// second factor) toward the same lock as wrong passwords. A locked account
// counts nothing (Locked is set: so does a disabled or password-less one);
// the result says whether this failure locked it. The second step calls it
// before comparing a code, like Authenticate's reservation, so parallel
// guesses cannot overshoot the lock.
func (s *Service) RecordFailedAttempt(ctx context.Context, userID uuid.UUID) (*LoginFailure, error) {
	failure := &LoginFailure{UserID: &userID}
	var lockedUntil sql.NullTime
	var lockedNow bool
	err := s.db.QueryRowContext(ctx, recordFailureQuery, s.countArgs(userID)...).
		Scan(&failure.FailedAttempts, &lockedUntil, &lockedNow)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		failure.Locked = true
		return failure, nil
	case err != nil:
		return nil, fmt.Errorf("failed to record sign-in attempt: %w", err)
	}
	if lockedNow {
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

// VerifyCurrentPassword checks a signed-in user's current password before a
// change on the account page, under the sign-in lockout: the attempt is
// reserved first, a wrong password stays counted, and a right one takes back
// only its own count (the streak is never cleared here). A refusal is a
// *LoginFailure (Locked when the account is locked).
func (s *Service) VerifyCurrentPassword(ctx context.Context, userID uuid.UUID, password string) error {
	_, err := s.VerifyCurrentCredentials(ctx, userID, password, "", false)
	return err
}

// VerifyCurrentCredentials is VerifyCurrentPassword that, with withCode,
// also checks a second factor (a TOTP code or a recovery code, which is then
// used up) under the same single reservation, so a right password never
// resets what wrong codes counted. It returns the second factor's method.
func (s *Service) VerifyCurrentCredentials(ctx context.Context, userID uuid.UUID, password, code string, withCode bool) (string, error) {
	var hash sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM admin_users WHERE id = $1`, userID).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrUserNotFound
		}
		return "", fmt.Errorf("failed to load password: %w", err)
	}
	if !hash.Valid || hash.String == "" {
		return "", ErrNotLocalAccount
	}
	reserved, err := s.RecordFailedAttempt(ctx, userID)
	switch {
	case pgerr.IsMissingSchema(err):
		// Before migration 000117 there is no lockout to count against.
		reserved = &LoginFailure{UserID: &userID}
	case err != nil:
		return "", err
	case reserved.Locked:
		s.burnPasswordCompare(password)
		return "", reserved
	}
	match, err := auth.ComparePassword(hash.String, password)
	if err != nil {
		return "", err
	}
	if !match {
		return "", reserved
	}
	method := ""
	if withCode {
		method, err = s.checkSecondFactorOnce(ctx, userID, code)
		if errors.Is(err, ErrInvalidSecondFactor) {
			reserved.SecondFactor = true
			return "", reserved
		}
		if err != nil {
			return "", err
		}
	}
	if reserved.LockedNow {
		// Right, but this attempt completed a streak: the lock stands.
		return "", reserved
	}
	if _, err := s.db.ExecContext(ctx, releaseReservationQuery, userID); err != nil && !pgerr.IsMissingSchema(err) {
		return "", fmt.Errorf("failed to release attempt: %w", err)
	}
	return method, nil
}

// UnlockUser lifts a user's lock and clears their failure streak. It reports
// whether the account was locked; ErrUserNotFound for an unknown id.
func (s *Service) UnlockUser(ctx context.Context, userID uuid.UUID) (bool, error) {
	var wasLocked bool
	err := s.db.QueryRowContext(ctx, `
		UPDATE admin_users u
		SET failed_login_count = 0, last_failed_login_at = NULL, locked_until = NULL
		FROM (SELECT id, locked_until FROM admin_users WHERE id = $1 FOR UPDATE) prior
		WHERE u.id = prior.id
		RETURNING COALESCE(prior.locked_until > `+lockoutNow+`, FALSE)
	`, userID, s.offsetSeconds()).Scan(&wasLocked)
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

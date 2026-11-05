package adminauth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Login brute-force policy. All of it lives in Postgres, so it holds across
// API replicas and restarts: a real account's streak in admin_users, and the
// streak of a username with no password account in login_failure_streaks
// (migration 000106), counted by the same SQL so the two are
// indistinguishable from outside. There is deliberately no per-address
// stage: behind a proxy or NAT every user shares one address, and blocking
// it would let anyone deny sign-in to all of them.
const (
	// LockoutThreshold failed passwords lock the account. Every further
	// multiple of it locks again, for twice as long.
	LockoutThreshold = 10
	// LockoutBase is the first lock's length; LockoutMax caps the doubling.
	LockoutBase = 15 * time.Minute
	LockoutMax  = 4 * time.Hour
	// lockoutMemory forgets a failure streak after this much quiet, so a
	// year of occasional typos never adds up to a lock.
	lockoutMemory = 24 * time.Hour
)

var (
	// ErrInvalidCredentials is every refused password login that is not a
	// lockout: unknown user, SSO-only user, wrong password.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrAccountLocked is a login refused without checking the password.
	ErrAccountLocked = errors.New("too many failed sign-in attempts")
)

// LoginFailure describes a refused password login. It matches
// ErrAccountLocked when the attempt was refused because of a lock, and
// ErrInvalidCredentials otherwise. The details are for audit and logs only;
// the client sees one generic message per kind.
type LoginFailure struct {
	// Locked: refused without comparing the password.
	Locked bool
	// LockedNow: this failure crossed the threshold and locked the account.
	LockedNow bool
	// RetryAfter is how long the lock has left (Locked or LockedNow).
	RetryAfter time.Duration
	// UserID is set when the username names a real account.
	UserID *uuid.UUID
	// FailedAttempts is the account's failure streak after this attempt.
	FailedAttempts int
}

func (f *LoginFailure) Error() string {
	if f.Locked {
		return ErrAccountLocked.Error()
	}
	return ErrInvalidCredentials.Error()
}

// Is lets errors.Is match the sentinel for the failure's kind.
func (f *LoginFailure) Is(target error) bool {
	if f.Locked {
		return target == ErrAccountLocked
	}
	return target == ErrInvalidCredentials
}

// lockoutAssignments is the SET list that records one more failure on a
// streak row: the count (restarting after lockoutMemory of quiet), its time,
// and a lock of LockoutBase·2^(k-1), capped at LockoutMax, on every k-th
// multiple of the threshold. col qualifies the row's columns ("" or "s.").
// It reads the row's current values, so inside an UPDATE (or ON CONFLICT DO
// UPDATE) it sees the re-fetched row when concurrent attempts serialize on
// the row lock. Parameters: $2 threshold, $3 base seconds, $4 max seconds.
func lockoutAssignments(col string) string {
	count := fmt.Sprintf(`(CASE
	WHEN %[1]slast_failed_login_at IS NULL OR %[1]slast_failed_login_at < NOW() - make_interval(secs => %[2]d)
	THEN 1 ELSE %[1]sfailed_login_count + 1 END)`, col, int(lockoutMemory.Seconds()))
	return strings.NewReplacer("$COUNT", count, "$COL", col).Replace(`
	failed_login_count = $COUNT,
	last_failed_login_at = NOW(),
	locked_until = CASE
	    WHEN $COUNT % $2::int = 0
	    THEN NOW() + make_interval(secs => LEAST($3::float8 * power(2, LEAST($COUNT / $2::int - 1, 30)), $4::float8))
	    ELSE $COLlocked_until
	END`)
}

// reserveAttemptQuery counts an attempt as a failure BEFORE the password is
// compared, and refuses it (no row) while the account is locked. Reserving
// first is what bounds concurrent guessing: N parallel requests cannot all
// pass a "not locked yet" read and then all be compared. A correct password
// clears the reservation (resetFailuresQuery).
var reserveAttemptQuery = `
	UPDATE admin_users
	SET ` + lockoutAssignments("") + `
	WHERE id = $1 AND (locked_until IS NULL OR locked_until <= NOW())
	RETURNING failed_login_count, locked_until
`

// reserveGhostQuery is reserveAttemptQuery for a username with no password
// account, keyed by ghostKey: same counting, doubling, cap and expiry, and
// the same "no row while locked" refusal (ON CONFLICT DO UPDATE ... WHERE).
var reserveGhostQuery = `
	INSERT INTO login_failure_streaks AS s (username_hash, failed_login_count, last_failed_login_at)
	VALUES ($1, 1, NOW())
	ON CONFLICT (username_hash) DO UPDATE
	SET ` + lockoutAssignments("s.") + `
	WHERE s.locked_until IS NULL OR s.locked_until <= NOW()
	RETURNING failed_login_count, locked_until
`

const resetFailuresQuery = `
	UPDATE admin_users
	SET failed_login_count = 0, last_failed_login_at = NULL, locked_until = NULL
	WHERE id = $1 AND (failed_login_count <> 0 OR locked_until IS NOT NULL)
`

// reserveAttempt records one attempt against the account. locked reports a
// refusal (the account is locked); otherwise count is the new streak and
// lockedUntil is set when this attempt crossed the threshold.
func (s *Service) reserveAttempt(ctx context.Context, userID uuid.UUID) (count int, lockedUntil *time.Time, locked bool, err error) {
	return s.reserve(ctx, reserveAttemptQuery, userID)
}

// reserveGhost records one attempt against a username with no password
// account (see reserveAttempt).
func (s *Service) reserveGhost(ctx context.Context, key string) (count int, lockedUntil *time.Time, locked bool, err error) {
	return s.reserve(ctx, reserveGhostQuery, key)
}

func (s *Service) reserve(ctx context.Context, query string, key any) (count int, lockedUntil *time.Time, locked bool, err error) {
	var until sql.NullTime
	err = s.db.QueryRowContext(ctx, query, key,
		LockoutThreshold, LockoutBase.Seconds(), LockoutMax.Seconds(),
	).Scan(&count, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, true, nil
	}
	if err != nil {
		return 0, nil, false, err
	}
	if until.Valid {
		lockedUntil = &until.Time
	}
	return count, lockedUntil, false, nil
}

// UnlockUser clears a user's lockout and failure streak. It reports whether
// the account was locked. Before migration 000103 there is nothing to clear.
func (s *Service) UnlockUser(ctx context.Context, userID uuid.UUID) (bool, error) {
	var wasLocked bool
	err := s.db.QueryRowContext(ctx, `
		WITH prior AS (
			SELECT id, (locked_until IS NOT NULL AND locked_until > NOW()) AS was_locked
			FROM admin_users WHERE id = $1 FOR UPDATE
		)
		UPDATE admin_users u
		SET failed_login_count = 0, last_failed_login_at = NULL, locked_until = NULL
		FROM prior
		WHERE u.id = prior.id
		RETURNING prior.was_locked
	`, userID).Scan(&wasLocked)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, ErrUserNotFound
	case isMissingSchema(err):
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM admin_users WHERE id = $1)`, userID).Scan(&exists); err != nil {
			return false, fmt.Errorf("failed to query admin user: %w", err)
		}
		if !exists {
			return false, ErrUserNotFound
		}
		return false, nil
	case err != nil:
		return false, fmt.Errorf("failed to unlock admin user: %w", err)
	}
	return wasLocked, nil
}

// ghostKey identifies a username with no password account in
// login_failure_streaks: a SHA-256 of the username exactly as the account
// lookup matches it (case-sensitive), so case variants form separate streaks
// for real and unknown names alike, and the table never stores usernames.
func ghostKey(username string) string {
	sum := sha256.Sum256([]byte(username))
	return hex.EncodeToString(sum[:])
}

// ghostLockedFor reads how long a refused unknown username stays locked.
func (s *Service) ghostLockedFor(ctx context.Context, key string) time.Duration {
	var until sql.NullTime
	if err := s.db.QueryRowContext(ctx,
		`SELECT locked_until FROM login_failure_streaks WHERE username_hash = $1`, key,
	).Scan(&until); err != nil || !until.Valid {
		return 0
	}
	return time.Until(until.Time)
}

// ghostPruneEvery is the share of unknown-username failures that also prune
// (1 in N): enough to bound the table under a username spray without
// turning every failure into a DELETE.
const ghostPruneEvery = 50

// ghostPruneBatch bounds one prune.
const ghostPruneBatch = 1000

// PruneGhostStreaks deletes unknown-username streaks forgotten by
// lockoutMemory. A forgotten streak is never locked (LockoutMax is shorter
// than lockoutMemory and a lock starts at the last failure), so pruning
// changes no answer. Called opportunistically from failed logins; there is
// no other writer to clean up after.
func PruneGhostStreaks(ctx context.Context, q Querier) (int64, error) {
	res, err := q.ExecContext(ctx, `
		DELETE FROM login_failure_streaks
		WHERE username_hash IN (
			SELECT username_hash FROM login_failure_streaks
			WHERE last_failed_login_at < NOW() - make_interval(secs => $1)
			LIMIT $2
		)
	`, int(lockoutMemory.Seconds()), ghostPruneBatch)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

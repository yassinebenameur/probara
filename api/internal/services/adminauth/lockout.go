package adminauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Login brute-force policy. The account lockout lives in admin_users so it
// holds across API replicas; the in-memory throttles only add a per-replica
// layer in front of it.
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

	// IPFailureLimit failed logins from one client address within
	// LockoutBase block that address for LockoutBase (per replica).
	IPFailureLimit = 50
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

// failureCountExpr is the account's failure streak after one more failure:
// a streak whose last failure is older than lockoutMemory starts over.
// It reads the row's current values, so inside an UPDATE it sees the
// re-fetched row when concurrent attempts serialize on the row lock.
var failureCountExpr = fmt.Sprintf(`(CASE
	WHEN last_failed_login_at IS NULL OR last_failed_login_at < NOW() - make_interval(secs => %d)
	THEN 1 ELSE failed_login_count + 1 END)`, int(lockoutMemory.Seconds()))

// reserveAttemptQuery counts an attempt as a failure BEFORE the password is
// compared, and refuses it (no row) while the account is locked. Reserving
// first is what bounds concurrent guessing: N parallel requests cannot all
// pass a "not locked yet" read and then all be compared. A correct password
// clears the reservation (resetFailuresQuery). Crossing a multiple of the
// threshold locks for LockoutBase·2^(k-1), capped at LockoutMax.
var reserveAttemptQuery = strings.NewReplacer("$COUNT", failureCountExpr).Replace(`
	UPDATE admin_users
	SET failed_login_count = $COUNT,
	    last_failed_login_at = NOW(),
	    locked_until = CASE
	        WHEN $COUNT % $2::int = 0
	        THEN NOW() + make_interval(secs => LEAST($3::float8 * power(2, LEAST($COUNT / $2::int - 1, 30)), $4::float8))
	        ELSE locked_until
	    END
	WHERE id = $1 AND (locked_until IS NULL OR locked_until <= NOW())
	RETURNING failed_login_count, locked_until
`)

const resetFailuresQuery = `
	UPDATE admin_users
	SET failed_login_count = 0, last_failed_login_at = NULL, locked_until = NULL
	WHERE id = $1 AND (failed_login_count <> 0 OR locked_until IS NOT NULL)
`

// reserveAttempt records one attempt against the account. locked reports a
// refusal (the account is locked); otherwise count is the new streak and
// lockedUntil is set when this attempt crossed the threshold.
func (s *Service) reserveAttempt(ctx context.Context, userID uuid.UUID) (count int, lockedUntil *time.Time, locked bool, err error) {
	var until sql.NullTime
	err = s.db.QueryRowContext(ctx, reserveAttemptQuery, userID,
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

// FailureThrottle is the in-memory twin of the account lockout, keyed by an
// arbitrary string: every limit-th failure in a streak blocks the key, for
// block doubled per repeat and capped at maxBlock, and a streak ends after
// window without failures. It is per process: replicas keep their own counts.
type FailureThrottle struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	block    time.Duration
	maxBlock time.Duration
	entries  map[string]*throttleEntry
}

type throttleEntry struct {
	failures     int
	lastFailure  time.Time
	blockedUntil time.Time
}

// throttleMaxEntries bounds memory under a spray of distinct keys; stale
// entries are pruned first, and only if that is not enough is the map reset.
const throttleMaxEntries = 10000

// NewFailureThrottle returns a throttle that blocks a key after limit
// failures in a streak (see FailureThrottle). maxBlock <= block disables the
// doubling.
func NewFailureThrottle(limit int, window, block, maxBlock time.Duration) *FailureThrottle {
	if maxBlock < block {
		maxBlock = block
	}
	return &FailureThrottle{limit: limit, window: window, block: block, maxBlock: maxBlock, entries: map[string]*throttleEntry{}}
}

// BlockedFor reports how long key stays blocked (0 when it is not).
func (t *FailureThrottle) BlockedFor(key string, now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.entries[key]; ok && e.blockedUntil.After(now) {
		return e.blockedUntil.Sub(now)
	}
	return 0
}

// Fail records a failure for key and reports how long it is now blocked
// (0 when this failure did not block it).
func (t *FailureThrottle) Fail(key string, now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[key]
	if !ok {
		if len(t.entries) >= throttleMaxEntries {
			t.pruneLocked(now)
		}
		e = &throttleEntry{}
		t.entries[key] = e
	}
	if now.Sub(e.lastFailure) >= t.window {
		e.failures = 0
	}
	e.failures++
	e.lastFailure = now
	if e.failures%t.limit == 0 {
		e.blockedUntil = now.Add(lockDuration(e.failures/t.limit, t.block, t.maxBlock))
	}
	if e.blockedUntil.After(now) {
		return e.blockedUntil.Sub(now)
	}
	return 0
}

// Reset forgets key (a successful login).
func (t *FailureThrottle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}

func (t *FailureThrottle) pruneLocked(now time.Time) {
	for k, e := range t.entries {
		if now.Sub(e.lastFailure) >= t.window && !e.blockedUntil.After(now) {
			delete(t.entries, k)
		}
	}
	if len(t.entries) >= throttleMaxEntries {
		t.entries = map[string]*throttleEntry{}
	}
}

// lockDuration is the k-th lock's length (k >= 1): base doubled per repeat,
// capped at maxBlock. The SQL in reserveAttemptQuery computes the same.
func lockDuration(k int, base, maxBlock time.Duration) time.Duration {
	d := base
	for i := 1; i < k && d < maxBlock; i++ {
		d *= 2
	}
	if d > maxBlock {
		d = maxBlock
	}
	return d
}

// usernameKey normalizes a username for the in-memory throttle.
func usernameKey(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

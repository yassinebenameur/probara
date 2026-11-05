//go:build !unit

package adminauth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

const testPassword = "correct-horse-battery"

func setupService(t *testing.T) (context.Context, *db.Client, *Service) {
	t.Helper()
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	return ctx, dbClient, NewService(dbClient, 4)
}

func insertUser(ctx context.Context, t *testing.T, dbClient *db.Client, username string) uuid.UUID {
	t.Helper()
	hash, err := auth.HashPassword(testPassword, 4)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO admin_users (id, username, password_hash, platform_role) VALUES ($1, $2, $3, 'superadmin')
	`, id, username, hash); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func loginState(ctx context.Context, t *testing.T, dbClient *db.Client, id uuid.UUID) (count int, lockedFor time.Duration) {
	t.Helper()
	var seconds float64
	if err := dbClient.QueryRowContext(ctx, `
		SELECT failed_login_count, COALESCE(EXTRACT(EPOCH FROM locked_until - NOW()), 0)
		FROM admin_users WHERE id = $1
	`, id).Scan(&count, &seconds); err != nil {
		t.Fatalf("load login state: %v", err)
	}
	return count, time.Duration(seconds * float64(time.Second))
}

func failLogins(ctx context.Context, t *testing.T, s *Service, username string, n int) (last error) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, last = s.Authenticate(ctx, username, "wrong-password")
		if !errors.Is(last, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: err = %v, want invalid credentials", i+1, last)
		}
	}
	return last
}

func TestLockoutAfterThresholdFailures(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "alice")

	failLogins(ctx, t, s, "alice", LockoutThreshold-1)
	if count, locked := loginState(ctx, t, dbClient, id); count != LockoutThreshold-1 || locked > 0 {
		t.Fatalf("after %d failures: count=%d locked=%v, want no lock yet", LockoutThreshold-1, count, locked)
	}

	err := failLogins(ctx, t, s, "alice", 1)
	var failure *LoginFailure
	if !errors.As(err, &failure) || !failure.LockedNow || failure.UserID == nil || *failure.UserID != id {
		t.Fatalf("threshold failure = %#v, want LockedNow for alice", err)
	}
	if _, locked := loginState(ctx, t, dbClient, id); locked < LockoutBase-time.Minute || locked > LockoutBase {
		t.Fatalf("locked for %v, want ~%v", locked, LockoutBase)
	}

	// Locked: even the right password is refused, without being compared.
	_, err = s.Authenticate(ctx, "alice", testPassword)
	if !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("correct password while locked: err = %v, want ErrAccountLocked", err)
	}
	if count, _ := loginState(ctx, t, dbClient, id); count != LockoutThreshold {
		t.Fatalf("a refused attempt must not count: count=%d", count)
	}
}

func TestSuccessfulLoginResetsFailures(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "bob")

	failLogins(ctx, t, s, "bob", 5)
	if _, err := s.Authenticate(ctx, "bob", testPassword); err != nil {
		t.Fatalf("correct password: %v", err)
	}
	if count, locked := loginState(ctx, t, dbClient, id); count != 0 || locked != 0 {
		t.Fatalf("after success: count=%d locked=%v, want reset", count, locked)
	}
	// A fresh streak needs the full threshold again.
	failLogins(ctx, t, s, "bob", LockoutThreshold-1)
	if _, locked := loginState(ctx, t, dbClient, id); locked > 0 {
		t.Fatal("locked before the threshold after a reset")
	}
}

func TestLockoutExpiresAndDoublesOnRepeat(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "carol")

	failLogins(ctx, t, s, "carol", LockoutThreshold)
	// Let the first lock run out; the streak is still remembered.
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET locked_until = NOW() - INTERVAL '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	failLogins(ctx, t, s, "carol", LockoutThreshold)
	if count, locked := loginState(ctx, t, dbClient, id); count != 2*LockoutThreshold || locked < 2*LockoutBase-time.Minute || locked > 2*LockoutBase {
		t.Fatalf("second lock: count=%d locked=%v, want %d / ~%v", count, locked, 2*LockoutThreshold, 2*LockoutBase)
	}

	// Expired lock: the right password signs in and clears everything.
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET locked_until = NOW() - INTERVAL '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "carol", testPassword); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	if count, locked := loginState(ctx, t, dbClient, id); count != 0 || locked != 0 {
		t.Fatalf("after expiry login: count=%d locked=%v, want reset", count, locked)
	}
}

func TestLockoutCapAndStreakMemory(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "dave")

	// A long streak is capped at LockoutMax.
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE admin_users SET failed_login_count = $2, last_failed_login_at = NOW() WHERE id = $1
	`, id, 10*LockoutThreshold-1); err != nil {
		t.Fatal(err)
	}
	failLogins(ctx, t, s, "dave", 1)
	if _, locked := loginState(ctx, t, dbClient, id); locked < LockoutMax-time.Minute || locked > LockoutMax {
		t.Fatalf("locked for %v, want the %v cap", locked, LockoutMax)
	}

	// A streak whose last failure is older than lockoutMemory starts over.
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE admin_users SET failed_login_count = $2, locked_until = NULL,
		       last_failed_login_at = NOW() - INTERVAL '25 hours'
		WHERE id = $1
	`, id, LockoutThreshold-1); err != nil {
		t.Fatal(err)
	}
	failLogins(ctx, t, s, "dave", 1)
	if count, locked := loginState(ctx, t, dbClient, id); count != 1 || locked != 0 {
		t.Fatalf("stale streak: count=%d locked=%v, want 1 and no lock", count, locked)
	}
}

// Concurrent guesses cannot overshoot the threshold: each attempt is
// reserved before its password is compared.
func TestConcurrentFailuresCompareAtMostThreshold(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "erin")

	const attempts = 3 * LockoutThreshold
	var wg sync.WaitGroup
	var mu sync.Mutex
	compared, refused := 0, 0
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Authenticate(ctx, "erin", "wrong-password")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, ErrAccountLocked):
				refused++
			case errors.Is(err, ErrInvalidCredentials):
				compared++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if compared != LockoutThreshold || refused != attempts-LockoutThreshold {
		t.Fatalf("compared=%d refused=%d, want %d / %d", compared, refused, LockoutThreshold, attempts-LockoutThreshold)
	}
	if count, locked := loginState(ctx, t, dbClient, id); count != LockoutThreshold || locked <= 0 {
		t.Fatalf("count=%d locked=%v, want %d and locked", count, locked, LockoutThreshold)
	}
}

func TestUnlockUser(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "frank")

	failLogins(ctx, t, s, "frank", LockoutThreshold)
	wasLocked, err := s.UnlockUser(ctx, id)
	if err != nil || !wasLocked {
		t.Fatalf("UnlockUser = %v, %v; want true, nil", wasLocked, err)
	}
	if _, err := s.Authenticate(ctx, "frank", testPassword); err != nil {
		t.Fatalf("login after unlock: %v", err)
	}
	if wasLocked, err := s.UnlockUser(ctx, id); err != nil || wasLocked {
		t.Fatalf("second UnlockUser = %v, %v; want false, nil", wasLocked, err)
	}
	if _, err := s.UnlockUser(ctx, uuid.New()); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unknown user: err = %v, want ErrUserNotFound", err)
	}
}

// An unknown username (or an SSO-only one) is throttled like a real account,
// so a 429 says nothing about whether the account exists.
func TestUnknownUsernameThrottledLikeAnAccount(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO admin_users (username, email, password_hash, platform_role) VALUES ('sso-only', 'sso@example.com', NULL, 'member')
	`); err != nil {
		t.Fatal(err)
	}

	for _, username := range []string{"nobody", "sso-only"} {
		err := failLogins(ctx, t, s, username, LockoutThreshold)
		var failure *LoginFailure
		if !errors.As(err, &failure) || !failure.LockedNow || failure.UserID != nil {
			t.Fatalf("%s: threshold failure = %#v, want LockedNow without a user", username, err)
		}
		if _, err := s.Authenticate(ctx, username, "anything"); !errors.Is(err, ErrAccountLocked) {
			t.Fatalf("%s: err = %v, want ErrAccountLocked", username, err)
		}
	}
}

func TestAuthenticateToleratesPreLockoutSchema(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	insertUser(ctx, t, dbClient, "grace")
	if _, err := dbClient.ExecContext(ctx, `
		ALTER TABLE admin_users
			DROP COLUMN failed_login_count, DROP COLUMN last_failed_login_at,
			DROP COLUMN locked_until, DROP COLUMN password_changed_at
	`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Authenticate(ctx, "grace", testPassword); err != nil {
		t.Fatalf("correct password on the old schema: %v", err)
	}
	if _, err := s.Authenticate(ctx, "grace", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password on the old schema: err = %v, want invalid credentials", err)
	}
	var id uuid.UUID
	if err := dbClient.QueryRowContext(ctx, `SELECT id FROM admin_users WHERE username = 'grace'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UnlockUser(ctx, id); err != nil {
		t.Fatalf("UnlockUser on the old schema: %v", err)
	}
}

func TestFailureThrottleStreaks(t *testing.T) {
	th := NewFailureThrottle(3, time.Hour, time.Minute, 4*time.Minute)
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 2; i++ {
		if wait := th.Fail("k", now); wait != 0 {
			t.Fatalf("failure %d blocked for %v", i+1, wait)
		}
	}
	if wait := th.Fail("k", now); wait != time.Minute {
		t.Fatalf("third failure blocks for %v, want 1m", wait)
	}
	if th.BlockedFor("k", now.Add(59*time.Second)) == 0 || th.BlockedFor("k", now.Add(time.Minute)) != 0 {
		t.Fatal("block window wrong")
	}
	later := now.Add(2 * time.Minute)
	th.Fail("k", later)
	th.Fail("k", later)
	if wait := th.Fail("k", later); wait != 2*time.Minute {
		t.Fatalf("second block %v, want doubled 2m", wait)
	}
	// A quiet window ends the streak.
	if wait := th.Fail("k", later.Add(2*time.Hour)); wait != 0 {
		t.Fatalf("after a quiet window: blocked %v", wait)
	}
	th.Reset("k")
	if th.BlockedFor("k", later) != 0 {
		t.Fatal("Reset left the key blocked")
	}
}

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

const (
	testPassword = "correct horse battery"
	wrongPass    = "wrong password guess"
)

// fakeClock is the lockout's injectable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newLockoutService(t *testing.T) (*Service, *db.Client, *fakeClock) {
	t.Helper()
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	clock := &fakeClock{t: time.Now().UTC().Truncate(time.Microsecond)}
	svc := NewService(dbClient, 4)
	svc.now = clock.now
	return svc, dbClient, clock
}

func insertPasswordUser(t *testing.T, dbClient *db.Client, username string) uuid.UUID {
	t.Helper()
	hash, err := auth.HashPassword(testPassword, 4)
	if err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := dbClient.QueryRowContext(context.Background(), `
		INSERT INTO admin_users (username, password_hash, platform_role) VALUES ($1, $2, 'member') RETURNING id
	`, username, hash).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func lockState(t *testing.T, dbClient *db.Client, id uuid.UUID) (int, *time.Time) {
	t.Helper()
	var count int
	var until *time.Time
	if err := dbClient.QueryRowContext(context.Background(),
		`SELECT failed_login_count, locked_until FROM admin_users WHERE id = $1`, id,
	).Scan(&count, &until); err != nil {
		t.Fatal(err)
	}
	return count, until
}

func failure(t *testing.T, err error) *LoginFailure {
	t.Helper()
	var f *LoginFailure
	if !errors.As(err, &f) || !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("want a *LoginFailure, got %v", err)
	}
	return f
}

func TestLockoutTenthFailureLocksForFifteenMinutes(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, clock := newLockoutService(t)
	id := insertPasswordUser(t, dbClient, "alice")

	for i := 1; i < LockoutThreshold; i++ {
		_, err := svc.Authenticate(ctx, "alice", wrongPass)
		f := failure(t, err)
		if f.Locked || f.LockedNow || f.FailedAttempts != i {
			t.Fatalf("failure %d: %+v, want an unlocked streak of %d", i, f, i)
		}
	}
	_, err := svc.Authenticate(ctx, "alice", wrongPass)
	f := failure(t, err)
	if !f.LockedNow || f.UserID == nil || *f.UserID != id {
		t.Fatalf("10th failure: %+v, want it to lock alice", f)
	}
	if want := clock.now().Add(LockoutDuration); !f.LockedUntil.Equal(want) {
		t.Fatalf("locked until %v, want %v", f.LockedUntil, want)
	}
	if count, until := lockState(t, dbClient, id); count != 0 || until == nil {
		t.Fatalf("row = %d/%v, want streak restarted and a lock", count, until)
	}

	// The correct password is refused during the lock, with the same error,
	// and the attempt does not extend the lock.
	clock.advance(LockoutDuration - time.Second)
	_, err = svc.Authenticate(ctx, "alice", testPassword)
	f = failure(t, err)
	if !f.Locked {
		t.Fatalf("correct password while locked: %+v, want refused as locked", f)
	}
	if _, until := lockState(t, dbClient, id); until == nil || !until.Equal(clock.now().Add(time.Second)) {
		t.Fatalf("lock moved to %v during the lock", until)
	}

	// The lock expires on its own.
	clock.advance(time.Second)
	user, err := svc.Authenticate(ctx, "alice", testPassword)
	if err != nil || user.ID != id {
		t.Fatalf("after the lock: %v, want a successful sign-in", err)
	}
	if count, until := lockState(t, dbClient, id); count != 0 || until != nil {
		t.Fatalf("after success row = %d/%v, want cleared", count, until)
	}
}

func TestLockoutSuccessResetsTheStreak(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	id := insertPasswordUser(t, dbClient, "bob")

	for i := 0; i < LockoutThreshold-1; i++ {
		_, _ = svc.Authenticate(ctx, "bob", wrongPass)
	}
	if _, err := svc.Authenticate(ctx, "bob", testPassword); err != nil {
		t.Fatalf("correct password at a streak of 9: %v", err)
	}
	if count, _ := lockState(t, dbClient, id); count != 0 {
		t.Fatalf("streak = %d after success, want 0", count)
	}
	// "Consecutive": nine more failures do not lock.
	for i := 0; i < LockoutThreshold-1; i++ {
		_, err := svc.Authenticate(ctx, "bob", wrongPass)
		if f := failure(t, err); f.Locked || f.LockedNow {
			t.Fatalf("failure %d after a success locked: %+v", i+1, f)
		}
	}
}

func TestLockoutUnknownUsersLookLikeWrongPasswords(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	insertPasswordUser(t, dbClient, "carol")
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO admin_users (username, email, platform_role) VALUES ('sso-only', 'sso@example.com', 'member');
		INSERT INTO admin_users (username, password_hash, platform_role, disabled_at)
		VALUES ('gone', 'x', 'member', NOW());
	`); err != nil {
		t.Fatal(err)
	}

	_, wrongErr := svc.Authenticate(ctx, "carol", wrongPass)
	for _, name := range []string{"nobody", "sso-only", "gone"} {
		for i := 0; i < LockoutThreshold+2; i++ {
			_, err := svc.Authenticate(ctx, name, testPassword)
			f := failure(t, err)
			if f.UserID != nil || f.Locked || f.LockedNow {
				t.Fatalf("%s: %+v, want an anonymous refusal", name, f)
			}
			if err.Error() != wrongErr.Error() {
				t.Fatalf("%s: error %q differs from a wrong password's %q", name, err, wrongErr)
			}
		}
	}
	var touched int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM admin_users
		WHERE username IN ('sso-only', 'gone') AND (failed_login_count <> 0 OR locked_until IS NOT NULL)
	`).Scan(&touched); err != nil {
		t.Fatal(err)
	}
	if touched != 0 {
		t.Fatalf("%d accounts without a password were counted, want none", touched)
	}
}

func TestUnlockUser(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	id := insertPasswordUser(t, dbClient, "dave")

	if was, err := svc.UnlockUser(ctx, id); err != nil || was {
		t.Fatalf("unlock of an unlocked user = %v/%v, want false/nil", was, err)
	}
	for i := 0; i < LockoutThreshold; i++ {
		_, _ = svc.Authenticate(ctx, "dave", wrongPass)
	}
	was, err := svc.UnlockUser(ctx, id)
	if err != nil || !was {
		t.Fatalf("unlock = %v/%v, want true/nil", was, err)
	}
	if _, err := svc.Authenticate(ctx, "dave", testPassword); err != nil {
		t.Fatalf("sign-in after unlock: %v", err)
	}
	if _, err := svc.UnlockUser(ctx, uuid.New()); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unlock of an unknown id = %v, want ErrUserNotFound", err)
	}
}

// Parallel guesses serialize on the row: exactly LockoutThreshold of them are
// compared (and counted), one of those locks, every other one is refused as
// locked. Nothing deadlocks and nothing is lost.
func TestLockoutConcurrentFailuresNeitherUndercountNorDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	svc, dbClient, _ := newLockoutService(t)
	id := insertPasswordUser(t, dbClient, "erin")

	const attempts = 40
	var (
		wg                           sync.WaitGroup
		mu                           sync.Mutex
		compared, lockedNow, refused int
	)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Authenticate(ctx, "erin", wrongPass)
			var f *LoginFailure
			if !errors.As(err, &f) {
				t.Errorf("attempt: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			switch {
			case f.Locked:
				refused++
			case f.LockedNow:
				lockedNow++
				compared++
			default:
				compared++
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		t.Fatal("concurrent attempts did not finish: deadlock or lock wait")
	}
	if compared != LockoutThreshold || lockedNow != 1 || refused != attempts-LockoutThreshold {
		t.Fatalf("compared=%d lockedNow=%d refused=%d, want %d/1/%d", compared, lockedNow, refused, LockoutThreshold, attempts-LockoutThreshold)
	}
	if count, until := lockState(t, dbClient, id); count != 0 || until == nil {
		t.Fatalf("row = %d/%v, want locked with the streak restarted", count, until)
	}
}

// A failed second sign-in step counts toward the same lock as passwords.
func TestRecordFailedAttemptSharesTheLock(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	id := insertPasswordUser(t, dbClient, "frank")
	for i := 0; i < LockoutThreshold-1; i++ {
		_, _ = svc.Authenticate(ctx, "frank", wrongPass)
	}
	f, err := svc.RecordFailedAttempt(ctx, id)
	if err != nil || !f.LockedNow {
		t.Fatalf("10th failure via a second step = %+v/%v, want it to lock", f, err)
	}
	f, err = svc.RecordFailedAttempt(ctx, id)
	if err != nil || !f.Locked {
		t.Fatalf("failure during the lock = %+v/%v, want refused as locked", f, err)
	}
	if _, err := svc.Authenticate(ctx, "frank", testPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("password during the lock = %v, want refused", err)
	}
}

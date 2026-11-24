//go:build !unit

package adminauth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// insertMCPConnection gives userID an MCP OAuth grant and a code not yet
// exchanged, and returns the grant id.
func insertMCPConnection(t *testing.T, dbClient *db.Client, userID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tenant := testutil.InsertTenant(ctx, t, dbClient, "MCP "+uuid.NewString()[:8])
	var client, grant uuid.UUID
	if err := dbClient.QueryRowContext(ctx, `
		INSERT INTO mcp_oauth_clients (client_name, redirect_uris) VALUES ('agent', ARRAY['http://127.0.0.1/cb']) RETURNING id
	`).Scan(&client); err != nil {
		t.Fatal(err)
	}
	if err := dbClient.QueryRowContext(ctx, `
		INSERT INTO mcp_oauth_grants (client_id, admin_user_id, tenant_id, scope) VALUES ($1, $2, $3, 'write') RETURNING id
	`, client, userID, tenant).Scan(&grant); err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO mcp_oauth_codes (code_hash, client_id, admin_user_id, tenant_id, scope, redirect_uri, code_challenge, expires_at)
		VALUES ($1, $2, $3, $4, 'write', 'http://127.0.0.1/cb', 'c', NOW() + INTERVAL '5 minutes')
	`, uuid.NewString(), client, userID, tenant); err != nil {
		t.Fatal(err)
	}
	return grant
}

func mcpConnectionLive(t *testing.T, dbClient *db.Client, userID uuid.UUID) (grants, codes int) {
	t.Helper()
	if err := dbClient.QueryRowContext(context.Background(), `
		SELECT (SELECT COUNT(*) FROM mcp_oauth_grants WHERE admin_user_id = $1 AND revoked_at IS NULL),
		       (SELECT COUNT(*) FROM mcp_oauth_codes WHERE admin_user_id = $1)
	`, userID).Scan(&grants, &codes); err != nil {
		t.Fatal(err)
	}
	return grants, codes
}

// Every password set ends the user's MCP connections: a reset, an invite,
// the user's own change, and RevokeMCPAccess (superadmin set).
func TestPasswordSetsRevokeMCPConnections(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	alice := insertPasswordUser(t, dbClient, "alice")

	insertMCPConnection(t, dbClient, alice)
	reset, err := svc.IssuePasswordReset(ctx, alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RedeemPasswordToken(ctx, reset.Token.Token, PurposePasswordReset, newPassword); err != nil {
		t.Fatal(err)
	}
	if g, c := mcpConnectionLive(t, dbClient, alice); g != 0 || c != 0 {
		t.Fatalf("after a reset: %d grants, %d codes live", g, c)
	}

	insertMCPConnection(t, dbClient, alice)
	here, _ := svc.CreateSession(ctx, alice, "here", time.Now().Add(time.Hour), "", "", AuthMethodPassword)
	if _, err := svc.ChangePassword(ctx, alice, here, "yet another passphrase"); err != nil {
		t.Fatal(err)
	}
	if g, c := mcpConnectionLive(t, dbClient, alice); g != 0 || c != 0 {
		t.Fatalf("after an own change: %d grants, %d codes live", g, c)
	}
	if active, _ := svc.SessionActive(ctx, alice, here); !active {
		t.Fatal("own change ended the session making it")
	}

	insertMCPConnection(t, dbClient, alice)
	if n, err := RevokeMCPAccess(ctx, dbClient, alice); err != nil || n != 1 {
		t.Fatalf("RevokeMCPAccess: %d %v", n, err)
	}
	if g, c := mcpConnectionLive(t, dbClient, alice); g != 0 || c != 0 {
		t.Fatalf("after RevokeMCPAccess: %d grants, %d codes live", g, c)
	}

	// Another user's connections are untouched.
	bob := insertPasswordUser(t, dbClient, "bob")
	insertMCPConnection(t, dbClient, bob)
	if _, err := RevokeMCPAccess(ctx, dbClient, alice); err != nil {
		t.Fatal(err)
	}
	if g, _ := mcpConnectionLive(t, dbClient, bob); g != 1 {
		t.Fatal("bob's connection was revoked")
	}
}

// Confirming a new address retires invite and reset links, which went to
// the old one.
func TestEmailChangeRetiresMailedLinks(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	setEmail(t, dbClient, alice, "alice@example.com")
	reset, err := svc.IssuePasswordReset(ctx, alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	change, err := svc.RequestEmailChange(ctx, alice, "moved@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmEmailChange(ctx, change.Token.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RedeemPasswordToken(ctx, reset.Token.Token, PurposePasswordReset, newPassword); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("reset link after an address change: %v", err)
	}

	// The superadmin path (user management) calls RetireMailedLinksTx.
	again, err := svc.IssuePasswordReset(ctx, alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := RetireMailedLinksTx(ctx, tx, alice); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RedeemPasswordToken(ctx, again.Token.Token, PurposePasswordReset, newPassword); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("reset link after a superadmin address change: %v", err)
	}
}

// Wrong codes that lock the account are counted; the third such lock within
// 24 hours holds it until an unlock or a completed reset, and a reset keeps
// the count (the next lock holds again) and the second factor.
func TestRepeatedSecondFactorLockoutsHoldTheAccount(t *testing.T) {
	ctx := context.Background()
	svc, dbClient := newTwoFactorService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	setEmail(t, dbClient, alice, "alice@example.com")
	secret, _ := enrol(t, svc, alice)

	lockWithCodes := func() *LoginFailure {
		t.Helper()
		challenge := signInFirstStep(t, svc, "alice", testPassword)
		var last *LoginFailure
		for i := 0; i < LockoutThreshold; i++ {
			_, err := svc.CompleteSecondFactor(ctx, challenge, "000001")
			last = failure(t, err)
		}
		if !last.LockedNow {
			t.Fatalf("ten wrong codes did not lock: %+v", last)
		}
		return last
	}

	for round := 1; round <= SecondFactorLockoutsToHold; round++ {
		f := lockWithCodes()
		if f.SecondFactorLockouts != round || f.Held != (round == SecondFactorLockoutsToHold) {
			t.Fatalf("round %d: lockouts=%d held=%v", round, f.SecondFactorLockouts, f.Held)
		}
		svc.clockOffset += LockoutDuration + time.Minute
	}
	// The ordinary 15 minutes have passed: a held lock is still in force.
	if _, err := svc.Authenticate(ctx, "alice", testPassword); !failure(t, err).Locked {
		t.Fatalf("held account after 15 minutes: %v", err)
	}
	var held bool
	_ = dbClient.QueryRowContext(ctx, `SELECT lockout_held FROM admin_users WHERE id = $1`, alice).Scan(&held)
	if !held {
		t.Fatal("lockout_held not set")
	}

	// A completed reset lifts the hold; the second factor stays on.
	reset, err := svc.IssuePasswordReset(ctx, alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RedeemPasswordToken(ctx, reset.Token.Token, PurposePasswordReset, newPassword); err != nil {
		t.Fatal(err)
	}
	challenge := signInFirstStep(t, svc, "alice", newPassword)
	// The count survived the reset: the next lock holds at once.
	for i := 0; i < LockoutThreshold-1; i++ {
		_, _ = svc.CompleteSecondFactor(ctx, challenge, "000001")
	}
	_, err = svc.CompleteSecondFactor(ctx, challenge, "000001")
	if f := failure(t, err); !f.Held || f.SecondFactorLockouts != SecondFactorLockoutsToHold+1 {
		t.Fatalf("lock after a reset: %+v, want held again", f)
	}

	// A superadmin unlock lifts it and clears the count.
	if was, err := svc.UnlockUser(ctx, alice); err != nil || !was {
		t.Fatalf("unlock: %v %v", was, err)
	}
	var lockouts int
	_ = dbClient.QueryRowContext(ctx, `SELECT second_factor_lockouts, lockout_held FROM admin_users WHERE id = $1`, alice).Scan(&lockouts, &held)
	if lockouts != 0 || held {
		t.Fatalf("after unlock: lockouts=%d held=%v", lockouts, held)
	}
	challenge = signInFirstStep(t, svc, "alice", newPassword)
	if _, err := svc.CompleteSecondFactor(ctx, challenge, stepCode(t, dbClient, secret, alice, 1)); err != nil {
		t.Fatalf("sign-in after unlock: %v", err)
	}

	// Wrong passwords never count as second-factor lockouts.
	for i := 0; i < LockoutThreshold; i++ {
		_, _ = svc.Authenticate(ctx, "alice", wrongPass)
	}
	_ = dbClient.QueryRowContext(ctx, `SELECT second_factor_lockouts FROM admin_users WHERE id = $1`, alice).Scan(&lockouts)
	if lockouts != 0 {
		t.Fatalf("password lockout counted as a second-factor lockout: %d", lockouts)
	}
}

// Without the key that sealed the secret, codes cannot be checked: the
// attempt is given back (no lockout behind a configuration fault) and
// recovery codes still sign in.
func TestUnreadableSecretGivesTheAttemptBack(t *testing.T) {
	ctx := context.Background()
	svc, dbClient := newTwoFactorService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	secret, codes := enrol(t, svc, alice)
	svc.ConfigureEncryption(nil) // PROBARA_SECRETS_KEY removed

	for i := 0; i < LockoutThreshold+2; i++ {
		challenge := signInFirstStep(t, svc, "alice", testPassword)
		_, err := svc.CompleteSecondFactor(ctx, challenge, stepCode(t, dbClient, secret, alice, 1))
		if !errors.Is(err, ErrSecretUnreadable) {
			t.Fatalf("attempt %d: %v, want ErrSecretUnreadable", i+1, err)
		}
	}
	if count, until := lockState(t, dbClient, alice); count != 0 || until != nil {
		t.Fatalf("unreadable secret counted toward the lockout: %d %v", count, until)
	}
	if _, err := svc.VerifyCurrentCredentials(ctx, alice, testPassword, "123456", true); !errors.Is(err, ErrSecretUnreadable) {
		t.Fatalf("account page check: %v", err)
	}
	if count, _ := lockState(t, dbClient, alice); count != 0 {
		t.Fatalf("account page check counted: %d", count)
	}
	challenge := signInFirstStep(t, svc, "alice", testPassword)
	if done, err := svc.CompleteSecondFactor(ctx, challenge, codes[0]); err != nil || done.Method != SecondFactorRecoveryCode {
		t.Fatalf("recovery code without the key: %+v %v", done, err)
	}
}

// parallel runs fn twice at once and returns both errors.
func parallel(fn func() error) [2]error {
	var wg sync.WaitGroup
	var out [2]error
	start := make(chan struct{})
	for i := range out {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			out[i] = fn()
		}(i)
	}
	close(start)
	wg.Wait()
	return out
}

func successes(errs [2]error) int {
	n := 0
	for _, err := range errs {
		if err == nil {
			n++
		}
	}
	return n
}

func TestConcurrentUsesOfOneCredentialOneWins(t *testing.T) {
	ctx := context.Background()
	svc, dbClient := newTwoFactorService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	setEmail(t, dbClient, alice, "alice@example.com")
	secret, codes := enrol(t, svc, alice)

	// The same TOTP code on two sign-ins at once.
	code := stepCode(t, dbClient, secret, alice, 1)
	c1 := signInFirstStep(t, svc, "alice", testPassword)
	c2 := signInFirstStep(t, svc, "alice", testPassword)
	challenges := make(chan string, 2)
	challenges <- c1
	challenges <- c2
	errs := parallel(func() error {
		_, err := svc.CompleteSecondFactor(ctx, <-challenges, code)
		return err
	})
	if successes(errs) != 1 {
		t.Fatalf("one TOTP code twice in parallel: %v", errs)
	}
	if _, err := svc.UnlockUser(ctx, alice); err != nil {
		t.Fatal(err)
	}

	// The same recovery code twice at once.
	c1 = signInFirstStep(t, svc, "alice", testPassword)
	c2 = signInFirstStep(t, svc, "alice", testPassword)
	challenges <- c1
	challenges <- c2
	errs = parallel(func() error {
		_, err := svc.CompleteSecondFactor(ctx, <-challenges, codes[0])
		return err
	})
	if successes(errs) != 1 {
		t.Fatalf("one recovery code twice in parallel: %v", errs)
	}

	// One reset link redeemed twice at once.
	reset, err := svc.IssuePasswordReset(ctx, alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	errs = parallel(func() error {
		_, err := svc.RedeemPasswordToken(ctx, reset.Token.Token, PurposePasswordReset, newPassword)
		return err
	})
	if successes(errs) != 1 {
		t.Fatalf("one link redeemed twice in parallel: %v", errs)
	}

	// Two accounts confirming the same new address at once.
	bob := insertPasswordUser(t, dbClient, "bob")
	setEmail(t, dbClient, bob, "bob@example.com")
	a, err := svc.RequestEmailChange(ctx, alice, "shared@example.com")
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.RequestEmailChange(ctx, bob, "shared@example.com")
	if err != nil {
		t.Fatal(err)
	}
	tokens := make(chan string, 2)
	tokens <- a.Token.Token
	tokens <- b.Token.Token
	errs = parallel(func() error {
		_, err := svc.ConfirmEmailChange(ctx, <-tokens)
		return err
	})
	if successes(errs) != 1 {
		t.Fatalf("two confirmations for one address: %v", errs)
	}
	for _, err := range errs {
		if err != nil && !errors.Is(err, ErrEmailTaken) {
			t.Fatalf("the losing confirmation: %v, want ErrEmailTaken", err)
		}
	}
	var holders int
	_ = dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users WHERE email = 'shared@example.com'`).Scan(&holders)
	if holders != 1 {
		t.Fatalf("%d accounts hold the address", holders)
	}
}

func TestOverlongPasswordIsAValidationErrorNotABcryptFailure(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	reset, err := svc.IssuePasswordReset(ctx, alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("密", 30) // 90 bytes
	if _, err := svc.RedeemPasswordToken(ctx, reset.Token.Token, PurposePasswordReset, long); !errors.Is(err, auth.ErrPasswordTooLong) {
		t.Fatalf("reset with 90 bytes: %v", err)
	}
	if _, err := svc.ChangePassword(ctx, alice, uuid.Nil, long); !errors.Is(err, auth.ErrPasswordTooLong) {
		t.Fatalf("own change with 90 bytes: %v", err)
	}
}

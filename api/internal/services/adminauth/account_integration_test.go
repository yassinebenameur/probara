//go:build !unit

package adminauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/db"
)

const newPassword = "a brand new passphrase"

func setEmail(t *testing.T, dbClient *db.Client, id uuid.UUID, email string) {
	t.Helper()
	if _, err := dbClient.ExecContext(context.Background(), `UPDATE admin_users SET email = $2 WHERE id = $1`, id, email); err != nil {
		t.Fatal(err)
	}
}

func insertPasswordlessUser(t *testing.T, dbClient *db.Client, username, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := dbClient.QueryRowContext(context.Background(), `
		INSERT INTO admin_users (username, email, platform_role) VALUES ($1, $2, 'member') RETURNING id
	`, username, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func liveSessions(t *testing.T, dbClient *db.Client, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := dbClient.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM admin_session_families WHERE admin_user_id = $1 AND revoked_at IS NULL
	`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func passwordWorks(t *testing.T, svc *Service, username, password string) bool {
	t.Helper()
	_, err := svc.Authenticate(context.Background(), username, password)
	var second *SecondFactorRequired
	return err == nil || errors.As(err, &second)
}

// expireToken moves a token's expiry into the past (the database clock
// decides expiry).
func expireToken(t *testing.T, dbClient *db.Client, plain string) {
	t.Helper()
	if _, err := dbClient.ExecContext(context.Background(), `
		UPDATE admin_account_tokens SET expires_at = NOW() - INTERVAL '1 second' WHERE token_hash = $1
	`, hashAccountToken(plain)); err != nil {
		t.Fatal(err)
	}
}

func TestAccountTokensAreSingleUseExpireAndKeepToTheirPurpose(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	setEmail(t, dbClient, alice, "alice@example.com")
	invited := insertPasswordlessUser(t, dbClient, "invited", "invited@example.com")
	admin := insertPasswordUser(t, dbClient, "root")

	reset, err := svc.IssuePasswordReset(ctx, alice, &admin)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := svc.IssueInvite(ctx, invited, &admin)
	if err != nil {
		t.Fatal(err)
	}
	if got := reset.Token.ExpiresAt.Sub(time.Now()); got < 50*time.Minute || got > 70*time.Minute {
		t.Fatalf("reset link lifetime %v, want about 1h", got)
	}
	if got := invite.Token.ExpiresAt.Sub(time.Now()); got < 6*24*time.Hour || got > 8*24*time.Hour {
		t.Fatalf("invite lifetime %v, want about 7 days", got)
	}

	// Purpose confusion: an invite token never resets alice's password, a
	// reset token never accepts an invite or confirms an email.
	if _, err := svc.RedeemPasswordToken(ctx, invite.Token.Token, PurposePasswordReset, newPassword); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("invite token as a reset: %v", err)
	}
	if _, err := svc.RedeemPasswordToken(ctx, reset.Token.Token, PurposeInvite, newPassword); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("reset token as an invite: %v", err)
	}
	if _, err := svc.ConfirmEmailChange(ctx, reset.Token.Token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("reset token as an email confirmation: %v", err)
	}
	if _, err := svc.RedeemPasswordToken(ctx, reset.Token.Token, PurposeMFAChallenge, newPassword); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("reset token for another purpose: %v", err)
	}

	// Single use.
	if _, err := svc.RedeemPasswordToken(ctx, reset.Token.Token, PurposePasswordReset, newPassword); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := svc.RedeemPasswordToken(ctx, reset.Token.Token, PurposePasswordReset, "another passphrase!"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("second use: %v", err)
	}
	if !passwordWorks(t, svc, "alice", newPassword) {
		t.Fatal("new password does not work")
	}

	// Expiry.
	expireToken(t, dbClient, invite.Token.Token)
	if _, err := svc.RedeemPasswordToken(ctx, invite.Token.Token, PurposeInvite, newPassword); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expired invite: %v", err)
	}
	if _, _, err := svc.InviteSubject(ctx, invite.Token.Token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expired invite inspect: %v", err)
	}

	// A tampered token is unknown.
	if _, err := svc.RedeemPasswordToken(ctx, reset.Token.Token[:len(reset.Token.Token)-1]+"A", PurposePasswordReset, newPassword); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("tampered token: %v", err)
	}
}

func TestNewerLinkSupersedesTheOlderOne(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	invited := insertPasswordlessUser(t, dbClient, "invited", "invited@example.com")

	first, err := svc.IssueInvite(ctx, invited, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.IssueInvite(ctx, invited, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RedeemPasswordToken(ctx, first.Token.Token, PurposeInvite, newPassword); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("superseded invite: %v", err)
	}
	account, expires, err := svc.InviteSubject(ctx, second.Token.Token)
	if err != nil || account.Username != "invited" || expires.IsZero() {
		t.Fatalf("inspect: %+v %v %v", account, expires, err)
	}
	if _, err := svc.RedeemPasswordToken(ctx, second.Token.Token, PurposeInvite, newPassword); err != nil {
		t.Fatalf("newest invite: %v", err)
	}
	if !passwordWorks(t, svc, "invited", newPassword) {
		t.Fatal("invited user cannot sign in with the chosen password")
	}
	// Now that it has a password, it gets reset links, not invites.
	if _, err := svc.IssueInvite(ctx, invited, nil); !errors.Is(err, ErrHasPassword) {
		t.Fatalf("invite with a password: %v", err)
	}
}

func TestLinksAreForEnabledLocalAccountsOnly(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	sso := insertPasswordlessUser(t, dbClient, "sso", "sso@example.com")
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET external_issuer = 'https://idp', external_subject = 'sub-1' WHERE id = $1`, sso); err != nil {
		t.Fatal(err)
	}
	disabled := insertPasswordUser(t, dbClient, "gone")
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET disabled_at = NOW() WHERE id = $1`, disabled); err != nil {
		t.Fatal(err)
	}
	pending := insertPasswordlessUser(t, dbClient, "pending", "pending@example.com")

	if _, err := svc.IssueInvite(ctx, sso, nil); !errors.Is(err, ErrNotLocalAccount) {
		t.Fatalf("invite SSO: %v", err)
	}
	if _, err := svc.IssuePasswordReset(ctx, sso, nil); !errors.Is(err, ErrNotLocalAccount) {
		t.Fatalf("reset SSO: %v", err)
	}
	if _, err := svc.IssuePasswordReset(ctx, disabled, nil); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("reset disabled: %v", err)
	}
	if _, err := svc.IssuePasswordReset(ctx, pending, nil); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("reset password-less: %v", err)
	}
	if _, err := svc.IssueInvite(ctx, uuid.New(), nil); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("invite unknown: %v", err)
	}

	// A link issued before the account was disabled stops working.
	invite, err := svc.IssueInvite(ctx, pending, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET disabled_at = NOW() WHERE id = $1`, pending); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RedeemPasswordToken(ctx, invite.Token.Token, PurposeInvite, newPassword); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("invite of a disabled account: %v", err)
	}
}

// Forgot password mails only an enabled local account with an address (a
// locked one included), at most PasswordResetMailsPerHour times an hour, and
// answers nil for everything else.
func TestRequestPasswordResetEligibilityAndHourlyCap(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	setEmail(t, dbClient, alice, "Alice@Example.com")
	locked := insertPasswordUser(t, dbClient, "locked")
	setEmail(t, dbClient, locked, "locked@example.com")
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET locked_until = NOW() + INTERVAL '10 minutes' WHERE id = $1`, locked); err != nil {
		t.Fatal(err)
	}
	disabled := insertPasswordUser(t, dbClient, "disabled")
	setEmail(t, dbClient, disabled, "disabled@example.com")
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET disabled_at = NOW() WHERE id = $1`, disabled); err != nil {
		t.Fatal(err)
	}
	sso := insertPasswordUser(t, dbClient, "sso")
	setEmail(t, dbClient, sso, "sso@example.com")
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET external_issuer = 'https://idp', external_subject = 's' WHERE id = $1`, sso); err != nil {
		t.Fatal(err)
	}
	insertPasswordUser(t, dbClient, "no-email")

	for _, login := range []string{"nobody", "nobody@example.com", "disabled", "sso", "sso@example.com", "no-email", ""} {
		link, err := svc.RequestPasswordReset(ctx, login)
		if err != nil || link != nil {
			t.Fatalf("%q: link=%v err=%v, want nothing to send", login, link, err)
		}
	}
	if link, err := svc.RequestPasswordReset(ctx, "locked"); err != nil || link == nil {
		t.Fatalf("locked account: %v %v, want a reset link", link, err)
	}
	// By username or by address, case-insensitively.
	for i, login := range []string{"alice", "alice@example.com", "ALICE@example.COM"} {
		link, err := svc.RequestPasswordReset(ctx, login)
		if err != nil || link == nil || link.Account.ID != alice {
			t.Fatalf("request %d (%s): %+v %v", i+1, login, link, err)
		}
	}
	if link, err := svc.RequestPasswordReset(ctx, "alice"); err != nil || link != nil {
		t.Fatalf("request past the hourly cap: %v %v, want nothing", link, err)
	}
	// Links a superadmin issues are not capped.
	if _, err := svc.IssuePasswordReset(ctx, alice, &alice); err != nil {
		t.Fatalf("superadmin link past the cap: %v", err)
	}
}

func TestResetRevokesSessionsClearsTheLockAndRetiresOtherLinks(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	setEmail(t, dbClient, alice, "alice@example.com")
	for i := 0; i < 2; i++ {
		if _, err := svc.CreateSession(ctx, alice, uuid.NewString(), time.Now().Add(time.Hour), "", "", AuthMethodPassword); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < LockoutThreshold; i++ {
		_, _ = svc.Authenticate(ctx, "alice", wrongPass)
	}
	if _, until := lockState(t, dbClient, alice); until == nil || !until.After(time.Now()) {
		t.Fatal("setup: alice is not locked")
	}
	change, err := svc.RequestEmailChange(ctx, alice, "elsewhere@example.com")
	if err != nil {
		t.Fatal(err)
	}
	reset, err := svc.IssuePasswordReset(ctx, alice, nil)
	if err != nil {
		t.Fatal(err)
	}

	account, err := svc.RedeemPasswordToken(ctx, reset.Token.Token, PurposePasswordReset, newPassword)
	if err != nil || account.ID != alice {
		t.Fatalf("reset: %v", err)
	}
	if n := liveSessions(t, dbClient, alice); n != 0 {
		t.Fatalf("%d sessions survived the reset", n)
	}
	if count, until := lockState(t, dbClient, alice); count != 0 || until != nil {
		t.Fatalf("lockout after reset: %d %v, want cleared", count, until)
	}
	if _, err := svc.ConfirmEmailChange(ctx, change.Token.Token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("email change after a reset: %v, want it cancelled", err)
	}
	if !passwordWorks(t, svc, "alice", newPassword) {
		t.Fatal("new password does not work")
	}
}

func TestEmailChangeAppliesOnlyOnConfirmation(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	setEmail(t, dbClient, alice, "alice@example.com")
	bob := insertPasswordUser(t, dbClient, "bob")
	setEmail(t, dbClient, bob, "bob@example.com")

	if _, err := svc.RequestEmailChange(ctx, alice, "ALICE@example.com"); !errors.Is(err, ErrSameEmail) {
		t.Fatalf("same address: %v", err)
	}
	first, err := svc.RequestEmailChange(ctx, alice, "first@example.com")
	if err != nil {
		t.Fatal(err)
	}
	change, err := svc.RequestEmailChange(ctx, alice, "New@Example.com")
	if err != nil {
		t.Fatal(err)
	}
	if change.NewEmail != "new@example.com" {
		t.Fatalf("new email %q, want it lowercased", change.NewEmail)
	}
	var email string
	_ = dbClient.QueryRowContext(ctx, `SELECT email FROM admin_users WHERE id = $1`, alice).Scan(&email)
	if email != "alice@example.com" {
		t.Fatalf("email changed before confirmation: %s", email)
	}
	pending, _, err := svc.PendingEmailChange(ctx, alice)
	if err != nil || pending == nil || *pending != "new@example.com" {
		t.Fatalf("pending: %v %v", pending, err)
	}
	if _, err := svc.ConfirmEmailChange(ctx, first.Token.Token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("superseded confirmation: %v", err)
	}

	done, err := svc.ConfirmEmailChange(ctx, change.Token.Token)
	if err != nil || done.NewEmail != "new@example.com" || done.OldEmail == nil || *done.OldEmail != "alice@example.com" {
		t.Fatalf("confirm: %+v %v", done, err)
	}
	_ = dbClient.QueryRowContext(ctx, `SELECT email FROM admin_users WHERE id = $1`, alice).Scan(&email)
	if email != "new@example.com" {
		t.Fatalf("email after confirmation: %s", email)
	}
	if _, err := svc.ConfirmEmailChange(ctx, change.Token.Token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("second confirmation: %v", err)
	}

	// An address another account holds is refused at confirmation, and the
	// link stays usable once the address is free again.
	taken, err := svc.RequestEmailChange(ctx, alice, "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmEmailChange(ctx, taken.Token.Token); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("taken address: %v", err)
	}
	setEmail(t, dbClient, bob, "bob2@example.com")
	if _, err := svc.ConfirmEmailChange(ctx, taken.Token.Token); err != nil {
		t.Fatalf("freed address: %v", err)
	}
}

func TestEmailChangeIsCancelledByCredentialResets(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	setEmail(t, dbClient, alice, "alice@example.com")

	// Superadmin password set (user management calls RetireAccountTokens).
	change, err := svc.RequestEmailChange(ctx, alice, "thief@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := RetireAccountTokens(ctx, dbClient, alice); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmEmailChange(ctx, change.Token.Token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("after a superadmin password set: %v", err)
	}

	// The user's own password change.
	change, err = svc.RequestEmailChange(ctx, alice, "thief@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ChangePassword(ctx, alice, uuid.Nil, newPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmEmailChange(ctx, change.Token.Token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("after a password change: %v", err)
	}

	// A superadmin two-factor reset.
	change, err = svc.RequestEmailChange(ctx, alice, "thief@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResetTwoFactor(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmEmailChange(ctx, change.Token.Token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("after a two-factor reset: %v", err)
	}
}

func TestOwnPasswordChangeKeepsThisSessionAndCountsWrongPasswords(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	here, err := svc.CreateSession(ctx, alice, "here", time.Now().Add(time.Hour), "", "", AuthMethodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSession(ctx, alice, "elsewhere", time.Now().Add(time.Hour), "", "", AuthMethodPassword); err != nil {
		t.Fatal(err)
	}

	err = svc.VerifyCurrentPassword(ctx, alice, wrongPass)
	if f := failure(t, err); f.Locked || f.FailedAttempts != 1 {
		t.Fatalf("wrong current password: %+v", f)
	}
	// A right one takes back only its own count.
	if err := svc.VerifyCurrentPassword(ctx, alice, testPassword); err != nil {
		t.Fatal(err)
	}
	if count, _ := lockState(t, dbClient, alice); count != 1 {
		t.Fatalf("streak after a right password: %d, want the earlier failure kept", count)
	}
	for i := 1; i < LockoutThreshold; i++ {
		_ = svc.VerifyCurrentPassword(ctx, alice, wrongPass)
	}
	if err := svc.VerifyCurrentPassword(ctx, alice, testPassword); !failure(t, err).Locked {
		t.Fatalf("right password while locked: %v, want refused as locked", err)
	}
	if _, err := svc.UnlockUser(ctx, alice); err != nil {
		t.Fatal(err)
	}

	revoked, err := svc.ChangePassword(ctx, alice, here, newPassword)
	if err != nil || revoked != 1 {
		t.Fatalf("change: revoked=%d err=%v", revoked, err)
	}
	if active, _ := svc.SessionActive(ctx, alice, here); !active {
		t.Fatal("the session making the change was signed out")
	}
	if n := liveSessions(t, dbClient, alice); n != 1 {
		t.Fatalf("%d live sessions, want only this one", n)
	}
	if !passwordWorks(t, svc, "alice", newPassword) || passwordWorks(t, svc, "alice", testPassword) {
		t.Fatal("password did not change")
	}

	// SSO accounts change nothing here.
	sso := insertPasswordUser(t, dbClient, "sso")
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET external_issuer = 'https://idp', external_subject = 's' WHERE id = $1`, sso); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ChangePassword(ctx, sso, uuid.Nil, newPassword); !errors.Is(err, ErrNotLocalAccount) {
		t.Fatalf("SSO password change: %v", err)
	}
	if _, err := svc.RequestEmailChange(ctx, sso, "x@example.com"); !errors.Is(err, ErrNotLocalAccount) {
		t.Fatalf("SSO email change: %v", err)
	}
}

func TestPruneSessionsDropsLongExpiredAccountTokens(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	old, err := svc.IssuePasswordReset(ctx, alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_account_tokens SET expires_at = NOW() - INTERVAL '40 days' WHERE token_hash = $1`, hashAccountToken(old.Token.Token)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IssueInvite(ctx, insertPasswordlessUser(t, dbClient, "p", "p@example.com"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := PruneSessions(ctx, dbClient, SessionRetention(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_account_tokens`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d tokens left, want the live invite only", n)
	}
}

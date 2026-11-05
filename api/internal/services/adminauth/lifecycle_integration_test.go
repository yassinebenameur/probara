//go:build !unit

package adminauth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/db"
)

func setEmail(ctx context.Context, t *testing.T, dbClient *db.Client, id uuid.UUID, email string) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET email = $2 WHERE id = $1`, id, email); err != nil {
		t.Fatal(err)
	}
}

func liveSessions(ctx context.Context, t *testing.T, dbClient *db.Client, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM admin_session_families WHERE admin_user_id = $1 AND revoked_at IS NULL
	`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPasswordResetTokenLifecycle(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "alice")
	setEmail(ctx, t, dbClient, id, "Alice@Example.com")
	if _, err := s.CreateSession(ctx, id, "h1", time.Now().Add(time.Hour), "", "", AuthMethodPassword); err != nil {
		t.Fatal(err)
	}

	// Unknown logins, SSO accounts and accounts without email get nothing.
	ssoID := insertUser(ctx, t, dbClient, "sso")
	setEmail(ctx, t, dbClient, ssoID, "sso@example.com")
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET external_subject = 'sub-1' WHERE id = $1`, ssoID); err != nil {
		t.Fatal(err)
	}
	insertUser(ctx, t, dbClient, "noemail")
	for _, login := range []string{"nobody", "nobody@example.com", "sso", "sso@example.com", "noemail", ""} {
		if got, err := s.RequestPasswordReset(ctx, login); err != nil || got != nil {
			t.Fatalf("RequestPasswordReset(%q) = %+v, %v; want nothing", login, got, err)
		}
	}

	// By username, and by email in any case.
	reset, err := s.RequestPasswordReset(ctx, "alice")
	if err != nil || reset == nil || reset.Email != "Alice@Example.com" || reset.UserID != id {
		t.Fatalf("by username: %+v, %v", reset, err)
	}
	if again, err := s.RequestPasswordReset(ctx, "alice@example.COM"); err != nil || again != nil {
		t.Fatalf("second request inside the throttle = %+v, %v; want nothing", again, err)
	}
	// Past the throttle a new link supersedes the first.
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_user_tokens SET created_at = NOW() - INTERVAL '2 minutes'`); err != nil {
		t.Fatal(err)
	}
	second, err := s.RequestPasswordReset(ctx, "alice@example.COM")
	if err != nil || second == nil {
		t.Fatalf("by email after throttle: %+v, %v", second, err)
	}
	if _, err := s.RedeemPasswordToken(ctx, reset.Token.Token, PurposePasswordReset, "brand-new-password-1"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("superseded token: err = %v", err)
	}
	// Wrong purpose is refused.
	if _, err := s.RedeemPasswordToken(ctx, second.Token.Token, PurposeInvite, "brand-new-password-1"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("reset token as invite: err = %v", err)
	}

	account, err := s.RedeemPasswordToken(ctx, second.Token.Token, PurposePasswordReset, "brand-new-password-1")
	if err != nil || account.ID != id {
		t.Fatalf("redeem: %+v, %v", account, err)
	}
	if n := liveSessions(ctx, t, dbClient, id); n != 0 {
		t.Fatalf("live sessions after reset = %d, want 0", n)
	}
	if _, err := s.Authenticate(ctx, "alice", "brand-new-password-1"); err != nil {
		t.Fatalf("login with the new password: %v", err)
	}
	// Single use.
	if _, err := s.RedeemPasswordToken(ctx, second.Token.Token, PurposePasswordReset, "another-password-22"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("reused token: err = %v", err)
	}
}

func TestAccountTokenExpiryAndConcurrentRedeem(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "bob")
	_, invite, err := s.IssueInvite(ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if invite.ExpiresAt.Before(time.Now().Add(InviteTTL - time.Minute)) {
		t.Fatalf("invite expires %v, want ~%v from now", invite.ExpiresAt, InviteTTL)
	}
	account, _, err := s.InviteSubject(ctx, invite.Token)
	if err != nil || account.Username != "bob" {
		t.Fatalf("InviteSubject = %+v, %v", account, err)
	}

	// Concurrent redemptions: exactly one wins.
	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = s.RedeemPasswordToken(ctx, invite.Token, PurposeInvite, "invited-password-1")
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrTokenInvalid) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d redemptions succeeded, want 1", wins)
	}

	// An expired link is refused, and inspecting it says nothing more.
	_, expired, err := s.IssueInvite(ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_user_tokens SET expires_at = NOW() - INTERVAL '1 second' WHERE used_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.InviteSubject(ctx, expired.Token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expired inspect: %v", err)
	}
	if _, err := s.RedeemPasswordToken(ctx, expired.Token, PurposeInvite, "invited-password-2"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expired redeem: %v", err)
	}
	var stored int
	if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_user_tokens WHERE token_hash = $1`, expired.Token).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("plain token stored: %d %v", stored, err)
	}
}

func TestSelfServiceRefusedForLinkedAccounts(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "carol")
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET external_subject = 'sub-carol' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangePassword(ctx, id, uuid.Nil, "whatever-password-1"); !errors.Is(err, ErrNotLocalAccount) {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := s.RequestEmailChange(ctx, id, "new@example.com"); !errors.Is(err, ErrNotLocalAccount) {
		t.Fatalf("RequestEmailChange: %v", err)
	}
	if _, _, err := s.IssueInvite(ctx, id, nil); !errors.Is(err, ErrInviteSSOOnly) {
		t.Fatalf("IssueInvite: %v", err)
	}
	if _, _, err := s.IssuePasswordReset(ctx, id, nil); !errors.Is(err, ErrNotLocalAccount) {
		t.Fatalf("IssuePasswordReset: %v", err)
	}
	if _, err := s.BeginTOTPEnrollment(ctx, id); !errors.Is(err, ErrNotLocalAccount) {
		t.Fatalf("BeginTOTPEnrollment: %v", err)
	}
}

func TestEmailChangeConfirmsThroughNewAddress(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "dave")
	setEmail(ctx, t, dbClient, id, "dave@old.example")
	other := insertUser(ctx, t, dbClient, "erin")
	setEmail(ctx, t, dbClient, other, "erin@example.com")

	if _, err := s.RequestEmailChange(ctx, id, "ERIN@example.com"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("taken: %v", err)
	}
	if _, err := s.RequestEmailChange(ctx, id, "dave@old.example"); !errors.Is(err, ErrSameEmail) {
		t.Fatalf("same: %v", err)
	}
	change, err := s.RequestEmailChange(ctx, id, " Dave@New.Example ")
	if err != nil || change.NewEmail != "dave@new.example" {
		t.Fatalf("request: %+v, %v", change, err)
	}
	// Nothing changes until the link is used.
	var email string
	_ = dbClient.QueryRowContext(ctx, `SELECT email FROM admin_users WHERE id = $1`, id).Scan(&email)
	if email != "dave@old.example" {
		t.Fatalf("email changed before confirmation: %s", email)
	}
	done, err := s.ConfirmEmailChange(ctx, change.Token.Token)
	if err != nil || done.NewEmail != "dave@new.example" || done.OldEmail == nil || *done.OldEmail != "dave@old.example" {
		t.Fatalf("confirm: %+v, %v", done, err)
	}
	_ = dbClient.QueryRowContext(ctx, `SELECT email FROM admin_users WHERE id = $1`, id).Scan(&email)
	if email != "dave@new.example" {
		t.Fatalf("email = %s", email)
	}
	if _, err := s.ConfirmEmailChange(ctx, change.Token.Token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("reused: %v", err)
	}
}

func TestChangePasswordKeepsCurrentSession(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "frank")
	keep, _ := s.CreateSession(ctx, id, "k", time.Now().Add(time.Hour), "", "", AuthMethodPassword)
	if _, err := s.CreateSession(ctx, id, "o", time.Now().Add(time.Hour), "", "", AuthMethodPassword); err != nil {
		t.Fatal(err)
	}
	_, reset, _ := s.IssuePasswordReset(ctx, id, nil)
	if err := s.VerifyPassword(ctx, id, "wrong-password"); err == nil {
		t.Fatal("wrong password verified")
	}
	if count, _ := loginState(ctx, t, dbClient, id); count != 1 {
		t.Fatalf("failed attempts = %d, want 1", count)
	}
	if err := s.VerifyPassword(ctx, id, testPassword); err != nil {
		t.Fatal(err)
	}
	revoked, err := s.ChangePassword(ctx, id, keep, "changed-password-1")
	if err != nil || revoked != 1 {
		t.Fatalf("ChangePassword = %d, %v", revoked, err)
	}
	if active, _ := SessionFamilyActive(ctx, dbClient, id, keep); !active {
		t.Fatal("current session ended")
	}
	if _, err := s.RedeemPasswordToken(ctx, reset.Token, PurposePasswordReset, "x-password-12345"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("reset link survived a password change: %v", err)
	}
}

// enrollTOTP enables TOTP for id and returns the secret and recovery codes.
func enrollTOTP(ctx context.Context, t *testing.T, s *Service, id uuid.UUID) (string, []string) {
	t.Helper()
	secret, err := s.BeginTOTPEnrollment(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnableTOTP(ctx, id, "000000x"); !errors.Is(err, ErrInvalidMFACode) {
		t.Fatalf("enable with a bad code: %v", err)
	}
	// Enroll with the previous step's code, so the current one stays usable.
	codes, err := s.EnableTOTP(ctx, id, codeAt(t, secret, time.Now().Add(-totpPeriod*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("%d recovery codes", len(codes))
	}
	return secret, codes
}

func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	return hotp(key, uint64(totpStep(at)), totpDigits)
}

func TestTOTPEnrollmentLoginAndReplay(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "grace")
	secret, codes := enrollTOTP(ctx, t, s, id)

	var sealed string
	_ = dbClient.QueryRowContext(ctx, `SELECT totp_secret_enc FROM admin_users WHERE id = $1`, id).Scan(&sealed)
	if sealed == "" {
		t.Fatal("secret not stored")
	}
	if _, err := s.BeginTOTPEnrollment(ctx, id); !errors.Is(err, ErrMFAAlreadyEnabled) {
		t.Fatalf("re-enroll while enabled: %v", err)
	}

	// Step one: the password alone is not a sign-in.
	_, err := s.Authenticate(ctx, "grace", testPassword)
	var sf *SecondFactorRequired
	if !errors.As(err, &sf) || sf.UserID != id || sf.Binding == "" {
		t.Fatalf("Authenticate = %v, want SecondFactorRequired", err)
	}
	if _, err := s.CompleteSecondFactor(ctx, id, "stale-binding", codeAt(t, secret, time.Now())); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("wrong binding: %v", err)
	}
	code := codeAt(t, secret, time.Now())
	method, err := s.CompleteSecondFactor(ctx, id, sf.Binding, code)
	if err != nil || method != MFAMethodTOTP {
		t.Fatalf("step two = %q, %v", method, err)
	}
	// Replay of the same code fails.
	if _, err := s.CompleteSecondFactor(ctx, id, sf.Binding, code); err == nil {
		t.Fatal("replayed code accepted")
	}

	// A recovery code works once.
	if method, err := s.CompleteSecondFactor(ctx, id, sf.Binding, codes[0]); err != nil || method != MFAMethodRecoveryCode {
		t.Fatalf("recovery code = %q, %v", method, err)
	}
	if _, err := s.CompleteSecondFactor(ctx, id, sf.Binding, codes[0]); err == nil {
		t.Fatal("recovery code reused")
	}
	st, err := s.GetMFAStatus(ctx, id)
	if err != nil || !st.Enabled || st.RecoveryCodesRemaining != RecoveryCodeCount-1 {
		t.Fatalf("status = %+v, %v", st, err)
	}

	// A password change invalidates the step-one binding.
	if _, err := s.ChangePassword(ctx, id, uuid.Nil, "changed-password-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteSecondFactor(ctx, id, sf.Binding, codes[1]); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("binding after password change: %v", err)
	}

	// Superadmin reset turns it off; the password alone signs in again.
	was, err := s.ResetMFA(ctx, id)
	if err != nil || !was {
		t.Fatalf("ResetMFA = %v, %v", was, err)
	}
	if _, err := s.Authenticate(ctx, "grace", "changed-password-1"); err != nil {
		t.Fatalf("login after reset: %v", err)
	}
	var left int
	_ = dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_user_recovery_codes WHERE admin_user_id = $1`, id).Scan(&left)
	if left != 0 {
		t.Fatalf("%d recovery codes left after reset", left)
	}
}

// Second-factor failures count toward the login lockout, and a correct
// password at step one never clears them.
func TestSecondFactorFailuresLockTheAccount(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "heidi")
	secret, _ := enrollTOTP(ctx, t, s, id)

	for i := 1; i < LockoutThreshold; i++ {
		_, err := s.Authenticate(ctx, "heidi", testPassword)
		var sf *SecondFactorRequired
		if !errors.As(err, &sf) {
			t.Fatalf("attempt %d: Authenticate = %v", i, err)
		}
		if _, err := s.CompleteSecondFactor(ctx, id, sf.Binding, "000000"); err == nil {
			t.Fatalf("attempt %d: wrong code accepted", i)
		}
		if count, _ := loginState(ctx, t, dbClient, id); count != i {
			t.Fatalf("after %d wrong codes the streak is %d", i, count)
		}
	}
	_, err := s.Authenticate(ctx, "heidi", testPassword)
	var sf *SecondFactorRequired
	if !errors.As(err, &sf) {
		t.Fatalf("Authenticate = %v", err)
	}
	_, err = s.CompleteSecondFactor(ctx, id, sf.Binding, "000000")
	var failure *LoginFailure
	if !errors.As(err, &failure) || !failure.LockedNow {
		t.Fatalf("10th wrong code = %v, want the lock", err)
	}
	// Locked: even the right code is refused without being checked.
	_, err = s.CompleteSecondFactor(ctx, id, sf.Binding, codeAt(t, secret, time.Now()))
	if !errors.As(err, &failure) || !failure.Locked {
		t.Fatalf("locked step two = %v", err)
	}
	if _, err := s.Authenticate(ctx, "heidi", testPassword); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("locked step one = %v", err)
	}

	// VerifyCredentials: a right password with a wrong code still counts.
	if _, err := s.UnlockUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	_, err = s.VerifyCredentials(ctx, id, testPassword, "000000", true)
	if !errors.As(err, &failure) || !failure.SecondFactor {
		t.Fatalf("VerifyCredentials = %v", err)
	}
	if count, _ := loginState(ctx, t, dbClient, id); count != 1 {
		t.Fatalf("streak = %d, want 1", count)
	}
}

// Before migration 000118 the binary still signs people in, and reports
// 2FA as unavailable rather than failing.
func TestMFAToleratesMissingSchema(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "ivan")
	if _, err := dbClient.ExecContext(ctx, `
		DROP TABLE admin_user_recovery_codes;
		ALTER TABLE admin_users DROP COLUMN totp_secret_enc, DROP COLUMN totp_enabled_at, DROP COLUMN totp_last_step;
		DROP TABLE admin_user_tokens;
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "ivan", testPassword); err != nil {
		t.Fatalf("login before 000118: %v", err)
	}
	st, err := s.GetMFAStatus(ctx, id)
	if err != nil || st.Available {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if _, err := s.BeginTOTPEnrollment(ctx, id); !errors.Is(err, ErrMFAUnavailable) {
		t.Fatalf("enroll: %v", err)
	}
	if _, _, err := s.IssueInvite(ctx, id, nil); !errors.Is(err, ErrTokensUnavailable) {
		t.Fatalf("invite: %v", err)
	}
	if got, err := s.RequestPasswordReset(ctx, "ivan"); got != nil || (err != nil && !errors.Is(err, ErrTokensUnavailable)) {
		t.Fatalf("reset: %+v, %v", got, err)
	}
	if was, err := s.ResetMFA(ctx, id); err != nil || was {
		t.Fatalf("ResetMFA = %v, %v", was, err)
	}
	// Retiring links is skipped, not fatal, without admin_user_tokens.
	if _, err := s.ChangePassword(ctx, id, uuid.Nil, "changed-password-1"); err != nil {
		t.Fatalf("ChangePassword before 000117: %v", err)
	}
	if _, err := s.RevokeUserAccess(ctx, id); err != nil {
		t.Fatalf("RevokeUserAccess before 000117: %v", err)
	}
	if err := RetireCredentialTokens(ctx, dbClient, id); err != nil {
		t.Fatalf("RetireCredentialTokens before 000117: %v", err)
	}
}

// Every credential reset cancels a pending email change, so an intruder who
// started one cannot confirm it after the owner recovers the account.
func TestCredentialResetsCancelPendingEmailChange(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	n := 0
	pending := func(t *testing.T) (uuid.UUID, string) {
		t.Helper()
		n++
		id := insertUser(ctx, t, dbClient, "victim"+string(rune('a'+n)))
		setEmail(ctx, t, dbClient, id, "victim"+string(rune('a'+n))+"@example.com")
		change, err := s.RequestEmailChange(ctx, id, "intruder"+string(rune('a'+n))+"@evil.test")
		if err != nil {
			t.Fatal(err)
		}
		return id, change.Token.Token
	}
	refused := func(t *testing.T, token string) {
		t.Helper()
		if _, err := s.ConfirmEmailChange(ctx, token); !errors.Is(err, ErrTokenInvalid) {
			t.Fatalf("pending email change confirmed after the reset: %v", err)
		}
	}

	t.Run("password change", func(t *testing.T) {
		id, token := pending(t)
		if _, err := s.ChangePassword(ctx, id, uuid.Nil, "changed-password-1"); err != nil {
			t.Fatal(err)
		}
		refused(t, token)
		// A change requested after the new password still works.
		again, err := s.RequestEmailChange(ctx, id, "owner-new@example.com")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ConfirmEmailChange(ctx, again.Token.Token); err != nil {
			t.Fatalf("email change after the password change: %v", err)
		}
	})
	t.Run("password reset", func(t *testing.T) {
		id, token := pending(t)
		_, reset, err := s.IssuePasswordReset(ctx, id, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.RedeemPasswordToken(ctx, reset.Token, PurposePasswordReset, "reset-password-12"); err != nil {
			t.Fatal(err)
		}
		refused(t, token)
	})
	t.Run("superadmin sign-out everywhere", func(t *testing.T) {
		id, token := pending(t)
		if _, err := s.CreateSession(ctx, id, "intruder-"+id.String(), time.Now().Add(time.Hour), "", "", AuthMethodPassword); err != nil {
			t.Fatal(err)
		}
		if revoked, err := s.RevokeUserAccess(ctx, id); err != nil || revoked != 1 {
			t.Fatalf("RevokeUserAccess = %d, %v", revoked, err)
		}
		refused(t, token)
	})
	t.Run("superadmin password set", func(t *testing.T) {
		id, token := pending(t)
		if err := RetireCredentialTokens(ctx, dbClient, id); err != nil {
			t.Fatal(err)
		}
		refused(t, token)
	})
	t.Run("superadmin 2FA reset", func(t *testing.T) {
		id, token := pending(t)
		if _, err := s.ResetMFA(ctx, id); err != nil {
			t.Fatal(err)
		}
		refused(t, token)
	})
	t.Run("password changed by a path that retires nothing", func(t *testing.T) {
		// cmd/admin's break-glass only records password_changed_at.
		id, token := pending(t)
		if _, err := dbClient.ExecContext(ctx, `UPDATE admin_users SET password_changed_at = NOW() WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
		refused(t, token)
	})
}

// A password reset must not wash out the second-factor failure streak: a
// mailbox holder could otherwise reset every minute for a fresh batch of
// code guesses. Without two-factor a reset still clears the lockout.
func TestPasswordResetKeepsSecondFactorLock(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "ivan")
	enrollTOTP(ctx, t, s, id)
	for i := 0; i < LockoutThreshold; i++ {
		_, err := s.Authenticate(ctx, "ivan", testPassword)
		var sf *SecondFactorRequired
		if !errors.As(err, &sf) {
			t.Fatalf("attempt %d: Authenticate = %v", i+1, err)
		}
		if _, err := s.CompleteSecondFactor(ctx, id, sf.Binding, "000000"); err == nil {
			t.Fatalf("attempt %d: wrong code accepted", i+1)
		}
	}
	count, lockedFor := loginState(ctx, t, dbClient, id)
	if count != LockoutThreshold || lockedFor <= 0 {
		t.Fatalf("before reset: streak %d, locked for %v", count, lockedFor)
	}

	_, reset, err := s.IssuePasswordReset(ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPasswordToken(ctx, reset.Token, PurposePasswordReset, "reset-password-12"); err != nil {
		t.Fatal(err)
	}
	if count, lockedFor := loginState(ctx, t, dbClient, id); count != LockoutThreshold || lockedFor <= 0 {
		t.Fatalf("after reset: streak %d, locked for %v; want the lock kept", count, lockedFor)
	}
	if _, err := s.Authenticate(ctx, "ivan", "reset-password-12"); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("login after reset = %v, want the lock", err)
	}

	// No second factor: the reset clears the lock as before.
	plain := insertUser(ctx, t, dbClient, "judy")
	failLogins(ctx, t, s, "judy", LockoutThreshold)
	if _, lockedFor := loginState(ctx, t, dbClient, plain); lockedFor <= 0 {
		t.Fatal("judy not locked")
	}
	_, reset, err = s.IssuePasswordReset(ctx, plain, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPasswordToken(ctx, reset.Token, PurposePasswordReset, "reset-password-12"); err != nil {
		t.Fatal(err)
	}
	if count, lockedFor := loginState(ctx, t, dbClient, plain); count != 0 || lockedFor > 0 {
		t.Fatalf("no-2FA reset: streak %d, locked for %v; want cleared", count, lockedFor)
	}
}

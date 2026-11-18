//go:build !unit

package adminauth

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/secrets"
)

type testKeyring struct{ key []byte }

func (k testKeyring) CurrentKey() ([]byte, int, error) { return k.key, 1, nil }
func (k testKeyring) KeyForVersion(v int) ([]byte, error) {
	if v != 1 {
		return nil, errors.New("unknown key version")
	}
	return k.key, nil
}

func newTwoFactorService(t *testing.T) (*Service, *db.Client) {
	t.Helper()
	svc, dbClient, _ := newLockoutService(t)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	svc.ConfigureEncryption(secrets.NewAESGCMEncryptor(testKeyring{key: key}))
	return svc, dbClient
}

// codeAt is the code an authenticator shows d from now.
func codeAt(t *testing.T, secret string, d time.Duration) string {
	t.Helper()
	code, err := TOTPCodeAt(secret, time.Now().Add(d))
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// stepCode is the code for a time step offset from userID's last accepted
// one: 0 replays it, 1 is the next (always inside the ±1 window right after
// a use, whichever way the clock ticked meanwhile).
func stepCode(t *testing.T, dbClient *db.Client, secret string, userID uuid.UUID, offset int64) string {
	t.Helper()
	var last int64
	if err := dbClient.QueryRowContext(context.Background(), `SELECT totp_last_step FROM admin_users WHERE id = $1`, userID).Scan(&last); err != nil {
		t.Fatal(err)
	}
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	return hotp(key, uint64(last+offset), totpDigits)
}

// enrol turns two-factor on for userID and returns the secret and the
// recovery codes. The enabling code used the current step.
func enrol(t *testing.T, svc *Service, userID uuid.UUID) (string, []string) {
	t.Helper()
	ctx := context.Background()
	secret, err := svc.BeginTOTPEnrolment(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := svc.EnableTOTP(ctx, userID, codeAt(t, secret, 0))
	if err != nil {
		t.Fatal(err)
	}
	return secret, codes
}

// signInFirstStep runs the password step and returns the challenge.
func signInFirstStep(t *testing.T, svc *Service, username, password string) string {
	t.Helper()
	_, err := svc.Authenticate(context.Background(), username, password)
	var second *SecondFactorRequired
	if !errors.As(err, &second) {
		t.Fatalf("password step: %v, want a second-factor challenge", err)
	}
	if got := time.Until(second.Challenge.ExpiresAt); got < 4*time.Minute || got > 6*time.Minute {
		t.Fatalf("challenge lifetime %v, want about 5 minutes", got)
	}
	return second.Challenge.Token
}

func TestEnrolmentNeedsASecretsKeyAndStoresTheSecretEncrypted(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	if _, err := svc.BeginTOTPEnrolment(ctx, alice); !errors.Is(err, ErrTwoFactorNeedsKey) {
		t.Fatalf("enrolment without a key: %v", err)
	}
	var stored *string
	_ = dbClient.QueryRowContext(ctx, `SELECT totp_secret_enc FROM admin_users WHERE id = $1`, alice).Scan(&stored)
	if stored != nil {
		t.Fatal("a secret was stored without a key")
	}

	svc2, dbClient2 := newTwoFactorService(t)
	bob := insertPasswordUser(t, dbClient2, "bob")
	secret, err := svc2.BeginTOTPEnrolment(ctx, bob)
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := dbClient2.QueryRowContext(ctx, `SELECT totp_secret_enc FROM admin_users WHERE id = $1`, bob).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !secrets.LooksLikeEnvelope(raw) || strings.Contains(raw, secret) {
		t.Fatalf("stored secret is not an encrypted envelope: %q", raw)
	}
	// Pending: nothing changes at sign-in until a code confirms it.
	if _, err := svc2.Authenticate(ctx, "bob", testPassword); err != nil {
		t.Fatalf("sign-in with a pending enrolment: %v", err)
	}
	if _, err := svc2.EnableTOTP(ctx, bob, "000000"); !errors.Is(err, ErrInvalidSecondFactor) && codeAt(t, secret, 0) != "000000" {
		t.Fatalf("wrong confirming code: %v", err)
	}
	codes, err := svc2.EnableTOTP(ctx, bob, codeAt(t, secret, 0))
	if err != nil || len(codes) != RecoveryCodeCount {
		t.Fatalf("enable: %d codes, %v", len(codes), err)
	}
	var hashes int
	_ = dbClient2.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_user_recovery_codes WHERE admin_user_id = $1 AND code_hash <> ALL($2)`, bob, pq.Array(codes)).Scan(&hashes)
	if hashes != RecoveryCodeCount {
		t.Fatal("recovery codes are not stored hashed")
	}
	if _, err := svc2.BeginTOTPEnrolment(ctx, bob); !errors.Is(err, ErrTwoFactorEnabled) {
		t.Fatalf("second enrolment: %v", err)
	}
	// SSO accounts cannot enrol.
	sso := insertPasswordUser(t, dbClient2, "sso")
	if _, err := dbClient2.ExecContext(ctx, `UPDATE admin_users SET external_issuer = 'i', external_subject = 's' WHERE id = $1`, sso); err != nil {
		t.Fatal(err)
	}
	if _, err := svc2.BeginTOTPEnrolment(ctx, sso); !errors.Is(err, ErrNotLocalAccount) {
		t.Fatalf("SSO enrolment: %v", err)
	}
}

func TestTwoStepSignInReplayAndChallengeRules(t *testing.T) {
	ctx := context.Background()
	svc, dbClient := newTwoFactorService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	secret, _ := enrol(t, svc, alice)

	// A wrong password on a two-factor account is the ordinary refusal.
	if _, err := svc.Authenticate(ctx, "alice", wrongPass); failure(t, err).SecondFactor {
		t.Fatal("wrong password marked as a second-factor failure")
	}
	if _, err := svc.UnlockUser(ctx, alice); err != nil {
		t.Fatal(err)
	}

	challenge := signInFirstStep(t, svc, "alice", testPassword)
	// The correct password counted nothing.
	if count, _ := lockState(t, dbClient, alice); count != 0 {
		t.Fatalf("streak after the password step: %d", count)
	}
	// The enabling code's step is used: replay is refused (and counted).
	_, err := svc.CompleteSecondFactor(ctx, challenge, stepCode(t, dbClient, secret, alice, 0))
	if f := failure(t, err); !f.SecondFactor {
		t.Fatalf("replayed code: %+v", f)
	}
	// Tampered or unknown challenges.
	if _, err := svc.CompleteSecondFactor(ctx, challenge+"x", stepCode(t, dbClient, secret, alice, 1)); !errors.Is(err, ErrMFAChallengeInvalid) {
		t.Fatalf("tampered challenge: %v", err)
	}
	// The next step's code (within the ±1 window) works once.
	done, err := svc.CompleteSecondFactor(ctx, challenge, stepCode(t, dbClient, secret, alice, 1))
	if err != nil || done.User.ID != alice || done.Method != SecondFactorTOTP {
		t.Fatalf("second step: %+v %v", done, err)
	}
	if count, _ := lockState(t, dbClient, alice); count != 0 {
		t.Fatalf("streak after a completed sign-in: %d, want cleared", count)
	}
	// The challenge is used up.
	if _, err := svc.CompleteSecondFactor(ctx, challenge, stepCode(t, dbClient, secret, alice, 1)); !errors.Is(err, ErrMFAChallengeInvalid) {
		t.Fatalf("reused challenge: %v", err)
	}
	// An expired challenge.
	expired := signInFirstStep(t, svc, "alice", testPassword)
	expireToken(t, dbClient, expired)
	if _, err := svc.CompleteSecondFactor(ctx, expired, codeAt(t, secret, 0)); !errors.Is(err, ErrMFAChallengeInvalid) {
		t.Fatalf("expired challenge: %v", err)
	}
	// A challenge of another user's sign-in cannot sign this one in: it is
	// bound to its own account.
	bob := insertPasswordUser(t, dbClient, "bob")
	bobSecret, _ := enrol(t, svc, bob)
	bobChallenge := signInFirstStep(t, svc, "bob", testPassword)
	done, err = svc.CompleteSecondFactor(ctx, bobChallenge, stepCode(t, dbClient, bobSecret, bob, 1))
	if err != nil || done.User.ID != bob {
		t.Fatalf("bob's second step: %+v %v", done, err)
	}
}

func TestRecoveryCodesAreSingleUse(t *testing.T) {
	ctx := context.Background()
	svc, dbClient := newTwoFactorService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	_, codes := enrol(t, svc, alice)

	challenge := signInFirstStep(t, svc, "alice", testPassword)
	done, err := svc.CompleteSecondFactor(ctx, challenge, strings.ToUpper(codes[0]))
	if err != nil || done.Method != SecondFactorRecoveryCode || done.RecoveryCodesRemaining != RecoveryCodeCount-1 {
		t.Fatalf("recovery code sign-in: %+v %v", done, err)
	}
	challenge = signInFirstStep(t, svc, "alice", testPassword)
	if _, err := svc.CompleteSecondFactor(ctx, challenge, codes[0]); !failure(t, err).SecondFactor {
		t.Fatalf("reused recovery code: %v", err)
	}
	// Regenerating replaces the whole set.
	fresh, err := svc.RegenerateRecoveryCodes(ctx, alice)
	if err != nil || len(fresh) != RecoveryCodeCount {
		t.Fatal(err)
	}
	if _, err := svc.CompleteSecondFactor(ctx, challenge, codes[1]); !failure(t, err).SecondFactor {
		t.Fatalf("replaced recovery code: %v", err)
	}
	if _, err := svc.CompleteSecondFactor(ctx, challenge, fresh[0]); err != nil {
		t.Fatalf("new recovery code: %v", err)
	}
}

// Wrong codes count toward the same lock as wrong passwords; a lock during
// the second step refuses even the right code, exactly like a wrong one.
func TestFailedCodesLockTheAccount(t *testing.T) {
	ctx := context.Background()
	svc, dbClient := newTwoFactorService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	secret, _ := enrol(t, svc, alice)

	challenge := signInFirstStep(t, svc, "alice", testPassword)
	var last *LoginFailure
	for i := 1; i <= LockoutThreshold; i++ {
		_, err := svc.CompleteSecondFactor(ctx, challenge, "000001")
		last = failure(t, err)
	}
	if !last.LockedNow {
		t.Fatalf("10th wrong code: %+v, want it to lock", last)
	}
	_, err := svc.CompleteSecondFactor(ctx, challenge, stepCode(t, dbClient, secret, alice, 1))
	if f := failure(t, err); !f.Locked {
		t.Fatalf("right code while locked: %+v", f)
	}
	// The password step refuses the right password too, without a challenge.
	if _, err := svc.Authenticate(ctx, "alice", testPassword); !failure(t, err).Locked {
		t.Fatalf("password while locked: %v", err)
	}

	// A correct password never washes out failed codes: eight wrong codes,
	// then a new sign-in's right password keeps the streak at eight, and two
	// more wrong codes lock.
	if _, err := svc.UnlockUser(ctx, alice); err != nil {
		t.Fatal(err)
	}
	challenge = signInFirstStep(t, svc, "alice", testPassword)
	for i := 1; i <= LockoutThreshold-2; i++ {
		_, _ = svc.CompleteSecondFactor(ctx, challenge, "000001")
	}
	challenge = signInFirstStep(t, svc, "alice", testPassword)
	if count, _ := lockState(t, dbClient, alice); count != LockoutThreshold-2 {
		t.Fatalf("streak after a right password: %d, want %d", count, LockoutThreshold-2)
	}
	if _, err := svc.CompleteSecondFactor(ctx, challenge, "000001"); failure(t, err).LockedNow {
		t.Fatal("ninth failure locked")
	}
	if _, err := svc.CompleteSecondFactor(ctx, challenge, "000001"); !failure(t, err).LockedNow {
		t.Fatal("tenth failure across steps did not lock")
	}

	// A right password that is the tenth attempt keeps the lock it set and
	// gets the ordinary refusal, not a challenge.
	if _, err := svc.UnlockUser(ctx, alice); err != nil {
		t.Fatal(err)
	}
	challenge = signInFirstStep(t, svc, "alice", testPassword)
	for i := 1; i < LockoutThreshold; i++ {
		_, _ = svc.CompleteSecondFactor(ctx, challenge, "000001")
	}
	if _, err := svc.Authenticate(ctx, "alice", testPassword); !failure(t, err).LockedNow {
		t.Fatalf("right password as the tenth attempt: %v, want the lock", err)
	}
}

// A password reset keeps two-factor on and a two-factor account's lock in
// place, and retires open sign-in challenges.
func TestPasswordResetKeepsTwoFactorAndItsLock(t *testing.T) {
	ctx := context.Background()
	svc, dbClient := newTwoFactorService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	setEmail(t, dbClient, alice, "alice@example.com")
	secret, _ := enrol(t, svc, alice)

	pending := signInFirstStep(t, svc, "alice", testPassword)
	for i := 0; i < LockoutThreshold; i++ {
		_, _ = svc.CompleteSecondFactor(ctx, pending, "000001")
	}
	reset, err := svc.IssuePasswordReset(ctx, alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RedeemPasswordToken(ctx, reset.Token.Token, PurposePasswordReset, newPassword); err != nil {
		t.Fatal(err)
	}
	if _, until := lockState(t, dbClient, alice); until == nil || !until.After(time.Now()) {
		t.Fatal("a password reset cleared a two-factor account's lock")
	}
	status, _ := svc.GetTwoFactorStatus(ctx, alice)
	if !status.Enabled {
		t.Fatal("a password reset turned two-factor off")
	}
	if _, err := svc.UnlockUser(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteSecondFactor(ctx, pending, stepCode(t, dbClient, secret, alice, 1)); !errors.Is(err, ErrMFAChallengeInvalid) {
		t.Fatalf("challenge from before the reset: %v", err)
	}
	challenge := signInFirstStep(t, svc, "alice", newPassword)
	if _, err := svc.CompleteSecondFactor(ctx, challenge, stepCode(t, dbClient, secret, alice, 1)); err != nil {
		t.Fatalf("sign-in after reset: %v", err)
	}
}

func TestDisableNeedsPasswordAndCodeAndSuperadminReset(t *testing.T) {
	ctx := context.Background()
	svc, dbClient := newTwoFactorService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	secret, codes := enrol(t, svc, alice)
	here, _ := svc.CreateSession(ctx, alice, "here", time.Now().Add(time.Hour), "", "", AuthMethodPassword)
	if _, err := svc.CreateSession(ctx, alice, "other", time.Now().Add(time.Hour), "", "", AuthMethodPassword); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.VerifyCurrentCredentials(ctx, alice, testPassword, "000001", true); !failure(t, err).SecondFactor {
		t.Fatalf("wrong code: %v", err)
	}
	if _, err := svc.VerifyCurrentCredentials(ctx, alice, wrongPass, stepCode(t, dbClient, secret, alice, 1), true); failure(t, err).SecondFactor {
		t.Fatalf("wrong password reported as a code failure: %v", err)
	}
	method, err := svc.VerifyCurrentCredentials(ctx, alice, testPassword, codes[0], true)
	if err != nil || method != SecondFactorRecoveryCode {
		t.Fatalf("password and recovery code: %q %v", method, err)
	}
	revoked, err := svc.DisableTOTP(ctx, alice, here)
	if err != nil || revoked != 1 {
		t.Fatalf("disable: %d %v", revoked, err)
	}
	if active, _ := svc.SessionActive(ctx, alice, here); !active {
		t.Fatal("disable signed out the session making the change")
	}
	if _, err := svc.Authenticate(ctx, "alice", testPassword); err != nil {
		t.Fatalf("sign-in after disable: %v", err)
	}
	var left int
	_ = dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_user_recovery_codes WHERE admin_user_id = $1`, alice).Scan(&left)
	if left != 0 {
		t.Fatal("recovery codes survived disable")
	}
	if _, err := svc.DisableTOTP(ctx, alice, here); !errors.Is(err, ErrTwoFactorDisabled) {
		t.Fatalf("second disable: %v", err)
	}

	// Superadmin reset (lost device): no proof, sessions untouched.
	enrol(t, svc, alice)
	pending := signInFirstStep(t, svc, "alice", testPassword)
	was, err := svc.ResetTwoFactor(ctx, alice)
	if err != nil || !was {
		t.Fatalf("reset: %v %v", was, err)
	}
	if active, _ := svc.SessionActive(ctx, alice, here); !active {
		t.Fatal("two-factor reset signed the user out")
	}
	if _, err := svc.CompleteSecondFactor(ctx, pending, "123456"); !errors.Is(err, ErrMFAChallengeInvalid) {
		t.Fatalf("challenge after reset: %v", err)
	}
	if _, err := svc.Authenticate(ctx, "alice", testPassword); err != nil {
		t.Fatalf("sign-in after reset: %v", err)
	}
	if was, err := svc.ResetTwoFactor(ctx, alice); err != nil || was {
		t.Fatalf("reset with nothing on: %v %v", was, err)
	}
	if _, err := svc.ResetTwoFactor(ctx, uuid.New()); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("reset unknown user: %v", err)
	}
}

func TestSessionsShowTheirSecondFactor(t *testing.T) {
	ctx := context.Background()
	svc, dbClient := newTwoFactorService(t)
	alice := insertPasswordUser(t, dbClient, "alice")
	family, err := svc.CreateSession(ctx, alice, "h", time.Now().Add(time.Hour), "", "", AuthMethodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.MarkSessionSecondFactor(ctx, family, SecondFactorTOTP); err != nil {
		t.Fatal(err)
	}
	plain, _ := svc.CreateSession(ctx, alice, "h2", time.Now().Add(time.Hour), "", "", AuthMethodPassword)
	sessions, err := svc.ListSessions(ctx, alice, uuid.Nil)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("list: %v %v", sessions, err)
	}
	for _, s := range sessions {
		want := ""
		if s.ID == family {
			want = SecondFactorTOTP
		}
		if s.ID == plain && s.SecondFactor != "" || s.SecondFactor != want {
			t.Fatalf("session %s second factor %q, want %q", s.ID, s.SecondFactor, want)
		}
	}
}

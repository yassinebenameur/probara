package adminauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db/pgerr"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// Optional TOTP second factor (migration 000124), for password accounts
// only: an SSO user's second factor is the identity provider's business.
//
//   - The secret is sealed with the PROBARA_SECRETS_KEY encryptor and never
//     stored in plaintext: without a key, enrolment is refused
//     (ErrTwoFactorNeedsKey) rather than falling back to the dev no-op
//     encryptor other secrets use.
//   - totp_last_step makes every code single use; recovery codes are stored
//     as SHA-256 hashes and burn on use.
//   - Sign-in becomes two steps (Authenticate → SecondFactorRequired →
//     CompleteSecondFactor). Failed codes count toward the password lockout,
//     and a password reset neither turns the second factor off nor clears a
//     two-factor account's lock (RedeemPasswordToken).

// RecoveryCodeCount is how many recovery codes enrolment issues.
const RecoveryCodeCount = 10

// Second-factor methods, recorded on the session and in audit.
const (
	SecondFactorTOTP         = "totp"
	SecondFactorRecoveryCode = "recovery_code"
)

var (
	// ErrTwoFactorUnavailable: migration 000124 has not run yet.
	ErrTwoFactorUnavailable = errors.New("two-factor authentication is not available until the database is migrated")
	// ErrTwoFactorNeedsKey: no PROBARA_SECRETS_KEY, so the secret could not
	// be stored encrypted.
	ErrTwoFactorNeedsKey = errors.New("two-factor authentication needs PROBARA_SECRETS_KEY on the API, so the authenticator secret can be stored encrypted; ask your administrator")
	ErrTwoFactorEnabled  = errors.New("two-factor authentication is already on")
	ErrTwoFactorDisabled = errors.New("two-factor authentication is not on")
	// ErrNoPendingEnrolment: enable was called without a setup first.
	ErrNoPendingEnrolment = errors.New("start two-factor setup first")
	// ErrInvalidSecondFactor is a wrong, reused or malformed code.
	ErrInvalidSecondFactor = errors.New("the authentication code is incorrect")
	// ErrSecretUnreadable: a stored authenticator secret cannot be opened
	// (PROBARA_SECRETS_KEY missing, or the key version that sealed it
	// removed). Authenticator codes cannot be checked; recovery codes still
	// work, and a superadmin's two-factor reset is the fix.
	ErrSecretUnreadable = errors.New("the stored authenticator secret cannot be decrypted: PROBARA_SECRETS_KEY is missing or no longer holds the key version that sealed it; recovery codes still work, and a superadmin can reset two-factor sign-in")
	// ErrMFAChallengeInvalid is an unknown, used, expired or retired
	// second-step challenge: the sign-in must start again.
	ErrMFAChallengeInvalid = errors.New("the sign-in attempt expired; enter your password again")
)

// SecondFactorRequired is Authenticate's answer for a correct password on an
// account with two-factor on. It is an error on purpose: a caller that does
// not know the second step fails closed instead of starting a session.
// Challenge is the sign-in's single-use second-step token.
type SecondFactorRequired struct {
	UserID    uuid.UUID
	Username  string
	Challenge IssuedToken
}

func (e *SecondFactorRequired) Error() string { return "second factor required" }

// ConfigureEncryption installs the encryptor for TOTP secrets: the
// PROBARA_SECRETS_KEY encryptor, or nil when no key is configured, which
// turns enrolment off.
func (s *Service) ConfigureEncryption(enc secrets.Encryptor) {
	s.enc = enc
}

// TwoFactorEnrolmentAvailable reports whether a secrets key is configured.
func (s *Service) TwoFactorEnrolmentAvailable() bool {
	return s.enc != nil
}

// TwoFactorStatus is a user's second-factor state.
type TwoFactorStatus struct {
	// Supported is false before migration 000124.
	Supported              bool
	Enabled                bool
	EnabledAt              *time.Time
	RecoveryCodesRemaining int
}

// GetTwoFactorStatus reads a user's second-factor state.
func (s *Service) GetTwoFactorStatus(ctx context.Context, userID uuid.UUID) (*TwoFactorStatus, error) {
	st := TwoFactorStatus{Supported: true}
	var enabledAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT u.totp_enabled_at,
		       (SELECT COUNT(*) FROM admin_user_recovery_codes c
		        WHERE c.admin_user_id = u.id AND c.used_at IS NULL)
		FROM admin_users u WHERE u.id = $1
	`, userID).Scan(&enabledAt, &st.RecoveryCodesRemaining)
	switch {
	case pgerr.IsMissingSchema(err):
		return &TwoFactorStatus{}, nil
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrUserNotFound
	case err != nil:
		return nil, fmt.Errorf("failed to load two-factor status: %w", err)
	}
	if enabledAt.Valid {
		st.Enabled = true
		st.EnabledAt = &enabledAt.Time
	}
	return &st, nil
}

// secondFactorEnabled reports whether userID signs in with a second factor.
// Before migration 000124 nobody does.
func secondFactorEnabled(ctx context.Context, q SessionQuerier, userID uuid.UUID) (bool, error) {
	var enabled bool
	err := q.QueryRowContext(ctx, `SELECT totp_enabled_at IS NOT NULL FROM admin_users WHERE id = $1`, userID).Scan(&enabled)
	if pgerr.IsMissingSchema(err) || errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check two-factor: %w", err)
	}
	return enabled, nil
}

// secondFactorEnabledTx is secondFactorEnabled inside tx, under a savepoint
// so a database before migration 000124 leaves tx usable.
func secondFactorEnabledTx(ctx context.Context, tx *sql.Tx, userID uuid.UUID) (bool, error) {
	enabled := false
	err := tolerateMissingSchema(ctx, tx, func() error {
		return tx.QueryRowContext(ctx, `SELECT totp_enabled_at IS NOT NULL FROM admin_users WHERE id = $1`, userID).Scan(&enabled)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}

// BeginTOTPEnrolment generates a secret for a password account and stores it
// sealed as a pending enrolment (not in force until EnableTOTP confirms it;
// a new setup replaces a pending one). It returns the plain base32 secret,
// shown once.
func (s *Service) BeginTOTPEnrolment(ctx context.Context, userID uuid.UUID) (string, error) {
	if s.enc == nil {
		return "", ErrTwoFactorNeedsKey
	}
	account, err := s.LoadAccount(ctx, userID)
	if err != nil {
		return "", err
	}
	if !account.Local() {
		return "", ErrNotLocalAccount
	}
	secret, err := newTOTPSecret()
	if err != nil {
		return "", err
	}
	sealed, err := s.enc.Encrypt(secret)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt the authenticator secret: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE admin_users
		SET totp_secret_enc = $2, totp_last_step = NULL, updated_at = NOW()
		WHERE id = $1 AND totp_enabled_at IS NULL
	`, userID, sealed)
	if pgerr.IsMissingSchema(err) {
		return "", ErrTwoFactorUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("failed to store the authenticator secret: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrTwoFactorEnabled
	}
	return secret, nil
}

// EnableTOTP confirms a pending enrolment with a code from the app, turns
// the second factor on and returns the recovery codes, shown once. A wrong
// code is ErrInvalidSecondFactor; it does not count toward the lockout (the
// caller is signed in and nothing is protected yet).
func (s *Service) EnableTOTP(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	var codes []string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var sealed sql.NullString
		var enabledAt sql.NullTime
		err := tx.QueryRowContext(ctx, `
			SELECT totp_secret_enc, totp_enabled_at FROM admin_users WHERE id = $1 FOR UPDATE
		`, userID).Scan(&sealed, &enabledAt)
		switch {
		case pgerr.IsMissingSchema(err):
			return ErrTwoFactorUnavailable
		case errors.Is(err, sql.ErrNoRows):
			return ErrUserNotFound
		case err != nil:
			return err
		}
		if enabledAt.Valid {
			return ErrTwoFactorEnabled
		}
		if !sealed.Valid || sealed.String == "" {
			return ErrNoPendingEnrolment
		}
		secret, err := s.openSecret(sealed.String)
		if err != nil {
			return err
		}
		step, ok := matchTOTP(secret, code, time.Now(), -1)
		if !ok {
			return ErrInvalidSecondFactor
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE admin_users SET totp_enabled_at = NOW(), totp_last_step = $2, updated_at = NOW() WHERE id = $1
		`, userID, step); err != nil {
			return err
		}
		codes, err = replaceRecoveryCodes(ctx, tx, userID)
		return err
	})
	return codes, err
}

// openSecret decrypts a stored TOTP secret. Only an encrypted envelope is
// accepted: TOTP secrets were never stored in plaintext, so a bare value is
// tampering, and with no key configured nothing can be opened.
func (s *Service) openSecret(sealed string) (string, error) {
	if s.enc == nil {
		return "", ErrSecretUnreadable
	}
	if !secrets.LooksLikeEnvelope(sealed) {
		return "", fmt.Errorf("%w (the stored value is not an encrypted envelope)", ErrSecretUnreadable)
	}
	secret, err := s.enc.Decrypt(sealed)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSecretUnreadable, err)
	}
	return secret, nil
}

// DisableTOTP turns the user's own second factor off (the caller checked the
// password and a code) and signs out every other session, keep being the
// one making the change. It returns how many sessions ended.
func (s *Service) DisableTOTP(ctx context.Context, userID, keep uuid.UUID) (int, error) {
	var revoked int
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		was, err := clearTOTP(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !was {
			return ErrTwoFactorDisabled
		}
		revoked, err = RevokeUserSessions(ctx, tx, userID, keep, RevokeReasonUser)
		return err
	})
	if pgerr.IsMissingSchema(err) {
		return 0, ErrTwoFactorUnavailable
	}
	return revoked, err
}

// ResetTwoFactor is the superadmin reset for a lost device: the second
// factor, any pending enrolment and the recovery codes are gone, and the
// user's pending email change and open sign-in challenges are retired (a
// two-factor reset is an account recovery step, like a password reset). It
// reports whether a second factor was on. Sessions are left alone.
func (s *Service) ResetTwoFactor(ctx context.Context, userID uuid.UUID) (bool, error) {
	var was bool
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		if was, err = clearTOTP(ctx, tx, userID); err != nil {
			return err
		}
		return retireTokensTolerant(ctx, tx, userID, PurposeEmailChange, PurposeMFAChallenge)
	})
	if pgerr.IsMissingSchema(err) {
		// Before migration 000124 nobody has a second factor; the user
		// must still exist.
		if _, err := s.LoadAccount(ctx, userID); err != nil {
			return false, err
		}
		return false, nil
	}
	return was, err
}

func clearTOTP(ctx context.Context, tx *sql.Tx, userID uuid.UUID) (bool, error) {
	var was bool
	err := tx.QueryRowContext(ctx, `
		WITH prior AS (
			SELECT id, totp_enabled_at IS NOT NULL AS was FROM admin_users WHERE id = $1 FOR UPDATE
		)
		UPDATE admin_users u
		SET totp_secret_enc = NULL, totp_enabled_at = NULL, totp_last_step = NULL, updated_at = NOW()
		FROM prior WHERE u.id = prior.id
		RETURNING prior.was
	`, userID).Scan(&was)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrUserNotFound
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_user_recovery_codes WHERE admin_user_id = $1`, userID); err != nil {
		return false, err
	}
	return was, nil
}

// RegenerateRecoveryCodes replaces an enrolled user's recovery codes.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]string, error) {
	var codes []string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var enabled bool
		err := tx.QueryRowContext(ctx, `
			SELECT totp_enabled_at IS NOT NULL FROM admin_users WHERE id = $1 FOR UPDATE
		`, userID).Scan(&enabled)
		switch {
		case pgerr.IsMissingSchema(err):
			return ErrTwoFactorUnavailable
		case errors.Is(err, sql.ErrNoRows):
			return ErrUserNotFound
		case err != nil:
			return err
		}
		if !enabled {
			return ErrTwoFactorDisabled
		}
		codes, err = replaceRecoveryCodes(ctx, tx, userID)
		return err
	})
	return codes, err
}

// recoveryAlphabet avoids look-alike characters (no 0/o, 1/l/i).
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// newRecoveryCode returns a code like "k7m2q-x9p4t" (about 50 bits).
func newRecoveryCode() (string, error) {
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, c := range raw {
		if i == 5 {
			b.WriteByte('-')
		}
		// The modulo bias of 256 % 31 is negligible against the code space,
		// and a code is useful only together with the password.
		b.WriteByte(recoveryAlphabet[int(c)%len(recoveryAlphabet)])
	}
	return b.String(), nil
}

// normalizeRecoveryCode drops separators and case, so "K7M2Q X9P4T" and
// "k7m2q-x9p4t" are the same code.
func normalizeRecoveryCode(code string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte("probara-recovery:" + normalizeRecoveryCode(code)))
	return hex.EncodeToString(sum[:])
}

func replaceRecoveryCodes(ctx context.Context, tx *sql.Tx, userID uuid.UUID) ([]string, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_user_recovery_codes WHERE admin_user_id = $1`, userID); err != nil {
		return nil, err
	}
	codes := make([]string, 0, RecoveryCodeCount)
	for len(codes) < RecoveryCodeCount {
		code, err := newRecoveryCode()
		if err != nil {
			return nil, err
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO admin_user_recovery_codes (admin_user_id, code_hash) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, userID, hashRecoveryCode(code))
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			codes = append(codes, code)
		}
	}
	return codes, nil
}

// checkSecondFactorTx verifies a TOTP code or a recovery code of an enrolled
// user inside tx, holding the user row: a TOTP code moves totp_last_step and
// a recovery code is burned, so neither is accepted again, even by a
// concurrent request on another replica. ErrInvalidSecondFactor for a wrong,
// reused or malformed code, or when no second factor is on.
func (s *Service) checkSecondFactorTx(ctx context.Context, tx *sql.Tx, userID uuid.UUID, code string) (string, error) {
	var sealed sql.NullString
	var enabledAt sql.NullTime
	var lastStep sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT totp_secret_enc, totp_enabled_at, totp_last_step FROM admin_users WHERE id = $1 FOR UPDATE
	`, userID).Scan(&sealed, &enabledAt, &lastStep)
	switch {
	case pgerr.IsMissingSchema(err), errors.Is(err, sql.ErrNoRows):
		return "", ErrInvalidSecondFactor
	case err != nil:
		return "", fmt.Errorf("failed to load second factor: %w", err)
	}
	if !enabledAt.Valid || !sealed.Valid {
		return "", ErrInvalidSecondFactor
	}
	if normalizeTOTPCode(code) != "" {
		secret, err := s.openSecret(sealed.String)
		if err != nil {
			return "", err
		}
		last := int64(-1)
		if lastStep.Valid {
			last = lastStep.Int64
		}
		step, ok := matchTOTP(secret, code, time.Now(), last)
		if !ok {
			return "", ErrInvalidSecondFactor
		}
		if _, err := tx.ExecContext(ctx, `UPDATE admin_users SET totp_last_step = $2 WHERE id = $1`, userID, step); err != nil {
			return "", fmt.Errorf("failed to record the code: %w", err)
		}
		return SecondFactorTOTP, nil
	}
	if normalizeRecoveryCode(code) == "" {
		return "", ErrInvalidSecondFactor
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE admin_user_recovery_codes SET used_at = NOW()
		WHERE admin_user_id = $1 AND code_hash = $2 AND used_at IS NULL
	`, userID, hashRecoveryCode(code))
	if err != nil {
		return "", fmt.Errorf("failed to use recovery code: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrInvalidSecondFactor
	}
	return SecondFactorRecoveryCode, nil
}

// checkSecondFactorOnce is checkSecondFactorTx in its own transaction.
func (s *Service) checkSecondFactorOnce(ctx context.Context, userID uuid.UUID, code string) (string, error) {
	var method string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		method, err = s.checkSecondFactorTx(ctx, tx, userID, code)
		return err
	})
	return method, err
}

// SecondStep is a completed two-factor sign-in.
type SecondStep struct {
	User models.AdminUser
	// Method is SecondFactorTOTP or SecondFactorRecoveryCode.
	Method string
	// RecoveryCodesRemaining after a recovery code was used.
	RecoveryCodesRemaining int
}

// CompleteSecondFactor is the second step of a password sign-in. Under the
// same lockout as the first (the attempt is reserved before the code is
// compared, so code guesses lock the account exactly like password guesses),
// it checks the code against the challenge's account and, on success, uses
// the challenge up and clears the failure streak. A refusal is
// ErrMFAChallengeInvalid (unknown, used, expired or retired challenge: sign
// in again) or a *LoginFailure (wrong code, or a locked, disabled or
// SSO-only account), which callers answer alike.
func (s *Service) CompleteSecondFactor(ctx context.Context, challenge, code string) (*SecondStep, error) {
	rec, err := peekToken(ctx, s.db, challenge, PurposeMFAChallenge)
	if err != nil {
		if errors.Is(err, ErrTokenInvalid) || errors.Is(err, ErrTokensUnavailable) {
			return nil, ErrMFAChallengeInvalid
		}
		return nil, err
	}
	userID := rec.UserID
	reserved, err := s.RecordFailedAttempt(ctx, userID)
	if err != nil {
		return nil, err
	}
	if reserved.Locked {
		return nil, reserved
	}

	var out SecondStep
	codeWrong := false
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		// The user row is held from here (checkSecondFactorTx), so a
		// concurrent password reset or two-factor reset either lands first
		// and retires this challenge, or waits for this sign-in.
		method, err := s.checkSecondFactorTx(ctx, tx, userID, code)
		if errors.Is(err, ErrInvalidSecondFactor) {
			codeWrong = true
		}
		if err != nil {
			return err
		}
		if reserved.LockedNow {
			// A right code that completed a streak: the lock stands.
			return ErrInvalidSecondFactor
		}
		if _, err := consumeToken(ctx, tx, challenge, PurposeMFAChallenge); err != nil {
			if errors.Is(err, ErrTokenInvalid) {
				return ErrMFAChallengeInvalid
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, clearFailuresQuery, userID); err != nil {
			return fmt.Errorf("failed to reset sign-in failures: %w", err)
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT id, username, created_at, updated_at, last_login_at, disabled_at
			FROM admin_users WHERE id = $1 AND disabled_at IS NULL
		`, userID).Scan(&out.User.ID, &out.User.Username, &out.User.CreatedAt, &out.User.UpdatedAt,
			&out.User.LastLoginAt, &out.User.DisabledAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrInvalidSecondFactor
			}
			return err
		}
		out.Method = method
		if method == SecondFactorRecoveryCode {
			return tx.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM admin_user_recovery_codes WHERE admin_user_id = $1 AND used_at IS NULL
			`, userID).Scan(&out.RecoveryCodesRemaining)
		}
		return nil
	})
	switch {
	case errors.Is(err, ErrInvalidSecondFactor):
		reserved.SecondFactor = true
		if reserved.LockedNow && codeWrong {
			// A wrong code locked the account: whoever is signing in has the
			// password. Count it, and hold the account after repeated ones.
			if err := s.recordSecondFactorLockout(ctx, reserved); err != nil {
				return nil, err
			}
		}
		return nil, reserved
	case errors.Is(err, ErrMFAChallengeInvalid):
		return nil, err
	case err != nil:
		// Not the user's fault (an unreadable secret, a database error):
		// give the attempt back so it cannot lock them out.
		s.releaseReservation(ctx, userID, reserved)
		return nil, err
	}
	return &out, nil
}

// MarkSessionSecondFactor records on a session how its sign-in passed the
// second step, for the session list. Best effort before migration 000124.
func (s *Service) MarkSessionSecondFactor(ctx context.Context, familyID uuid.UUID, method string) error {
	if familyID == uuid.Nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE admin_session_families SET second_factor = $2 WHERE id = $1`, familyID, method)
	if err != nil && !pgerr.IsMissingSchema(err) {
		return fmt.Errorf("failed to record the session's second factor: %w", err)
	}
	return nil
}

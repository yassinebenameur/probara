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

	"github.com/yassinebenameur/probara/shared/secrets"
)

// TOTP second factor (migration 000118). Local accounts only: an SSO user's
// second factor is the IdP's business. The secret is sealed with the
// PROBARA_SECRETS_KEY encryptor (plaintext under the dev no-op encryptor,
// like every other stored secret), totp_last_step makes each code single
// use, and recovery codes are stored as SHA-256 hashes and burn on use.

// RecoveryCodeCount is how many recovery codes an enrollment issues.
const RecoveryCodeCount = 10

var (
	ErrMFAUnavailable    = errors.New("two-factor authentication is not available until the database is migrated")
	ErrMFAAlreadyEnabled = errors.New("two-factor authentication is already enabled")
	ErrMFANotEnabled     = errors.New("two-factor authentication is not enabled")
	ErrMFANoEnrollment   = errors.New("start two-factor setup first")
	// ErrInvalidMFACode is a wrong, reused or malformed code.
	ErrInvalidMFACode = errors.New("invalid authentication code")
)

// SecondFactorRequired is Authenticate's answer for a correct password on
// an account with TOTP enabled. It is an error on purpose: a caller that
// does not know about the second step fails closed instead of issuing a
// session. Binding fingerprints the password and enrollment the step-one
// token is valid for.
type SecondFactorRequired struct {
	UserID   uuid.UUID
	Username string
	Binding  string
}

func (e *SecondFactorRequired) Error() string { return "second factor required" }

// ConfigureEncryption sets the encryptor for TOTP secrets.
func (s *Service) ConfigureEncryption(enc secrets.Encryptor) {
	if enc != nil {
		s.enc = enc
	}
}

func (s *Service) encryptor() secrets.Encryptor {
	if s.enc == nil {
		return secrets.NoOpEncryptor{}
	}
	return s.enc
}

// MFAStatus is a user's second-factor state.
type MFAStatus struct {
	// Available is false before migration 000118.
	Available              bool       `json:"available"`
	Enabled                bool       `json:"enabled"`
	EnabledAt              *time.Time `json:"enabled_at,omitempty"`
	RecoveryCodesRemaining int        `json:"recovery_codes_remaining"`
}

// GetMFAStatus reads a user's second-factor state.
func (s *Service) GetMFAStatus(ctx context.Context, userID uuid.UUID) (*MFAStatus, error) {
	st := MFAStatus{Available: true}
	var enabledAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT u.totp_enabled_at,
		       (SELECT COUNT(*) FROM admin_user_recovery_codes c
		        WHERE c.admin_user_id = u.id AND c.used_at IS NULL)
		FROM admin_users u WHERE u.id = $1
	`, userID).Scan(&enabledAt, &st.RecoveryCodesRemaining)
	switch {
	case isMissingSchema(err):
		return &MFAStatus{}, nil
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrUserNotFound
	case err != nil:
		return nil, fmt.Errorf("load two-factor status: %w", err)
	}
	if enabledAt.Valid {
		st.Enabled = true
		st.EnabledAt = &enabledAt.Time
	}
	return &st, nil
}

// BeginTOTPEnrollment generates a new secret for a local account and stores
// it as pending (not yet enabled). It returns the plain base32 secret.
func (s *Service) BeginTOTPEnrollment(ctx context.Context, userID uuid.UUID) (string, error) {
	a, err := s.LoadAccount(ctx, userID)
	if err != nil {
		return "", err
	}
	if !a.Local() {
		return "", ErrNotLocalAccount
	}
	secret, err := newTOTPSecret()
	if err != nil {
		return "", err
	}
	sealed, err := s.encryptor().Encrypt(secret)
	if err != nil {
		return "", fmt.Errorf("encrypt TOTP secret: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE admin_users
		SET totp_secret_enc = $2, totp_last_step = NULL, updated_at = NOW()
		WHERE id = $1 AND totp_enabled_at IS NULL
	`, userID, sealed)
	if isMissingSchema(err) {
		return "", ErrMFAUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("store TOTP secret: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrMFAAlreadyEnabled
	}
	return secret, nil
}

// EnableTOTP confirms a pending enrollment with a code from the app, turns
// the second factor on and returns the recovery codes (shown once).
func (s *Service) EnableTOTP(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	var codes []string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var sealed sql.NullString
		var enabledAt sql.NullTime
		err := tx.QueryRowContext(ctx, `
			SELECT totp_secret_enc, totp_enabled_at FROM admin_users WHERE id = $1 FOR UPDATE
		`, userID).Scan(&sealed, &enabledAt)
		if isMissingSchema(err) {
			return ErrMFAUnavailable
		}
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		if err != nil {
			return err
		}
		if enabledAt.Valid {
			return ErrMFAAlreadyEnabled
		}
		if !sealed.Valid || sealed.String == "" {
			return ErrMFANoEnrollment
		}
		secret, err := s.encryptor().Decrypt(sealed.String)
		if err != nil {
			return fmt.Errorf("decrypt TOTP secret: %w", err)
		}
		step, ok := matchTOTP(secret, code, time.Now(), -1)
		if !ok {
			return ErrInvalidMFACode
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

// DisableTOTP turns the second factor off and drops the secret and the
// recovery codes. The caller has checked the password and a current code.
func (s *Service) DisableTOTP(ctx context.Context, userID uuid.UUID) (bool, error) {
	var was bool
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		was, err = clearTOTP(ctx, tx, userID)
		return err
	})
	if isMissingSchema(err) {
		return false, nil
	}
	return was, err
}

// ResetMFA is the superadmin reset: same as DisableTOTP, no proof needed.
// It reports whether a second factor was enabled.
func (s *Service) ResetMFA(ctx context.Context, userID uuid.UUID) (bool, error) {
	if _, err := s.LoadAccount(ctx, userID); err != nil {
		return false, err
	}
	return s.DisableTOTP(ctx, userID)
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

// RegenerateRecoveryCodes replaces the recovery codes of an enrolled user.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]string, error) {
	var codes []string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var enabled bool
		err := tx.QueryRowContext(ctx, `
			SELECT totp_enabled_at IS NOT NULL FROM admin_users WHERE id = $1 FOR UPDATE
		`, userID).Scan(&enabled)
		if isMissingSchema(err) {
			return ErrMFAUnavailable
		}
		if err != nil {
			return err
		}
		if !enabled {
			return ErrMFANotEnabled
		}
		codes, err = replaceRecoveryCodes(ctx, tx, userID)
		return err
	})
	return codes, err
}

// recoveryAlphabet avoids look-alike characters (no 0/o, 1/l/i).
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// newRecoveryCode returns a code like "k7m2q-x9p4t" (50 bits).
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
		// 256 % 31 bias is negligible against 50 bits of code space.
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

// Second-factor methods, for audit.
const (
	MFAMethodTOTP         = "totp"
	MFAMethodRecoveryCode = "recovery_code"
)

// CheckSecondFactor verifies a TOTP code (or a recovery code) for an
// enrolled user without the login lockout: the self-service routes behind
// a session use it after VerifyPassword. A TOTP code moves totp_last_step
// and a recovery code is burned, so neither is accepted again.
func (s *Service) CheckSecondFactor(ctx context.Context, userID uuid.UUID, code string) (string, error) {
	var sealed sql.NullString
	var enabledAt sql.NullTime
	var lastStep sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT totp_secret_enc, totp_enabled_at, totp_last_step FROM admin_users WHERE id = $1
	`, userID).Scan(&sealed, &enabledAt, &lastStep)
	if isMissingSchema(err) {
		return "", ErrMFANotEnabled
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load second factor: %w", err)
	}
	if !enabledAt.Valid || !sealed.Valid {
		return "", ErrMFANotEnabled
	}
	if normalizeTOTPCode(code) != "" {
		secret, err := s.encryptor().Decrypt(sealed.String)
		if err != nil {
			return "", fmt.Errorf("decrypt TOTP secret: %w", err)
		}
		last := int64(-1)
		if lastStep.Valid {
			last = lastStep.Int64
		}
		step, ok := matchTOTP(secret, code, time.Now(), last)
		if !ok {
			return "", ErrInvalidMFACode
		}
		// The conditional UPDATE is the replay guard across concurrent
		// requests and replicas: only one can move the step forward.
		res, err := s.db.ExecContext(ctx, `
			UPDATE admin_users SET totp_last_step = $2
			WHERE id = $1 AND (totp_last_step IS NULL OR totp_last_step < $2)
		`, userID, step)
		if err != nil {
			return "", fmt.Errorf("record TOTP step: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return "", ErrInvalidMFACode
		}
		return MFAMethodTOTP, nil
	}
	if normalizeRecoveryCode(code) == "" {
		return "", ErrInvalidMFACode
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE admin_user_recovery_codes SET used_at = NOW()
		WHERE admin_user_id = $1 AND code_hash = $2 AND used_at IS NULL
	`, userID, hashRecoveryCode(code))
	if err != nil {
		return "", fmt.Errorf("use recovery code: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrInvalidMFACode
	}
	return MFAMethodRecoveryCode, nil
}

// secondFactorBinding fingerprints what a step-one login token vouches for:
// the password hash and the TOTP enrollment. Changing either invalidates
// outstanding step-one tokens.
func secondFactorBinding(passwordHash, sealedSecret string) string {
	sum := sha256.Sum256([]byte(passwordHash + "\x00" + sealedSecret))
	return hex.EncodeToString(sum[:12])
}

// totpRequired reports whether a password-verified user must pass a
// second factor, and the binding for the step-one token. Before migration
// 000118 nobody has a second factor.
func (s *Service) totpRequired(ctx context.Context, userID uuid.UUID, passwordHash string) (string, bool, error) {
	var sealed sql.NullString
	var enabledAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT totp_secret_enc, totp_enabled_at FROM admin_users WHERE id = $1
	`, userID).Scan(&sealed, &enabledAt)
	if isMissingSchema(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !enabledAt.Valid {
		return "", false, nil
	}
	return secondFactorBinding(passwordHash, sealed.String), true, nil
}

// releaseReservation undoes the failure Authenticate reserved for a
// password that turned out correct on an account whose sign-in is not over
// yet (second factor pending). It does NOT clear the streak, so password
// logins cannot wash out failed second-factor attempts; only a completed
// sign-in does (CompleteSecondFactor). lockedNow lifts the lock this very
// reservation set.
const releaseReservationQuery = `
	UPDATE admin_users
	SET failed_login_count = GREATEST(failed_login_count - 1, 0),
	    locked_until = CASE WHEN $2 THEN NULL ELSE locked_until END
	WHERE id = $1
`

// CompleteSecondFactor is step two of a password login: under the same
// lockout as step one (the attempt is reserved first, so second-factor
// guesses lock the account exactly like password guesses), it checks the
// binding of the step-one token and the code, and on success clears the
// failure streak. A refusal is a *LoginFailure (wrong code or locked) or
// ErrTokenInvalid (stale step-one token).
func (s *Service) CompleteSecondFactor(ctx context.Context, userID uuid.UUID, binding, code string) (string, error) {
	var hash, sealed sql.NullString
	var enabledAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT password_hash, totp_secret_enc, totp_enabled_at
		FROM admin_users
		WHERE id = $1 AND disabled_at IS NULL AND external_subject IS NULL
	`, userID).Scan(&hash, &sealed, &enabledAt)
	if errors.Is(err, sql.ErrNoRows) || isMissingSchema(err) {
		return "", ErrTokenInvalid
	}
	if err != nil {
		return "", fmt.Errorf("load second factor: %w", err)
	}
	if !enabledAt.Valid || !hash.Valid || secondFactorBinding(hash.String, sealed.String) != binding {
		return "", ErrTokenInvalid
	}

	count, until, locked, err := s.reserveAttempt(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("record second-factor attempt: %w", err)
	}
	if locked {
		return "", &LoginFailure{Locked: true, UserID: &userID, RetryAfter: s.lockedFor(ctx, userID)}
	}
	method, err := s.CheckSecondFactor(ctx, userID, code)
	if errors.Is(err, ErrInvalidMFACode) || errors.Is(err, ErrMFANotEnabled) {
		failure := &LoginFailure{UserID: &userID, FailedAttempts: count}
		if until != nil && until.After(time.Now()) {
			failure.LockedNow = true
			failure.RetryAfter = time.Until(*until)
		}
		return "", failure
	}
	if err != nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx, resetFailuresQuery, userID); err != nil {
		return "", fmt.Errorf("reset login failures: %w", err)
	}
	return method, nil
}

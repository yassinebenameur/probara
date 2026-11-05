package adminauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/auth"
)

// User lifecycle: invites, password reset, self-service password and email
// change. A "local account" is one that signs in with a password Probara
// owns: a password hash and no linked IdP subject. Every self-service flow
// (and the reset) is for local accounts only: when external_subject is set
// the IdP owns the identity, and the email is what links it.

// RevokeReasonPasswordReset is recorded on the sessions a reset ends.
const RevokeReasonPasswordReset = "password_reset"

// passwordResetThrottle is the minimum gap between two reset mails for one
// account, whatever the request volume: the general rate limiter bounds
// requests per address, this bounds mail per mailbox.
const passwordResetThrottle = time.Minute

var (
	// ErrNotLocalAccount refuses a self-service change on an SSO account.
	ErrNotLocalAccount = errors.New("this account signs in through single sign-on; its password and email are managed by the identity provider")
	// ErrEmailTaken: another account already uses the address.
	ErrEmailTaken = errors.New("email already in use")
	// ErrSameEmail: the requested address is already the account's.
	ErrSameEmail = errors.New("that is already your email address")
	// ErrInviteSSOOnly: the account signs in through SSO and gets no
	// password link.
	ErrInviteSSOOnly = errors.New("this account signs in through single sign-on")
	// ErrNoPassword: a reset needs an account that has a password.
	ErrNoPassword = errors.New("this account has no password to reset; send an invite instead")
)

// Account is the identity facts the lifecycle flows decide on.
type Account struct {
	ID          uuid.UUID
	Username    string
	Email       *string
	HasPassword bool
	// Linked: an IdP subject is linked (external_subject set).
	Linked   bool
	Disabled bool
}

// Local reports whether the account signs in with a Probara-owned password.
func (a *Account) Local() bool {
	return a.HasPassword && !a.Linked
}

// LoadAccount reads the lifecycle facts of one user.
func (s *Service) LoadAccount(ctx context.Context, userID uuid.UUID) (*Account, error) {
	return loadAccount(ctx, s.db, userID)
}

func loadAccount(ctx context.Context, q Querier, userID uuid.UUID) (*Account, error) {
	var a Account
	err := q.QueryRowContext(ctx, `
		SELECT id, username, email,
		       COALESCE(password_hash, '') <> '',
		       external_subject IS NOT NULL,
		       disabled_at IS NOT NULL
		FROM admin_users WHERE id = $1
	`, userID).Scan(&a.ID, &a.Username, &a.Email, &a.HasPassword, &a.Linked, &a.Disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load account: %w", err)
	}
	return &a, nil
}

// PasswordResetMail is what the caller mails after RequestPasswordReset.
type PasswordResetMail struct {
	UserID   uuid.UUID
	Username string
	Email    string
	Token    IssuedToken
}

// RequestPasswordReset issues a reset token for the active local account
// whose username, or email (case-insensitive), is login, and returns where
// to mail it. It returns nil, nil when there is nothing to send: no such
// account, an SSO or password-less account, no email on file, or a reset
// issued for it in the last passwordResetThrottle. Callers must answer the
// same way in every case (no account enumeration).
func (s *Service) RequestPasswordReset(ctx context.Context, login string) (*PasswordResetMail, error) {
	login = strings.TrimSpace(login)
	if login == "" {
		return nil, nil
	}
	var out PasswordResetMail
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var email sql.NullString
		err := tx.QueryRowContext(ctx, `
			SELECT id, username, email
			FROM admin_users
			WHERE (username = $1 OR (email IS NOT NULL AND lower(email) = lower($1)))
			  AND disabled_at IS NULL
			  AND COALESCE(password_hash, '') <> ''
			  AND external_subject IS NULL
			ORDER BY (username = $1) DESC
			LIMIT 1
			FOR UPDATE
		`, login).Scan(&out.UserID, &out.Username, &email)
		if errors.Is(err, sql.ErrNoRows) {
			return errNothingToSend
		}
		if err != nil {
			return err
		}
		if !email.Valid || email.String == "" {
			return errNothingToSend
		}
		out.Email = email.String
		var recent bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM admin_user_tokens
				WHERE admin_user_id = $1 AND purpose = $2
				  AND created_at > NOW() - make_interval(secs => $3)
			)
		`, out.UserID, PurposePasswordReset, passwordResetThrottle.Seconds()).Scan(&recent); err != nil {
			return mapTokenSchema(err)
		}
		if recent {
			return errNothingToSend
		}
		issued, err := issueToken(ctx, tx, out.UserID, PurposePasswordReset, nil, nil)
		if err != nil {
			return err
		}
		out.Token = *issued
		return nil
	})
	if errors.Is(err, errNothingToSend) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// errNothingToSend ends RequestPasswordReset's transaction without a token.
var errNothingToSend = errors.New("nothing to send")

// IssuePasswordReset is the superadmin-issued reset link for a local
// account (no throttle: a superadmin asked for it).
func (s *Service) IssuePasswordReset(ctx context.Context, userID uuid.UUID, createdBy *uuid.UUID) (*Account, *IssuedToken, error) {
	var account *Account
	var issued *IssuedToken
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		a, err := loadAccount(ctx, tx, userID)
		if err != nil {
			return err
		}
		if a.Linked {
			return ErrNotLocalAccount
		}
		if !a.HasPassword {
			return ErrNoPassword
		}
		account = a
		issued, err = issueToken(ctx, tx, userID, PurposePasswordReset, nil, createdBy)
		return err
	})
	return account, issued, err
}

// IssueInvite mints an invite (set-your-password) token. The account must
// not be linked to an IdP; one that already has a password gets a link
// that replaces it, which is how a superadmin re-sends access.
func (s *Service) IssueInvite(ctx context.Context, userID uuid.UUID, createdBy *uuid.UUID) (*Account, *IssuedToken, error) {
	var account *Account
	var issued *IssuedToken
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		a, err := loadAccount(ctx, tx, userID)
		if err != nil {
			return err
		}
		if a.Linked {
			return ErrInviteSSOOnly
		}
		account = a
		issued, err = issueToken(ctx, tx, userID, PurposeInvite, nil, createdBy)
		return err
	})
	return account, issued, err
}

// RedeemPasswordToken consumes an invite or reset token and sets the
// account's password: it records password_changed_at, clears the lockout,
// supersedes the account's other open invite and reset links, and ends
// every session. It returns the account. An account that was disabled or
// linked to an IdP since the link was issued gets ErrTokenInvalid.
func (s *Service) RedeemPasswordToken(ctx context.Context, plain, purpose, newPassword string) (*Account, error) {
	if purpose != PurposeInvite && purpose != PurposePasswordReset {
		return nil, ErrTokenInvalid
	}
	hash, err := auth.HashPassword(newPassword, s.bcryptCost)
	if err != nil {
		return nil, err
	}
	var account *Account
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		rec, err := consumeToken(ctx, tx, plain, purpose)
		if err != nil {
			return err
		}
		a, err := loadAccount(ctx, tx, rec.UserID)
		if err != nil {
			return ErrTokenInvalid
		}
		if a.Disabled || a.Linked || (purpose == PurposePasswordReset && !a.HasPassword) {
			return ErrTokenInvalid
		}
		if err := setPassword(ctx, tx, a.ID, hash); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE admin_user_tokens SET used_at = NOW()
			WHERE admin_user_id = $1 AND purpose IN ($2, $3) AND used_at IS NULL
		`, a.ID, PurposeInvite, PurposePasswordReset); err != nil {
			return err
		}
		reason := RevokeReasonPasswordReset
		if purpose == PurposeInvite {
			reason = RevokeReasonPasswordChange
		}
		if _, err := RevokeUserSessions(ctx, tx, a.ID, uuid.Nil, reason); err != nil {
			return err
		}
		a.HasPassword = true
		account = a
		return nil
	})
	return account, err
}

// setPassword stores a new hash, records the change and clears the lockout.
func setPassword(ctx context.Context, q Querier, userID uuid.UUID, hash string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE admin_users
		SET password_hash = $2, password_changed_at = NOW(), updated_at = NOW(),
		    failed_login_count = 0, last_failed_login_at = NULL, locked_until = NULL
		WHERE id = $1
	`, userID, hash)
	return err
}

// VerifyPassword checks a signed-in user's current password before a
// sensitive change, under the login lockout (see VerifyCredentials).
func (s *Service) VerifyPassword(ctx context.Context, userID uuid.UUID, password string) error {
	_, err := s.VerifyCredentials(ctx, userID, password, "", false)
	return err
}

// VerifyCredentials checks a signed-in user's current password, and with
// withCode also a TOTP or recovery code, under the login lockout: ONE
// attempt is reserved first, any wrong factor leaves it counted as a
// failed login, and only all factors passing clears the streak. Checking
// both in one reservation matters: a correct password must not reset the
// streak that wrong codes build up. A refusal is a *LoginFailure, with
// SecondFactor set when the password was right and the code was not.
func (s *Service) VerifyCredentials(ctx context.Context, userID uuid.UUID, password, code string, withCode bool) (string, error) {
	var hash sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM admin_users WHERE id = $1`, userID).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrUserNotFound
		}
		return "", fmt.Errorf("load password: %w", err)
	}
	if !hash.Valid || hash.String == "" {
		return "", ErrNotLocalAccount
	}
	count, until, locked, err := s.reserveAttempt(ctx, userID)
	if err != nil && !isMissingSchema(err) {
		return "", fmt.Errorf("record password attempt: %w", err)
	}
	if locked {
		return "", &LoginFailure{Locked: true, UserID: &userID, RetryAfter: s.lockedFor(ctx, userID)}
	}
	failed := func(secondFactor bool) error {
		failure := &LoginFailure{UserID: &userID, FailedAttempts: count, SecondFactor: secondFactor}
		if until != nil && until.After(time.Now()) {
			failure.LockedNow = true
			failure.RetryAfter = time.Until(*until)
		}
		return failure
	}
	match, err := auth.ComparePassword(hash.String, password)
	if err != nil {
		return "", err
	}
	if !match {
		return "", failed(false)
	}
	method := ""
	if withCode {
		method, err = s.CheckSecondFactor(ctx, userID, code)
		if errors.Is(err, ErrInvalidMFACode) {
			return "", failed(true)
		}
		if err != nil {
			return "", err
		}
	}
	if _, err := s.db.ExecContext(ctx, resetFailuresQuery, userID); err != nil && !isMissingSchema(err) {
		return "", fmt.Errorf("reset login failures: %w", err)
	}
	return method, nil
}

// lockedFor reads how long an account's lock has left.
func (s *Service) lockedFor(ctx context.Context, userID uuid.UUID) time.Duration {
	var until sql.NullTime
	if err := s.db.QueryRowContext(ctx, `SELECT locked_until FROM admin_users WHERE id = $1`, userID).Scan(&until); err != nil || !until.Valid {
		return 0
	}
	return time.Until(until.Time)
}

// ChangePassword sets a signed-in local user's new password (the caller has
// verified the current one) and ends every other session; keep is the
// session making the change.
func (s *Service) ChangePassword(ctx context.Context, userID, keep uuid.UUID, newPassword string) (int, error) {
	hash, err := auth.HashPassword(newPassword, s.bcryptCost)
	if err != nil {
		return 0, err
	}
	var revoked int
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		a, err := loadAccount(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !a.Local() {
			return ErrNotLocalAccount
		}
		if err := setPassword(ctx, tx, userID, hash); err != nil {
			return err
		}
		// Outstanding reset links were for the old password.
		if _, err := tx.ExecContext(ctx, `
			UPDATE admin_user_tokens SET used_at = NOW()
			WHERE admin_user_id = $1 AND purpose IN ($2, $3) AND used_at IS NULL
		`, userID, PurposeInvite, PurposePasswordReset); err != nil && !isMissingSchema(err) {
			return err
		}
		revoked, err = RevokeUserSessions(ctx, tx, userID, keep, RevokeReasonPasswordChange)
		return err
	})
	return revoked, err
}

// EmailChange is a pending change the caller mails to NewEmail.
type EmailChange struct {
	Account  *Account
	NewEmail string
	Token    IssuedToken
}

// RequestEmailChange issues a confirmation token for a local user's new
// address (the caller verified the current password). The address changes
// only when the link sent to it is used.
func (s *Service) RequestEmailChange(ctx context.Context, userID uuid.UUID, newEmail string) (*EmailChange, error) {
	newEmail = strings.ToLower(strings.TrimSpace(newEmail))
	var out EmailChange
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		a, err := loadAccount(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !a.Local() {
			return ErrNotLocalAccount
		}
		if a.Email != nil && strings.EqualFold(*a.Email, newEmail) {
			return ErrSameEmail
		}
		var taken bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM admin_users WHERE lower(email) = $1 AND id <> $2)
		`, newEmail, userID).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return ErrEmailTaken
		}
		issued, err := issueToken(ctx, tx, userID, PurposeEmailChange, &newEmail, &userID)
		if err != nil {
			return err
		}
		out = EmailChange{Account: a, NewEmail: newEmail, Token: *issued}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ConfirmedEmailChange reports a completed change.
type ConfirmedEmailChange struct {
	Account  *Account
	OldEmail *string
	NewEmail string
}

// ConfirmEmailChange consumes an email-change token and sets the address.
func (s *Service) ConfirmEmailChange(ctx context.Context, plain string) (*ConfirmedEmailChange, error) {
	var out ConfirmedEmailChange
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		rec, err := consumeToken(ctx, tx, plain, PurposeEmailChange)
		if err != nil {
			return err
		}
		a, err := loadAccount(ctx, tx, rec.UserID)
		if err != nil || a.Disabled || !a.Local() || rec.NewEmail == nil {
			return ErrTokenInvalid
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE admin_users SET email = $2, updated_at = NOW() WHERE id = $1
		`, a.ID, *rec.NewEmail); err != nil {
			var pqErr *pq.Error
			if errors.As(err, &pqErr) && pqErr.Code == "23505" {
				return ErrEmailTaken
			}
			return err
		}
		out = ConfirmedEmailChange{Account: a, OldEmail: a.Email, NewEmail: *rec.NewEmail}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// InviteSubject is the account an invite link sets up (accept-invite page).
func (s *Service) InviteSubject(ctx context.Context, plain string) (*Account, time.Time, error) {
	rec, err := s.InspectToken(ctx, plain, PurposeInvite)
	if err != nil {
		return nil, time.Time{}, err
	}
	a, err := s.LoadAccount(ctx, rec.UserID)
	if err != nil || a.Disabled || a.Linked {
		return nil, time.Time{}, ErrTokenInvalid
	}
	return a, rec.ExpiresAt, nil
}

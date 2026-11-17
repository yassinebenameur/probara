package adminauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db/pgerr"
)

// Account self-service and its superadmin side: invites, password reset,
// email change and the user's own password change. They are for local
// accounts only, those that sign in with a password Probara owns and have no
// linked identity-provider subject: for an SSO account the IdP owns the
// password, and the email is what links the identity.

// PasswordResetMailsPerHour caps the reset mails the public forgot-password
// form sends one account in an hour, whatever the request volume: the API
// rate limiter bounds requests per address, this bounds mail per mailbox.
// Links a superadmin generates are not counted.
const PasswordResetMailsPerHour = 3

var (
	// ErrNotLocalAccount refuses a password, email or two-factor change on
	// an SSO account.
	ErrNotLocalAccount = errors.New("this account signs in with single sign-on; its password, email address and second factor are managed by the identity provider")
	// ErrAccountDisabled: invites and reset links are not issued for a
	// disabled account.
	ErrAccountDisabled = errors.New("the account is disabled")
	// ErrHasPassword: an invite is for an account without a password.
	ErrHasPassword = errors.New("the account already has a password; generate a password reset link instead")
	// ErrNoPassword: a reset link needs an account that has a password.
	ErrNoPassword = errors.New("the account has no password yet; send an invite instead")
	// ErrEmailTaken: another account uses the address.
	ErrEmailTaken = errors.New("that email address is already used by another account")
	// ErrSameEmail: the requested address is already the account's.
	ErrSameEmail = errors.New("that is already your email address")
)

// Account is the identity facts the account flows decide on.
type Account struct {
	ID          uuid.UUID
	Username    string
	Email       *string
	HasPassword bool
	// Linked: an identity-provider subject is linked (external_subject).
	Linked   bool
	Disabled bool
}

// Local reports whether the account signs in with a Probara-owned password
// and no identity provider.
func (a *Account) Local() bool {
	return a.HasPassword && !a.Linked
}

// LoadAccount reads one user's account facts; ErrUserNotFound for an unknown
// id.
func (s *Service) LoadAccount(ctx context.Context, userID uuid.UUID) (*Account, error) {
	return loadAccount(ctx, s.db, userID, false)
}

func loadAccount(ctx context.Context, q SessionQuerier, userID uuid.UUID, forUpdate bool) (*Account, error) {
	query := `
		SELECT id, username, email,
		       COALESCE(password_hash, '') <> '',
		       external_subject IS NOT NULL,
		       disabled_at IS NOT NULL
		FROM admin_users WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var a Account
	err := q.QueryRowContext(ctx, query, userID).Scan(&a.ID, &a.Username, &a.Email, &a.HasPassword, &a.Linked, &a.Disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load account: %w", err)
	}
	return &a, nil
}

// AccountLink is an issued invite or reset link and the account it is for.
type AccountLink struct {
	Account *Account
	Token   IssuedToken
}

// IssueInvite mints a set-your-password invite for an enabled account with
// no password and no linked identity provider. It supersedes the account's
// previous invite. createdBy is the superadmin asking.
func (s *Service) IssueInvite(ctx context.Context, userID uuid.UUID, createdBy *uuid.UUID) (*AccountLink, error) {
	return s.issueLink(ctx, userID, PurposeInvite, createdBy, func(a *Account) error {
		switch {
		case a.Linked:
			return ErrNotLocalAccount
		case a.HasPassword:
			return ErrHasPassword
		}
		return nil
	})
}

// IssuePasswordReset mints a superadmin-issued reset link for an enabled
// local account (no hourly cap: a superadmin asked for it).
func (s *Service) IssuePasswordReset(ctx context.Context, userID uuid.UUID, createdBy *uuid.UUID) (*AccountLink, error) {
	return s.issueLink(ctx, userID, PurposePasswordReset, createdBy, func(a *Account) error {
		switch {
		case a.Linked:
			return ErrNotLocalAccount
		case !a.HasPassword:
			return ErrNoPassword
		}
		return nil
	})
}

func (s *Service) issueLink(ctx context.Context, userID uuid.UUID, purpose string, createdBy *uuid.UUID, eligible func(*Account) error) (*AccountLink, error) {
	var out AccountLink
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		a, err := loadAccount(ctx, tx, userID, true)
		if err != nil {
			return err
		}
		if a.Disabled {
			return ErrAccountDisabled
		}
		if err := eligible(a); err != nil {
			return err
		}
		issued, err := issueToken(ctx, tx, userID, purpose, nil, createdBy)
		if err != nil {
			return err
		}
		out = AccountLink{Account: a, Token: *issued}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// RequestPasswordReset issues a reset token for the enabled local account
// whose username, or email (case-insensitive), is login, and returns where to
// mail it. It returns nil, nil when there is nothing to send: no such
// account, an SSO, disabled or password-less account, no email on file, or
// PasswordResetMailsPerHour reached. A locked account does get one (the reset
// is how its owner gets back in). Callers answer the same way in every case.
func (s *Service) RequestPasswordReset(ctx context.Context, login string) (*AccountLink, error) {
	login = strings.TrimSpace(login)
	if login == "" {
		return nil, nil
	}
	var out *AccountLink
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var id uuid.UUID
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM admin_users
			WHERE (username = $1 OR (email IS NOT NULL AND lower(email) = lower($1)))
			  AND disabled_at IS NULL
			  AND COALESCE(password_hash, '') <> ''
			  AND external_subject IS NULL
			  AND COALESCE(email, '') <> ''
			ORDER BY (username = $1) DESC
			LIMIT 1
			FOR UPDATE
		`, login).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var recent int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM admin_account_tokens
			WHERE admin_user_id = $1 AND purpose = $2 AND created_by IS NULL
			  AND created_at > NOW() - INTERVAL '1 hour'
		`, id, PurposePasswordReset).Scan(&recent); err != nil {
			return mapTokenSchema(err)
		}
		if recent >= PasswordResetMailsPerHour {
			return nil
		}
		a, err := loadAccount(ctx, tx, id, false)
		if err != nil {
			return err
		}
		issued, err := issueToken(ctx, tx, id, PurposePasswordReset, nil, nil)
		if err != nil {
			return err
		}
		out = &AccountLink{Account: a, Token: *issued}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// InviteSubject is the account a live invite link sets up, and the link's
// expiry, without using the link (the accept-invite page names the account).
func (s *Service) InviteSubject(ctx context.Context, plain string) (*Account, time.Time, error) {
	rec, err := peekToken(ctx, s.db, plain, PurposeInvite)
	if err != nil {
		return nil, time.Time{}, err
	}
	a, err := s.LoadAccount(ctx, rec.UserID)
	if err != nil || a.Disabled || a.Linked {
		return nil, time.Time{}, ErrTokenInvalid
	}
	return a, rec.ExpiresAt, nil
}

// RedeemPasswordToken uses an invite or reset link to set the account's
// password. In one transaction it retires every other open link and sign-in
// challenge of the account, ends all of its sessions, and clears the sign-in
// lockout -- unless two-factor is on: then the streak and any lock survive
// and the second factor stays on, because a reset needs only the mailbox,
// and clearing the lock would let a mailbox holder buy a fresh batch of code
// guesses with each reset. A link of another purpose, or for an account that
// was disabled or linked to an identity provider since, is ErrTokenInvalid.
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
		a, err := loadAccount(ctx, tx, rec.UserID, true)
		if err != nil {
			return ErrTokenInvalid
		}
		if a.Disabled || a.Linked || (purpose == PurposePasswordReset && !a.HasPassword) {
			return ErrTokenInvalid
		}
		secondFactor, err := secondFactorEnabledTx(ctx, tx, a.ID)
		if err != nil {
			return err
		}
		if err := setPassword(ctx, tx, a.ID, hash, !secondFactor); err != nil {
			return err
		}
		if err := retireTokens(ctx, tx, a.ID, allTokenPurposes...); err != nil {
			return err
		}
		if _, err := RevokeUserSessions(ctx, tx, a.ID, uuid.Nil, RevokeReasonPasswordReset); err != nil {
			return err
		}
		a.HasPassword = true
		account = a
		return nil
	})
	return account, err
}

// setPassword stores a new hash and, with clearLockout, clears the failure
// streak and any lock (the old password being guessed no longer exists).
// Before migration 000117 there is no lockout to clear.
func setPassword(ctx context.Context, tx *sql.Tx, userID uuid.UUID, hash string, clearLockout bool) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_users SET password_hash = $2, updated_at = NOW() WHERE id = $1
	`, userID, hash); err != nil {
		return err
	}
	if !clearLockout {
		return nil
	}
	return tolerateMissingSchema(ctx, tx, func() error {
		_, err := tx.ExecContext(ctx, clearFailuresQuery, userID)
		return err
	})
}

// ChangePassword sets a signed-in local user's new password (the caller has
// verified the current one), retires every open link and sign-in challenge,
// and ends every other session; keep is the session making the change. The
// lockout and the second factor are left as they are. It returns how many
// sessions ended.
func (s *Service) ChangePassword(ctx context.Context, userID, keep uuid.UUID, newPassword string) (int, error) {
	hash, err := auth.HashPassword(newPassword, s.bcryptCost)
	if err != nil {
		return 0, err
	}
	var revoked int
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		a, err := loadAccount(ctx, tx, userID, true)
		if err != nil {
			return err
		}
		if !a.Local() {
			return ErrNotLocalAccount
		}
		if err := setPassword(ctx, tx, userID, hash, false); err != nil {
			return err
		}
		if err := retireTokensTolerant(ctx, tx, userID, allTokenPurposes...); err != nil {
			return err
		}
		revoked, err = RevokeUserSessions(ctx, tx, userID, keep, RevokeReasonPasswordReset)
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
// address (the caller verified the current password and the address shape).
// The address changes only when the link sent to it is used; a newer request
// supersedes the pending one. Whether another account uses the address is
// checked on confirmation, never here, so the form cannot be used to probe
// which addresses have accounts.
func (s *Service) RequestEmailChange(ctx context.Context, userID uuid.UUID, newEmail string) (*EmailChange, error) {
	newEmail = strings.ToLower(strings.TrimSpace(newEmail))
	var out EmailChange
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		a, err := loadAccount(ctx, tx, userID, true)
		if err != nil {
			return err
		}
		if !a.Local() {
			return ErrNotLocalAccount
		}
		if a.Email != nil && strings.EqualFold(*a.Email, newEmail) {
			return ErrSameEmail
		}
		issued, err := issueToken(ctx, tx, userID, PurposeEmailChange, &newEmail, nil)
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

// PendingEmailChange is the address a user's open email-change link would
// set, and when the link expires; nil when there is none.
func (s *Service) PendingEmailChange(ctx context.Context, userID uuid.UUID) (*string, *time.Time, error) {
	var email string
	var expires time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT new_email, expires_at FROM admin_account_tokens
		WHERE admin_user_id = $1 AND purpose = $2
		  AND used_at IS NULL AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC
		LIMIT 1
	`, userID, PurposeEmailChange).Scan(&email, &expires)
	if errors.Is(err, sql.ErrNoRows) || pgerr.IsMissingSchema(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load pending email change: %w", err)
	}
	return &email, &expires, nil
}

// ConfirmedEmailChange reports a completed change.
type ConfirmedEmailChange struct {
	Account  *Account
	OldEmail *string
	NewEmail string
}

// ConfirmEmailChange uses an email-change link and sets the address. The
// account must still be an enabled local account. An address another account
// took meanwhile is ErrEmailTaken, and the link stays usable.
func (s *Service) ConfirmEmailChange(ctx context.Context, plain string) (*ConfirmedEmailChange, error) {
	var out ConfirmedEmailChange
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		rec, err := consumeToken(ctx, tx, plain, PurposeEmailChange)
		if err != nil {
			return err
		}
		a, err := loadAccount(ctx, tx, rec.UserID, true)
		if err != nil || a.Disabled || !a.Local() || rec.NewEmail == nil {
			return ErrTokenInvalid
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE admin_users SET email = $2, updated_at = NOW() WHERE id = $1
		`, a.ID, *rec.NewEmail); err != nil {
			if pgerr.IsUniqueViolation(err) {
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

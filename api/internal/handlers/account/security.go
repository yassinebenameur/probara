package account

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/accountmail"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/mail"
)

// The signed-in user's own password, email address and second factor. Local
// (password) accounts only: an SSO account's identity provider owns all
// three. Every change re-checks the current password under the sign-in
// lockout, so a stolen session alone cannot lock the owner out or take the
// account over; a wrong current password is a 400, never a 401 (the web
// client takes a 401 for an expired session and would retry it).

type securityService interface {
	LoadAccount(ctx context.Context, userID uuid.UUID) (*adminauth.Account, error)
	GetTwoFactorStatus(ctx context.Context, userID uuid.UUID) (*adminauth.TwoFactorStatus, error)
	TwoFactorEnrolmentAvailable() bool
	PendingEmailChange(ctx context.Context, userID uuid.UUID) (*string, *time.Time, error)
	VerifyCurrentPassword(ctx context.Context, userID uuid.UUID, password string) error
	VerifyCurrentCredentials(ctx context.Context, userID uuid.UUID, password, code string, withCode bool) (string, error)
	ChangePassword(ctx context.Context, userID, keep uuid.UUID, newPassword string) (int, error)
	RequestEmailChange(ctx context.Context, userID uuid.UUID, newEmail string) (*adminauth.EmailChange, error)
	BeginTOTPEnrolment(ctx context.Context, userID uuid.UUID) (string, error)
	EnableTOTP(ctx context.Context, userID uuid.UUID, code string) ([]string, error)
	DisableTOTP(ctx context.Context, userID, keep uuid.UUID) (int, error)
	RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]string, error)
}

// WithSecurity attaches the password, email and two-factor operations and
// the account mailer (confirmation links and security notices).
func (h *Handlers) WithSecurity(svc securityService, m *accountmail.Mailer) *Handlers {
	h.security = svc
	h.mail = m
	return h
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(dst); err != nil {
		apierrors.WriteValidationError(w, "invalid request body")
		return false
	}
	return true
}

// recordAccount audits a change the user made to their own account.
func (h *Handlers) recordAccount(r *http.Request, action string, userID uuid.UUID, details map[string]any) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.Action = action
	event.Outcome = audit.OutcomeSuccess
	event.ActorType = audit.ActorAdminUser
	event.ActorID = &userID
	event.ResourceType = "user"
	event.ResourceID = userID.String()
	event.MergeDetails(details)
	h.audit.Record(event)
}

// writeError maps the service errors of the account routes.
func (h *Handlers) writeError(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, adminauth.ErrNotLocalAccount):
		apierrors.WriteError(w, http.StatusConflict, "not_local_account", err.Error())
	case errors.Is(err, adminauth.ErrSameEmail):
		apierrors.WriteValidationError(w, err.Error())
	case errors.Is(err, adminauth.ErrInvalidSecondFactor):
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_code", err.Error())
	case errors.Is(err, adminauth.ErrTwoFactorEnabled), errors.Is(err, adminauth.ErrTwoFactorDisabled),
		errors.Is(err, adminauth.ErrNoPendingEnrolment):
		apierrors.WriteError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, adminauth.ErrTwoFactorNeedsKey):
		apierrors.WriteError(w, http.StatusConflict, "two_factor_unavailable", err.Error())
	case errors.Is(err, adminauth.ErrTwoFactorUnavailable), errors.Is(err, adminauth.ErrTokensUnavailable):
		apierrors.WriteError(w, http.StatusServiceUnavailable, "unavailable", err.Error())
	case errors.Is(err, adminauth.ErrUserNotFound):
		apierrors.WriteUnauthorizedError(w, "invalid session")
	default:
		h.logger.WithError(err).Error("Failed to " + what)
		apierrors.WriteInternalError(w, "failed to "+what)
	}
}

// writeCredentialFailure answers a refused current-password (and code)
// check. The account is the caller's own, so a lock is said plainly.
func (h *Handlers) writeCredentialFailure(w http.ResponseWriter, r *http.Request, userID uuid.UUID, err error) {
	var failure *adminauth.LoginFailure
	if !errors.As(err, &failure) {
		h.writeError(w, err, "check the current password")
		return
	}
	if failure.LockedNow {
		h.recordLockout(r, userID, failure)
	}
	switch {
	case failure.Locked || failure.LockedNow:
		apierrors.WriteError(w, http.StatusTooManyRequests, "account_locked",
			"Too many failed attempts on this account. Try again in 15 minutes, or ask an administrator to unlock it.")
	case failure.SecondFactor:
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_code", adminauth.ErrInvalidSecondFactor.Error())
	default:
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_password", "the current password is incorrect")
	}
}

func (h *Handlers) recordLockout(r *http.Request, userID uuid.UUID, failure *adminauth.LoginFailure) {
	if h.audit == nil {
		return
	}
	lock := audit.FromRequest(r)
	lock.Action = "auth.lockout"
	lock.Outcome = audit.OutcomeSuccess
	lock.ActorType = audit.ActorAdminUser
	lock.ActorID = &userID
	lock.ResourceType = "user"
	lock.ResourceID = userID.String()
	lock.MergeDetails(map[string]any{
		"failed_attempts": adminauth.LockoutThreshold,
		"locked_until":    failure.LockedUntil.UTC(),
		"step":            "account_page",
	})
	h.audit.Record(lock)
}

// localAccount loads the caller's account and refuses an SSO one.
func (h *Handlers) localAccount(w http.ResponseWriter, r *http.Request, userID uuid.UUID) (*adminauth.Account, bool) {
	account, err := h.security.LoadAccount(r.Context(), userID)
	if err != nil {
		h.writeError(w, err, "load the account")
		return nil, false
	}
	if !account.Local() {
		h.writeError(w, adminauth.ErrNotLocalAccount, "")
		return nil, false
	}
	return account, true
}

// Security handles GET /api/v1/account/security: what the account page's
// Password, Email and Two-factor tabs show.
func (h *Handlers) Security(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := sessionCaller(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	account, err := h.security.LoadAccount(ctx, userID)
	if err != nil {
		h.writeError(w, err, "load the account")
		return
	}
	status, err := h.security.GetTwoFactorStatus(ctx, userID)
	if err != nil {
		h.writeError(w, err, "load two-factor status")
		return
	}
	pending, pendingExpires, err := h.security.PendingEmailChange(ctx, userID)
	if err != nil {
		h.writeError(w, err, "load the pending email change")
		return
	}
	method := adminauth.AuthMethodPassword
	if account.Linked {
		method = adminauth.AuthMethodOIDC
	}
	httpx.WriteJSON(w, http.StatusOK, models.AccountSecurity{
		AuthMethod:            method,
		Local:                 account.Local(),
		HasPassword:           account.HasPassword,
		Email:                 account.Email,
		PendingEmail:          pending,
		PendingEmailExpiresAt: pendingExpires,
		EmailDelivery:         h.mail.Enabled(),
		TwoFactor: models.TwoFactorSettings{
			Enabled:                status.Enabled,
			EnabledAt:              status.EnabledAt,
			RecoveryCodesRemaining: status.RecoveryCodesRemaining,
			EnrolmentAvailable:     status.Supported && h.security.TwoFactorEnrolmentAvailable(),
		},
	})
}

// ChangePassword handles POST /api/v1/account/password: a local user's new
// password, with the current one (checked under the sign-in lockout). Every
// other session of the account ends; this one stays signed in.
func (h *Handlers) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID, current, ok := sessionCaller(w, r)
	if !ok {
		return
	}
	var req models.ChangePasswordRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if err := auth.ValidatePassword(req.NewPassword); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}
	if _, ok := h.localAccount(w, r, userID); !ok {
		return
	}
	if err := h.security.VerifyCurrentPassword(r.Context(), userID, req.CurrentPassword); err != nil {
		h.writeCredentialFailure(w, r, userID, err)
		return
	}
	revoked, err := h.security.ChangePassword(r.Context(), userID, current, req.NewPassword)
	if err != nil {
		h.writeError(w, err, "change the password")
		return
	}
	h.recordAccount(r, "auth.password_change", userID, map[string]any{"revoked_sessions": revoked})
	httpx.WriteJSON(w, http.StatusOK, models.RevokeSessionsResponse{Revoked: revoked})
}

// ChangeEmail handles POST /api/v1/account/email: mails a confirmation link
// to the new address and a notice to the current one; the address changes
// only when the link is used. It needs email delivery.
func (h *Handlers) ChangeEmail(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := sessionCaller(w, r)
	if !ok {
		return
	}
	var req models.ChangeEmailRequest
	if !decodeBody(w, r, &req) {
		return
	}
	newEmail := strings.ToLower(strings.TrimSpace(req.NewEmail))
	if addr, err := mail.ParseRecipient(newEmail); err != nil || addr.Address != newEmail || len(newEmail) > 254 {
		apierrors.WriteValidationError(w, "enter a plain email address, like name@example.com")
		return
	}
	if _, ok := h.localAccount(w, r, userID); !ok {
		return
	}
	if !h.mail.Enabled() {
		apierrors.WriteError(w, http.StatusConflict, "email_unavailable",
			"this installation cannot send email, so a new address cannot be confirmed; ask a superadmin to change it")
		return
	}
	if err := h.security.VerifyCurrentPassword(r.Context(), userID, req.CurrentPassword); err != nil {
		h.writeCredentialFailure(w, r, userID, err)
		return
	}
	change, err := h.security.RequestEmailChange(r.Context(), userID, newEmail)
	if err != nil {
		h.writeError(w, err, "change the email address")
		return
	}
	if err := h.mail.ConfirmEmail(r.Context(), change.NewEmail, change.Account.Username, change.Token.Token, change.Token.ExpiresAt); err != nil {
		h.logger.WithError(err).Warn("Failed to send the email confirmation")
		apierrors.WriteError(w, http.StatusBadGateway, "delivery_failed", "the confirmation email could not be sent; try again later")
		return
	}
	if old := change.Account.Email; old != nil && *old != "" {
		to, username, next := *old, change.Account.Username, change.NewEmail
		h.mail.Background(h.logger, "email change notice", func(ctx context.Context) error {
			return h.mail.EmailChangeRequested(ctx, to, username, next)
		})
	}
	h.recordAccount(r, "auth.email_change_request", userID, map[string]any{"new_email": change.NewEmail})
	httpx.WriteJSON(w, http.StatusAccepted, models.EmailChangePendingResponse{PendingEmail: change.NewEmail, ExpiresAt: change.Token.ExpiresAt})
}

// StartTwoFactor handles POST /api/v1/account/two-factor/setup: re-checks
// the password and starts a pending enrolment, returned once (secret and
// otpauth:// URI). Nothing changes at sign-in until EnableTwoFactor.
func (h *Handlers) StartTwoFactor(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := sessionCaller(w, r)
	if !ok {
		return
	}
	var req models.PasswordConfirmRequest
	if !decodeBody(w, r, &req) {
		return
	}
	account, ok := h.localAccount(w, r, userID)
	if !ok {
		return
	}
	if !h.security.TwoFactorEnrolmentAvailable() {
		h.writeError(w, adminauth.ErrTwoFactorNeedsKey, "")
		return
	}
	if err := h.security.VerifyCurrentPassword(r.Context(), userID, req.CurrentPassword); err != nil {
		h.writeCredentialFailure(w, r, userID, err)
		return
	}
	secret, err := h.security.BeginTOTPEnrolment(r.Context(), userID)
	if err != nil {
		h.writeError(w, err, "start two-factor setup")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, models.TOTPSetupResponse{Secret: secret, OTPAuthURI: adminauth.TOTPURI(account.Username, secret)})
}

// EnableTwoFactor handles POST /api/v1/account/two-factor/enable: confirms
// the pending enrolment with a code and returns the recovery codes, once.
// Other sessions stay signed in; the account is mailed a notice.
func (h *Handlers) EnableTwoFactor(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := sessionCaller(w, r)
	if !ok {
		return
	}
	var req models.TOTPCodeRequest
	if !decodeBody(w, r, &req) {
		return
	}
	account, ok := h.localAccount(w, r, userID)
	if !ok {
		return
	}
	codes, err := h.security.EnableTOTP(r.Context(), userID, req.Code)
	if err != nil {
		h.writeError(w, err, "turn on two-factor sign-in")
		return
	}
	h.recordAccount(r, "auth.two_factor_enable", userID, nil)
	h.notify(account, "two-factor enabled notice", func(ctx context.Context, to string) error {
		return h.mail.TwoFactorEnabled(ctx, to, account.Username)
	})
	httpx.WriteJSON(w, http.StatusOK, models.RecoveryCodesResponse{RecoveryCodes: codes})
}

// DisableTwoFactor handles POST /api/v1/account/two-factor/disable: needs
// the password and a current code (or a recovery code). Every other session
// ends; the account is mailed a notice.
func (h *Handlers) DisableTwoFactor(w http.ResponseWriter, r *http.Request) {
	userID, current, ok := sessionCaller(w, r)
	if !ok {
		return
	}
	var req models.TwoFactorProofRequest
	if !decodeBody(w, r, &req) {
		return
	}
	account, ok := h.enrolledAccount(w, r, userID)
	if !ok {
		return
	}
	if _, err := h.security.VerifyCurrentCredentials(r.Context(), userID, req.CurrentPassword, req.Code, true); err != nil {
		h.writeCredentialFailure(w, r, userID, err)
		return
	}
	revoked, err := h.security.DisableTOTP(r.Context(), userID, current)
	if err != nil {
		h.writeError(w, err, "turn off two-factor sign-in")
		return
	}
	h.recordAccount(r, "auth.two_factor_disable", userID, map[string]any{"revoked_sessions": revoked})
	h.notify(account, "two-factor disabled notice", func(ctx context.Context, to string) error {
		return h.mail.TwoFactorDisabled(ctx, to, account.Username, false)
	})
	httpx.WriteJSON(w, http.StatusOK, models.RevokeSessionsResponse{Revoked: revoked})
}

// RegenerateRecoveryCodes handles POST
// /api/v1/account/two-factor/recovery-codes: replaces every recovery code
// (password and a current code required) and returns the new set, once.
func (h *Handlers) RegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := sessionCaller(w, r)
	if !ok {
		return
	}
	var req models.TwoFactorProofRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if _, ok := h.enrolledAccount(w, r, userID); !ok {
		return
	}
	if _, err := h.security.VerifyCurrentCredentials(r.Context(), userID, req.CurrentPassword, req.Code, true); err != nil {
		h.writeCredentialFailure(w, r, userID, err)
		return
	}
	codes, err := h.security.RegenerateRecoveryCodes(r.Context(), userID)
	if err != nil {
		h.writeError(w, err, "replace the recovery codes")
		return
	}
	h.recordAccount(r, "auth.recovery_codes_regenerate", userID, nil)
	httpx.WriteJSON(w, http.StatusOK, models.RecoveryCodesResponse{RecoveryCodes: codes})
}

// enrolledAccount loads the caller's account and refuses one without a
// second factor. An SSO-linked account that enrolled while it was a password
// account may still turn it off here.
func (h *Handlers) enrolledAccount(w http.ResponseWriter, r *http.Request, userID uuid.UUID) (*adminauth.Account, bool) {
	account, err := h.security.LoadAccount(r.Context(), userID)
	if err != nil {
		h.writeError(w, err, "load the account")
		return nil, false
	}
	status, err := h.security.GetTwoFactorStatus(r.Context(), userID)
	if err != nil {
		h.writeError(w, err, "load two-factor status")
		return nil, false
	}
	if !status.Enabled {
		h.writeError(w, adminauth.ErrTwoFactorDisabled, "")
		return nil, false
	}
	return account, true
}

// notify mails the account a security notice in the background, when
// account mail is configured and the account has an address.
func (h *Handlers) notify(account *adminauth.Account, what string, send func(ctx context.Context, to string) error) {
	if !h.mail.Enabled() || account.Email == nil || *account.Email == "" {
		return
	}
	to := *account.Email
	h.mail.Background(h.logger, what, func(ctx context.Context) error { return send(ctx, to) })
}

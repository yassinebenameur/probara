package auth

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/accountmail"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/auth"
)

// User lifecycle routes (B4b): invites, password reset, self-service
// password and email change, and the login page's capabilities.
//
//   - /auth/* here are public (the router-wide anonymous rate limiter covers
//     them by address). Link tokens travel in the request body, never the
//     path, and the UI puts them in the URL fragment so they stay out of
//     every log.
//   - /account/* are signed-in browser sessions only (sessionUser).
//   - /users/{id}/invite and /password-reset are superadmin actions.
//
// Every refusal of a public route is generic: one 400 "invalid or expired
// link", and the forgot-password route always answers 202.

// forgotWorkers bounds concurrent background reset lookups; past it a
// request is still answered 202 and simply dropped.
const forgotWorkers = 8

// forgotTimeout bounds one background reset (lookup plus SMTP).
const forgotTimeout = 30 * time.Second

// WithAccountMail attaches the account mailer (invites, resets, email
// change). Nil or disabled: superadmins get links to hand over instead.
func (h *Handlers) WithAccountMail(m *accountmail.Mailer) *Handlers {
	h.mail = m
	return h
}

func (h *Handlers) mailEnabled() bool {
	return h.mail.Enabled()
}

func (h *Handlers) oidcEnabled() bool {
	return h.oidc != nil && h.oidc.Enabled()
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(dst); err != nil {
		errors.WriteValidationError(w, "invalid request body")
		return false
	}
	return true
}

func writeInvalidToken(w http.ResponseWriter) {
	errors.WriteError(w, http.StatusBadRequest, "invalid_token", adminauth.ErrTokenInvalid.Error())
}

// writeLifecycleError maps the service errors shared by several routes.
func (h *Handlers) writeLifecycleError(w http.ResponseWriter, err error, what string) {
	switch {
	case stderrors.Is(err, adminauth.ErrTokenInvalid):
		writeInvalidToken(w)
	case stderrors.Is(err, adminauth.ErrTokensUnavailable), stderrors.Is(err, adminauth.ErrMFAUnavailable):
		errors.WriteError(w, http.StatusServiceUnavailable, "unavailable", err.Error())
	case stderrors.Is(err, adminauth.ErrNotLocalAccount), stderrors.Is(err, adminauth.ErrInviteSSOOnly), stderrors.Is(err, adminauth.ErrNoPassword):
		errors.WriteError(w, http.StatusConflict, "not_local_account", err.Error())
	case stderrors.Is(err, adminauth.ErrEmailTaken):
		errors.WriteError(w, http.StatusConflict, "conflict", "that email address is already in use")
	case stderrors.Is(err, adminauth.ErrSameEmail):
		errors.WriteValidationError(w, err.Error())
	case stderrors.Is(err, adminauth.ErrUserNotFound):
		errors.WriteNotFoundError(w, "user not found")
	default:
		h.logger.WithError(err).Error("Failed to " + what)
		errors.WriteInternalError(w, "failed to "+what)
	}
}

// writeCredentialFailure answers a wrong current password (or code) on a
// signed-in route: 400, never 401 (the UI would take a 401 for an expired
// session and retry it after a refresh, counting the attempt twice), and
// the shared 429 for a lockout.
func (h *Handlers) writeCredentialFailure(w http.ResponseWriter, r *http.Request, adminID uuid.UUID, err error) {
	var failure *adminauth.LoginFailure
	if !stderrors.As(err, &failure) {
		h.writeLifecycleError(w, err, "verify credentials")
		return
	}
	if failure.Locked {
		writeLoginThrottled(w, failure.RetryAfter)
		return
	}
	if failure.LockedNow {
		h.recordLockout(r, "", adminID, failure)
	}
	if failure.SecondFactor {
		errors.WriteError(w, http.StatusBadRequest, "invalid_code", "the authentication code is incorrect")
		return
	}
	errors.WriteError(w, http.StatusBadRequest, "invalid_password", "the current password is incorrect")
}

// recordUserEvent audits a lifecycle action on a user account.
func (h *Handlers) recordUserEvent(r *http.Request, action, outcome string, userID uuid.UUID, label string, details map[string]any) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.Action = action
	event.Outcome = outcome
	event.ResourceType = "user"
	event.ResourceID = userID.String()
	if event.ActorID == nil {
		// Public routes: the link holder acts as that account.
		event.ActorType = audit.ActorAdminUser
		id := userID
		event.ActorID = &id
		event.ActorLabel = label
	}
	event.MergeDetails(details)
	h.audit.Record(event)
}

// Capabilities handles GET /api/v1/auth/capabilities (public).
func (h *Handlers) Capabilities(w http.ResponseWriter, r *http.Request) {
	resp := models.AuthCapabilities{
		PasswordReset: h.mailEnabled(),
		EmailDelivery: h.mailEnabled(),
		OIDC:          h.oidcEnabled(),
	}
	if resp.OIDC {
		resp.OIDCLabel = h.oidc.ProviderLabel()
	}
	writeJSON(w, http.StatusOK, resp)
}

// ForgotPassword handles POST /api/v1/auth/password/forgot (public). It
// answers 202 at once in every case — known or unknown login, SSO account,
// no email, mail off, throttled — and does the lookup and the sending in
// the background, so neither the answer nor its timing says whether an
// account exists.
func (h *Handlers) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req models.ForgotPasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	login := strings.TrimSpace(req.Login)
	if login == "" || len(login) > 254 {
		errors.WriteValidationError(w, "enter your username or email address")
		return
	}
	if h.mailEnabled() {
		event := audit.FromRequest(r)
		select {
		case h.forgotSlots <- struct{}{}:
			go func() {
				defer func() { <-h.forgotSlots }()
				ctx, cancel := context.WithTimeout(context.Background(), forgotTimeout)
				defer cancel()
				h.sendPasswordReset(ctx, login, event)
			}()
		default:
			h.logger.Warn("Password reset request dropped: too many in flight")
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

// sendPasswordReset is ForgotPassword's background half.
func (h *Handlers) sendPasswordReset(ctx context.Context, login string, event audit.Event) {
	reset, err := h.service.RequestPasswordReset(ctx, login)
	if err != nil {
		h.logger.WithError(err).Error("Failed to issue password reset")
		return
	}
	if reset == nil {
		return
	}
	outcome := audit.OutcomeSuccess
	details := map[string]any{"delivered": true}
	if err := h.mail.PasswordReset(ctx, reset.Email, reset.Username, reset.Token.Token, reset.Token.ExpiresAt); err != nil {
		h.logger.WithError(err).Warn("Failed to send password reset email")
		outcome = audit.OutcomeFailure
		details["delivered"] = false
	}
	if h.audit != nil {
		event.Action = "auth.password_reset_request"
		event.Outcome = outcome
		event.ResourceType = "user"
		event.ResourceID = reset.UserID.String()
		event.ActorType = audit.ActorAnonymous
		event.ActorLabel = login
		event.MergeDetails(details)
		h.audit.Record(event)
	}
}

// ResetPassword handles POST /api/v1/auth/password/reset (public): sets the
// password from a reset link and signs the account out everywhere. The
// second factor stays as it was: a mailbox alone never bypasses 2FA.
func (h *Handlers) ResetPassword(w http.ResponseWriter, r *http.Request) {
	h.redeemPasswordLink(w, r, adminauth.PurposePasswordReset, "auth.password_reset")
}

// AcceptInvite handles POST /api/v1/auth/invite/accept (public): sets the
// invited account's password.
func (h *Handlers) AcceptInvite(w http.ResponseWriter, r *http.Request) {
	h.redeemPasswordLink(w, r, adminauth.PurposeInvite, "auth.invite_accept")
}

func (h *Handlers) redeemPasswordLink(w http.ResponseWriter, r *http.Request, purpose, action string) {
	var req models.TokenPasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		writeInvalidToken(w)
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}
	account, err := h.service.RedeemPasswordToken(r.Context(), strings.TrimSpace(req.Token), purpose, req.Password)
	if err != nil {
		h.writeLifecycleError(w, err, "set password")
		return
	}
	h.recordUserEvent(r, action, audit.OutcomeSuccess, account.ID, account.Username, nil)
	// Every session of the account just ended; this browser's too.
	h.clearCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

// InspectInvite handles POST /api/v1/auth/invite/inspect (public): whose
// account an invite link sets up, without using it.
func (h *Handlers) InspectInvite(w http.ResponseWriter, r *http.Request) {
	var req models.TokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	account, expires, err := h.service.InviteSubject(r.Context(), strings.TrimSpace(req.Token))
	if err != nil {
		h.writeLifecycleError(w, err, "read invite")
		return
	}
	writeJSON(w, http.StatusOK, models.InviteInfo{Username: account.Username, Email: account.Email, ExpiresAt: expires})
}

// ConfirmEmail handles POST /api/v1/auth/email/confirm (public): completes
// an email change from the link sent to the new address, and tells the old
// address.
func (h *Handlers) ConfirmEmail(w http.ResponseWriter, r *http.Request) {
	var req models.TokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	changed, err := h.service.ConfirmEmailChange(r.Context(), strings.TrimSpace(req.Token))
	if err != nil {
		h.writeLifecycleError(w, err, "change email")
		return
	}
	details := map[string]any{"new_email": changed.NewEmail}
	if changed.OldEmail != nil && *changed.OldEmail != "" {
		details["old_email"] = *changed.OldEmail
		if h.mailEnabled() {
			if err := h.mail.EmailChanged(r.Context(), *changed.OldEmail, changed.Account.Username, changed.NewEmail); err != nil {
				h.logger.WithError(err).Warn("Failed to notify the previous email address")
			}
		}
	}
	h.recordUserEvent(r, "auth.email_change", audit.OutcomeSuccess, changed.Account.ID, changed.Account.Username, details)
	writeJSON(w, http.StatusOK, models.EmailConfirmedResponse{Email: changed.NewEmail})
}

// ChangeMyPassword handles PATCH /api/v1/account/password. Local accounts
// only; the current password is checked under the login lockout, and every
// other session of the account ends.
func (h *Handlers) ChangeMyPassword(w http.ResponseWriter, r *http.Request) {
	adminID, current, ok := sessionUser(w, r)
	if !ok {
		return
	}
	var req models.ChangePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := auth.ValidatePassword(req.NewPassword); err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}
	if !h.requireLocal(w, r, adminID) {
		return
	}
	if err := h.service.VerifyPassword(r.Context(), adminID, req.CurrentPassword); err != nil {
		h.writeCredentialFailure(w, r, adminID, err)
		return
	}
	revoked, err := h.service.ChangePassword(r.Context(), adminID, current, req.NewPassword)
	if err != nil {
		h.writeLifecycleError(w, err, "change password")
		return
	}
	h.recordSessionEvent(r, "auth.password_change", adminID, map[string]any{"revoked_sessions": revoked})
	writeJSON(w, http.StatusOK, models.RevokeSessionsResponse{Revoked: revoked})
}

// RequestMyEmailChange handles POST /api/v1/account/email: mails a
// confirmation link to the new address; the email changes when it is used.
func (h *Handlers) RequestMyEmailChange(w http.ResponseWriter, r *http.Request) {
	adminID, _, ok := sessionUser(w, r)
	if !ok {
		return
	}
	var req models.ChangeEmailRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	newEmail := strings.ToLower(strings.TrimSpace(req.NewEmail))
	if !validEmail(newEmail) {
		errors.WriteValidationError(w, "enter a valid email address")
		return
	}
	if !h.requireLocal(w, r, adminID) {
		return
	}
	if !h.mailEnabled() {
		errors.WriteError(w, http.StatusServiceUnavailable, "email_unavailable",
			"email delivery is not configured on this installation, so the new address cannot be confirmed; ask a superadmin to change it")
		return
	}
	if err := h.service.VerifyPassword(r.Context(), adminID, req.CurrentPassword); err != nil {
		h.writeCredentialFailure(w, r, adminID, err)
		return
	}
	change, err := h.service.RequestEmailChange(r.Context(), adminID, newEmail)
	if err != nil {
		h.writeLifecycleError(w, err, "change email")
		return
	}
	if err := h.mail.ConfirmEmail(r.Context(), change.NewEmail, change.Account.Username, change.Token.Token, change.Token.ExpiresAt); err != nil {
		h.logger.WithError(err).Warn("Failed to send email confirmation")
		errors.WriteError(w, http.StatusBadGateway, "delivery_failed", "the confirmation email could not be sent; try again later")
		return
	}
	h.recordSessionEvent(r, "auth.email_change_request", adminID, map[string]any{"new_email": change.NewEmail})
	writeJSON(w, http.StatusAccepted, models.EmailChangePendingResponse{PendingEmail: change.NewEmail, ExpiresAt: change.Token.ExpiresAt})
}

// requireLocal refuses self-service identity changes for SSO accounts.
func (h *Handlers) requireLocal(w http.ResponseWriter, r *http.Request, adminID uuid.UUID) bool {
	account, err := h.service.LoadAccount(r.Context(), adminID)
	if err != nil {
		h.writeLifecycleError(w, err, "load account")
		return false
	}
	if !account.Local() {
		h.writeLifecycleError(w, adminauth.ErrNotLocalAccount, "")
		return false
	}
	return true
}

func validEmail(email string) bool {
	if email == "" || len(email) > 254 {
		return false
	}
	addr, err := netmail.ParseAddress(email)
	return err == nil && strings.EqualFold(addr.Address, email)
}

// SendInvite handles POST /api/v1/users/{id}/invite (superadmin). It mails
// the invite when it can; otherwise (no SMTP, no APP_BASE_URL, no email on
// the account) the response carries the link for manual delivery.
func (h *Handlers) SendInvite(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		errors.WriteValidationError(w, "invalid user ID")
		return
	}
	var req models.SendInviteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil && !stderrors.Is(err, io.EOF) {
		errors.WriteValidationError(w, "invalid request body")
		return
	}
	account, err := h.service.LoadAccount(r.Context(), userID)
	if err != nil {
		h.writeLifecycleError(w, err, "load user")
		return
	}
	if account.Disabled {
		errors.WriteError(w, http.StatusConflict, "conflict", "the account is disabled")
		return
	}
	method := req.Method
	switch method {
	case "":
		method = "password"
		if account.Linked || (!account.HasPassword && h.oidcEnabled()) {
			method = "sso"
		}
	case "password", "sso":
	default:
		errors.WriteValidationError(w, `method must be "password" or "sso"`)
		return
	}
	if method == "password" && account.Linked {
		h.writeLifecycleError(w, adminauth.ErrInviteSSOOnly, "")
		return
	}
	if method == "sso" && !h.oidcEnabled() {
		errors.WriteError(w, http.StatusConflict, "conflict", "single sign-on is not configured on this installation")
		return
	}

	resp := models.AccountLinkResponse{Method: method, Email: account.Email}
	actor := currentAdmin(r)
	if method == "sso" {
		resp.Path = accountmail.LoginPath
		resp.URL = h.mail.URL(accountmail.LoginPath)
		resp.Reason = h.deliver(r.Context(), account, func(ctx context.Context, to string) error {
			return h.mail.InviteSSO(ctx, to, account.Username, h.oidc.ProviderLabel())
		})
	} else {
		_, issued, err := h.service.IssueInvite(r.Context(), userID, actor)
		if err != nil {
			h.writeLifecycleError(w, err, "create invite")
			return
		}
		resp.ExpiresAt = &issued.ExpiresAt
		resp.Reason = h.deliver(r.Context(), account, func(ctx context.Context, to string) error {
			return h.mail.Invite(ctx, to, account.Username, issued.Token, issued.ExpiresAt)
		})
		if resp.Reason != "" {
			resp.Path = accountmail.LinkPath(accountmail.AcceptInvitePath, issued.Token)
			resp.URL = h.mail.URL(resp.Path)
		}
	}
	resp.Delivered = resp.Reason == ""
	if resp.Delivered && method == "password" {
		resp.Path, resp.URL = "", ""
	}
	h.recordSessionEvent(r, "auth.invite", userID, map[string]any{"method": method, "delivered": resp.Delivered})
	writeJSON(w, http.StatusOK, resp)
}

// SendPasswordReset handles POST /api/v1/users/{id}/password-reset
// (superadmin): a reset link for a local account, mailed when possible and
// otherwise returned for manual delivery.
func (h *Handlers) SendPasswordReset(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		errors.WriteValidationError(w, "invalid user ID")
		return
	}
	account, issued, err := h.service.IssuePasswordReset(r.Context(), userID, currentAdmin(r))
	if err != nil {
		h.writeLifecycleError(w, err, "create password reset")
		return
	}
	resp := models.AccountLinkResponse{Method: "password", Email: account.Email, ExpiresAt: &issued.ExpiresAt}
	resp.Reason = h.deliver(r.Context(), account, func(ctx context.Context, to string) error {
		return h.mail.PasswordReset(ctx, to, account.Username, issued.Token, issued.ExpiresAt)
	})
	resp.Delivered = resp.Reason == ""
	if !resp.Delivered {
		resp.Path = accountmail.LinkPath(accountmail.ResetPasswordPath, issued.Token)
		resp.URL = h.mail.URL(resp.Path)
	}
	h.recordSessionEvent(r, "auth.password_reset_issue", userID, map[string]any{"delivered": resp.Delivered})
	writeJSON(w, http.StatusOK, resp)
}

// deliver mails through send when it can and returns "" on success, or
// why the link has to be handed over instead.
func (h *Handlers) deliver(ctx context.Context, account *adminauth.Account, send func(ctx context.Context, to string) error) string {
	switch {
	case !h.mailEnabled():
		return "email delivery is not configured"
	case account.Email == nil || *account.Email == "":
		return "the account has no email address"
	}
	if err := send(ctx, *account.Email); err != nil {
		h.logger.WithError(err).Warn("Failed to send account email")
		return "the email could not be sent"
	}
	return ""
}

// currentAdmin is the signed-in admin making a request, if any.
func currentAdmin(r *http.Request) *uuid.UUID {
	raw, err := middleware.GetAdminID(r.Context())
	if err != nil {
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil
	}
	return &id
}

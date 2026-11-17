package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/accountmail"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/auth"
)

// Public account-link routes: forgot password, reset, invite acceptance and
// email confirmation. A link's token rides in the URL fragment of the
// operator-UI page and reaches the API in the X-Action-Token header, never a
// path or query string, so request logs and metric labels never hold it
// (the alert-action and report-unsubscribe pattern). The router-wide
// anonymous rate limiter covers these routes by address.

// TokenHeader carries a link's token from the page to the API.
const TokenHeader = "X-Action-Token"

// forgotWorkers bounds concurrent background forgot-password lookups; past
// it a request is still answered alike and simply dropped.
const forgotWorkers = 8

// WithAccountMail attaches the account mailer (reset links and security
// notices). Disabled without SMTP or APP_BASE_URL.
func (h *Handlers) WithAccountMail(m *accountmail.Mailer) *Handlers {
	h.mail = m
	return h
}

func linkToken(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get(TokenHeader))
}

func writeInvalidLink(w http.ResponseWriter) {
	apierrors.WriteError(w, http.StatusBadRequest, "invalid_token", adminauth.ErrTokenInvalid.Error())
}

// writeLinkError maps the service errors of the link routes.
func (h *Handlers) writeLinkError(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, adminauth.ErrTokenInvalid):
		writeInvalidLink(w)
	case errors.Is(err, adminauth.ErrTokensUnavailable):
		apierrors.WriteError(w, http.StatusServiceUnavailable, "unavailable", err.Error())
	case errors.Is(err, adminauth.ErrEmailTaken):
		apierrors.WriteError(w, http.StatusConflict, "email_taken", err.Error())
	default:
		h.logger.WithError(err).Error("Failed to " + what)
		apierrors.WriteInternalError(w, "failed to "+what)
	}
}

// recordAccountEvent audits an action a link holder took on an account: the
// account acts on itself.
func (h *Handlers) recordAccountEvent(r *http.Request, action string, account *adminauth.Account, details map[string]any) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.Action = action
	event.Outcome = audit.OutcomeSuccess
	event.ActorType = audit.ActorAdminUser
	id := account.ID
	event.ActorID = &id
	event.ActorLabel = account.Username
	event.ResourceType = "user"
	event.ResourceID = account.ID.String()
	event.MergeDetails(details)
	h.audit.Record(event)
}

// Capabilities handles GET /api/v1/auth/capabilities (public): whether the
// forgot-password form can mail links. It says nothing about any account.
func (h *Handlers) Capabilities(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, models.AuthCapabilities{EmailDelivery: h.mail.Enabled()})
}

// forgotAccepted is the one answer of the forgot-password route.
var forgotAccepted = map[string]string{"status": "accepted"}

// ForgotPassword handles POST /api/v1/auth/password/forgot (public). Known or
// unknown login, SSO-only, locked, disabled or password-less account, no
// address on file, mail off, hourly cap reached: every case answers 202 with
// the same body at once, and the lookup and the mail happen in the
// background, so neither the answer nor its timing tells whether an account
// exists. Without email delivery nothing is sent and the page tells the user
// to ask an administrator.
func (h *Handlers) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req models.ForgotPasswordRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body")
		return
	}
	login := strings.TrimSpace(req.Login)
	if login == "" || len(login) > 254 {
		apierrors.WriteValidationError(w, "enter your username or email address")
		return
	}
	if h.mail.Enabled() {
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
	httpx.WriteJSON(w, http.StatusAccepted, forgotAccepted)
}

// forgotTimeout bounds one background reset: the lookup and the mail (the
// mailer's own bound is 30 s).
const forgotTimeout = 40 * time.Second

// sendPasswordReset is ForgotPassword's background half.
func (h *Handlers) sendPasswordReset(ctx context.Context, login string, event audit.Event) {
	link, err := h.service.RequestPasswordReset(ctx, login)
	if err != nil {
		h.logger.WithError(err).Error("Failed to issue password reset")
		return
	}
	if link == nil {
		return
	}
	outcome := audit.OutcomeSuccess
	details := map[string]any{"delivered": true}
	if err := h.mail.PasswordReset(ctx, *link.Account.Email, link.Account.Username, link.Token.Token, link.Token.ExpiresAt); err != nil {
		h.logger.WithError(err).Warn("Failed to send password reset email")
		outcome = audit.OutcomeFailure
		details["delivered"] = false
	}
	if h.audit != nil {
		event.Action = "auth.password_reset_request"
		event.Outcome = outcome
		event.ActorLabel = login
		event.ResourceType = "user"
		event.ResourceID = link.Account.ID.String()
		event.MergeDetails(details)
		h.audit.Record(event)
	}
}

// ResetPassword handles POST /api/v1/auth/password/reset (public): sets the
// password through a reset link and signs the account out everywhere. The
// second factor stays on, and a two-factor account's lockout stays as it
// was: a mailbox alone never gets past the second factor.
func (h *Handlers) ResetPassword(w http.ResponseWriter, r *http.Request) {
	h.redeemPasswordLink(w, r, adminauth.PurposePasswordReset, "auth.password_reset")
}

// AcceptInvite handles POST /api/v1/auth/invite/accept (public): sets the
// invited account's first password.
func (h *Handlers) AcceptInvite(w http.ResponseWriter, r *http.Request) {
	h.redeemPasswordLink(w, r, adminauth.PurposeInvite, "auth.invite_accept")
}

func (h *Handlers) redeemPasswordLink(w http.ResponseWriter, r *http.Request, purpose, action string) {
	token := linkToken(r)
	if token == "" {
		writeInvalidLink(w)
		return
	}
	var req models.SetPasswordRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body")
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}
	account, err := h.service.RedeemPasswordToken(r.Context(), token, purpose, req.Password)
	if err != nil {
		h.writeLinkError(w, err, "set the password")
		return
	}
	h.recordAccountEvent(r, action, account, nil)
	w.WriteHeader(http.StatusNoContent)
}

// InviteInfo handles GET /api/v1/auth/invite (public): the username an
// invite link sets up, without using the link.
func (h *Handlers) InviteInfo(w http.ResponseWriter, r *http.Request) {
	account, expires, err := h.service.InviteSubject(r.Context(), linkToken(r))
	if err != nil {
		h.writeLinkError(w, err, "read the invite")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, models.InviteInfo{Username: account.Username, ExpiresAt: expires})
}

// ConfirmEmail handles POST /api/v1/auth/email/confirm (public): completes an
// email change from the link mailed to the new address. The page posts it
// from a button, never on load, because mail scanners open links.
func (h *Handlers) ConfirmEmail(w http.ResponseWriter, r *http.Request) {
	changed, err := h.service.ConfirmEmailChange(r.Context(), linkToken(r))
	if err != nil {
		h.writeLinkError(w, err, "change the email address")
		return
	}
	details := map[string]any{"new_email": changed.NewEmail}
	if changed.OldEmail != nil {
		details["old_email"] = *changed.OldEmail
	}
	h.recordAccountEvent(r, "auth.email_change", changed.Account, details)
	httpx.WriteJSON(w, http.StatusOK, models.EmailConfirmedResponse{Email: changed.NewEmail})
}

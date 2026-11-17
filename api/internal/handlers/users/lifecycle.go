package users

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/accountmail"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// A superadmin's account actions for another user: send an invite, generate
// a password reset link, reset two-factor sign-in (lost device). With email
// delivery (SMTP and APP_BASE_URL) a link is mailed to the account and never
// returned; without it, or when the account has no address, the response
// carries the link once for the superadmin to hand over. Links are never
// logged or audited.

// accountLinks is the adminauth side of these actions.
type accountLinks interface {
	LoadAccount(ctx context.Context, userID uuid.UUID) (*adminauth.Account, error)
	IssueInvite(ctx context.Context, userID uuid.UUID, createdBy *uuid.UUID) (*adminauth.AccountLink, error)
	IssuePasswordReset(ctx context.Context, userID uuid.UUID, createdBy *uuid.UUID) (*adminauth.AccountLink, error)
	ResetTwoFactor(ctx context.Context, userID uuid.UUID) (bool, error)
}

// ssoProvider is what an SSO invite needs to know about single sign-on.
type ssoProvider interface {
	Enabled() bool
	ProviderLabel() string
}

// Invite methods.
const (
	inviteMethodPassword = "password"
	inviteMethodSSO      = "sso"
)

// WithAccountLinks attaches invites, reset links and the two-factor reset.
func (h *Handlers) WithAccountLinks(svc accountLinks, m *accountmail.Mailer, sso ssoProvider) *Handlers {
	h.links = svc
	h.mail = m
	h.sso = sso
	return h
}

func (h *Handlers) ssoEnabled() bool {
	return h.sso != nil && h.sso.Enabled()
}

// writeLinkError maps the account-action errors.
func (h *Handlers) writeLinkError(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, adminauth.ErrUserNotFound):
		apierrors.WriteNotFoundError(w, "user not found")
	case errors.Is(err, adminauth.ErrAccountDisabled), errors.Is(err, adminauth.ErrNotLocalAccount),
		errors.Is(err, adminauth.ErrHasPassword), errors.Is(err, adminauth.ErrNoPassword):
		apierrors.WriteError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, adminauth.ErrTokensUnavailable), errors.Is(err, adminauth.ErrTwoFactorUnavailable):
		apierrors.WriteError(w, http.StatusServiceUnavailable, "unavailable", err.Error())
	default:
		h.logger.WithError(err).Error("Failed to " + what)
		apierrors.WriteInternalError(w, "failed to "+what)
	}
}

// actingAdmin is the superadmin making the request.
func actingAdmin(r *http.Request) *uuid.UUID {
	raw, ok := ctxpkg.GetAdminID(r.Context())
	if !ok {
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil
	}
	return &id
}

// errDeliveryFailed: email delivery is configured but the relay refused.
var errDeliveryFailed = errors.New("the email could not be sent; try again later")

// deliver mails through send when account mail can reach the account. It
// returns "" when mailed, or why the link must be handed over instead; a
// send that fails with delivery configured is errDeliveryFailed (the link is
// not returned then: a configured relay is the channel).
func (h *Handlers) deliver(ctx context.Context, account *adminauth.Account, send func(ctx context.Context, to string) error) (string, error) {
	switch {
	case !h.mail.Enabled():
		return "email delivery is not configured (SMTP and APP_BASE_URL)", nil
	case account.Email == nil || *account.Email == "":
		return "the account has no email address", nil
	}
	if err := send(ctx, *account.Email); err != nil {
		h.logger.WithError(err).Warn("Failed to send account email")
		return "", errDeliveryFailed
	}
	return "", nil
}

func writeDeliveryFailed(w http.ResponseWriter) {
	apierrors.WriteError(w, http.StatusBadGateway, "delivery_failed", errDeliveryFailed.Error())
}

// SendInvite handles POST /api/v1/users/{id}/invite. "password" mails a
// single-use set-your-password link (7 days; a newer invite replaces it) to
// an account with no password; "sso" mails a note to sign in with single
// sign-on and sets nothing. Without email delivery the response carries the
// link to hand over.
func (h *Handlers) SendInvite(w http.ResponseWriter, r *http.Request) {
	user, ok := h.targetUser(w, r)
	if !ok {
		return
	}
	var req models.SendInviteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		apierrors.WriteValidationError(w, "invalid request body")
		return
	}
	account, err := h.links.LoadAccount(r.Context(), user.ID)
	if err != nil {
		h.writeLinkError(w, err, "load the user")
		return
	}
	if account.Disabled {
		h.writeLinkError(w, adminauth.ErrAccountDisabled, "")
		return
	}
	method := req.Method
	switch method {
	case "":
		method = inviteMethodPassword
		if account.Linked {
			method = inviteMethodSSO
		}
	case inviteMethodPassword, inviteMethodSSO:
	default:
		apierrors.WriteValidationError(w, `method must be "password" or "sso"`)
		return
	}
	if method == inviteMethodSSO && !h.ssoEnabled() {
		apierrors.WriteError(w, http.StatusConflict, "conflict", "single sign-on is not configured on this installation")
		return
	}

	resp := models.AccountLinkResponse{Method: method, Email: account.Email}
	if method == inviteMethodSSO {
		reason, err := h.deliver(r.Context(), account, func(ctx context.Context, to string) error {
			return h.mail.InviteSSO(ctx, to, account.Username, h.sso.ProviderLabel())
		})
		if err != nil {
			writeDeliveryFailed(w)
			return
		}
		resp.Reason = reason
		if reason != "" {
			// Not a secret: the sign-in page, for the superadmin to pass on.
			resp.Link = h.mail.URL(accountmail.LoginPath)
		}
	} else {
		link, err := h.links.IssueInvite(r.Context(), user.ID, actingAdmin(r))
		if err != nil {
			h.writeLinkError(w, err, "create the invite")
			return
		}
		resp.ExpiresAt = &link.Token.ExpiresAt
		reason, err := h.deliver(r.Context(), account, func(ctx context.Context, to string) error {
			return h.mail.Invite(ctx, to, account.Username, link.Token.Token, link.Token.ExpiresAt)
		})
		if err != nil {
			writeDeliveryFailed(w)
			return
		}
		resp.Reason = reason
		if reason != "" {
			resp.Link = h.mail.URL(accountmail.LinkPath(accountmail.AcceptInvitePath, link.Token.Token))
		}
	}
	resp.Delivered = resp.Reason == ""
	h.recordUserEvent(r, "user.invite", audit.OutcomeSuccess, user.ID.String(), map[string]any{
		"username":  user.Username,
		"method":    method,
		"delivered": resp.Delivered,
	})
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// GeneratePasswordReset handles POST /api/v1/users/{id}/password-reset: a
// single-use reset link (1 hour; a newer one replaces it) for a password
// account, mailed when possible and otherwise returned to hand over.
func (h *Handlers) GeneratePasswordReset(w http.ResponseWriter, r *http.Request) {
	user, ok := h.targetUser(w, r)
	if !ok {
		return
	}
	link, err := h.links.IssuePasswordReset(r.Context(), user.ID, actingAdmin(r))
	if err != nil {
		h.writeLinkError(w, err, "create the password reset link")
		return
	}
	resp := models.AccountLinkResponse{Method: inviteMethodPassword, Email: link.Account.Email, ExpiresAt: &link.Token.ExpiresAt}
	reason, err := h.deliver(r.Context(), link.Account, func(ctx context.Context, to string) error {
		return h.mail.PasswordReset(ctx, to, link.Account.Username, link.Token.Token, link.Token.ExpiresAt)
	})
	if err != nil {
		writeDeliveryFailed(w)
		return
	}
	resp.Reason = reason
	resp.Delivered = reason == ""
	if !resp.Delivered {
		resp.Link = h.mail.URL(accountmail.LinkPath(accountmail.ResetPasswordPath, link.Token.Token))
	}
	h.recordUserEvent(r, "user.password_reset_link", audit.OutcomeSuccess, user.ID.String(), map[string]any{
		"username":  user.Username,
		"delivered": resp.Delivered,
	})
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// ResetTwoFactor handles POST /api/v1/users/{id}/reset-2fa: turns a user's
// two-factor sign-in off (lost device), so they sign in with the password
// alone and can enrol again. It also cancels a pending email change. The
// user is mailed a notice; sessions are left alone.
func (h *Handlers) ResetTwoFactor(w http.ResponseWriter, r *http.Request) {
	user, ok := h.targetUser(w, r)
	if !ok {
		return
	}
	was, err := h.links.ResetTwoFactor(r.Context(), user.ID)
	if err != nil {
		h.writeLinkError(w, err, "reset two-factor sign-in")
		return
	}
	h.recordUserEvent(r, "user.two_factor_reset", audit.OutcomeSuccess, user.ID.String(), map[string]any{
		"username":    user.Username,
		"was_enabled": was,
	})
	if was && h.mail.Enabled() && user.Email != nil && *user.Email != "" {
		to, username := *user.Email, user.Username
		h.mail.Background(h.logger, "two-factor reset notice", func(ctx context.Context) error {
			return h.mail.TwoFactorDisabled(ctx, to, username, true)
		})
	}
	httpx.WriteJSON(w, http.StatusOK, models.ResetTwoFactorResponse{WasEnabled: was})
}

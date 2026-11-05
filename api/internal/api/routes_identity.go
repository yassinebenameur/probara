package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	authhandlers "github.com/yassinebenameur/probara/api/internal/handlers/auth"
	"github.com/yassinebenameur/probara/api/internal/services/accountmail"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/email"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// identityMailerOverride replaces the platform mailer for account mail in
// tests.
var identityMailerOverride mail.Sender

// platformMailer is the API's transactional mailer (SMTP_*), or nil when
// SMTP is not configured.
func platformMailer(cfg *config.APIConfig, log *logger.Logger) mail.Sender {
	if cfg.SMTPHost == "" || cfg.SMTPFrom == "" {
		return nil
	}
	m, err := email.NewSMTPMailer(email.SMTPParams{
		Host:       cfg.SMTPHost,
		Port:       cfg.SMTPPort,
		Username:   cfg.SMTPUsername,
		Password:   cfg.SMTPPassword,
		From:       cfg.SMTPFrom,
		FromName:   cfg.SMTPFromName,
		UseTLS:     cfg.SMTPUseTLS,
		AppBaseURL: cfg.AppBaseURL,
	})
	if err != nil {
		log.WithError(err).Warn("Failed to configure SMTP mailer")
		return nil
	}
	return m
}

// configureIdentity wires the user-lifecycle dependencies into the auth
// handlers: TOTP secrets sealed with the PROBARA_SECRETS_KEY encryptor, and
// account mail (invites, resets, email change) through the platform mailer
// with links on APP_BASE_URL. Without SMTP or APP_BASE_URL, "Forgot
// password" is hidden and superadmins get invite and reset links to hand
// over instead.
func configureIdentity(h *authhandlers.Handlers, svc interface{ ConfigureEncryption(secrets.Encryptor) }, enc secrets.Encryptor, cfg *config.APIConfig, log *logger.Logger) {
	svc.ConfigureEncryption(enc)
	sender := identityMailerOverride
	if sender == nil {
		sender = platformMailer(cfg, log)
	}
	h.WithAccountMail(accountmail.New(sender, cfg.AppBaseURL))
}

// identityAuthRoutes adds the public lifecycle routes to the /auth
// subrouter: no session, covered by the router-wide anonymous rate limiter
// (they are not behind Authenticate, so the router-derived classifier
// charges them per address). Link tokens travel in the body, never the
// path. The handlers record their own audit events.
func identityAuthRoutes(r chi.Router, h *authhandlers.Handlers) {
	r.Get("/capabilities", h.Capabilities)
	r.Post("/login/mfa", h.LoginMFA)
	r.Post("/password/forgot", h.ForgotPassword)
	r.Post("/password/reset", h.ResetPassword)
	r.Post("/invite/inspect", h.InspectInvite)
	r.Post("/invite/accept", h.AcceptInvite)
	r.Post("/email/confirm", h.ConfirmEmail)
}

// identityAccountRoutes mounts the signed-in user's own account routes. A
// browser session only (the handlers refuse API keys and MCP tokens), and
// outside the write gate and generic audit like the MCP connection routes:
// every role may end its own sessions and manage its own password, email
// and second factor, and the handlers record their own audit events.
func identityAccountRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, h *authhandlers.Handlers) {
	r.Route("/account", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/sessions", h.ListMySessions)
		r.Post("/sessions/revoke-others", h.RevokeMyOtherSessions)
		r.Post("/sessions/revoke-all", h.RevokeAllMySessions)
		r.Delete("/sessions/{id}", h.RevokeMySession)
		r.Patch("/password", h.ChangeMyPassword)
		r.Post("/email", h.RequestMyEmailChange)
		r.Get("/2fa", h.GetMyMFA)
		r.Post("/2fa/setup", h.SetupMyTOTP)
		r.Post("/2fa/enable", h.EnableMyTOTP)
		r.Post("/2fa/disable", h.DisableMyTOTP)
		r.Post("/2fa/recovery-codes", h.RegenerateMyRecoveryCodes)
	})
}

// identityUserRoutes adds the superadmin identity actions to the /users
// subrouter (already behind RequireSuperadmin and outside generic audit;
// the handlers record auth.unlock, auth.sessions_revoke, auth.invite,
// auth.password_reset_issue and auth.mfa_reset).
func identityUserRoutes(r chi.Router, h *authhandlers.Handlers) {
	r.Post("/{id}/unlock", h.UnlockUser)
	r.Delete("/{id}/sessions", h.RevokeUserSessions)
	r.Post("/{id}/invite", h.SendInvite)
	r.Post("/{id}/password-reset", h.SendPasswordReset)
	r.Post("/{id}/reset-2fa", h.ResetUserMFA)
}

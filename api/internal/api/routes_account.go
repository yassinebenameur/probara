// Routes for the signed-in user's own account (sessions, password, email,
// two-factor) and the wiring of account links and two-factor sign-in.

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	accounthandlers "github.com/yassinebenameur/probara/api/internal/handlers/account"
	authhandlers "github.com/yassinebenameur/probara/api/internal/handlers/auth"
	userhandlers "github.com/yassinebenameur/probara/api/internal/handlers/users"
	"github.com/yassinebenameur/probara/api/internal/services/accountmail"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/oidcauth"
	"github.com/yassinebenameur/probara/shared/bootstrap"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// wireAccountSecurity sets up invites, password resets, email changes and
// two-factor sign-in: the account mailer (SMTP and APP_BASE_URL; without
// them superadmins get copyable links and the forgot-password form sends
// nothing), the TOTP secret encryptor (only a real PROBARA_SECRETS_KEY, never
// the no-op fallback, so secrets are never stored in plaintext), and the
// handlers that use them. It returns the mailer for the account routes.
func wireAccountSecurity(cfg *config.APIConfig, log *logger.Logger, authService *adminauth.Service, enc secrets.Encryptor, keyConfigured bool,
	authHandlers *authhandlers.Handlers, userHandlers *userhandlers.Handlers, oidc *oidcauth.Service) *accountmail.Mailer {
	sender, err := bootstrap.MailSender(cfg.SMTPConfig)
	if err != nil {
		log.WithError(err).Warn("SMTP misconfigured; account emails are off")
	}
	mailer := accountmail.New(sender, cfg.AppBaseURL)
	if keyConfigured {
		authService.ConfigureEncryption(enc)
	}
	authHandlers.WithAccountMail(mailer)
	userHandlers.WithAccountLinks(authService, mailer, oidc)
	return mailer
}

// mountAccountRoutes mounts /account: a signed-in user session only (the
// handlers refuse API keys and MCP tokens), outside the write gate -- a
// viewer manages their own account -- and outside generic audit (the
// handlers record their own events). Users are platform-scoped, so no tenant.
func mountAccountRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, sessions *adminauth.Service, mailer *accountmail.Mailer, log *logger.Logger, recorder *audit.Recorder) {
	h := accounthandlers.NewHandlers(sessions, log, recorder).WithSecurity(sessions, mailer)
	r.Route("/account", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/sessions", h.ListSessions)
		r.Delete("/sessions/{id}", h.RevokeSession)
		r.Post("/sessions/revoke-others", h.RevokeOtherSessions)
		r.Get("/security", h.Security)
		r.Post("/password", h.ChangePassword)
		r.Post("/email", h.ChangeEmail)
		r.Post("/two-factor/setup", h.StartTwoFactor)
		r.Post("/two-factor/enable", h.EnableTwoFactor)
		r.Post("/two-factor/disable", h.DisableTwoFactor)
		r.Post("/two-factor/recovery-codes", h.RegenerateRecoveryCodes)
	})
}

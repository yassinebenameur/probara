package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	slackhandlers "github.com/yassinebenameur/probara/api/internal/handlers/slackapp"
	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	alertchannelservice "github.com/yassinebenameur/probara/api/internal/services/alertchannels"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	auditservice "github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	slackplugin "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/slackapp"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// slackRoutes mounts the Slack app integration (SLACK_*,
// api/internal/handlers/slackapp) under /api/v1/integrations/slack:
//   - GET /install: tenant admins only, a browser session (it signs the
//     OAuth state with the user and workspace). A GET that only redirects,
//     so generic audit is not involved; the callback records the install.
//   - GET /callback and POST /interactions: public. Slack's redirect and
//     Slack's signed button presses carry no Probara credential; the signed
//     state (plus the browser's nonce cookie) and the X-Slack-Signature
//     authorize them. They are outside the protected group, audit their own
//     events and are charged to the client address by the router-wide
//     anonymous rate limiter (RateLimiter.Anonymous, routes_edge.go).
//
// All three share one mount: a second /integrations/slack mount would shadow
// the other.
func slackRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, dbClient *db.Client, enc secrets.Encryptor, hub *alertservice.Hub, recorder *auditservice.Recorder, cfg *config.APIConfig, log *logger.Logger) {
	h := slackhandlers.NewHandlers(slackhandlers.Config{
		ClientID:      cfg.Slack.ClientID,
		ClientSecret:  cfg.Slack.ClientSecret,
		SigningSecret: cfg.Slack.SigningSecret,
		PublicBaseURL: cfg.PublicBaseURL,
		AppBaseURL:    cfg.AppBaseURL,
		CookieSecure:  cfg.AdminCookieSecure,
	}, slackhandlers.Deps{
		DB:        dbClient,
		Installer: slackplugin.NewOAuthClient(),
		Replier:   slackplugin.NewResponder(),
		Alerts:    alertservice.NewService(dbClient, nil),
		Channels:  alertchannelservice.NewService(dbClient, enc),
		Hub:       hub,
		Audit:     recorder,
		Log:       log,
	})
	if cfg.Slack.InstallEnabled() && cfg.PublicBaseURL == "" {
		log.Warn("SLACK_CLIENT_ID is set but PUBLIC_BASE_URL is empty: Add to Slack stays off (the OAuth redirect URL is PUBLIC_BASE_URL" + slackhandlers.CallbackPath + ")")
	}
	r.Route("/integrations/slack", func(r chi.Router) {
		r.Get("/callback", h.Callback)
		r.Post("/interactions", h.Interactions)
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Use(apimiddleware.RequireTenantAdmin)
			r.Get("/install", h.Install)
		})
	})
}

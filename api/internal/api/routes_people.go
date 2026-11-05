package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	contacthandlers "github.com/yassinebenameur/probara/api/internal/handlers/contacts"
	oncallhandlers "github.com/yassinebenameur/probara/api/internal/handlers/oncall"
	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	auditservice "github.com/yassinebenameur/probara/api/internal/services/audit"
	contactservice "github.com/yassinebenameur/probara/api/internal/services/contacts"
	oncallservice "github.com/yassinebenameur/probara/api/internal/services/oncall"
	"github.com/yassinebenameur/probara/shared/config"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/email"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// peopleDeps are the people layer's services: verified contacts, the
// workspace's personal SMS channel and on-call schedules.
type peopleDeps struct {
	contacts *contactservice.Service
	oncall   *oncallservice.Service
	audit    *auditservice.Recorder
	log      *logger.Logger
}

// newPeopleDeps builds the people services. Email contact codes go through
// the platform mailer (SMTP_*); without it email contacts cannot be verified.
func newPeopleDeps(dbClient *shareddb.Client, enc secrets.Encryptor, cfg *config.APIConfig, recorder *auditservice.Recorder, log *logger.Logger) *peopleDeps {
	var mailer mail.Sender
	if cfg.SMTPHost != "" && cfg.SMTPFrom != "" {
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
			log.WithError(err).Warn("Failed to configure SMTP mailer; email contacts cannot be verified")
		} else {
			mailer = m
		}
	}
	return &peopleDeps{
		contacts: contactservice.NewService(dbClient, enc, mailer),
		oncall:   oncallservice.NewService(dbClient, cfg.PublicBaseURL),
		audit:    recorder,
		log:      log,
	}
}

// peopleOpenRoutes mounts the people routes outside the protected group:
//   - the signed-in user's contacts under /account/contacts (a browser
//     session only, outside the write gate like the session routes, audited
//     here since the generic audit middleware is not on this path);
//   - the public on-call iCal feed. No session: the unguessable token in the
//     path is the credential (stored hashed, revocable), the route is
//     read-only, and the router-wide anonymous rate limiter
//     (RateLimiter.Anonymous) covers it. It reveals a schedule's name and
//     its members' usernames and shifts, nothing else.
func peopleOpenRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, d *peopleDeps) {
	h := &contacthandlers.Handlers{Service: d.contacts, Log: d.log}
	r.Route("/account/contacts", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Use(apimiddleware.AuditMutations(d.audit))
		h.AccountRoutes(r)
	})
	feeds := &oncallhandlers.Handlers{Service: d.oncall, Log: d.log}
	r.Get("/oncall-feeds/{token}.ics", feeds.Feed)
}

// peopleRoutes mounts the tenant-scoped people routes inside the protected
// group (auth, generic audit, write gate).
func peopleRoutes(r chi.Router, d *peopleDeps) {
	h := &contacthandlers.Handlers{Service: d.contacts, Log: d.log}
	r.Route("/people-settings", h.SettingsRoutes)
	schedules := &oncallhandlers.Handlers{Service: d.oncall, Log: d.log}
	r.Route("/oncall-schedules", schedules.Routes)
}

package api

import (
	"github.com/go-chi/chi/v5"

	subscriberhandlers "github.com/yassinebenameur/probara/api/internal/handlers/statuspagesubscribers"
	auditservice "github.com/yassinebenameur/probara/api/internal/services/audit"
	pagesubscriberservice "github.com/yassinebenameur/probara/api/internal/services/pagesubscribers"
	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/bootstrap"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

// newStatusPageSubscribers wires status-page email subscribers: their
// routes and the background runner that mails them, publishes outages to
// pages that opted in and announces starting maintenance. The API runs it
// because it alone writes subscribers and has the mailer; every replica
// runs one (SKIP LOCKED claims keep them apart). Without SMTP, APP_BASE_URL,
// STATUS_PAGE_BASE_URL, PROBARA_SECRETS_KEY or STATUS_PAGE_INTERNAL_TOKEN no
// page can turn subscriptions on; automatic publishing runs regardless.
func newStatusPageSubscribers(cfg *config.APIConfig, dbClient *db.Client, recorder *auditservice.Recorder, publisher *statusupdates.Publisher, log *logger.Logger) (*subscriberhandlers.Handlers, *pagesubscriberservice.Runner) {
	mailer, err := bootstrap.MailSender(cfg.SMTPConfig)
	if err != nil {
		log.WithError(err).Warn("SMTP misconfigured; status page subscriber mail is off")
	}
	links, err := actionlink.FromEnv()
	if err != nil {
		// NewServer already refuses a malformed PROBARA_SECRETS_KEY.
		log.WithError(err).Error("Status page subscription links disabled: invalid PROBARA_SECRETS_KEY")
	}
	pc := pagesubscriberservice.Config{
		Mailer: mailer, Links: links, AppBaseURL: cfg.AppBaseURL, StatusPageBaseURL: cfg.StatusPagePublicBaseURL,
		InternalToken: cfg.StatusPageInternalToken, Log: log,
	}
	if recorder != nil {
		pc.Audit = recorder
	}
	if publisher != nil {
		pc.Publisher = publisher
	}
	svc := pagesubscriberservice.NewService(dbClient, pc)
	h := &subscriberhandlers.Handlers{Service: svc, Log: log}
	if recorder != nil {
		h.Audit = recorder
	}
	return h, pagesubscriberservice.NewRunner(svc)
}

// mountStatusPageSubscriberPublicRoutes mounts the visitor routes the
// status-page service forwards to, and the one-click unsubscribe target of
// subscriber mail. Outside the protected group and so outside generic
// audit; confirm and unsubscribe record their own events.
func mountStatusPageSubscriberPublicRoutes(r chi.Router, h *subscriberhandlers.Handlers) {
	r.Route("/public", h.PublicRoutes)
}

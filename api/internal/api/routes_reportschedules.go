package api

import (
	"github.com/go-chi/chi/v5"

	reportschedulehandlers "github.com/yassinebenameur/probara/api/internal/handlers/reportschedules"
	auditservice "github.com/yassinebenameur/probara/api/internal/services/audit"
	reportscheduleservice "github.com/yassinebenameur/probara/api/internal/services/reportschedules"
	slaservice "github.com/yassinebenameur/probara/api/internal/services/slas"
	"github.com/yassinebenameur/probara/shared/actionlink"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/bootstrap"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
)

// newReportSchedules wires emailed SLA report schedules: their routes and
// the background runner that issues and mails each closed period. The API
// runs it because it owns report issuing and has the mailer; every replica
// runs one (SKIP LOCKED claims keep them apart). Without SMTP, APP_BASE_URL
// or PROBARA_SECRETS_KEY no schedule can be enabled and the runner stays
// off.
func newReportSchedules(cfg *config.APIConfig, dbClient *db.Client, recorder *auditservice.Recorder, log *logger.Logger) (*reportschedulehandlers.Handlers, *reportscheduleservice.Runner) {
	mailer, err := bootstrap.MailSender(cfg.SMTPConfig)
	if err != nil {
		log.WithError(err).Warn("SMTP misconfigured; scheduled SLA reports are off")
	}
	links, err := actionlink.FromEnv()
	if err != nil {
		// NewServer already refuses a malformed PROBARA_SECRETS_KEY.
		log.WithError(err).Error("Report unsubscribe links disabled: invalid PROBARA_SECRETS_KEY")
	}
	reports := slaservice.NewService(dbClient, sharedanalytics.NewRepository(dbClient), cfg.LocalAI.Disabled || !cfg.AITriageEnabled)
	svc := reportscheduleservice.NewService(dbClient, reports, reportscheduleservice.Config{
		Mailer: mailer, Links: links, AppBaseURL: cfg.AppBaseURL, Audit: recorder, Log: log,
	})
	return &reportschedulehandlers.Handlers{Service: svc, Log: log}, reportscheduleservice.NewRunner(svc)
}

// mountReportUnsubscribeRoutes mounts the public unsubscribe link of report
// emails. Outside the protected group and so outside generic audit; the
// POST records its own sla-report-schedule.unsubscribe event.
func mountReportUnsubscribeRoutes(r chi.Router, h *reportschedulehandlers.Handlers) {
	r.Route("/report-unsubscribe", h.PublicRoutes)
}

// mountReportScheduleRoutes mounts an SLA's schedule, keyed by SLA ID.
func mountReportScheduleRoutes(r chi.Router, h *reportschedulehandlers.Handlers) {
	r.Route("/sla-report-schedules", h.Routes)
}

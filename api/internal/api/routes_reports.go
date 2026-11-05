package api

import (
	"github.com/go-chi/chi/v5"

	reportschedulehandlers "github.com/yassinebenameur/probara/api/internal/handlers/reportschedules"
	auditservice "github.com/yassinebenameur/probara/api/internal/services/audit"
	reportscheduleservice "github.com/yassinebenameur/probara/api/internal/services/reportschedules"
	slaservice "github.com/yassinebenameur/probara/api/internal/services/slas"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/config"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/email"
)

// reportDeps are the scheduled SLA report services: the schedules, their
// background runner and the handlers both route groups share.
type reportDeps struct {
	handlers *reportschedulehandlers.Handlers
	runner   *reportscheduleservice.Runner
}

// newReportDeps builds the report schedule service. Reports go through the
// platform mailer (SMTP_*) and link to APP_BASE_URL for unsubscribing;
// without either, schedules cannot be enabled (the service says which is
// missing).
func newReportDeps(dbClient *shareddb.Client, cfg *config.APIConfig, recorder *auditservice.Recorder, log *logger.Logger) *reportDeps {
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
			log.WithError(err).Warn("Failed to configure SMTP mailer; SLA report schedules cannot be enabled")
		} else {
			mailer = m
		}
	}
	reports := slaservice.NewService(dbClient, sharedanalytics.NewRepository(dbClient), cfg.LocalAI.Disabled || !cfg.AITriageEnabled)
	svc := reportscheduleservice.NewService(dbClient, reports, mailer, cfg.AppBaseURL, recorder, log)
	return &reportDeps{
		handlers: &reportschedulehandlers.Handlers{Service: svc, Log: log},
		runner:   reportscheduleservice.NewRunner(svc),
	}
}

// reportUnsubscribeRoutes mounts the public unsubscribe link of report
// emails. No session: the token in the path is the credential (an HMAC
// under the schedule's key, revoked with the schedule), it sits outside the
// protected group and so outside generic audit (the POST records its own
// report-schedule.unsubscribe event), and the router-wide anonymous rate
// limiter (RateLimiter.Anonymous) charges it to the client address.
func reportUnsubscribeRoutes(r chi.Router, d *reportDeps) {
	r.Route("/report-unsubscribe", d.handlers.UnsubscribeRoutes)
}

// reportScheduleRoutes mounts the tenant-scoped schedules inside the
// protected group (auth, generic audit, write gate).
func reportScheduleRoutes(r chi.Router, d *reportDeps) {
	r.Route("/report-schedules", d.handlers.Routes)
}

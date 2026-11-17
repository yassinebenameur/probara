package api

import (
	"github.com/go-chi/chi/v5"

	statuspagedomainhandlers "github.com/yassinebenameur/probara/api/internal/handlers/statuspagedomains"
	auditservice "github.com/yassinebenameur/probara/api/internal/services/audit"
	statuspagedomainservice "github.com/yassinebenameur/probara/api/internal/services/statuspagedomains"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

// newStatusPageDomains wires status pages' custom domains: their routes and
// the runner that re-verifies them and expires claims that never verified
// (audited as status-page-domain.supersede and .expire). The API runs it because it is the only
// writer of the table; every replica runs one (SKIP LOCKED claims keep them
// apart). The installation's own hosts are refused as custom domains.
func newStatusPageDomains(cfg *config.APIConfig, dbClient *db.Client, publisher *statusupdates.Publisher, recorder *auditservice.Recorder, log *logger.Logger) (*statuspagedomainhandlers.Handlers, *statuspagedomainservice.Runner) {
	svcCfg := statuspagedomainservice.Config{
		CNAMETarget: cfg.StatusPageCNAMETarget,
		ReservedHosts: statuspagedomainservice.ReservedHosts(
			cfg.AppBaseURL, cfg.PublicBaseURL, cfg.PublicNATSURL,
			cfg.StatusPagePublicBaseURL, cfg.StatusPageCNAMETarget,
		),
		Log: log,
	}
	if publisher != nil {
		svcCfg.Publisher = publisher
	}
	if recorder != nil {
		svcCfg.Audit = recorder
	}
	svc := statuspagedomainservice.NewService(dbClient, svcCfg)
	return &statuspagedomainhandlers.Handlers{Service: svc, Log: log}, statuspagedomainservice.NewRunner(svc)
}

// mountStatusPageDomainRoutes mounts /status-page-domains.
func mountStatusPageDomainRoutes(r chi.Router, h *statuspagedomainhandlers.Handlers) {
	r.Route("/status-page-domains", h.Routes)
}

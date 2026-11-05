package api

import (
	"github.com/go-chi/chi/v5"

	escalationhandlers "github.com/yassinebenameur/probara/api/internal/handlers/escalation"
	escalationservice "github.com/yassinebenameur/probara/api/internal/services/escalation"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
)

// escalationRoutes mounts the tenant-scoped escalation policies inside the
// protected group (auth, generic audit, write gate): reads for every member,
// writes for editors and admins. The workspace default policy is a
// notification setting (default_escalation_policy_id).
func escalationRoutes(r chi.Router, dbClient *shareddb.Client, log *logger.Logger) {
	h := &escalationhandlers.Handlers{Service: escalationservice.NewService(dbClient), Log: log}
	r.Route("/escalation-policies", h.Routes)
}

package api

import (
	"github.com/go-chi/chi/v5"

	alertactionhandlers "github.com/yassinebenameur/probara/api/internal/handlers/alertactions"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	auditservice "github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
)

// alertActionRoutes mounts the signed one-click alert actions that
// notifications link to. Public: the token in the path is the credential.
// They sit outside the protected group, so outside generic audit (the POST
// records its own alert.acknowledge event) and are charged to the client
// address by the router-wide anonymous rate limiter (routes_edge.go). GET
// describes and never acts; POST acknowledges.
func alertActionRoutes(r chi.Router, dbClient *db.Client, hub *alertservice.Hub, recorder *auditservice.Recorder, log *logger.Logger) {
	links, err := actionlink.FromEnv()
	if err != nil {
		// server.go already refuses a malformed PROBARA_SECRETS_KEY; a nil
		// signer answers every token as invalid.
		log.WithError(err).Error("Alert action links disabled: invalid PROBARA_SECRETS_KEY")
	}
	h := alertactionhandlers.NewHandlers(links, alertservice.NewService(dbClient, nil), alertactionhandlers.DBScope(dbClient), hub, recorder, log)
	r.Route("/alert-actions", func(r chi.Router) {
		r.Get("/{token}", h.Describe)
		r.Post("/{token}", h.Acknowledge)
	})
}

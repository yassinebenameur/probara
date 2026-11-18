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

// mountAlertActionRoutes mounts the signed acknowledge links that
// notifications carry. Public: the token (in a request header, never the
// URL) is the credential. They sit outside the protected group and so
// outside generic audit; the POST records its own alert.acknowledge event.
// GET describes and never acts; POST acknowledges.
func mountAlertActionRoutes(r chi.Router, dbClient *db.Client, hub *alertservice.Hub, recorder *auditservice.Recorder, log *logger.Logger) {
	links, err := actionlink.FromEnv()
	if err != nil {
		// NewServer already refuses a malformed PROBARA_SECRETS_KEY; a nil
		// signer answers every token as invalid.
		log.WithError(err).Error("Acknowledge links disabled: invalid PROBARA_SECRETS_KEY")
	}
	h := alertactionhandlers.NewHandlers(links, alertservice.NewService(dbClient, nil),
		alertactionhandlers.DBChannels(dbClient), hub, recorder, log)
	r.Route("/alert-actions", h.Routes)
}

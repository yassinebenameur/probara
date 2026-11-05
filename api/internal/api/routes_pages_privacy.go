package api

import (
	"github.com/go-chi/chi/v5"

	statuspagehandlers "github.com/yassinebenameur/probara/api/internal/handlers/statuspages"
)

// registerStatusPageAccessRoutes mounts the kiosk token routes under
// /status-pages. Visibility and the visitor password ride on the page's own
// create/update; tokens are separate because each is shown exactly once.
// Path parameter {accessId} is a row id, not the credential, which never
// appears in a path.
func registerStatusPageAccessRoutes(r chi.Router, h *statuspagehandlers.Handlers) {
	r.Route("/{id}/access-tokens", func(r chi.Router) {
		r.Get("/", h.ListAccessTokens)
		r.Post("/", h.CreateAccessToken)
		r.Delete("/{accessId}", h.RevokeAccessToken)
	})
}

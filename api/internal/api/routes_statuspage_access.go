package api

import (
	"github.com/go-chi/chi/v5"

	statuspagehandlers "github.com/yassinebenameur/probara/api/internal/handlers/statuspages"
)

// registerStatusPageAccessRoutes mounts a page's kiosk tokens under
// /status-pages. Visibility and the visitor password ride on the page's own
// create and update; tokens are separate because each is shown exactly once.
// {accessTokenId} is the token's row ID, never the credential, which never
// appears in a path (the name still gets it redacted from request logs).
func registerStatusPageAccessRoutes(r chi.Router, h *statuspagehandlers.Handlers) {
	r.Route("/{id}/access-tokens", func(r chi.Router) {
		r.Get("/", h.ListAccessTokens)
		r.Post("/", h.CreateAccessToken)
		// POST …/revoke rather than DELETE: a revoked token stays listed, and
		// the generic audit names this status-page.revoke.
		r.Post("/{accessTokenId}/revoke", h.RevokeAccessToken)
	})
}

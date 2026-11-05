package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	authhandlers "github.com/yassinebenameur/probara/api/internal/handlers/auth"
)

// identityAccountRoutes mounts the signed-in user's own account routes. A
// browser session only (the handlers refuse API keys and MCP tokens), and
// outside the write gate and generic audit like the MCP connection routes:
// every role may end its own sessions, and the handlers record their own
// audit events.
func identityAccountRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, h *authhandlers.Handlers) {
	r.Route("/account", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/sessions", h.ListMySessions)
		r.Post("/sessions/revoke-others", h.RevokeMyOtherSessions)
		r.Post("/sessions/revoke-all", h.RevokeAllMySessions)
		r.Delete("/sessions/{id}", h.RevokeMySession)
	})
}

// identityUserRoutes adds the superadmin lockout and session actions to the
// /users subrouter (already behind RequireSuperadmin and outside generic
// audit; the handlers record auth.unlock / auth.sessions_revoke).
func identityUserRoutes(r chi.Router, h *authhandlers.Handlers) {
	r.Post("/{id}/unlock", h.UnlockUser)
	r.Delete("/{id}/sessions", h.RevokeUserSessions)
}

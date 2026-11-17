// Routes for the signed-in user's own account (sessions).

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	accounthandlers "github.com/yassinebenameur/probara/api/internal/handlers/account"
	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/logger"
)

// mountAccountRoutes mounts /account: a signed-in user session only (the
// handlers refuse API keys and MCP tokens), outside the write gate -- a
// viewer manages their own sessions -- and outside generic audit (the
// handlers record their own events). Users are platform-scoped, so no tenant.
func mountAccountRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, sessions *adminauth.Service, log *logger.Logger, recorder *audit.Recorder) {
	h := accounthandlers.NewHandlers(sessions, log, recorder)
	r.Route("/account", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/sessions", h.ListSessions)
		r.Delete("/sessions/{id}", h.RevokeSession)
		r.Post("/sessions/revoke-others", h.RevokeOtherSessions)
	})
}

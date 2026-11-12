// Routes mounted outside the authenticated API group: push webhooks, OAuth discovery and the OAuth server, sign-in.

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	authhandlers "github.com/yassinebenameur/probara/api/internal/handlers/auth"
	oauthhandlers "github.com/yassinebenameur/probara/api/internal/handlers/oauth"
	pushhandlers "github.com/yassinebenameur/probara/api/internal/handlers/push"
	userhandlers "github.com/yassinebenameur/probara/api/internal/handlers/users"
)

// mountPushWebhookRoutes mounts the push monitors' webhook, authenticated by the token in the URL.
func mountPushWebhookRoutes(r chi.Router, pushHandlers *pushhandlers.Handler) {
	r.Route("/api/v1/push", func(r chi.Router) {
		r.Get("/{token}", pushHandlers.HandlePushGet)
		r.Post("/{token}", pushHandlers.HandlePushPost)
	})
}

// mountOAuthDiscoveryRoutes mounts the well-known OAuth metadata paths MCP clients probe.
func mountOAuthDiscoveryRoutes(r chi.Router, oauthHandlers *oauthhandlers.Handlers) {
	r.Group(func(r chi.Router) {
		r.Use(oauthhandlers.PublicCORS)
		r.Get("/.well-known/oauth-protected-resource", oauthHandlers.ProtectedResourceMetadata)
		r.Get("/.well-known/oauth-protected-resource/*", oauthHandlers.ProtectedResourceMetadata)
		r.Get("/.well-known/oauth-authorization-server", oauthHandlers.AuthorizationServerMetadata)
		r.Get("/.well-known/oauth-authorization-server/*", oauthHandlers.AuthorizationServerMetadata)
		r.Get("/.well-known/openid-configuration/*", oauthHandlers.AuthorizationServerMetadata)
	})
}

// mountOAuthRoutes mounts the MCP OAuth authorization server under /api/v1/oauth.
func mountOAuthRoutes(r chi.Router, oauthHandlers *oauthhandlers.Handlers, authMiddleware func(http.Handler) http.Handler) {
	r.Route("/oauth", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(oauthhandlers.PublicCORS)
			r.Get("/.well-known/oauth-authorization-server", oauthHandlers.AuthorizationServerMetadata)
			r.Get("/.well-known/openid-configuration", oauthHandlers.AuthorizationServerMetadata)
			r.Post("/register", oauthHandlers.Register)
			r.Get("/authorize", oauthHandlers.Authorize)
			r.Post("/token", oauthHandlers.Token)
			r.Post("/revoke", oauthHandlers.Revoke)
		})
		// Consent and connection management for the operator UI: a
		// signed-in user session only, outside the write gate (a viewer
		// may connect a read-only agent and revoke their own
		// connections) and outside generic audit (they record their own).
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Get("/authorize/request", oauthHandlers.AuthorizationRequest)
			r.Post("/authorize/decision", oauthHandlers.Decide)
			r.Get("/grants", oauthHandlers.ListGrants)
			r.Delete("/grants/{id}", oauthHandlers.RevokeGrant)
		})
	})
}

// mountAuthRoutes mounts sign-in, OIDC and first-user bootstrap, none of which require a session.
func mountAuthRoutes(r chi.Router, authHandlers *authhandlers.Handlers, adminUsersHandlers *userhandlers.Handlers) {
	r.Route("/auth", func(r chi.Router) {
		r.Post("/login", authHandlers.Login)
		r.Post("/refresh", authHandlers.Refresh)
		r.Post("/logout", authHandlers.Logout)
		r.Get("/me", authHandlers.Me)
		r.Route("/oidc", func(r chi.Router) {
			r.Get("/status", authHandlers.OIDCStatus)
			r.Get("/start", authHandlers.OIDCStart)
			r.Get("/callback", authHandlers.OIDCCallback)
		})
	})
	r.Get("/users/bootstrap/status", adminUsersHandlers.BootstrapStatus)
	r.Post("/users/bootstrap/first", adminUsersHandlers.BootstrapFirstUser)
}

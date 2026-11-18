// Routes for workspace and platform administration: AI settings and labels, API keys, tenants, audit log, users, OIDC mappings.

package api

import (
	"github.com/go-chi/chi/v5"

	ailabelshandlers "github.com/yassinebenameur/probara/api/internal/handlers/ailabels"
	aisettingshandlers "github.com/yassinebenameur/probara/api/internal/handlers/aisettings"
	apikeyhandlers "github.com/yassinebenameur/probara/api/internal/handlers/apikeys"
	audithandlers "github.com/yassinebenameur/probara/api/internal/handlers/audit"
	oidcmappinghandlers "github.com/yassinebenameur/probara/api/internal/handlers/oidcmappings"
	tenanthandlers "github.com/yassinebenameur/probara/api/internal/handlers/tenants"
	userhandlers "github.com/yassinebenameur/probara/api/internal/handlers/users"
	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	auditservice "github.com/yassinebenameur/probara/api/internal/services/audit"
	tenantservice "github.com/yassinebenameur/probara/api/internal/services/tenants"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
)

// mountAISettingsRoutes mounts the workspace AI settings.
func mountAISettingsRoutes(r chi.Router, aiSettingsHdlrs *aisettingshandlers.Handlers) {
	r.Route("/ai-settings", func(r chi.Router) {
		r.Get("/", aiSettingsHdlrs.GetSettings)
		r.Put("/", aiSettingsHdlrs.UpdateSettings)
		r.Post("/test", aiSettingsHdlrs.TestConnection)
	})
}

// mountAILabelRoutes mounts the AI label training export, for tenant admins only.
func mountAILabelRoutes(r chi.Router, aiLabelHdlrs *ailabelshandlers.Handlers) {
	r.Route("/ai-labels", func(r chi.Router) {
		r.Use(apimiddleware.RequireTenantAdmin)
		r.Post("/export", aiLabelHdlrs.Export)
	})
}

// mountAPIKeyRoutes mounts API-key listing and, for tenant admins, minting and revoking.
func mountAPIKeyRoutes(r chi.Router, apiKeyHandlers *apikeyhandlers.Handlers) {
	r.Route("/api-keys", func(r chi.Router) {
		r.Get("/", apiKeyHandlers.ListAPIKeys)
		// Key management is a tenant-admin capability: a viewer or
		// editor must not be able to mint themselves a write key.
		r.Group(func(r chi.Router) {
			r.Use(apimiddleware.RequireTenantAdmin)
			r.Post("/", apiKeyHandlers.CreateAPIKey)
			r.Delete("/{id}", apiKeyHandlers.RevokeAPIKey)
		})
	})
}

// mountTenantRoutes mounts tenant settings, the tenant list and, for
// superadmins, workspace management (create, rename, delete, overview).
func mountTenantRoutes(r chi.Router, tenantHandlers *tenanthandlers.Handlers, workspaceHandlers *tenanthandlers.WorkspaceHandlers) {
	r.Route("/tenant-settings", func(r chi.Router) {
		r.Get("/", tenantHandlers.GetTenantSettings)
		r.With(apimiddleware.RequireTenantAdmin).Patch("/", tenantHandlers.UpdateTenantSettings)
	})
	r.Route("/tenants", func(r chi.Router) {
		r.Get("/", tenantHandlers.ListTenants)
		r.Group(func(r chi.Router) {
			r.Use(apimiddleware.RequireSuperadmin)
			r.Get("/overview", workspaceHandlers.ListOverview)
			r.Post("/", workspaceHandlers.Create)
			r.Patch("/{id}", workspaceHandlers.Rename)
			r.Delete("/{id}", workspaceHandlers.Delete)
		})
	})
}

// newWorkspaceHandlers builds the workspace management handlers. The OIDC
// just-in-time provisioning workspace joins the default one as undeletable
// while SSO provisioning is on.
func newWorkspaceHandlers(svc *tenantservice.Service, cfg *config.APIConfig, recorder *auditservice.Recorder, log *logger.Logger) *tenanthandlers.WorkspaceHandlers {
	if cfg.OIDC.Enabled && cfg.OIDC.JITProvision {
		svc.ProtectWorkspaces(cfg.OIDC.JITDefaultTenantID)
	} else {
		svc.ProtectWorkspaces()
	}
	return tenanthandlers.NewWorkspaceHandlers(svc, recorder, log)
}

// mountAuditLogRoutes mounts the audit log, for tenant admins only.
func mountAuditLogRoutes(r chi.Router, auditHandlers *audithandlers.Handlers) {
	r.Route("/audit-log", func(r chi.Router) {
		r.Use(apimiddleware.RequireTenantAdmin)
		r.Get("/", auditHandlers.List)
		r.Get("/actions", auditHandlers.Actions)
	})
}

// mountUserRoutes mounts platform user administration, for superadmins only.
func mountUserRoutes(r chi.Router, adminUsersHandlers *userhandlers.Handlers) {
	r.Route("/users", func(r chi.Router) {
		r.Use(apimiddleware.RequireSuperadmin)
		r.Get("/", adminUsersHandlers.ListUsers)
		r.Post("/", adminUsersHandlers.CreateUser)
		r.Get("/{id}", adminUsersHandlers.GetUser)
		r.Patch("/{id}", adminUsersHandlers.UpdateUser)
		r.Delete("/{id}", adminUsersHandlers.DeleteUser)
		r.Post("/{id}/unlock", adminUsersHandlers.UnlockUser)
		r.Get("/{id}/sessions", adminUsersHandlers.ListUserSessions)
		r.Delete("/{id}/sessions", adminUsersHandlers.RevokeUserSessions)
		r.Delete("/{id}/sessions/{sessionId}", adminUsersHandlers.RevokeUserSession)
		r.Post("/{id}/invite", adminUsersHandlers.SendInvite)
		r.Post("/{id}/password-reset", adminUsersHandlers.GeneratePasswordReset)
		r.Post("/{id}/reset-2fa", adminUsersHandlers.ResetTwoFactor)
	})
}

// mountOIDCMappingRoutes mounts the OIDC group-to-role mappings, for superadmins only.
func mountOIDCMappingRoutes(r chi.Router, oidcMappingHandlers *oidcmappinghandlers.Handlers) {
	r.Route("/oidc-group-mappings", func(r chi.Router) {
		r.Use(apimiddleware.RequireSuperadmin)
		r.Get("/", oidcMappingHandlers.List)
		r.Post("/", oidcMappingHandlers.Create)
		r.Patch("/{id}", oidcMappingHandlers.Update)
		r.Delete("/{id}", oidcMappingHandlers.Delete)
	})
}

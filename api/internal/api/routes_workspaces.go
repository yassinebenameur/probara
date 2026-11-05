package api

import (
	"github.com/go-chi/chi/v5"

	tenanthandlers "github.com/yassinebenameur/probara/api/internal/handlers/tenants"
	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	auditservice "github.com/yassinebenameur/probara/api/internal/services/audit"
	tenantservice "github.com/yassinebenameur/probara/api/internal/services/tenants"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
)

// registerWorkspaceRoutes adds workspace create, rename and delete to the
// /tenants subrouter (superadmins only). The OIDC JIT default tenant joins
// the default workspace as undeletable.
func registerWorkspaceRoutes(r chi.Router, svc *tenantservice.Service, cfg *config.APIConfig, recorder *auditservice.Recorder, log *logger.Logger) {
	svc.WithProtectedTenants(cfg.OIDC.JITDefaultTenantID)
	h := tenanthandlers.NewWorkspaceHandlers(svc, recorder, log)
	r.Group(func(r chi.Router) {
		r.Use(apimiddleware.RequireSuperadmin)
		r.Post("/", h.CreateTenant)
		r.Patch("/{id}", h.RenameTenant)
		r.Delete("/{id}", h.DeleteTenant)
	})
}

package api

import (
	"github.com/go-chi/chi/v5"

	monitorhandlers "github.com/yassinebenameur/probara/api/internal/handlers/monitors"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/shared/logger"
)

// registerHistoryRoutes mounts monitor config version history on the
// /monitors subrouter. The versions are written by the monitors service on
// every create and update (services/monitors/versions.go); these routes read
// them and restore one. Restore is a write (audited, write gate).
func registerHistoryRoutes(r chi.Router, h *monitorhandlers.Handlers, svc *monitorservice.Service, log *logger.Logger) {
	svc.ConfigureVersionLog(log)
	if store := svc.Versions(); store != nil {
		h.ConfigureVersions(store)
	}
	r.Get("/{id}/versions", h.ListMonitorVersions)
	r.Get("/{id}/versions/{version}", h.GetMonitorVersion)
	r.Get("/{id}/versions/{version}/diff", h.DiffMonitorVersion)
	r.Post("/{id}/versions/{version}/restore", h.RestoreMonitorVersion)
}

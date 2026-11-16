package api

import (
	"github.com/go-chi/chi/v5"

	monitorhandlers "github.com/yassinebenameur/probara/api/internal/handlers/monitors"
)

// mountMonitorHistoryRoutes mounts monitor configuration history on the
// /monitors subrouter. Versions are written by the monitors service on every
// save (services/monitors/versions.go); these routes read, compare and
// restore them. Reads follow monitor read access; restore is a write, so the
// write gate and the audit trail cover it like any monitor update.
func mountMonitorHistoryRoutes(r chi.Router, h *monitorhandlers.Handlers) {
	r.Get("/{id}/versions", h.ListMonitorVersions)
	r.Get("/{id}/versions/{version}", h.GetMonitorVersion)
	r.Get("/{id}/versions/{version}/diff", h.DiffMonitorVersion)
	r.Post("/{id}/versions/{version}/restore", h.RestoreMonitorVersion)
}

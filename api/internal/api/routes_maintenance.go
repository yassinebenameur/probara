package api

import (
	"github.com/go-chi/chi/v5"

	maintenancewindowhandlers "github.com/yassinebenameur/probara/api/internal/handlers/maintenancewindows"
)

// registerMaintenanceRecurrenceRoutes mounts the recurring-maintenance
// additions on the /maintenance-windows subrouter. Preview computes a rule's
// next occurrences and saves nothing, so it is on the read-only POST
// allowlist (middleware/readonly.go); end is a write (audited, editors).
func registerMaintenanceRecurrenceRoutes(r chi.Router, h *maintenancewindowhandlers.Handlers) {
	r.Post("/preview", h.PreviewMaintenanceWindow)
	r.Post("/{id}/end", h.EndMaintenanceOccurrence)
}

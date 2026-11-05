package api

import (
	"github.com/go-chi/chi/v5"

	alerthandlers "github.com/yassinebenameur/probara/api/internal/handlers/alerts"
)

// registerPagingAlertRoutes mounts the paging actions on the /alerts
// subrouter: unacknowledge (single and bulk) and per-alert snooze. The bulk
// route sits under /alerts/bulk/, so the audit middleware leaves its success
// records to the handler's per-alert events, like bulk acknowledge.
func registerPagingAlertRoutes(r chi.Router, h *alerthandlers.PagingHandlers) {
	r.Post("/{id}/unacknowledge", h.UnacknowledgeAlert)
	r.Post("/{id}/snooze", h.SnoozeAlert)
	r.Post("/{id}/unsnooze", h.UnsnoozeAlert)
	r.Post("/bulk/unacknowledge", h.BulkUnacknowledgeAlerts)
}

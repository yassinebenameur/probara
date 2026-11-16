package api

import (
	"github.com/go-chi/chi/v5"

	deliveryhandlers "github.com/yassinebenameur/probara/api/internal/handlers/deliveries"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
)

// mountDeliveryRoutes mounts the notification delivery history reads on the
// protected router, under the alert and alert-channel paths. They are full
// paths rather than lines in mountAlertRoutes/mountAlertChannelRoutes: chi
// tries a parameter route before a mounted subrouter's catch-all, so
// /alerts/{id}/deliveries resolves here and every other /alerts/... path
// still reaches its subrouter (TestDeliveryRoutesBesideSubrouters).
func mountDeliveryRoutes(r chi.Router, db deliverylog.Querier, log *logger.Logger) {
	h := deliveryhandlers.NewHandlers(db, log)
	r.Get("/alerts/{id}/deliveries", h.ListAlertDeliveries)
	r.Get("/alert-channels/{id}/deliveries", h.ListChannelDeliveries)
}

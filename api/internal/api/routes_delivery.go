package api

import (
	"github.com/go-chi/chi/v5"

	deliveryhandlers "github.com/yassinebenameur/probara/api/internal/handlers/deliveries"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
)

// registerDeliveryAlertRoutes mounts an alert's notification delivery history
// on the /alerts subrouter.
func registerDeliveryAlertRoutes(r chi.Router, db deliverylog.Querier, log *logger.Logger) {
	r.Get("/{id}/deliveries", deliveryhandlers.NewHandlers(db, log).ListAlertDeliveries)
}

// registerDeliveryChannelRoutes mounts a channel's notification delivery
// history on the /alert-channels subrouter.
func registerDeliveryChannelRoutes(r chi.Router, db deliverylog.Querier, log *logger.Logger) {
	r.Get("/{id}/deliveries", deliveryhandlers.NewHandlers(db, log).ListChannelDeliveries)
}

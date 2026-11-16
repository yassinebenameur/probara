// Package deliveries serves the notification delivery history
// (shared/notifications/deliverylog) per alert and per alert channel. Both are
// reads, open to every role that can read alerts and channels.
package deliveries

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
)

// Handlers reads the delivery history of the request's workspace.
type Handlers struct {
	db     deliverylog.Querier
	logger *logger.Logger
}

// NewHandlers returns the delivery history handlers.
func NewHandlers(db deliverylog.Querier, log *logger.Logger) *Handlers {
	return &Handlers{db: db, logger: log}
}

// ListAlertDeliveries handles GET /api/v1/alerts/{id}/deliveries: every
// attempt for the alert, across its channels, in the order they happened.
func (h *Handlers) ListAlertDeliveries(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	alertID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid alert ID")
		return
	}
	page, size := httpx.PageWithLimits(r, deliverylog.DefaultPageSize, deliverylog.MaxPageSize)
	result, err := deliverylog.ListForAlert(r.Context(), h.db, tenantID, alertID, page, size)
	h.write(w, "alert", tenantID, alertID, result, err)
}

// ListChannelDeliveries handles GET /api/v1/alert-channels/{id}/deliveries:
// the channel's attempts, test sends included, newest first, optionally
// filtered by ?outcome= (comma-separated).
func (h *Handlers) ListChannelDeliveries(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	channelID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid alert channel ID")
		return
	}
	outcomes, err := deliverylog.ParseOutcomes(r.URL.Query().Get("outcome"))
	if err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}
	page, size := httpx.PageWithLimits(r, deliverylog.DefaultPageSize, deliverylog.MaxPageSize)
	result, err := deliverylog.ListForChannel(r.Context(), h.db, tenantID, channelID, outcomes, page, size)
	h.write(w, "alert channel", tenantID, channelID, result, err)
}

func (h *Handlers) write(w http.ResponseWriter, what string, tenantID, id uuid.UUID, result *deliverylog.Page, err error) {
	if err != nil {
		if errors.Is(err, deliverylog.ErrNotFound) {
			apierrors.WriteNotFoundError(w, what+" not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"id":        id,
		}).Error("Failed to list notification deliveries")
		apierrors.WriteInternalError(w, "failed to list deliveries")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

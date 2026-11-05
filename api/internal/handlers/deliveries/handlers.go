// Package deliveries serves the notification delivery history
// (shared/notifications/deliverylog) per alert and per alert channel.
package deliveries

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
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

// ListAlertDeliveries handles GET /api/v1/alerts/{id}/deliveries.
func (h *Handlers) ListAlertDeliveries(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, "alert", deliverylog.ListForAlert)
}

// ListChannelDeliveries handles GET /api/v1/alert-channels/{id}/deliveries.
func (h *Handlers) ListChannelDeliveries(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, "alert channel", deliverylog.ListForChannel)
}

type lister func(ctx context.Context, q deliverylog.Querier, tenantID, id uuid.UUID, page, pageSize int) (*deliverylog.Page, error)

func (h *Handlers) serve(w http.ResponseWriter, r *http.Request, what string, list lister) {
	tenant, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}
	tenantID, err := uuid.Parse(tenant)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		errors.WriteValidationError(w, "invalid "+what+" ID")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))

	result, err := list(r.Context(), h.db, tenantID, id, page, pageSize)
	if err != nil {
		if stderrors.Is(err, deliverylog.ErrNotFound) {
			errors.WriteNotFoundError(w, what+" not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"id":        id,
		}).Error("Failed to list notification deliveries")
		errors.WriteInternalError(w, "failed to list deliveries")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

package alerts

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
)

// PagingService is the alert service's paging surface: unacknowledge and
// per-alert snooze. *alertservice.Service implements it.
type PagingService interface {
	UnacknowledgeAlert(ctx context.Context, tenantID, alertID uuid.UUID, actor alertservice.Actor) (*models.AlertWithDetails, error)
	BulkUnacknowledge(ctx context.Context, tenantID uuid.UUID, alertIDs []uuid.UUID, actor alertservice.Actor) (*alertservice.BulkResult, error)
	SnoozeAlert(ctx context.Context, tenantID, alertID uuid.UUID, until time.Time, actor alertservice.Actor) (*models.AlertWithDetails, error)
	UnsnoozeAlert(ctx context.Context, tenantID, alertID uuid.UUID, actor alertservice.Actor) (*models.AlertWithDetails, error)
}

var _ PagingService = (*alertservice.Service)(nil)

// PagingHandlers serves the paging actions next to the alert handlers they
// share the SSE hub, logger and bulk audit with.
type PagingHandlers struct {
	*Handlers
	paging PagingService
}

// WithPaging returns the paging handlers backed by svc.
func (h *Handlers) WithPaging(svc PagingService) *PagingHandlers {
	return &PagingHandlers{Handlers: h, paging: svc}
}

// SnoozeAlertRequest is the body of POST /alerts/{id}/snooze: either minutes
// from now or an absolute until (RFC 3339). Exactly one is required.
type SnoozeAlertRequest struct {
	Minutes *int       `json:"minutes,omitempty"`
	Until   *time.Time `json:"until,omitempty"`
}

// UnacknowledgeAlert handles POST /api/v1/alerts/{id}/unacknowledge.
func (h *PagingHandlers) UnacknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	h.singleAction(w, r, "unacknowledge", func(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
		return h.paging.UnacknowledgeAlert(ctx, tenantID, alertID, alertservice.ActorFromContext(ctx))
	})
}

// UnsnoozeAlert handles POST /api/v1/alerts/{id}/unsnooze.
func (h *PagingHandlers) UnsnoozeAlert(w http.ResponseWriter, r *http.Request) {
	h.singleAction(w, r, "unsnooze", func(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
		return h.paging.UnsnoozeAlert(ctx, tenantID, alertID, alertservice.ActorFromContext(ctx))
	})
}

// SnoozeAlert handles POST /api/v1/alerts/{id}/snooze.
func (h *PagingHandlers) SnoozeAlert(w http.ResponseWriter, r *http.Request) {
	var req SnoozeAlertRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	if (req.Minutes == nil) == (req.Until == nil) {
		errors.WriteValidationError(w, "exactly one of minutes or until is required")
		return
	}
	var until time.Time
	if req.Minutes != nil {
		until = time.Now().Add(time.Duration(*req.Minutes) * time.Minute)
	} else {
		until = *req.Until
	}
	h.singleAction(w, r, "snooze", func(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
		return h.paging.SnoozeAlert(ctx, tenantID, alertID, until, alertservice.ActorFromContext(ctx))
	})
}

// BulkUnacknowledgeAlerts handles POST /api/v1/alerts/bulk/unacknowledge.
func (h *PagingHandlers) BulkUnacknowledgeAlerts(w http.ResponseWriter, r *http.Request) {
	h.bulkAction(w, r, "unacknowledge", h.paging.BulkUnacknowledge, h.hub.BroadcastAlertUpdated)
}

// singleAction runs one paging action on the {id} alert, maps the service's
// state errors to 400/404, and broadcasts the changed alert.
func (h *PagingHandlers) singleAction(
	w http.ResponseWriter,
	r *http.Request,
	verb string,
	apply func(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error),
) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}
	alertID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		errors.WriteValidationError(w, "invalid alert ID")
		return
	}

	alert, err := apply(r.Context(), tenantUUID, alertID)
	switch {
	case stderrors.Is(err, alertservice.ErrInvalidSnooze):
		errors.WriteValidationError(w, err.Error())
		return
	case stderrors.Is(err, alertservice.ErrNotAcknowledged),
		stderrors.Is(err, alertservice.ErrNotOpen),
		stderrors.Is(err, alertservice.ErrNotSnoozed):
		errors.WriteNotFoundError(w, err.Error())
		return
	case err != nil && err.Error() == "alert not found":
		errors.WriteNotFoundError(w, err.Error())
		return
	case err != nil:
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"alert_id":  alertID,
		}).Error("Failed to " + verb + " alert")
		errors.WriteInternalError(w, "failed to "+verb+" alert")
		return
	}

	if h.hub != nil {
		h.hub.BroadcastAlertUpdated(tenantUUID, alert)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(alert)
}

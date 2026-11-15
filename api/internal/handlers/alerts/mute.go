package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
)

// Unacknowledge, snooze and unsnooze, single and bulk. They are writes like
// acknowledge (RequireWrite), audited the same way: the single routes by the
// audit middleware (alert.unacknowledge, alert.snooze, alert.unsnooze), the
// bulk ones per alert by runBulk. Each change is broadcast as alert_updated.

// BulkSnoozeAlertsRequest is the body of POST /alerts/bulk/snooze: the IDs and
// exactly one of until or duration_minutes.
type BulkSnoozeAlertsRequest struct {
	IDs             []string   `json:"ids"`
	Until           *time.Time `json:"until,omitempty"`
	DurationMinutes *int       `json:"duration_minutes,omitempty"`
}

// snoozeEnd resolves a snooze body to its end time; false means a 400 was
// written. The bounds are the service's (alertservice.ValidateSnoozeEnd).
func snoozeEnd(w http.ResponseWriter, until *time.Time, minutes *int) (time.Time, bool) {
	var end time.Time
	switch {
	case until != nil && minutes != nil:
		apierrors.WriteValidationError(w, "provide either until or duration_minutes, not both")
		return end, false
	case until != nil:
		end = *until
	case minutes != nil:
		end = time.Now().Add(time.Duration(*minutes) * time.Minute)
	default:
		apierrors.WriteValidationError(w, "either until or duration_minutes is required")
		return end, false
	}
	if err := alertservice.ValidateSnoozeEnd(end); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return end, false
	}
	return end, true
}

// UnacknowledgeAlert handles POST /api/v1/alerts/{id}/unacknowledge.
func (h *Handlers) UnacknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	h.muteAction(w, r, "unacknowledge", func(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
		return h.service.UnacknowledgeAlert(ctx, tenantID, alertID)
	})
}

// SnoozeAlert handles POST /api/v1/alerts/{id}/snooze.
func (h *Handlers) SnoozeAlert(w http.ResponseWriter, r *http.Request) {
	var req models.SnoozeAlertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	until, ok := snoozeEnd(w, req.Until, req.DurationMinutes)
	if !ok {
		return
	}
	h.muteAction(w, r, "snooze", func(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
		return h.service.SnoozeAlert(ctx, tenantID, alertID, until)
	})
}

// UnsnoozeAlert handles POST /api/v1/alerts/{id}/unsnooze.
func (h *Handlers) UnsnoozeAlert(w http.ResponseWriter, r *http.Request) {
	h.muteAction(w, r, "unsnooze", func(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error) {
		return h.service.UnsnoozeAlert(ctx, tenantID, alertID)
	})
}

// muteAction runs one single-alert mute action: 404 when the alert is not in
// a state it applies to, as acknowledge and resolve answer.
func (h *Handlers) muteAction(
	w http.ResponseWriter,
	r *http.Request,
	verb string,
	apply func(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error),
) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	alertID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid alert ID")
		return
	}
	alert, err := apply(r.Context(), tenantUUID, alertID)
	if err != nil {
		switch {
		case errors.Is(err, alertservice.ErrAlertNotAcknowledged),
			errors.Is(err, alertservice.ErrAlertNotOpen),
			errors.Is(err, alertservice.ErrAlertNotSnoozed),
			errors.Is(err, alertservice.ErrAlertNotFound):
			apierrors.WriteNotFoundError(w, err.Error())
		case errors.Is(err, alertservice.ErrInvalidSnooze):
			apierrors.WriteValidationError(w, err.Error())
		default:
			h.logger.WithFields(map[string]interface{}{
				"error":     err.Error(),
				"tenant_id": tenantUUID.String(),
				"alert_id":  alertID,
			}).Error("Failed to " + verb + " alert")
			apierrors.WriteInternalError(w, "failed to "+verb+" alert")
		}
		return
	}
	if h.hub != nil {
		h.hub.BroadcastAlertUpdated(tenantUUID, alert)
	}
	httpx.WriteJSON(w, http.StatusOK, alert)
}

// BulkUnacknowledgeAlerts handles POST /api/v1/alerts/bulk/unacknowledge.
func (h *Handlers) BulkUnacknowledgeAlerts(w http.ResponseWriter, r *http.Request) {
	h.bulkAction(w, r, "unacknowledge", h.service.BulkUnacknowledge, h.hub.BroadcastAlertUpdated)
}

// BulkSnoozeAlerts handles POST /api/v1/alerts/bulk/snooze.
func (h *Handlers) BulkSnoozeAlerts(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	var req BulkSnoozeAlertsRequest
	if !decodeBulkBody(w, r, &req) {
		return
	}
	until, ok := snoozeEnd(w, req.Until, req.DurationMinutes)
	if !ok {
		return
	}
	h.runBulk(w, r, tenantUUID, "snooze", req.IDs,
		func(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) (*alertservice.BulkResult, error) {
			return h.service.BulkSnooze(ctx, tenantID, ids, until)
		}, h.hub.BroadcastAlertUpdated)
}

// BulkUnsnoozeAlerts handles POST /api/v1/alerts/bulk/unsnooze.
func (h *Handlers) BulkUnsnoozeAlerts(w http.ResponseWriter, r *http.Request) {
	h.bulkAction(w, r, "unsnooze", h.service.BulkUnsnooze, h.hub.BroadcastAlertUpdated)
}

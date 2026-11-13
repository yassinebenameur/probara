package monitors

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/api/internal/validation"
)

// BulkAlertingRequest applies alerting fields to many monitors. Nil fields
// mean "leave unchanged" (spec §7.4).
type BulkAlertingRequest struct {
	MonitorIDs                   []string                          `json:"monitor_ids"`
	ConsecutiveFailuresThreshold *int                              `json:"consecutive_failures_threshold,omitempty"`
	NotificationMode             *string                           `json:"notification_mode,omitempty"`
	NotificationChannels         []models.MonitorChannelAssignment `json:"notification_channels,omitempty"`
}

// BulkUpdateAlerting handles POST /api/v1/monitors/bulk/alerting
func (h *Handlers) BulkUpdateAlerting(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	var req BulkAlertingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if len(req.MonitorIDs) == 0 {
		apierrors.WriteValidationError(w, "monitor_ids cannot be empty")
		return
	}

	if err := validation.ValidateMonitorNotificationFields(req.ConsecutiveFailuresThreshold, req.NotificationMode, nil, req.NotificationChannels); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	monitorIDs := make([]uuid.UUID, 0, len(req.MonitorIDs))
	for _, idStr := range req.MonitorIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			apierrors.WriteValidationError(w, "invalid monitor_id: "+idStr)
			return
		}
		monitorIDs = append(monitorIDs, id)
	}

	updated, err := h.service.BulkUpdateAlerting(r.Context(), tenantUUID, monitorIDs,
		req.ConsecutiveFailuresThreshold, req.NotificationMode, req.NotificationChannels)
	if err != nil {
		if errors.Is(err, monitorservice.ErrCallMonitorForbidden) {
			apierrors.WriteForbiddenError(w, err.Error())
			return
		}
		msg := err.Error()
		if errors.Is(err, monitorservice.ErrMonitorsNotInTenant) {
			apierrors.WriteNotFoundError(w, "one or more monitors not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     msg,
			"tenant_id": tenantID,
		}).Error("Failed to bulk update alerting")
		apierrors.WriteInternalError(w, "failed to bulk update alerting")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

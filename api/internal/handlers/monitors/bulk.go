package monitors

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/dbtx"
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
	RecoveryThreshold            *int                              `json:"recovery_threshold,omitempty"`
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
	if err := validation.ValidateRecoveryThreshold(req.RecoveryThreshold); err != nil {
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
		req.ConsecutiveFailuresThreshold, req.RecoveryThreshold, req.NotificationMode, req.NotificationChannels)
	if err != nil {
		// Batches commit one after the other (BulkAlertingBatchSize): when
		// earlier ones were applied, every answer says so. The edit sets the
		// same values on every monitor, so repeating the request is safe.
		partial := ""
		if updated > 0 {
			partial = fmt.Sprintf(" %d of %d monitors were already updated by earlier batches and keep the change; "+
				"the edit is idempotent, so retrying the whole request is safe.", updated, len(monitorIDs))
		}
		if errors.Is(err, monitorservice.ErrCallMonitorForbidden) {
			apierrors.WriteForbiddenError(w, err.Error()+partial)
			return
		}
		msg := err.Error()
		if errors.Is(err, monitorservice.ErrMonitorsNotInTenant) {
			apierrors.WriteNotFoundError(w, "one or more monitors not found")
			return
		}
		if errors.Is(err, monitorservice.ErrChannelsNotInTenant) {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
		if errors.Is(err, monitorservice.ErrBulkAlertingRefused) {
			// Refused up front (nothing written), or in a later batch after a
			// racing type change, when the earlier batches stay applied.
			text := err.Error() + "; no monitor was changed"
			if updated > 0 {
				text = err.Error() + "; the remaining monitors were not updated." + partial
			}
			apierrors.WriteValidationError(w, text)
			return
		}
		if dbtx.IsConflict(err) {
			text := dbtx.ErrConflict.Error()
			if updated > 0 {
				text = "another change to the same monitors was in progress; the remaining monitors were not updated." + partial
			}
			apierrors.WriteError(w, http.StatusServiceUnavailable, "conflict", text)
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     msg,
			"tenant_id": tenantID,
			"updated":   updated,
		}).Error("Failed to bulk update alerting")
		text := "failed to bulk update alerting"
		if updated > 0 {
			text += "; the remaining monitors were not updated." + partial
		}
		apierrors.WriteInternalError(w, text)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, models.BulkAlertingResponse{Updated: updated})
}

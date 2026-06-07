package monitors

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/validation"
)

// BulkUpdateAlertPolicy handles POST /api/v1/monitors/bulk/alert-policy
func (h *Handlers) BulkUpdateAlertPolicy(w http.ResponseWriter, r *http.Request) {
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

	var req models.BulkUpdateAlertPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if len(req.MonitorIDs) == 0 {
		errors.WriteValidationError(w, "monitor_ids cannot be empty")
		return
	}
	if req.Op != models.BulkAlertPolicyOpAttach && req.Op != models.BulkAlertPolicyOpDetach {
		errors.WriteValidationError(w, "op must be 'attach' or 'detach'")
		return
	}

	policyID, err := uuid.Parse(req.PolicyID)
	if err != nil {
		errors.WriteValidationError(w, "invalid policy_id: "+err.Error())
		return
	}

	monitorIDs := make([]uuid.UUID, 0, len(req.MonitorIDs))
	for _, idStr := range req.MonitorIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			errors.WriteValidationError(w, "invalid monitor_id: "+idStr)
			return
		}
		monitorIDs = append(monitorIDs, id)
	}

	resp, err := h.service.BulkUpdateAlertPolicy(r.Context(), tenantUUID, monitorIDs, policyID, req.Op)
	if err != nil {
		msg := err.Error()
		// VerifyAlertPolicy returns "alert policy not found or does not belong to tenant"
		// and VerifyMonitorsBelongToTenant returns "one or more monitors not found or do
		// not belong to tenant". Match these exact sentinel strings (not substrings) so
		// that unrelated database errors that happen to contain "not found" are not
		// misclassified as 404.
		switch msg {
		case "alert policy not found or does not belong to tenant",
			"one or more monitors not found or do not belong to tenant":
			errors.WriteNotFoundError(w, "one or more resources not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     msg,
			"tenant_id": tenantID,
		}).Error("Failed to bulk update alert policy")
		errors.WriteInternalError(w, "failed to bulk update alert policy")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

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

	var req BulkAlertingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if len(req.MonitorIDs) == 0 {
		errors.WriteValidationError(w, "monitor_ids cannot be empty")
		return
	}

	if err := validation.ValidateMonitorNotificationFields(req.ConsecutiveFailuresThreshold, req.NotificationMode, req.NotificationChannels); err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}

	monitorIDs := make([]uuid.UUID, 0, len(req.MonitorIDs))
	for _, idStr := range req.MonitorIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			errors.WriteValidationError(w, "invalid monitor_id: "+idStr)
			return
		}
		monitorIDs = append(monitorIDs, id)
	}

	updated, err := h.service.BulkUpdateAlerting(r.Context(), tenantUUID, monitorIDs,
		req.ConsecutiveFailuresThreshold, req.NotificationMode, req.NotificationChannels)
	if err != nil {
		msg := err.Error()
		if msg == "one or more monitors not found or do not belong to tenant" {
			errors.WriteNotFoundError(w, "one or more monitors not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     msg,
			"tenant_id": tenantID,
		}).Error("Failed to bulk update alerting")
		errors.WriteInternalError(w, "failed to bulk update alerting")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]int{"updated": updated})
}

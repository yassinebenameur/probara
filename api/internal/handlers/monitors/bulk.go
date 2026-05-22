package monitors

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
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
		// VerifyAlertPolicy and VerifyMonitorsBelongToTenant return "not found
		// or do not belong to tenant" — surface those as 404 to avoid leaking
		// existence across tenants.
		if strings.Contains(msg, "not found") || strings.Contains(msg, "do not belong to tenant") {
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

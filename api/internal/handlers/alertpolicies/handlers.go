package alertpolicies

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	alertpolicyservice "github.com/yassinebenameur/probara/api/internal/services/alertpolicies"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers handles alert policy HTTP requests
type Handlers struct {
	service alertpolicyservice.AlertPolicyService
	logger  *logger.Logger
}

// NewHandlers creates a new alert policies handler
func NewHandlers(service alertpolicyservice.AlertPolicyService, log *logger.Logger) *Handlers {
	return &Handlers{
		service: service,
		logger:  log,
	}
}

// CreateAlertPolicy handles POST /api/v1/alert-policies
func (h *Handlers) CreateAlertPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	var req models.CreateAlertPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateAlertPolicy(&req); err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	policy, err := h.service.CreateAlertPolicy(r.Context(), tenantUUID, &req)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to create alert policy")
		errors.WriteInternalError(w, "failed to create alert policy")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(policy)
}

// GetAlertPolicy handles GET /api/v1/alert-policies/{id}
func (h *Handlers) GetAlertPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	policyIDStr := chi.URLParam(r, "id")
	policyID, err := uuid.Parse(policyIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid alert policy ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	policy, err := h.service.GetAlertPolicy(r.Context(), tenantUUID, policyID)
	if err != nil {
		if err.Error() == "alert policy not found" {
			errors.WriteNotFoundError(w, "alert policy not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"policy_id": policyID,
		}).Error("Failed to get alert policy")
		errors.WriteInternalError(w, "failed to get alert policy")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(policy)
}

// ListAlertPolicies handles GET /api/v1/alert-policies
func (h *Handlers) ListAlertPolicies(w http.ResponseWriter, r *http.Request) {
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

	page := 1
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	pageSize := 20
	if pageSizeStr := r.URL.Query().Get("page_size"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 && ps <= 100 {
			pageSize = ps
		}
	}

	result, err := h.service.ListAlertPolicies(r.Context(), tenantUUID, page, pageSize)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to list alert policies")
		errors.WriteInternalError(w, "failed to list alert policies")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// UpdateAlertPolicy handles PATCH /api/v1/alert-policies/{id}
func (h *Handlers) UpdateAlertPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	policyIDStr := chi.URLParam(r, "id")
	policyID, err := uuid.Parse(policyIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid alert policy ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	var req models.UpdateAlertPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateAlertPolicyUpdate(&req); err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}

	policy, err := h.service.UpdateAlertPolicy(r.Context(), tenantUUID, policyID, &req)
	if err != nil {
		if err.Error() == "alert policy not found" {
			errors.WriteNotFoundError(w, "alert policy not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"policy_id": policyID,
		}).Error("Failed to update alert policy")
		errors.WriteInternalError(w, "failed to update alert policy")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(policy)
}

// DeleteAlertPolicy handles DELETE /api/v1/alert-policies/{id}
func (h *Handlers) DeleteAlertPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	policyIDStr := chi.URLParam(r, "id")
	policyID, err := uuid.Parse(policyIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid alert policy ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	err = h.service.DeleteAlertPolicy(r.Context(), tenantUUID, policyID)
	if err != nil {
		if err.Error() == "alert policy not found" {
			errors.WriteNotFoundError(w, "alert policy not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"policy_id": policyID,
		}).Error("Failed to delete alert policy")
		errors.WriteInternalError(w, "failed to delete alert policy")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

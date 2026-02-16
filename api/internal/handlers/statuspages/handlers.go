package statuspages

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	statuspageservice "github.com/yassinebenameur/probara/api/internal/services/statuspages"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers handles status page HTTP requests
type Handlers struct {
	service statuspageservice.StatusPageService
	logger  *logger.Logger
}

// NewHandlers creates a new status pages handler
func NewHandlers(service statuspageservice.StatusPageService, log *logger.Logger) *Handlers {
	return &Handlers{
		service: service,
		logger:  log,
	}
}

// CreateStatusPage handles POST /api/v1/status-pages
func (h *Handlers) CreateStatusPage(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	var req models.CreateStatusPageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateStatusPage(&req); err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	page, err := h.service.CreateStatusPage(r.Context(), tenantUUID, &req)
	if err != nil {
		if err.Error() == "slug already exists" {
			errors.WriteValidationError(w, "slug already exists")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to create status page")
		errors.WriteInternalError(w, "failed to create status page")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(page)
}

// GetStatusPage handles GET /api/v1/status-pages/{id}
func (h *Handlers) GetStatusPage(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	pageIDStr := chi.URLParam(r, "id")
	pageID, err := uuid.Parse(pageIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid status page ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	page, err := h.service.GetStatusPage(r.Context(), tenantUUID, pageID)
	if err != nil {
		if err.Error() == "status page not found" {
			errors.WriteNotFoundError(w, "status page not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"page_id":   pageID,
		}).Error("Failed to get status page")
		errors.WriteInternalError(w, "failed to get status page")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(page)
}

// ListStatusPages handles GET /api/v1/status-pages
func (h *Handlers) ListStatusPages(w http.ResponseWriter, r *http.Request) {
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

	result, err := h.service.ListStatusPages(r.Context(), tenantUUID, page, pageSize)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to list status pages")
		errors.WriteInternalError(w, "failed to list status pages")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// UpdateStatusPage handles PATCH /api/v1/status-pages/{id}
func (h *Handlers) UpdateStatusPage(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	pageIDStr := chi.URLParam(r, "id")
	pageID, err := uuid.Parse(pageIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid status page ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	var req models.UpdateStatusPageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateStatusPageUpdate(&req); err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}

	page, err := h.service.UpdateStatusPage(r.Context(), tenantUUID, pageID, &req)
	if err != nil {
		if err.Error() == "status page not found" {
			errors.WriteNotFoundError(w, "status page not found")
			return
		}
		if err.Error() == "slug already exists" {
			errors.WriteValidationError(w, "slug already exists")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"page_id":   pageID,
		}).Error("Failed to update status page")
		errors.WriteInternalError(w, "failed to update status page")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(page)
}

// DeleteStatusPage handles DELETE /api/v1/status-pages/{id}
func (h *Handlers) DeleteStatusPage(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	pageIDStr := chi.URLParam(r, "id")
	pageID, err := uuid.Parse(pageIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid status page ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	err = h.service.DeleteStatusPage(r.Context(), tenantUUID, pageID)
	if err != nil {
		if err.Error() == "status page not found" {
			errors.WriteNotFoundError(w, "status page not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"page_id":   pageID,
		}).Error("Failed to delete status page")
		errors.WriteInternalError(w, "failed to delete status page")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

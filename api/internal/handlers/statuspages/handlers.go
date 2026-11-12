package statuspages

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
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
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	var req models.CreateStatusPageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateStatusPage(&req); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	page, err := h.service.CreateStatusPage(r.Context(), tenantUUID, &req)
	if err != nil {
		if errors.Is(err, statuspageservice.ErrSlugExists) {
			apierrors.WriteValidationError(w, "slug already exists")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to create status page")
		apierrors.WriteInternalError(w, "failed to create status page")
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, page)
}

// GetStatusPage handles GET /api/v1/status-pages/{id}
func (h *Handlers) GetStatusPage(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	pageIDStr := chi.URLParam(r, "id")
	pageID, err := uuid.Parse(pageIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid status page ID")
		return
	}

	page, err := h.service.GetStatusPage(r.Context(), tenantUUID, pageID)
	if err != nil {
		if errors.Is(err, statuspageservice.ErrStatusPageNotFound) {
			apierrors.WriteNotFoundError(w, "status page not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"page_id":   pageID,
		}).Error("Failed to get status page")
		apierrors.WriteInternalError(w, "failed to get status page")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, page)
}

// ListStatusPages handles GET /api/v1/status-pages
func (h *Handlers) ListStatusPages(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	page, pageSize := httpx.Page(r)

	result, err := h.service.ListStatusPages(r.Context(), tenantUUID, page, pageSize)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to list status pages")
		apierrors.WriteInternalError(w, "failed to list status pages")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, result)
}

// UpdateStatusPage handles PATCH /api/v1/status-pages/{id}
func (h *Handlers) UpdateStatusPage(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	pageIDStr := chi.URLParam(r, "id")
	pageID, err := uuid.Parse(pageIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid status page ID")
		return
	}

	var req models.UpdateStatusPageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateStatusPageUpdate(&req); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	page, err := h.service.UpdateStatusPage(r.Context(), tenantUUID, pageID, &req)
	if err != nil {
		if errors.Is(err, statuspageservice.ErrStatusPageNotFound) {
			apierrors.WriteNotFoundError(w, "status page not found")
			return
		}
		if errors.Is(err, statuspageservice.ErrSlugExists) {
			apierrors.WriteValidationError(w, "slug already exists")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"page_id":   pageID,
		}).Error("Failed to update status page")
		apierrors.WriteInternalError(w, "failed to update status page")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, page)
}

// DeleteStatusPage handles DELETE /api/v1/status-pages/{id}
func (h *Handlers) DeleteStatusPage(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	pageIDStr := chi.URLParam(r, "id")
	pageID, err := uuid.Parse(pageIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid status page ID")
		return
	}

	err = h.service.DeleteStatusPage(r.Context(), tenantUUID, pageID)
	if err != nil {
		if errors.Is(err, statuspageservice.ErrStatusPageNotFound) {
			apierrors.WriteNotFoundError(w, "status page not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"page_id":   pageID,
		}).Error("Failed to delete status page")
		apierrors.WriteInternalError(w, "failed to delete status page")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

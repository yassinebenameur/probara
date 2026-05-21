package tenants

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/tenants"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/logger"
)

type tenantService interface {
	ListTenants(ctx context.Context) ([]models.Tenant, error)
	GetTenantSettings(ctx context.Context, tenantID uuid.UUID) (*models.TenantSettings, error)
	UpdateTenantSettings(ctx context.Context, tenantID uuid.UUID, req *models.UpdateTenantSettingsRequest) (*models.TenantSettings, error)
}

// Handlers handles tenant requests.
type Handlers struct {
	service tenantService
	logger  *logger.Logger
}

// NewHandlers creates a new tenants handler.
func NewHandlers(service *tenants.Service, log *logger.Logger) *Handlers {
	return &Handlers{service: service, logger: log}
}

// ListTenants handles GET /api/v1/tenants
func (h *Handlers) ListTenants(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListTenants(r.Context())
	if err != nil {
		h.logger.WithError(err).Error("Failed to list tenants")
		errors.WriteInternalError(w, "failed to list tenants")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.TenantListResponse{Items: items})
}

// GetTenantSettings handles GET /api/v1/tenant-settings.
func (h *Handlers) GetTenantSettings(w http.ResponseWriter, r *http.Request) {
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

	settings, err := h.service.GetTenantSettings(r.Context(), tenantUUID)
	if err != nil {
		if err.Error() == "tenant not found" {
			errors.WriteNotFoundError(w, "tenant not found")
			return
		}
		h.logger.WithError(err).Error("Failed to get tenant settings")
		errors.WriteInternalError(w, "failed to get tenant settings")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}

// UpdateTenantSettings handles PATCH /api/v1/tenant-settings.
func (h *Handlers) UpdateTenantSettings(w http.ResponseWriter, r *http.Request) {
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

	var req models.UpdateTenantSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if req.DataRetentionDays != nil {
		if err := validation.ValidateTenantDataRetentionDays(*req.DataRetentionDays); err != nil {
			errors.WriteValidationError(w, err.Error())
			return
		}
	}

	if req.DashboardGroupTags != nil {
		if err := validation.ValidateDashboardGroupTags(*req.DashboardGroupTags); err != nil {
			errors.WriteValidationError(w, err.Error())
			return
		}
	}

	settings, err := h.service.UpdateTenantSettings(r.Context(), tenantUUID, &req)
	if err != nil {
		if err.Error() == "tenant not found" {
			errors.WriteNotFoundError(w, "tenant not found")
			return
		}
		h.logger.WithError(err).Error("Failed to update tenant settings")
		errors.WriteInternalError(w, "failed to update tenant settings")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}

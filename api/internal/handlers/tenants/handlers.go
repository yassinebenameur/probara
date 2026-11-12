package tenants

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
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
	// Tenant discovery is an admin-session concern; API keys are pinned to
	// one tenant and never switch.
	if _, err := middleware.GetAdminID(r.Context()); err != nil {
		apierrors.WriteForbiddenError(w, "admin session required")
		return
	}

	items, err := h.service.ListTenants(r.Context())
	if err != nil {
		h.logger.WithError(err).Error("Failed to list tenants")
		apierrors.WriteInternalError(w, "failed to list tenants")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, models.TenantListResponse{Items: items})
}

// GetTenantSettings handles GET /api/v1/tenant-settings.
func (h *Handlers) GetTenantSettings(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}

	settings, err := h.service.GetTenantSettings(r.Context(), tenantUUID)
	if err != nil {
		if errors.Is(err, tenants.ErrTenantNotFound) {
			apierrors.WriteNotFoundError(w, "tenant not found")
			return
		}
		h.logger.WithError(err).Error("Failed to get tenant settings")
		apierrors.WriteInternalError(w, "failed to get tenant settings")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, settings)
}

// UpdateTenantSettings handles PATCH /api/v1/tenant-settings.
func (h *Handlers) UpdateTenantSettings(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}

	var req models.UpdateTenantSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if req.DataRetentionDays != nil {
		if err := validation.ValidateTenantDataRetentionDays(*req.DataRetentionDays); err != nil {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
	}

	if req.DashboardGroupTags != nil {
		if err := validation.ValidateDashboardGroupTags(*req.DashboardGroupTags); err != nil {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
	}

	settings, err := h.service.UpdateTenantSettings(r.Context(), tenantUUID, &req)
	if err != nil {
		if errors.Is(err, tenants.ErrTenantNotFound) {
			apierrors.WriteNotFoundError(w, "tenant not found")
			return
		}
		h.logger.WithError(err).Error("Failed to update tenant settings")
		apierrors.WriteInternalError(w, "failed to update tenant settings")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, settings)
}

package notificationsettings

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	svc "github.com/yassinebenameur/probara/api/internal/services/notificationsettings"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers handles notification settings HTTP requests.
type Handlers struct {
	service *svc.Service
	logger  *logger.Logger
}

// NewHandlers creates a new notification settings handler.
func NewHandlers(service *svc.Service, log *logger.Logger) *Handlers {
	return &Handlers{service: service, logger: log}
}

// GetSettings handles GET /api/v1/notification-settings.
func (h *Handlers) GetSettings(w http.ResponseWriter, r *http.Request) {
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

	settings, err := h.service.Get(r.Context(), tenantUUID)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get notification settings")
		errors.WriteInternalError(w, "failed to load notification settings")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}

// UpdateSettings handles PUT /api/v1/notification-settings.
func (h *Handlers) UpdateSettings(w http.ResponseWriter, r *http.Request) {
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

	var req svc.UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	settings, err := h.service.Update(r.Context(), tenantUUID, req)
	if err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}

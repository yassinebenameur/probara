package notificationsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
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
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	settings, err := h.service.Get(r.Context(), tenantUUID)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to get notification settings")
		apierrors.WriteInternalError(w, "failed to load notification settings")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, settings)
}

// UpdateSettings handles PUT /api/v1/notification-settings.
func (h *Handlers) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	var req svc.UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	// Validate before calling the service so that client errors return 400
	// without ever touching the database.
	for i, ch := range req.DefaultChannels {
		if ch.DelaySeconds < 0 {
			apierrors.WriteValidationError(w, fmt.Sprintf("default_channels[%d].delay_seconds must be >= 0", i))
			return
		}
	}

	settings, err := h.service.Update(r.Context(), tenantUUID, req)
	if err != nil {
		if errors.Is(err, svc.ErrChannelNotFound) {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to update notification settings")
		apierrors.WriteInternalError(w, "failed to update notification settings")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, settings)
}

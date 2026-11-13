package alertchannels

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	alertchannelservice "github.com/yassinebenameur/probara/api/internal/services/alertchannels"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers handles alert channel HTTP requests
type Handlers struct {
	service alertchannelservice.AlertChannelService
	logger  *logger.Logger
}

// NewHandlers creates a new alert channels handler
func NewHandlers(service alertchannelservice.AlertChannelService, log *logger.Logger) *Handlers {
	return &Handlers{
		service: service,
		logger:  log,
	}
}

// CreateAlertChannel handles POST /api/v1/alert-channels
func (h *Handlers) CreateAlertChannel(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	var req models.CreateAlertChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateAlertChannel(&req); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	channel, err := h.service.CreateAlertChannel(r.Context(), tenantUUID, &req)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to create alert channel")
		apierrors.WriteInternalError(w, "failed to create alert channel")
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, channel)
}

// GetAlertChannel handles GET /api/v1/alert-channels/{id}
func (h *Handlers) GetAlertChannel(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	channelIDStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid alert channel ID")
		return
	}

	channel, err := h.service.GetAlertChannel(r.Context(), tenantUUID, channelID)
	if err != nil {
		if errors.Is(err, alertchannelservice.ErrAlertChannelNotFound) {
			apierrors.WriteNotFoundError(w, "alert channel not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"channel_id": channelID,
		}).Error("Failed to get alert channel")
		apierrors.WriteInternalError(w, "failed to get alert channel")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, channel)
}

// ListAlertChannels handles GET /api/v1/alert-channels
func (h *Handlers) ListAlertChannels(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	page, pageSize := httpx.Page(r)

	result, err := h.service.ListAlertChannels(r.Context(), tenantUUID, page, pageSize)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to list alert channels")
		apierrors.WriteInternalError(w, "failed to list alert channels")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, result)
}

// UpdateAlertChannel handles PATCH /api/v1/alert-channels/{id}
func (h *Handlers) UpdateAlertChannel(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	channelIDStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid alert channel ID")
		return
	}

	var req models.UpdateAlertChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateAlertChannelUpdate(&req); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	channel, err := h.service.UpdateAlertChannel(r.Context(), tenantUUID, channelID, &req)
	if err != nil {
		if errors.Is(err, alertchannelservice.ErrInvalidConfig) {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
		if errors.Is(err, alertchannelservice.ErrAlertChannelNotFound) {
			apierrors.WriteNotFoundError(w, "alert channel not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"channel_id": channelID,
		}).Error("Failed to update alert channel")
		apierrors.WriteInternalError(w, "failed to update alert channel")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, channel)
}

// DeleteAlertChannel handles DELETE /api/v1/alert-channels/{id}
func (h *Handlers) DeleteAlertChannel(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	channelIDStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid alert channel ID")
		return
	}

	if err := h.service.DeleteAlertChannel(r.Context(), tenantUUID, channelID); err != nil {
		if errors.Is(err, alertchannelservice.ErrAlertChannelNotFound) {
			apierrors.WriteNotFoundError(w, "alert channel not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"channel_id": channelID,
		}).Error("Failed to delete alert channel")
		apierrors.WriteInternalError(w, "failed to delete alert channel")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// TestAlertChannel handles POST /api/v1/alert-channels/{id}/test
func (h *Handlers) TestAlertChannel(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}

	channelIDStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid alert channel ID")
		return
	}

	if err := h.service.TestAlertChannel(r.Context(), tenantUUID, channelID); err != nil {
		if errors.Is(err, alertchannelservice.ErrAlertChannelNotFound) {
			apierrors.WriteNotFoundError(w, "alert channel not found")
			return
		}
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

package alertchannels

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
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
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	var req models.CreateAlertChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateAlertChannel(&req); err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	channel, err := h.service.CreateAlertChannel(r.Context(), tenantUUID, &req)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to create alert channel")
		errors.WriteInternalError(w, "failed to create alert channel")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(channel)
}

// GetAlertChannel handles GET /api/v1/alert-channels/{id}
func (h *Handlers) GetAlertChannel(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	channelIDStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid alert channel ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	channel, err := h.service.GetAlertChannel(r.Context(), tenantUUID, channelID)
	if err != nil {
		if err.Error() == "alert channel not found" {
			errors.WriteNotFoundError(w, "alert channel not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"channel_id": channelID,
		}).Error("Failed to get alert channel")
		errors.WriteInternalError(w, "failed to get alert channel")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channel)
}

// ListAlertChannels handles GET /api/v1/alert-channels
func (h *Handlers) ListAlertChannels(w http.ResponseWriter, r *http.Request) {
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

	result, err := h.service.ListAlertChannels(r.Context(), tenantUUID, page, pageSize)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to list alert channels")
		errors.WriteInternalError(w, "failed to list alert channels")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// UpdateAlertChannel handles PATCH /api/v1/alert-channels/{id}
func (h *Handlers) UpdateAlertChannel(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	channelIDStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid alert channel ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	var req models.UpdateAlertChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateAlertChannelUpdate(&req); err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}

	channel, err := h.service.UpdateAlertChannel(r.Context(), tenantUUID, channelID, &req)
	if err != nil {
		if err.Error() == "alert channel not found" {
			errors.WriteNotFoundError(w, "alert channel not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"channel_id": channelID,
		}).Error("Failed to update alert channel")
		errors.WriteInternalError(w, "failed to update alert channel")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channel)
}

// DeleteAlertChannel handles DELETE /api/v1/alert-channels/{id}
func (h *Handlers) DeleteAlertChannel(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	channelIDStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid alert channel ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	if err := h.service.DeleteAlertChannel(r.Context(), tenantUUID, channelID); err != nil {
		if err.Error() == "alert channel not found" {
			errors.WriteNotFoundError(w, "alert channel not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"channel_id": channelID,
		}).Error("Failed to delete alert channel")
		errors.WriteInternalError(w, "failed to delete alert channel")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// TestAlertChannel handles POST /api/v1/alert-channels/{id}/test
func (h *Handlers) TestAlertChannel(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	channelIDStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid alert channel ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	if err := h.service.TestAlertChannel(r.Context(), tenantUUID, channelID); err != nil {
		if err.Error() == "alert channel not found" {
			errors.WriteNotFoundError(w, "alert channel not found")
			return
		}
		errors.WriteValidationError(w, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

package alerts

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers handles alert HTTP requests
type Handlers struct {
	service alertservice.AlertService
	hub     *alertservice.Hub
	logger  *logger.Logger
}

// NewHandlers creates a new alerts handler
func NewHandlers(service alertservice.AlertService, hub *alertservice.Hub, log *logger.Logger) *Handlers {
	return &Handlers{
		service: service,
		hub:     hub,
		logger:  log,
	}
}

// GetHub returns the SSE hub for external access
func (h *Handlers) GetHub() *alertservice.Hub {
	return h.hub
}

// ListAlerts handles GET /api/v1/alerts
func (h *Handlers) ListAlerts(w http.ResponseWriter, r *http.Request) {
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

	params := &models.AlertListParams{
		Page:     1,
		PageSize: 20,
	}

	// Parse query parameters
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			params.Page = p
		}
	}

	if pageSizeStr := r.URL.Query().Get("page_size"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 && ps <= 100 {
			params.PageSize = ps
		}
	}

	if statusStr := r.URL.Query().Get("status"); statusStr != "" {
		status := models.AlertStatus(statusStr)
		if status == models.AlertStatusActive || status == models.AlertStatusAcknowledged || status == models.AlertStatusResolved {
			params.Status = &status
		}
	}

	if monitorIDStr := r.URL.Query().Get("monitor_id"); monitorIDStr != "" {
		if monitorID, err := uuid.Parse(monitorIDStr); err == nil {
			params.MonitorID = &monitorID
		}
	}

	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if since, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			params.Since = &since
		}
	}

	// suppressed=true|false narrows to alerts whose notifications the alerter
	// is (or is not) currently suppressing, e.g. because an upstream
	// dependency is down.
	if suppressedStr := r.URL.Query().Get("suppressed"); suppressedStr != "" {
		if suppressed, err := strconv.ParseBool(suppressedStr); err == nil {
			params.Suppressed = &suppressed
		}
	}

	result, err := h.service.ListAlerts(r.Context(), tenantUUID, params)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to list alerts")
		errors.WriteInternalError(w, "failed to list alerts")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// GetAlert handles GET /api/v1/alerts/{id}
func (h *Handlers) GetAlert(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	alertIDStr := chi.URLParam(r, "id")
	alertID, err := uuid.Parse(alertIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid alert ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	alert, err := h.service.GetAlert(r.Context(), tenantUUID, alertID)
	if err != nil {
		if err.Error() == "alert not found" {
			errors.WriteNotFoundError(w, "alert not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"alert_id":  alertID,
		}).Error("Failed to get alert")
		errors.WriteInternalError(w, "failed to get alert")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(alert)
}

// GetRecentAlerts handles GET /api/v1/alerts/recent
func (h *Handlers) GetRecentAlerts(w http.ResponseWriter, r *http.Request) {
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

	limit := 10
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 50 {
			limit = l
		}
	}

	alerts, err := h.service.GetRecentAlerts(r.Context(), tenantUUID, limit)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to get recent alerts")
		errors.WriteInternalError(w, "failed to get recent alerts")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(alerts)
}

// AcknowledgeAlert handles POST /api/v1/alerts/{id}/acknowledge
func (h *Handlers) AcknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	alertIDStr := chi.URLParam(r, "id")
	alertID, err := uuid.Parse(alertIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid alert ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	alert, err := h.service.AcknowledgeAlert(r.Context(), tenantUUID, alertID)
	if err != nil {
		if err.Error() == "alert not found or not active" {
			errors.WriteNotFoundError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"alert_id":  alertID,
		}).Error("Failed to acknowledge alert")
		errors.WriteInternalError(w, "failed to acknowledge alert")
		return
	}

	// Broadcast via SSE
	h.hub.BroadcastAlertAcknowledged(tenantUUID, alert)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(alert)
}

// ResolveAlert handles POST /api/v1/alerts/{id}/resolve
func (h *Handlers) ResolveAlert(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	alertIDStr := chi.URLParam(r, "id")
	alertID, err := uuid.Parse(alertIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid alert ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	alert, err := h.service.ResolveAlert(r.Context(), tenantUUID, alertID)
	if err != nil {
		if err.Error() == "alert not found or already resolved" {
			errors.WriteNotFoundError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"alert_id":  alertID,
		}).Error("Failed to resolve alert")
		errors.WriteInternalError(w, "failed to resolve alert")
		return
	}

	// Broadcast via SSE
	h.hub.BroadcastAlertResolved(tenantUUID, alert)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(alert)
}

// GetAlertCountsByPolicy handles GET /api/v1/alerts/counts/by-policy
func (h *Handlers) GetAlertCountsByPolicy(w http.ResponseWriter, r *http.Request) {
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

	counts, err := h.service.GetAlertCountsByPolicy(r.Context(), tenantUUID)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to get alert counts")
		errors.WriteInternalError(w, "failed to get alert counts")
		return
	}

	// Convert UUID keys to strings for JSON
	result := make(map[string]int)
	for k, v := range counts {
		result[k.String()] = v
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// GetMonitorCountsByPolicy handles GET /api/v1/alerts/counts/monitors-by-policy
func (h *Handlers) GetMonitorCountsByPolicy(w http.ResponseWriter, r *http.Request) {
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

	counts, err := h.service.GetMonitorCountsByPolicy(r.Context(), tenantUUID)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to get monitor counts")
		errors.WriteInternalError(w, "failed to get monitor counts")
		return
	}

	// Convert UUID keys to strings for JSON
	result := make(map[string]int)
	for k, v := range counts {
		result[k.String()] = v
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

package alerts

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/shared/logger"
)

// maxAlertQueryLength bounds the list's q= search term.
const maxAlertQueryLength = 200

// Handlers handles alert HTTP requests
type Handlers struct {
	service alertservice.AlertService
	hub     *alertservice.Hub
	logger  *logger.Logger
	audit   auditRecorder
}

// NewHandlers creates a new alerts handler
func NewHandlers(service alertservice.AlertService, hub *alertservice.Hub, log *logger.Logger) *Handlers {
	return &Handlers{
		service: service,
		hub:     hub,
		logger:  log,
	}
}

// ListAlerts handles GET /api/v1/alerts
func (h *Handlers) ListAlerts(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	// Parse query parameters
	params := &models.AlertListParams{}
	params.Page, params.PageSize = httpx.Page(r)

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

	// q= searches the alert's subject (monitor or mesh location name) or ID,
	// so pickers can reach alerts beyond the newest page.
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		if utf8.RuneCountInString(q) > maxAlertQueryLength {
			apierrors.WriteValidationError(w, fmt.Sprintf("q must be at most %d characters", maxAlertQueryLength))
			return
		}
		params.Query = q
	}

	// sort=triggered_at|status|monitor|duration, dir=asc|desc (whitelisted
	// in the service; anything else is a 400, not a silent default).
	sort, err := alertservice.ParseListSort(r.URL.Query().Get("sort"), r.URL.Query().Get("dir"))
	if err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	result, err := h.service.ListAlerts(r.Context(), tenantUUID, params, sort)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to list alerts")
		apierrors.WriteInternalError(w, "failed to list alerts")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, result)
}

// GetAlert handles GET /api/v1/alerts/{id}
func (h *Handlers) GetAlert(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	alertIDStr := chi.URLParam(r, "id")
	alertID, err := uuid.Parse(alertIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid alert ID")
		return
	}

	alert, err := h.service.GetAlert(r.Context(), tenantUUID, alertID)
	if err != nil {
		if errors.Is(err, alertservice.ErrAlertNotFound) {
			apierrors.WriteNotFoundError(w, "alert not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"alert_id":  alertID,
		}).Error("Failed to get alert")
		apierrors.WriteInternalError(w, "failed to get alert")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, alert)
}

// GetRecentAlerts handles GET /api/v1/alerts/recent
func (h *Handlers) GetRecentAlerts(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

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
		apierrors.WriteInternalError(w, "failed to get recent alerts")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, alerts)
}

// AcknowledgeAlert handles POST /api/v1/alerts/{id}/acknowledge
func (h *Handlers) AcknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	alertIDStr := chi.URLParam(r, "id")
	alertID, err := uuid.Parse(alertIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid alert ID")
		return
	}

	alert, err := h.service.AcknowledgeAlert(r.Context(), tenantUUID, alertID)
	if err != nil {
		if errors.Is(err, alertservice.ErrAlertNotActive) {
			apierrors.WriteNotFoundError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"alert_id":  alertID,
		}).Error("Failed to acknowledge alert")
		apierrors.WriteInternalError(w, "failed to acknowledge alert")
		return
	}

	// Broadcast via SSE
	h.hub.BroadcastAlertAcknowledged(tenantUUID, alert)

	httpx.WriteJSON(w, http.StatusOK, alert)
}

// ResolveAlert handles POST /api/v1/alerts/{id}/resolve
func (h *Handlers) ResolveAlert(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	alertIDStr := chi.URLParam(r, "id")
	alertID, err := uuid.Parse(alertIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid alert ID")
		return
	}

	alert, err := h.service.ResolveAlert(r.Context(), tenantUUID, alertID)
	if err != nil {
		if errors.Is(err, alertservice.ErrAlertAlreadyResolved) {
			apierrors.WriteNotFoundError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"alert_id":  alertID,
		}).Error("Failed to resolve alert")
		apierrors.WriteInternalError(w, "failed to resolve alert")
		return
	}

	// Broadcast via SSE
	h.hub.BroadcastAlertResolved(tenantUUID, alert)

	httpx.WriteJSON(w, http.StatusOK, alert)
}

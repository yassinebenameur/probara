// Reads of a monitor's history: results, analytics and metrics snapshots.

package monitors

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	resultservice "github.com/yassinebenameur/probara/api/internal/services/results"
)

// GetMonitorResults handles GET /api/v1/monitors/{id}/results
func (h *Handlers) GetMonitorResults(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	// Parse query parameters
	limit := 0
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	var since *time.Time
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			since = &t
		}
	}

	results, err := h.resultService.GetMonitorResults(r.Context(), tenantUUID, monitorID, limit, since)
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorNotFound) {
			apierrors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to get monitor results")
		apierrors.WriteInternalError(w, "failed to get monitor results")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, results)
}

// GetMonitorResultsBatch handles GET /api/v1/monitors/results/batch: the
// newest ?limit= (default 20, max 100) results for each of up to 100
// monitors, named by ?ids= (comma-separated, repeatable). It is a GET so
// viewers and read-scope keys reach it without a postReadPaths entry.
func (h *Handlers) GetMonitorResultsBatch(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	query := r.URL.Query()
	var monitorIDs []uuid.UUID
	for _, value := range query["ids"] {
		for _, raw := range strings.Split(value, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			id, err := uuid.Parse(raw)
			if err != nil {
				apierrors.WriteValidationError(w, fmt.Sprintf("invalid monitor ID %q", raw))
				return
			}
			monitorIDs = append(monitorIDs, id)
		}
	}

	limit := 0
	if limitStr := query.Get("limit"); limitStr != "" {
		parsed, err := strconv.Atoi(limitStr)
		if err != nil || parsed < 1 {
			apierrors.WriteValidationError(w, fmt.Sprintf("limit must be an integer between 1 and %d", resultservice.MaxBatchResultsLimit))
			return
		}
		limit = parsed
	}

	response, err := h.resultService.GetMonitorResultsBatch(r.Context(), tenantUUID, monitorIDs, limit)
	if err != nil {
		if errors.Is(err, resultservice.ErrInvalidBatchRequest) {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":         err.Error(),
			"tenant_id":     tenantID,
			"monitor_count": len(monitorIDs),
		}).Error("Failed to get batch monitor results")
		apierrors.WriteInternalError(w, "failed to get monitor results")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, response)
}

// GetMonitorAnalytics handles GET /api/v1/monitors/{id}/analytics
func (h *Handlers) GetMonitorAnalytics(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	rangeValue := parseAnalyticsRange(r)

	response, err := h.resultService.GetMonitorAnalytics(r.Context(), tenantUUID, monitorID, rangeValue)
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorNotFound) {
			apierrors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
			"range":      rangeValue,
		}).Error("Failed to get monitor analytics")
		apierrors.WriteInternalError(w, "failed to get monitor analytics")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, response)
}

// parseAnalyticsRange reads ?range=, defaulting unknown values to 24h.
func parseAnalyticsRange(r *http.Request) models.MonitorAnalyticsRange {
	switch value := models.MonitorAnalyticsRange(r.URL.Query().Get("range")); value {
	case models.MonitorAnalyticsRange1h,
		models.MonitorAnalyticsRange6h,
		models.MonitorAnalyticsRange24h,
		models.MonitorAnalyticsRange7d,
		models.MonitorAnalyticsRange30d,
		models.MonitorAnalyticsRange90d,
		models.MonitorAnalyticsRange365d:
		return value
	}
	return models.MonitorAnalyticsRange24h
}

// GetMonitorMetricsSnapshots handles GET /api/v1/monitors/{id}/metrics-snapshots
func (h *Handlers) GetMonitorMetricsSnapshots(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	monitorID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	rangeValue := parseAnalyticsRange(r)
	response, err := h.resultService.GetMonitorMetricsSnapshots(r.Context(), tenantUUID, monitorID, rangeValue)
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorNotFound) {
			apierrors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
			"range":      rangeValue,
		}).Error("Failed to get monitor metrics snapshots")
		apierrors.WriteInternalError(w, "failed to get monitor metrics snapshots")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, response)
}

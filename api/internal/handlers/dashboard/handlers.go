package dashboard

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	dashboardservice "github.com/yassinebenameur/probara/api/internal/services/dashboard"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers handles dashboard HTTP requests.
type Handlers struct {
	service dashboardservice.DashboardService
	logger  *logger.Logger
}

// NewHandlers creates a new dashboard handler.
func NewHandlers(service dashboardservice.DashboardService, log *logger.Logger) *Handlers {
	return &Handlers{
		service: service,
		logger:  log,
	}
}

// GetOverview handles GET /api/v1/dashboard/overview.
func (h *Handlers) GetOverview(w http.ResponseWriter, r *http.Request) {
	tenantID, tenantUUID, ok := tenantFromRequest(w, r)
	if !ok {
		return
	}

	params := parseOverviewParams(r)
	overview, err := h.service.GetOverview(r.Context(), tenantUUID, params)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to get dashboard overview")
		errors.WriteInternalError(w, "failed to get dashboard overview")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(overview)
}

// GetSummary handles GET /api/v1/dashboard/summary.
func (h *Handlers) GetSummary(w http.ResponseWriter, r *http.Request) {
	tenantID, tenantUUID, ok := tenantFromRequest(w, r)
	if !ok {
		return
	}

	summary, err := h.service.GetSummary(r.Context(), tenantUUID, parseSummaryParams(r))
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to get dashboard summary")
		errors.WriteInternalError(w, "failed to get dashboard summary")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summary)
}

// GetProblemMonitors handles GET /api/v1/dashboard/problem-monitors.
func (h *Handlers) GetProblemMonitors(w http.ResponseWriter, r *http.Request) {
	tenantID, tenantUUID, ok := tenantFromRequest(w, r)
	if !ok {
		return
	}

	response, err := h.service.GetProblemMonitors(r.Context(), tenantUUID, parseListParams(r, 5))
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to get dashboard problem monitors")
		errors.WriteInternalError(w, "failed to get dashboard problem monitors")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetRecentFailures handles GET /api/v1/dashboard/recent-failures.
func (h *Handlers) GetRecentFailures(w http.ResponseWriter, r *http.Request) {
	tenantID, tenantUUID, ok := tenantFromRequest(w, r)
	if !ok {
		return
	}

	response, err := h.service.GetRecentFailures(r.Context(), tenantUUID, parseListParams(r, 10))
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to get dashboard recent failures")
		errors.WriteInternalError(w, "failed to get dashboard recent failures")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetRecentAlerts handles GET /api/v1/dashboard/recent-alerts.
func (h *Handlers) GetRecentAlerts(w http.ResponseWriter, r *http.Request) {
	tenantID, tenantUUID, ok := tenantFromRequest(w, r)
	if !ok {
		return
	}

	response, err := h.service.GetRecentAlerts(r.Context(), tenantUUID, parseListParams(r, 10))
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to get dashboard recent alerts")
		errors.WriteInternalError(w, "failed to get dashboard recent alerts")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetGroupSparkline handles GET /api/v1/dashboard/group-sparkline.
//
//	?group=<tagName>  omit or empty = ungrouped row
//	&range=<1h|24h|7d|30d|90d|365d>
//	&tag=<a>&tag=<b>  top-level dashboard tag filter (repeated query param, like other dashboard endpoints)
func (h *Handlers) GetGroupSparkline(w http.ResponseWriter, r *http.Request) {
	tenantID, tenantUUID, ok := tenantFromRequest(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	var tagPtr *string
	if raw := q.Get("group"); raw != "" {
		t := raw
		tagPtr = &t
	}

	params := &models.DashboardGroupSparklineQuery{
		Tag:   tagPtr,
		Range: parseRange(r),
		Tags:  q["tag"],
	}

	resp, err := h.service.GetGroupSparkline(r.Context(), tenantUUID, params)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to get dashboard group sparkline")
		errors.WriteInternalError(w, "failed to get dashboard group sparkline")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func tenantFromRequest(w http.ResponseWriter, r *http.Request) (string, uuid.UUID, bool) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return "", uuid.UUID{}, false
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return "", uuid.UUID{}, false
	}

	return tenantID, tenantUUID, true
}

func parseSummaryParams(r *http.Request) *models.DashboardOverviewQuery {
	return &models.DashboardOverviewQuery{
		Range: parseRange(r),
		Tags:  r.URL.Query()["tag"],
	}
}

func parseOverviewParams(r *http.Request) *models.DashboardOverviewQuery {
	params := parseSummaryParams(r)
	params.FailuresLimit = parsePositiveLimit(r.URL.Query().Get("failures_limit"), 10)
	params.AlertsLimit = parsePositiveLimit(r.URL.Query().Get("alerts_limit"), 10)
	return params
}

func parseListParams(r *http.Request, defaultLimit int) *models.DashboardListQuery {
	return &models.DashboardListQuery{
		Range: parseRange(r),
		Limit: parsePositiveLimit(r.URL.Query().Get("limit"), defaultLimit),
		Tags:  r.URL.Query()["tag"],
	}
}

func parseRange(r *http.Request) models.DashboardRange {
	switch models.DashboardRange(r.URL.Query().Get("range")) {
	case models.DashboardRange1h, models.DashboardRange24h, models.DashboardRange7d, models.DashboardRange30d, models.DashboardRange90d, models.DashboardRange365d:
		return models.DashboardRange(r.URL.Query().Get("range"))
	default:
		return models.DashboardRange24h
	}
}

func parsePositiveLimit(raw string, defaultValue int) int {
	limit := defaultValue
	if raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > 50 {
		return 50
	}
	return limit
}

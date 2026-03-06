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

	params := &models.DashboardOverviewQuery{
		Range:         models.DashboardRange24h,
		FailuresLimit: 10,
		AlertsLimit:   10,
		Tags:          r.URL.Query()["tag"],
	}

	switch models.DashboardRange(r.URL.Query().Get("range")) {
	case models.DashboardRange24h, models.DashboardRange7d, models.DashboardRange30d, models.DashboardRange90d, models.DashboardRange365d:
		params.Range = models.DashboardRange(r.URL.Query().Get("range"))
	}

	if failuresLimitStr := r.URL.Query().Get("failures_limit"); failuresLimitStr != "" {
		if v, err := strconv.Atoi(failuresLimitStr); err == nil && v > 0 {
			params.FailuresLimit = v
		}
	}
	if params.FailuresLimit > 50 {
		params.FailuresLimit = 50
	}

	if alertsLimitStr := r.URL.Query().Get("alerts_limit"); alertsLimitStr != "" {
		if v, err := strconv.Atoi(alertsLimitStr); err == nil && v > 0 {
			params.AlertsLimit = v
		}
	}
	if params.AlertsLimit > 50 {
		params.AlertsLimit = 50
	}

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

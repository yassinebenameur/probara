package maintenancewindows

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	maintenancewindowservice "github.com/yassinebenameur/probara/api/internal/services/maintenancewindows"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers handles maintenance window HTTP requests
type Handlers struct {
	service maintenancewindowservice.MaintenanceWindowService
	logger  *logger.Logger
}

// NewHandlers creates a new maintenance windows handler
func NewHandlers(service maintenancewindowservice.MaintenanceWindowService, log *logger.Logger) *Handlers {
	return &Handlers{service: service, logger: log}
}

// CreateMaintenanceWindow handles POST /api/v1/maintenance-windows
func (h *Handlers) CreateMaintenanceWindow(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenant(w, r)
	if !ok {
		return
	}

	var req models.CreateMaintenanceWindowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	window, err := h.service.Create(r.Context(), tenantUUID, &req)
	if err != nil {
		h.writeServiceError(w, r, err, "create maintenance window")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(window)
}

// ListMaintenanceWindows handles GET /api/v1/maintenance-windows
func (h *Handlers) ListMaintenanceWindows(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenant(w, r)
	if !ok {
		return
	}

	status := r.URL.Query().Get("status")
	monitorID := uuid.Nil
	if s := r.URL.Query().Get("monitor_id"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			apierrors.WriteValidationError(w, "invalid monitor_id")
			return
		}
		monitorID = id
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

	result, err := h.service.List(r.Context(), tenantUUID, status, monitorID, page, pageSize)
	if err != nil {
		h.writeServiceError(w, r, err, "list maintenance windows")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// GetMaintenanceWindow handles GET /api/v1/maintenance-windows/{id}
func (h *Handlers) GetMaintenanceWindow(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenant(w, r)
	if !ok {
		return
	}
	windowID, ok := h.windowID(w, r)
	if !ok {
		return
	}

	window, err := h.service.Get(r.Context(), tenantUUID, windowID)
	if err != nil {
		h.writeServiceError(w, r, err, "get maintenance window")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(window)
}

// UpdateMaintenanceWindow handles PATCH /api/v1/maintenance-windows/{id}
func (h *Handlers) UpdateMaintenanceWindow(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenant(w, r)
	if !ok {
		return
	}
	windowID, ok := h.windowID(w, r)
	if !ok {
		return
	}

	var req models.UpdateMaintenanceWindowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	window, err := h.service.Update(r.Context(), tenantUUID, windowID, &req)
	if err != nil {
		h.writeServiceError(w, r, err, "update maintenance window")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(window)
}

// DeleteMaintenanceWindow handles DELETE /api/v1/maintenance-windows/{id}
func (h *Handlers) DeleteMaintenanceWindow(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenant(w, r)
	if !ok {
		return
	}
	windowID, ok := h.windowID(w, r)
	if !ok {
		return
	}

	if err := h.service.Delete(r.Context(), tenantUUID, windowID); err != nil {
		h.writeServiceError(w, r, err, "delete maintenance window")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SnoozeMonitor handles POST /api/v1/monitors/{id}/snooze
func (h *Handlers) SnoozeMonitor(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenant(w, r)
	if !ok {
		return
	}

	monitorID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	var req models.SnoozeMonitorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	var until time.Time
	switch {
	case req.Until != nil && req.DurationMinutes != nil:
		apierrors.WriteValidationError(w, "provide either until or duration_minutes, not both")
		return
	case req.Until != nil:
		until = *req.Until
	case req.DurationMinutes != nil:
		if *req.DurationMinutes <= 0 {
			apierrors.WriteValidationError(w, "duration_minutes must be positive")
			return
		}
		until = time.Now().Add(time.Duration(*req.DurationMinutes) * time.Minute)
	default:
		apierrors.WriteValidationError(w, "either until or duration_minutes is required")
		return
	}

	window, err := h.service.SnoozeMonitor(r.Context(), tenantUUID, monitorID, until)
	if err != nil {
		h.writeServiceError(w, r, err, "snooze monitor")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(window)
}

func (h *Handlers) tenant(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "tenant ID not found")
		return uuid.Nil, false
	}
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		apierrors.WriteInternalError(w, "invalid tenant ID")
		return uuid.Nil, false
	}
	return tenantUUID, true
}

func (h *Handlers) windowID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid maintenance window ID")
		return uuid.Nil, false
	}
	return id, true
}

// writeServiceError maps service errors to HTTP responses. Validation-style
// errors surface as 400s; not-found as 404; everything else is a logged 500.
func (h *Handlers) writeServiceError(w http.ResponseWriter, r *http.Request, err error, action string) {
	if errors.Is(err, maintenancewindowservice.ErrNotFound) {
		apierrors.WriteNotFoundError(w, "maintenance window not found")
		return
	}
	switch err.Error() {
	case "title is required",
		"ends_at must be after starts_at",
		"ends_at must be in the future",
		"at least one monitor is required",
		"one or more monitors not found",
		"monitor not found",
		"snooze end time must be in the future":
		apierrors.WriteValidationError(w, err.Error())
		return
	}
	h.logger.WithFields(map[string]interface{}{
		"error": err.Error(),
	}).Error("Failed to " + action)
	apierrors.WriteInternalError(w, "failed to "+action)
}

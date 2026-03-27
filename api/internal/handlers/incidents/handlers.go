package incidents

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	incidentservice "github.com/yassinebenameur/probara/api/internal/services/incidents"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers handles incident HTTP requests.
type Handlers struct {
	service incidentservice.IncidentService
	logger  *logger.Logger
}

// NewHandlers creates a new incident handlers instance.
func NewHandlers(service incidentservice.IncidentService, log *logger.Logger) *Handlers {
	return &Handlers{
		service: service,
		logger:  log,
	}
}

func tenantUUIDFromContext(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
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

func incidentIDFromRequest(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	incidentIDStr := chi.URLParam(r, "id")
	incidentID, err := uuid.Parse(incidentIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid incident ID")
		return uuid.Nil, false
	}

	return incidentID, true
}

func requestUUIDParam(w http.ResponseWriter, r *http.Request, paramName, resourceName string) (uuid.UUID, bool) {
	value := strings.TrimSpace(chi.URLParam(r, paramName))
	if value == "" {
		apierrors.WriteValidationError(w, resourceName+" ID is required")
		return uuid.Nil, false
	}

	id, err := uuid.Parse(value)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid "+resourceName+" ID")
		return uuid.Nil, false
	}

	return id, true
}

func writeIncidentError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}

	switch err.Error() {
	case "request is required",
		"title is required",
		"summary is required",
		"alert ID is required",
		"monitor ID is required",
		"invalid incident state",
		"invalid incident timeline entry type",
		"invalid alert ID",
		"invalid monitor ID",
		"message is required",
		"selected monitors must be linked to the incident and status page",
		"resolved incidents cannot be reopened":
		apierrors.WriteValidationError(w, err.Error())
	case "incident not found",
		"alert not found",
		"monitor not found",
		"status page not found":
		apierrors.WriteNotFoundError(w, err.Error())
	default:
		apierrors.WriteInternalError(w, "failed to process incident")
	}
}

func isIncidentNotFoundError(err error) bool {
	return err != nil && err.Error() == "incident not found"
}

func isIncidentExpectedTransitionError(err error) bool {
	if err == nil {
		return false
	}

	switch err.Error() {
	case "incident not found",
		"resolved incidents cannot be reopened":
		return true
	default:
		return false
	}
}

func isIncidentExpectedMutationError(err error) bool {
	if err == nil {
		return false
	}

	switch err.Error() {
	case "request is required",
		"incident not found",
		"alert not found",
		"monitor not found",
		"status page not found",
		"monitor ID is required",
		"invalid monitor ID",
		"selected monitors must be linked to the incident and status page":
		return true
	default:
		return false
	}
}

// ListIncidents handles GET /api/v1/incidents
func (h *Handlers) ListIncidents(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := tenantUUIDFromContext(w, r)
	if !ok {
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

	result, err := h.service.ListIncidents(r.Context(), tenantUUID, page, pageSize)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantUUID.String(),
		}).Error("Failed to list incidents")
		apierrors.WriteInternalError(w, "failed to list incidents")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// GetIncident handles GET /api/v1/incidents/{id}
func (h *Handlers) GetIncident(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := tenantUUIDFromContext(w, r)
	if !ok {
		return
	}

	incidentID, ok := incidentIDFromRequest(w, r)
	if !ok {
		return
	}

	incident, err := h.service.GetIncident(r.Context(), tenantUUID, incidentID)
	if err != nil {
		if isIncidentNotFoundError(err) {
			apierrors.WriteNotFoundError(w, "incident not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":       err.Error(),
			"tenant_id":   tenantUUID.String(),
			"incident_id": incidentID.String(),
		}).Error("Failed to get incident")
		apierrors.WriteInternalError(w, "failed to get incident")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(incident)
}

// CreateIncident handles POST /api/v1/incidents
func (h *Handlers) CreateIncident(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := tenantUUIDFromContext(w, r)
	if !ok {
		return
	}

	var req models.CreateIncidentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateCreateIncident(&req); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	incident, err := h.service.CreateIncident(r.Context(), tenantUUID, &req)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantUUID.String(),
		}).Error("Failed to create incident")
		writeIncidentError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(incident)
}

// UpdateIncident handles PATCH /api/v1/incidents/{id}
func (h *Handlers) UpdateIncident(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := tenantUUIDFromContext(w, r)
	if !ok {
		return
	}

	incidentID, ok := incidentIDFromRequest(w, r)
	if !ok {
		return
	}

	var req models.UpdateIncidentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateUpdateIncident(&req); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	incident, err := h.service.UpdateIncident(r.Context(), tenantUUID, incidentID, &req)
	if err != nil {
		if isIncidentNotFoundError(err) {
			apierrors.WriteNotFoundError(w, "incident not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":       err.Error(),
			"tenant_id":   tenantUUID.String(),
			"incident_id": incidentID.String(),
		}).Error("Failed to update incident")
		writeIncidentError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(incident)
}

// TransitionIncidentState handles POST /api/v1/incidents/{id}/state
func (h *Handlers) TransitionIncidentState(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := tenantUUIDFromContext(w, r)
	if !ok {
		return
	}

	incidentID, ok := incidentIDFromRequest(w, r)
	if !ok {
		return
	}

	var req models.TransitionIncidentStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateIncidentStateTransition(&req); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	incident, err := h.service.TransitionIncidentState(r.Context(), tenantUUID, incidentID, &req)
	if err != nil {
		if !isIncidentExpectedTransitionError(err) {
			h.logger.WithFields(map[string]interface{}{
				"error":       err.Error(),
				"tenant_id":   tenantUUID.String(),
				"incident_id": incidentID.String(),
			}).Error("Failed to transition incident state")
		}
		writeIncidentError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(incident)
}

// CreateTimelineEntry handles POST /api/v1/incidents/{id}/timeline
func (h *Handlers) CreateTimelineEntry(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := tenantUUIDFromContext(w, r)
	if !ok {
		return
	}

	incidentID, ok := incidentIDFromRequest(w, r)
	if !ok {
		return
	}

	var req models.CreateIncidentTimelineEntryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateCreateIncidentTimelineEntry(&req); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	incident, err := h.service.CreateIncidentTimelineEntry(r.Context(), tenantUUID, incidentID, &req)
	if err != nil {
		if isIncidentNotFoundError(err) {
			apierrors.WriteNotFoundError(w, "incident not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":       err.Error(),
			"tenant_id":   tenantUUID.String(),
			"incident_id": incidentID.String(),
		}).Error("Failed to create incident timeline entry")
		writeIncidentError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(incident)
}

// AttachIncidentAlert handles POST /api/v1/incidents/{id}/alerts
func (h *Handlers) AttachIncidentAlert(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := tenantUUIDFromContext(w, r)
	if !ok {
		return
	}

	incidentID, ok := incidentIDFromRequest(w, r)
	if !ok {
		return
	}

	var req models.AttachIncidentAlertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	alertIDValue := strings.TrimSpace(req.AlertID)
	if alertIDValue == "" {
		apierrors.WriteValidationError(w, "alert ID is required")
		return
	}

	alertID, err := uuid.Parse(alertIDValue)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid alert ID")
		return
	}

	incident, err := h.service.AttachAlert(r.Context(), tenantUUID, incidentID, alertID)
	if err != nil {
		if !isIncidentExpectedMutationError(err) {
			h.logger.WithFields(map[string]interface{}{
				"error":       err.Error(),
				"tenant_id":   tenantUUID.String(),
				"incident_id": incidentID.String(),
				"alert_id":    alertID.String(),
			}).Error("Failed to attach incident alert")
		}
		writeIncidentError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(incident)
}

// DetachIncidentAlert handles DELETE /api/v1/incidents/{id}/alerts/{alertId}
func (h *Handlers) DetachIncidentAlert(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := tenantUUIDFromContext(w, r)
	if !ok {
		return
	}

	incidentID, ok := incidentIDFromRequest(w, r)
	if !ok {
		return
	}

	alertID, ok := requestUUIDParam(w, r, "alertId", "alert")
	if !ok {
		return
	}

	incident, err := h.service.DetachAlert(r.Context(), tenantUUID, incidentID, alertID)
	if err != nil {
		if !isIncidentExpectedMutationError(err) {
			h.logger.WithFields(map[string]interface{}{
				"error":       err.Error(),
				"tenant_id":   tenantUUID.String(),
				"incident_id": incidentID.String(),
				"alert_id":    alertID.String(),
			}).Error("Failed to detach incident alert")
		}
		writeIncidentError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(incident)
}

// AttachIncidentMonitor handles POST /api/v1/incidents/{id}/monitors
func (h *Handlers) AttachIncidentMonitor(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := tenantUUIDFromContext(w, r)
	if !ok {
		return
	}

	incidentID, ok := incidentIDFromRequest(w, r)
	if !ok {
		return
	}

	var req models.AttachIncidentMonitorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	monitorIDValue := strings.TrimSpace(req.MonitorID)
	if monitorIDValue == "" {
		apierrors.WriteValidationError(w, "monitor ID is required")
		return
	}

	monitorID, err := uuid.Parse(monitorIDValue)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	incident, err := h.service.AttachMonitor(r.Context(), tenantUUID, incidentID, monitorID)
	if err != nil {
		if !isIncidentExpectedMutationError(err) {
			h.logger.WithFields(map[string]interface{}{
				"error":       err.Error(),
				"tenant_id":   tenantUUID.String(),
				"incident_id": incidentID.String(),
				"monitor_id":  monitorID.String(),
			}).Error("Failed to attach incident monitor")
		}
		writeIncidentError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(incident)
}

// DetachIncidentMonitor handles DELETE /api/v1/incidents/{id}/monitors/{monitorId}
func (h *Handlers) DetachIncidentMonitor(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := tenantUUIDFromContext(w, r)
	if !ok {
		return
	}

	incidentID, ok := incidentIDFromRequest(w, r)
	if !ok {
		return
	}

	monitorID, ok := requestUUIDParam(w, r, "monitorId", "monitor")
	if !ok {
		return
	}

	incident, err := h.service.DetachMonitor(r.Context(), tenantUUID, incidentID, monitorID)
	if err != nil {
		if !isIncidentExpectedMutationError(err) {
			h.logger.WithFields(map[string]interface{}{
				"error":       err.Error(),
				"tenant_id":   tenantUUID.String(),
				"incident_id": incidentID.String(),
				"monitor_id":  monitorID.String(),
			}).Error("Failed to detach incident monitor")
		}
		writeIncidentError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(incident)
}

// PublishIncidentToStatusPage handles PUT /api/v1/incidents/{id}/status-pages/{statusPageId}
func (h *Handlers) PublishIncidentToStatusPage(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := tenantUUIDFromContext(w, r)
	if !ok {
		return
	}

	incidentID, ok := incidentIDFromRequest(w, r)
	if !ok {
		return
	}

	statusPageID, ok := requestUUIDParam(w, r, "statusPageId", "status page")
	if !ok {
		return
	}

	var req models.UpsertIncidentPublicationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	incident, err := h.service.PublishIncidentToStatusPage(r.Context(), tenantUUID, incidentID, statusPageID, &req)
	if err != nil {
		if !isIncidentExpectedMutationError(err) {
			h.logger.WithFields(map[string]interface{}{
				"error":          err.Error(),
				"tenant_id":      tenantUUID.String(),
				"incident_id":    incidentID.String(),
				"status_page_id": statusPageID.String(),
			}).Error("Failed to publish incident to status page")
		}
		writeIncidentError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(incident)
}

// UnpublishIncidentFromStatusPage handles DELETE /api/v1/incidents/{id}/status-pages/{statusPageId}
func (h *Handlers) UnpublishIncidentFromStatusPage(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := tenantUUIDFromContext(w, r)
	if !ok {
		return
	}

	incidentID, ok := incidentIDFromRequest(w, r)
	if !ok {
		return
	}

	statusPageID, ok := requestUUIDParam(w, r, "statusPageId", "status page")
	if !ok {
		return
	}

	incident, err := h.service.UnpublishIncidentFromStatusPage(r.Context(), tenantUUID, incidentID, statusPageID)
	if err != nil {
		if !isIncidentExpectedMutationError(err) {
			h.logger.WithFields(map[string]interface{}{
				"error":          err.Error(),
				"tenant_id":      tenantUUID.String(),
				"incident_id":    incidentID.String(),
				"status_page_id": statusPageID.String(),
			}).Error("Failed to unpublish incident from status page")
		}
		writeIncidentError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(incident)
}

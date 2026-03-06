package monitors

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	resultservice "github.com/yassinebenameur/probara/api/internal/services/results"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/logger"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

type checkJobPublisher interface {
	PublishJSON(ctx context.Context, subject string, v interface{}, headers map[string][]string) error
}

// Handlers handles monitor HTTP requests
type Handlers struct {
	service       monitorservice.MonitorService
	groupService  groupservice.GroupService
	resultService resultservice.ResultsService
	jobPublisher  checkJobPublisher
	checkSubject  string
	artifactsDir  string
	logger        *logger.Logger
}

// NewHandlers creates a new monitors handler
func NewHandlers(service monitorservice.MonitorService, groupSvc groupservice.GroupService, resultSvc resultservice.ResultsService, log *logger.Logger, artifactsDir string) *Handlers {
	baseDir := strings.TrimSpace(artifactsDir)
	if baseDir == "" {
		baseDir = filepath.Join(os.TempDir(), "probara", "synthetic-browser-artifacts")
	}

	return &Handlers{
		service:       service,
		groupService:  groupSvc,
		resultService: resultSvc,
		checkSubject:  "check.jobs",
		artifactsDir:  baseDir,
		logger:        log,
	}
}

// ConfigureCheckJobs sets the publisher and subject used for on-demand monitor runs.
func (h *Handlers) ConfigureCheckJobs(publisher checkJobPublisher, subject string) {
	h.jobPublisher = publisher
	if strings.TrimSpace(subject) != "" {
		h.checkSubject = strings.TrimSpace(subject)
	}
}

// CreateMonitor handles POST /api/v1/monitors
func (h *Handlers) CreateMonitor(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	var req models.CreateMonitorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateMonitor(&req); err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	monitor, err := h.service.CreateMonitor(r.Context(), tenantUUID, &req)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to create monitor")
		errors.WriteInternalError(w, "failed to create monitor")
		return
	}

	// If this is a group monitor, add its members to the junction table
	if req.Type == models.MonitorTypeGroup {
		var groupConfig models.GroupConfig
		if err := json.Unmarshal(req.Config, &groupConfig); err == nil && len(groupConfig.MonitorIDs) > 0 {
			// Convert string IDs to UUIDs
			monitorIDs := make([]uuid.UUID, 0, len(groupConfig.MonitorIDs))
			for _, idStr := range groupConfig.MonitorIDs {
				if monitorID, err := uuid.Parse(idStr); err == nil {
					monitorIDs = append(monitorIDs, monitorID)
				} else {
					h.logger.WithFields(map[string]interface{}{
						"error":      err.Error(),
						"monitor_id": idStr,
						"tenant_id":  tenantID,
						"group_id":   monitor.ID,
					}).Warn("Invalid monitor ID in group config")
				}
			}

			if len(monitorIDs) > 0 {
				err = h.groupService.AddMonitorsToGroup(r.Context(), tenantUUID, monitor.ID, monitorIDs)
				if err != nil {
					// Log the error but don't fail the creation - the group exists, just without members
					h.logger.WithFields(map[string]interface{}{
						"error":      err.Error(),
						"tenant_id":  tenantID,
						"monitor_id": monitor.ID,
					}).Warn("Failed to add monitors to newly created group")
				}
			}
		}
	}

	h.logger.WithFields(map[string]interface{}{
		"tenant_id":  tenantID,
		"monitor_id": monitor.ID,
		"name":       monitor.Name,
	}).Info("Monitor created")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(monitor)
}

// GetMonitor handles GET /api/v1/monitors/{id}
func (h *Handlers) GetMonitor(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	monitor, err := h.service.GetMonitor(r.Context(), tenantUUID, monitorID)
	if err != nil {
		if err.Error() == "monitor not found" {
			errors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to get monitor")
		errors.WriteInternalError(w, "failed to get monitor")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(monitor)
}

// ListMonitors handles GET /api/v1/monitors
func (h *Handlers) ListMonitors(w http.ResponseWriter, r *http.Request) {
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

	// Parse query parameters
	var tag *string
	if tagStr := r.URL.Query().Get("tag"); tagStr != "" {
		tag = &tagStr
	}

	var enabled *bool
	if enabledStr := r.URL.Query().Get("enabled"); enabledStr != "" {
		enabledVal := enabledStr == "true"
		enabled = &enabledVal
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

	result, err := h.service.ListMonitors(r.Context(), tenantUUID, tag, enabled, page, pageSize)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to list monitors")
		errors.WriteInternalError(w, "failed to list monitors")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// UpdateMonitor handles PATCH /api/v1/monitors/{id}
func (h *Handlers) UpdateMonitor(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	// Get existing monitor for validation
	existingMonitor, err := h.service.GetMonitor(r.Context(), tenantUUID, monitorID)
	if err != nil {
		if err.Error() == "monitor not found" {
			errors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to get monitor")
		errors.WriteInternalError(w, "failed to get monitor")
		return
	}

	var req models.UpdateMonitorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateMonitorUpdate(&req, existingMonitor); err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}

	monitor, err := h.service.UpdateMonitor(r.Context(), tenantUUID, monitorID, &req)
	if err != nil {
		if err.Error() == "monitor not found" {
			errors.WriteNotFoundError(w, "monitor not found")
			return
		}
		if err.Error() == "timeout_seconds must be less than interval_seconds" {
			errors.WriteValidationError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to update monitor")
		errors.WriteInternalError(w, "failed to update monitor")
		return
	}

	// If this is a group monitor, sync the junction table with the new member list
	if existingMonitor.Type == models.MonitorTypeGroup && req.Config != nil {
		var groupConfig models.GroupConfig
		if err := json.Unmarshal(req.Config, &groupConfig); err == nil {
			// Convert string IDs to UUIDs
			newMonitorIDs := make([]uuid.UUID, 0, len(groupConfig.MonitorIDs))
			for _, idStr := range groupConfig.MonitorIDs {
				if monitorID, err := uuid.Parse(idStr); err == nil {
					newMonitorIDs = append(newMonitorIDs, monitorID)
				}
			}

			// Get current members
			currentMembers, err := h.groupService.GetGroupMembers(r.Context(), tenantUUID, monitorID)
			if err == nil {
				// Build map of current member IDs
				currentIDs := make(map[uuid.UUID]bool)
				for _, member := range currentMembers {
					currentIDs[member.ID] = true
				}

				// Build map of new member IDs
				newIDs := make(map[uuid.UUID]bool)
				for _, id := range newMonitorIDs {
					newIDs[id] = true
				}

				// Find IDs to add (in new but not in current)
				var toAdd []uuid.UUID
				for _, id := range newMonitorIDs {
					if !currentIDs[id] {
						toAdd = append(toAdd, id)
					}
				}

				// Find IDs to remove (in current but not in new)
				var toRemove []uuid.UUID
				for _, member := range currentMembers {
					if !newIDs[member.ID] {
						toRemove = append(toRemove, member.ID)
					}
				}

				// Add new members
				if len(toAdd) > 0 {
					if err := h.groupService.AddMonitorsToGroup(r.Context(), tenantUUID, monitorID, toAdd); err != nil {
						h.logger.WithFields(map[string]interface{}{
							"error":      err.Error(),
							"tenant_id":  tenantID,
							"monitor_id": monitorID,
						}).Warn("Failed to add monitors to group during update")
					}
				}

				// Remove old members
				if len(toRemove) > 0 {
					if err := h.groupService.RemoveMonitorsFromGroup(r.Context(), tenantUUID, monitorID, toRemove); err != nil {
						h.logger.WithFields(map[string]interface{}{
							"error":      err.Error(),
							"tenant_id":  tenantID,
							"monitor_id": monitorID,
						}).Warn("Failed to remove monitors from group during update")
					}
				}
			}
		}
	}

	h.logger.WithFields(map[string]interface{}{
		"tenant_id":  tenantID,
		"monitor_id": monitor.ID,
		"name":       monitor.Name,
	}).Info("Monitor updated")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(monitor)
}

// DeleteMonitor handles DELETE /api/v1/monitors/{id}
func (h *Handlers) DeleteMonitor(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	err = h.service.DeleteMonitor(r.Context(), tenantUUID, monitorID)
	if err != nil {
		if err.Error() == "monitor not found" {
			errors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to delete monitor")
		errors.WriteInternalError(w, "failed to delete monitor")
		return
	}

	h.logger.WithFields(map[string]interface{}{
		"tenant_id":  tenantID,
		"monitor_id": monitorID,
	}).Info("Monitor deleted")

	w.WriteHeader(http.StatusNoContent)
}

// GetMonitorResults handles GET /api/v1/monitors/{id}/results
func (h *Handlers) GetMonitorResults(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
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
		if err.Error() == "monitor not found" {
			errors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to get monitor results")
		errors.WriteInternalError(w, "failed to get monitor results")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// GetMonitorAnalytics handles GET /api/v1/monitors/{id}/analytics
func (h *Handlers) GetMonitorAnalytics(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	rangeValue := models.MonitorAnalyticsRange24h
	switch models.MonitorAnalyticsRange(r.URL.Query().Get("range")) {
	case models.MonitorAnalyticsRange1h,
		models.MonitorAnalyticsRange6h,
		models.MonitorAnalyticsRange24h,
		models.MonitorAnalyticsRange7d,
		models.MonitorAnalyticsRange30d,
		models.MonitorAnalyticsRange90d,
		models.MonitorAnalyticsRange365d:
		rangeValue = models.MonitorAnalyticsRange(r.URL.Query().Get("range"))
	}

	response, err := h.resultService.GetMonitorAnalytics(r.Context(), tenantUUID, monitorID, rangeValue)
	if err != nil {
		if err.Error() == "monitor not found" {
			errors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
			"range":      rangeValue,
		}).Error("Failed to get monitor analytics")
		errors.WriteInternalError(w, "failed to get monitor analytics")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// RunMonitorNow handles POST /api/v1/monitors/{id}/run
func (h *Handlers) RunMonitorNow(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	if h.jobPublisher == nil {
		errors.WriteInternalError(w, "on-demand monitor runs are unavailable")
		return
	}

	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	monitor, err := h.service.GetMonitor(r.Context(), tenantUUID, monitorID)
	if err != nil {
		if err.Error() == "monitor not found" {
			errors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID.String(),
		}).Error("Failed to fetch monitor for on-demand run")
		errors.WriteInternalError(w, "failed to fetch monitor")
		return
	}

	if monitor.Type == models.MonitorTypeGroup {
		errors.WriteValidationError(w, "group monitors cannot be run on-demand")
		return
	}

	now := time.Now()
	timeoutSeconds := monitor.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	deadline := now.Add(time.Duration(2*timeoutSeconds) * time.Second)

	payload := sharedmodels.CheckJobPayload{
		MonitorID:      monitor.ID.String(),
		Type:           string(monitor.Type),
		Config:         monitor.Config,
		TimeoutSeconds: timeoutSeconds,
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID.String(),
		}).Error("Failed to marshal check payload")
		errors.WriteInternalError(w, "failed to queue monitor run")
		return
	}

	jobID := uuid.New().String()
	job := sharedmodels.NewJob(jobID, tenantUUID.String(), sharedmodels.JobTypeCheck, "v1", payloadJSON).WithDeadline(deadline)

	if err := h.jobPublisher.PublishJSON(r.Context(), h.checkSubject, job, nil); err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID.String(),
			"subject":    h.checkSubject,
		}).Error("Failed to publish on-demand monitor run job")
		errors.WriteInternalError(w, "failed to queue monitor run")
		return
	}

	resp := models.RunMonitorNowResponse{
		JobID:     jobID,
		MonitorID: monitor.ID,
		QueuedAt:  now,
		Deadline:  deadline,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(resp)
}

// GetSyntheticBrowserScreenshot handles GET /api/v1/monitors/{id}/artifacts/screenshot?path=<relative-path>[&tenant_id=<uuid>]
func (h *Handlers) GetSyntheticBrowserScreenshot(w http.ResponseWriter, r *http.Request) {
	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	tenantUUID, err := getTenantIDForArtifactRequest(r)
	if err != nil {
		errors.WriteUnauthorizedError(w, err.Error())
		return
	}

	monitor, err := h.service.GetMonitor(r.Context(), tenantUUID, monitorID)
	if err != nil {
		if err.Error() == "monitor not found" {
			errors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantUUID.String(),
			"monitor_id": monitorID.String(),
		}).Error("Failed to get monitor for artifact request")
		errors.WriteInternalError(w, "failed to get monitor")
		return
	}

	if monitor.Type != models.MonitorTypeSyntheticBrowser {
		errors.WriteValidationError(w, "artifacts are only available for synthetic_browser monitors")
		return
	}

	rawPath := r.URL.Query().Get("path")
	if strings.TrimSpace(rawPath) == "" {
		errors.WriteValidationError(w, "artifact path is required")
		return
	}

	artifactPath, err := sanitizeArtifactPath(rawPath, monitorID)
	if err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}

	fullPath := filepath.Join(h.artifactsDir, artifactPath)
	fullPath = filepath.Clean(fullPath)
	basePath := filepath.Clean(h.artifactsDir)
	if fullPath != basePath && !strings.HasPrefix(fullPath, basePath+string(filepath.Separator)) {
		errors.WriteValidationError(w, "invalid artifact path")
		return
	}

	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			errors.WriteNotFoundError(w, "artifact not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantUUID.String(),
			"monitor_id": monitorID.String(),
			"path":       fullPath,
		}).Error("Failed to stat synthetic browser artifact")
		errors.WriteInternalError(w, "failed to read artifact")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, fullPath)
}

func getTenantIDForArtifactRequest(r *http.Request) (uuid.UUID, error) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err == nil {
		tenantUUID, parseErr := uuid.Parse(tenantID)
		if parseErr != nil {
			return uuid.Nil, fmt.Errorf("invalid tenant ID")
		}
		return tenantUUID, nil
	}

	fallback := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if fallback == "" {
		return uuid.Nil, fmt.Errorf("tenant ID not found")
	}

	tenantUUID, parseErr := uuid.Parse(fallback)
	if parseErr != nil {
		return uuid.Nil, fmt.Errorf("invalid tenant ID")
	}
	return tenantUUID, nil
}

func sanitizeArtifactPath(raw string, monitorID uuid.UUID) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", fmt.Errorf("artifact path is required")
	}

	clean := filepath.Clean(path)
	if clean == "." || clean == string(filepath.Separator) || filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid artifact path")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid artifact path")
	}

	normalized := filepath.ToSlash(clean)
	expectedPrefix := monitorID.String() + "/"
	if !strings.HasPrefix(normalized, expectedPrefix) {
		return "", fmt.Errorf("artifact path must belong to the requested monitor")
	}

	return clean, nil
}

// AddMonitorsToGroup handles POST /api/v1/monitors/{id}/members
func (h *Handlers) AddMonitorsToGroup(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	groupIDStr := chi.URLParam(r, "id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid group ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	var req models.AddMonitorsToGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if len(req.MonitorIDs) == 0 {
		errors.WriteValidationError(w, "monitor_ids is required and cannot be empty")
		return
	}

	// Convert string IDs to UUIDs
	monitorIDs := make([]uuid.UUID, len(req.MonitorIDs))
	for i, idStr := range req.MonitorIDs {
		monitorID, err := uuid.Parse(idStr)
		if err != nil {
			errors.WriteValidationError(w, fmt.Sprintf("invalid monitor ID: %s", idStr))
			return
		}
		monitorIDs[i] = monitorID
	}

	err = h.groupService.AddMonitorsToGroup(r.Context(), tenantUUID, groupID, monitorIDs)
	if err != nil {
		if err.Error() == "monitor not found" || err.Error() == "monitor is not a group" {
			errors.WriteNotFoundError(w, err.Error())
			return
		}
		if stderrors.Is(err, groupservice.ErrGroupCycleDetected) {
			errors.WriteValidationError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"group_id":  groupID,
		}).Error("Failed to add monitors to group")
		errors.WriteInternalError(w, err.Error())
		return
	}

	h.logger.WithFields(map[string]interface{}{
		"tenant_id":   tenantID,
		"group_id":    groupID,
		"monitor_ids": req.MonitorIDs,
	}).Info("Monitors added to group")

	w.WriteHeader(http.StatusNoContent)
}

// RemoveMonitorsFromGroup handles DELETE /api/v1/monitors/{id}/members
func (h *Handlers) RemoveMonitorsFromGroup(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	groupIDStr := chi.URLParam(r, "id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid group ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	var req models.RemoveMonitorsFromGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if len(req.MonitorIDs) == 0 {
		errors.WriteValidationError(w, "monitor_ids is required and cannot be empty")
		return
	}

	// Convert string IDs to UUIDs
	monitorIDs := make([]uuid.UUID, len(req.MonitorIDs))
	for i, idStr := range req.MonitorIDs {
		monitorID, err := uuid.Parse(idStr)
		if err != nil {
			errors.WriteValidationError(w, fmt.Sprintf("invalid monitor ID: %s", idStr))
			return
		}
		monitorIDs[i] = monitorID
	}

	err = h.groupService.RemoveMonitorsFromGroup(r.Context(), tenantUUID, groupID, monitorIDs)
	if err != nil {
		if err.Error() == "monitor not found" || err.Error() == "monitor is not a group" {
			errors.WriteNotFoundError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"group_id":  groupID,
		}).Error("Failed to remove monitors from group")
		errors.WriteInternalError(w, "failed to remove monitors from group")
		return
	}

	h.logger.WithFields(map[string]interface{}{
		"tenant_id":   tenantID,
		"group_id":    groupID,
		"monitor_ids": req.MonitorIDs,
	}).Info("Monitors removed from group")

	w.WriteHeader(http.StatusNoContent)
}

// GetGroupMembers handles GET /api/v1/monitors/{id}/members
func (h *Handlers) GetGroupMembers(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	groupIDStr := chi.URLParam(r, "id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		errors.WriteValidationError(w, "invalid group ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	members, err := h.groupService.GetGroupMembers(r.Context(), tenantUUID, groupID)
	if err != nil {
		if err.Error() == "monitor not found" || err.Error() == "monitor is not a group" {
			errors.WriteNotFoundError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"group_id":  groupID,
		}).Error("Failed to get group members")
		errors.WriteInternalError(w, "failed to get group members")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(members)
}

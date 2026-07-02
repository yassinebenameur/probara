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
	depservice "github.com/yassinebenameur/probara/api/internal/services/dependencies"
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

// checkRequester is the request-reply surface used for test-connection
// checks. Satisfied by *queue.Client; detected via type assertion in
// ConfigureCheckJobs so existing call sites don't change.
type checkRequester interface {
	Request(ctx context.Context, subject string, data []byte) ([]byte, error)
}

type groupMembershipService interface {
	AddMonitorsToGroup(ctx context.Context, tenantID, groupID uuid.UUID, monitorIDs []uuid.UUID) error
	RemoveMonitorsFromGroup(ctx context.Context, tenantID, groupID uuid.UUID, monitorIDs []uuid.UUID) error
	GetGroupMembers(ctx context.Context, tenantID, groupID uuid.UUID) ([]models.Monitor, error)
}

// Handlers handles monitor HTTP requests
type Handlers struct {
	service           monitorservice.MonitorService
	groupService      groupMembershipService
	dependencyService *depservice.Service
	resultService     resultservice.ResultsService
	jobPublisher      checkJobPublisher
	jobRequester      checkRequester
	checkSubject      string
	artifactsDir      string
	logger            *logger.Logger
}

// ConfigureDependencies sets the service backing the monitor dependency
// endpoints and the depends_on_ids create/update payload field.
func (h *Handlers) ConfigureDependencies(svc *depservice.Service) {
	h.dependencyService = svc
}

// NewHandlers creates a new monitors handler
func NewHandlers(service monitorservice.MonitorService, groupSvc groupMembershipService, resultSvc resultservice.ResultsService, log *logger.Logger, artifactsDir string) *Handlers {
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
	if requester, ok := publisher.(checkRequester); ok {
		h.jobRequester = requester
	}
	if strings.TrimSpace(subject) != "" {
		h.checkSubject = strings.TrimSpace(subject)
	}
}

// TestMonitorConfigRequest is the body for POST /api/v1/monitors/test.
type TestMonitorConfigRequest struct {
	Type           models.MonitorType `json:"type"`
	Config         json.RawMessage    `json:"config"`
	TimeoutSeconds int                `json:"timeout_seconds"`
	// MonitorID resolves write-only secret placeholders ("***") against the
	// stored monitor when testing an edit.
	MonitorID *string `json:"monitor_id,omitempty"`
	// LocationID routes the test to that private location's workers so it
	// runs from the same vantage point as the scheduled checks. Empty =
	// default platform fleet.
	LocationID *string `json:"location_id,omitempty"`
}

// TestMonitorConfig handles POST /api/v1/monitors/test — runs one ephemeral
// check via a worker (NATS request-reply) so a config can be validated before
// saving. Nothing is persisted.
func (h *Handlers) TestMonitorConfig(w http.ResponseWriter, r *http.Request) {
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

	if h.jobRequester == nil {
		errors.WriteInternalError(w, "test checks are not available (queue not configured)")
		return
	}

	var req TestMonitorConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if !validation.DefaultRegistry.Has(req.Type) {
		errors.WriteValidationError(w, "unknown monitor type: "+string(req.Type))
		return
	}
	if len(req.Config) == 0 {
		errors.WriteValidationError(w, "config is required")
		return
	}
	if err := validation.DefaultRegistry.Validate(req.Type, req.Config); err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 10
	}
	if req.TimeoutSeconds > 60 {
		req.TimeoutSeconds = 60
	}

	var monitorID *uuid.UUID
	if req.MonitorID != nil && *req.MonitorID != "" {
		parsed, err := uuid.Parse(*req.MonitorID)
		if err != nil {
			errors.WriteValidationError(w, "invalid monitor_id")
			return
		}
		monitorID = &parsed
	}

	config, err := h.service.ResolveTestConfig(r.Context(), tenantUUID, monitorID, req.Type, req.Config)
	if err != nil {
		if err.Error() == "monitor not found" {
			errors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "tenant_id": tenantID}).Error("Failed to resolve test config")
		errors.WriteInternalError(w, "failed to resolve config")
		return
	}

	payload, err := json.Marshal(sharedmodels.CheckJobPayload{
		Type:           string(req.Type),
		Config:         config,
		TimeoutSeconds: req.TimeoutSeconds,
	})
	if err != nil {
		errors.WriteInternalError(w, "failed to encode test payload")
		return
	}

	testSubject := sharedmodels.TestCheckSubject
	if req.LocationID != nil && *req.LocationID != "" {
		locationID, err := uuid.Parse(*req.LocationID)
		if err != nil {
			errors.WriteValidationError(w, "invalid location_id")
			return
		}
		testSubject = sharedmodels.TestCheckSubjectForLocation(locationID.String())
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.TimeoutSeconds+10)*time.Second)
	defer cancel()
	reply, err := h.jobRequester.Request(ctx, testSubject, payload)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "type": req.Type, "subject": testSubject}).Warn("Test check request failed")
		if testSubject != sharedmodels.TestCheckSubject {
			errors.WriteInternalError(w, "no worker answered at this location — is its worker running and connected?")
			return
		}
		errors.WriteInternalError(w, "no worker answered the test request — is a worker running?")
		return
	}

	var response sharedmodels.TestCheckResponse
	if err := json.Unmarshal(reply, &response); err != nil {
		errors.WriteInternalError(w, "invalid worker response")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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

	// Sync dependencies (best-effort, like group members: a brand-new monitor
	// cannot create a cycle, so failures here are only bad target IDs).
	if len(req.DependsOnIDs) > 0 && req.Type != models.MonitorTypeGroup {
		if err := h.syncDependencies(r.Context(), tenantUUID, monitor.ID, req.DependsOnIDs); err != nil {
			h.logger.WithFields(map[string]interface{}{
				"error":      err.Error(),
				"tenant_id":  tenantID,
				"monitor_id": monitor.ID,
			}).Warn("Failed to set dependencies on newly created monitor")
		} else {
			monitor.DependsOnIDs, _ = h.dependencyService.GetDependsOnIDs(r.Context(), monitor.ID)
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

	// Sync dependencies when the payload carries them (nil = unchanged).
	// Cycles are user-fixable, so they surface as 409 instead of a warn log.
	if req.DependsOnIDs != nil && existingMonitor.Type != models.MonitorTypeGroup {
		if err := h.syncDependencies(r.Context(), tenantUUID, monitorID, *req.DependsOnIDs); err != nil {
			h.writeDependencyError(w, tenantUUID, monitorID, err)
			return
		}
		monitor.DependsOnIDs, _ = h.dependencyService.GetDependsOnIDs(r.Context(), monitorID)
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

// BulkDeleteMonitors handles POST /api/v1/monitors/bulk/delete.
// Soft-deletes the listed monitors; child rows are purged asynchronously.
func (h *Handlers) BulkDeleteMonitors(w http.ResponseWriter, r *http.Request) {
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

	var req models.BulkDeleteMonitorsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	if len(req.MonitorIDs) == 0 {
		errors.WriteValidationError(w, "monitor_ids is required and cannot be empty")
		return
	}

	monitorIDs := make([]uuid.UUID, len(req.MonitorIDs))
	for i, idStr := range req.MonitorIDs {
		monitorID, err := uuid.Parse(idStr)
		if err != nil {
			errors.WriteValidationError(w, fmt.Sprintf("invalid monitor ID: %s", idStr))
			return
		}
		monitorIDs[i] = monitorID
	}

	deleted, err := h.service.BulkDeleteMonitors(r.Context(), tenantUUID, monitorIDs)
	if err != nil {
		if strings.Contains(err.Error(), "not found or do not belong to tenant") {
			errors.WriteValidationError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to bulk delete monitors")
		errors.WriteInternalError(w, "failed to bulk delete monitors")
		return
	}

	h.logger.WithFields(map[string]interface{}{
		"tenant_id":   tenantID,
		"deleted":     deleted,
		"monitor_ids": req.MonitorIDs,
	}).Info("Monitors bulk-deleted")

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.BulkDeleteMonitorsResponse{Deleted: deleted})
}

// DeleteMonitorHistory handles DELETE /api/v1/monitors/{id}/history
func (h *Handlers) DeleteMonitorHistory(w http.ResponseWriter, r *http.Request) {
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

	err = h.service.DeleteMonitorHistory(r.Context(), tenantUUID, monitorID)
	if err != nil {
		switch {
		case err.Error() == "monitor not found":
			errors.WriteNotFoundError(w, "monitor not found")
			return
		}

		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to delete monitor history")
		errors.WriteInternalError(w, "failed to delete monitor history")
		return
	}

	h.logger.WithFields(map[string]interface{}{
		"tenant_id":  tenantID,
		"monitor_id": monitorID,
	}).Info("Monitor history deleted")

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

	// Optional body: {"location_id": "..."} narrows the run to one of the
	// monitor's locations. Default: fan out exactly like the scheduler (all
	// selected locations, or the default fleet when none).
	var runReq struct {
		LocationID *string `json:"location_id,omitempty"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&runReq) // empty body is fine
	}

	locationIDs := []string{""}
	if len(monitor.LocationIDs) > 0 {
		locationIDs = locationIDs[:0]
		for _, id := range monitor.LocationIDs {
			locationIDs = append(locationIDs, id.String())
		}
	}
	if runReq.LocationID != nil && *runReq.LocationID != "" {
		requested, err := uuid.Parse(*runReq.LocationID)
		if err != nil {
			errors.WriteValidationError(w, "invalid location_id")
			return
		}
		found := false
		for _, id := range monitor.LocationIDs {
			if id == requested {
				found = true
				break
			}
		}
		if !found {
			errors.WriteValidationError(w, "location is not selected on this monitor")
			return
		}
		locationIDs = []string{requested.String()}
	}

	now := time.Now()
	timeoutSeconds := monitor.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	deadline := now.Add(time.Duration(2*timeoutSeconds) * time.Second)

	var jobID string
	for _, locationID := range locationIDs {
		payload := sharedmodels.CheckJobPayload{
			MonitorID:      monitor.ID.String(),
			Type:           string(monitor.Type),
			Config:         monitor.Config,
			TimeoutSeconds: timeoutSeconds,
			LocationID:     locationID,
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

		subject := sharedmodels.CheckJobSubjectDefault(h.checkSubject)
		if locationID != "" {
			subject = sharedmodels.CheckJobSubjectForLocation(h.checkSubject, locationID)
		}

		jobID = uuid.New().String()
		job := sharedmodels.NewJob(jobID, tenantUUID.String(), sharedmodels.JobTypeCheck, "v1", payloadJSON).WithDeadline(deadline)

		if err := h.jobPublisher.PublishJSON(r.Context(), subject, job, nil); err != nil {
			h.logger.WithFields(map[string]interface{}{
				"error":      err.Error(),
				"tenant_id":  tenantID,
				"monitor_id": monitorID.String(),
				"subject":    subject,
			}).Error("Failed to publish on-demand monitor run job")
			errors.WriteInternalError(w, "failed to queue monitor run")
			return
		}
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

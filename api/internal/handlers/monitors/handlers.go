package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	depservice "github.com/yassinebenameur/probara/api/internal/services/dependencies"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	resultservice "github.com/yassinebenameur/probara/api/internal/services/results"
	"github.com/yassinebenameur/probara/api/internal/validation"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/secrets"
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

type locationConfigProtector interface {
	ProtectMonitorConfigForWorker(ctx context.Context, tenantID, locationID uuid.UUID, monitorType string, config []byte) ([]byte, error)
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
	locationProtector locationConfigProtector
	checkSubject      string
	artifactsDir      string
	logger            *logger.Logger
}

// ConfigureLocationSecurity wires per-location config envelope protection for
// test and on-demand jobs sent to private workers.
func (h *Handlers) ConfigureLocationSecurity(protector locationConfigProtector) {
	h.locationProtector = protector
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
		checkSubject:  sharedmodels.CheckJobSubject,
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

// CreateMonitor handles POST /api/v1/monitors
func (h *Handlers) CreateMonitor(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	var req models.CreateMonitorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateMonitor(&req); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	monitor, err := h.service.CreateMonitor(r.Context(), tenantUUID, &req)
	if err != nil {
		if errors.Is(err, monitorservice.ErrCallMonitorForbidden) {
			apierrors.WriteForbiddenError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to create monitor")
		apierrors.WriteInternalError(w, "failed to create monitor")
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

	httpx.WriteJSON(w, http.StatusCreated, monitor)
}

// GetMonitor handles GET /api/v1/monitors/{id}
func (h *Handlers) GetMonitor(w http.ResponseWriter, r *http.Request) {
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

	monitor, err := h.service.GetMonitor(r.Context(), tenantUUID, monitorID)
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorNotFound) {
			apierrors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to get monitor")
		apierrors.WriteInternalError(w, "failed to get monitor")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, monitorForResponse(r.Context(), *monitor))
}

// monitorForResponse copies the model so redaction never changes stored state.
func monitorForResponse(ctx context.Context, monitor models.Monitor) models.Monitor {
	if !ctxpkg.CanWrite(ctx) {
		monitor.PushToken = nil
	}
	// Group membership reads load models directly, bypassing the monitor
	// service's normal masking boundary. Neither ciphertext nor plaintext
	// credentials should leave any read endpoint.
	if secrets.HasMonitorSecrets(string(monitor.Type)) {
		masked, err := secrets.MaskMonitorConfig(string(monitor.Type), monitor.Config)
		if err != nil {
			monitor.Config = nil
		} else {
			monitor.Config = masked
		}
	}
	return monitor
}

// ListMonitors handles GET /api/v1/monitors
func (h *Handlers) ListMonitors(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

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

	page, pageSize := httpx.Page(r)

	result, err := h.service.ListMonitors(r.Context(), tenantUUID, tag, enabled, page, pageSize)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to list monitors")
		apierrors.WriteInternalError(w, "failed to list monitors")
		return
	}

	for i := range result.Items {
		result.Items[i] = monitorForResponse(r.Context(), result.Items[i])
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

// UpdateMonitor handles PATCH /api/v1/monitors/{id}
func (h *Handlers) UpdateMonitor(w http.ResponseWriter, r *http.Request) {
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

	// Get existing monitor for validation
	existingMonitor, err := h.service.GetMonitor(r.Context(), tenantUUID, monitorID)
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorNotFound) {
			apierrors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to get monitor")
		apierrors.WriteInternalError(w, "failed to get monitor")
		return
	}

	var req models.UpdateMonitorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if err := validation.ValidateMonitorUpdate(&req, existingMonitor); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	monitor, err := h.service.UpdateMonitor(r.Context(), tenantUUID, monitorID, &req)
	if err != nil {
		if errors.Is(err, monitorservice.ErrCallMonitorForbidden) {
			apierrors.WriteForbiddenError(w, err.Error())
			return
		}
		if errors.Is(err, monitorservice.ErrMonitorNotFound) {
			apierrors.WriteNotFoundError(w, "monitor not found")
			return
		}
		if errors.Is(err, monitorservice.ErrTimeoutNotBelowInterval) {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to update monitor")
		apierrors.WriteInternalError(w, "failed to update monitor")
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

	httpx.WriteJSON(w, http.StatusOK, monitor)
}

// DeleteMonitor handles DELETE /api/v1/monitors/{id}
func (h *Handlers) DeleteMonitor(w http.ResponseWriter, r *http.Request) {
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

	err = h.service.DeleteMonitor(r.Context(), tenantUUID, monitorID)
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorNotFound) {
			apierrors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to delete monitor")
		apierrors.WriteInternalError(w, "failed to delete monitor")
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
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	var req models.BulkDeleteMonitorsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	if len(req.MonitorIDs) == 0 {
		apierrors.WriteValidationError(w, "monitor_ids is required and cannot be empty")
		return
	}

	monitorIDs := make([]uuid.UUID, len(req.MonitorIDs))
	for i, idStr := range req.MonitorIDs {
		monitorID, err := uuid.Parse(idStr)
		if err != nil {
			apierrors.WriteValidationError(w, fmt.Sprintf("invalid monitor ID: %s", idStr))
			return
		}
		monitorIDs[i] = monitorID
	}

	deleted, err := h.service.BulkDeleteMonitors(r.Context(), tenantUUID, monitorIDs)
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorsNotInTenant) {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to bulk delete monitors")
		apierrors.WriteInternalError(w, "failed to bulk delete monitors")
		return
	}

	h.logger.WithFields(map[string]interface{}{
		"tenant_id":   tenantID,
		"deleted":     deleted,
		"monitor_ids": req.MonitorIDs,
	}).Info("Monitors bulk-deleted")

	httpx.WriteJSON(w, http.StatusOK, models.BulkDeleteMonitorsResponse{Deleted: deleted})
}

// DeleteMonitorHistory handles DELETE /api/v1/monitors/{id}/history
func (h *Handlers) DeleteMonitorHistory(w http.ResponseWriter, r *http.Request) {
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

	err = h.service.DeleteMonitorHistory(r.Context(), tenantUUID, monitorID)
	if err != nil {
		switch {
		case errors.Is(err, monitorservice.ErrMonitorNotFound):
			apierrors.WriteNotFoundError(w, "monitor not found")
			return
		}

		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to delete monitor history")
		apierrors.WriteInternalError(w, "failed to delete monitor history")
		return
	}

	h.logger.WithFields(map[string]interface{}{
		"tenant_id":  tenantID,
		"monitor_id": monitorID,
	}).Info("Monitor history deleted")

	w.WriteHeader(http.StatusNoContent)
}

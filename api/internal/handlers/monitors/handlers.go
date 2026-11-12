package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	depservice "github.com/yassinebenameur/probara/api/internal/services/dependencies"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
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

// TestMonitorConfigRequest is the body for POST /api/v1/monitors/test.
// RunMonitorNowRequest is the optional body of POST /monitors/{id}/run.
type RunMonitorNowRequest struct {
	LocationID *string `json:"location_id,omitempty"`
}

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
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	if h.jobRequester == nil {
		apierrors.WriteInternalError(w, "test checks are not available (queue not configured)")
		return
	}

	var req TestMonitorConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if !validation.DefaultRegistry.Has(req.Type) {
		apierrors.WriteValidationError(w, "unknown monitor type: "+string(req.Type))
		return
	}
	if len(req.Config) == 0 {
		apierrors.WriteValidationError(w, "config is required")
		return
	}
	if err := validation.DefaultRegistry.Validate(req.Type, req.Config); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}
	// A test of a test-call config places a real call.
	if err := monitorservice.RequireCallPermission(r.Context(), req.Type, req.Config); err != nil {
		apierrors.WriteForbiddenError(w, err.Error())
		return
	}
	if containsEncryptedTestSecret(req.Type, req.Config) {
		apierrors.WriteValidationError(w, "encrypted credentials cannot be supplied in test requests; use saved secret placeholders")
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
			apierrors.WriteValidationError(w, "invalid monitor_id")
			return
		}
		monitorID = &parsed
	}

	// Custom destinations must not turn a read-only test into an oracle for
	// saved credentials. Tests using only caller-supplied credentials remain safe.
	if monitorID != nil && secrets.HasMonitorSecrets(string(req.Type)) && !ctxpkg.CanWrite(r.Context()) {
		apierrors.WriteForbiddenError(w, "testing with saved credentials requires write permission")
		return
	}
	config, err := h.service.ResolveTestConfig(r.Context(), tenantUUID, monitorID, req.Type, req.Config)
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorTypeMismatch) {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
		if errors.Is(err, monitorservice.ErrMonitorNotFound) {
			apierrors.WriteNotFoundError(w, "monitor not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "tenant_id": tenantID}).Error("Failed to resolve test config")
		apierrors.WriteInternalError(w, "failed to resolve config")
		return
	}

	testSubject := sharedmodels.TestCheckSubject
	if req.LocationID != nil && *req.LocationID != "" {
		locationID, err := uuid.Parse(*req.LocationID)
		if err != nil {
			apierrors.WriteValidationError(w, "invalid location_id")
			return
		}
		if h.locationProtector == nil {
			apierrors.WriteInternalError(w, "private location security is unavailable")
			return
		}
		config, err = h.locationProtector.ProtectMonitorConfigForWorker(r.Context(), tenantUUID, locationID, string(req.Type), config)
		if err != nil {
			h.logger.WithError(err).Warn("Failed to protect private-location test config")
			apierrors.WriteInternalError(w, "failed to protect test config")
			return
		}
		testSubject = sharedmodels.TestCheckSubjectForLocation(locationID.String())
	}

	payload, err := json.Marshal(sharedmodels.CheckJobPayload{
		Type:           string(req.Type),
		Config:         config,
		TimeoutSeconds: req.TimeoutSeconds,
	})
	if err != nil {
		apierrors.WriteInternalError(w, "failed to encode test payload")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.TimeoutSeconds+10)*time.Second)
	defer cancel()
	reply, err := h.jobRequester.Request(ctx, testSubject, payload)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "type": req.Type, "subject": testSubject}).Warn("Test check request failed")
		if testSubject != sharedmodels.TestCheckSubject {
			apierrors.WriteInternalError(w, "no worker answered at this location — is its worker running and connected?")
			return
		}
		apierrors.WriteInternalError(w, "no worker answered the test request — is a worker running?")
		return
	}

	var response sharedmodels.TestCheckResponse
	if err := json.Unmarshal(reply, &response); err != nil {
		apierrors.WriteInternalError(w, "invalid worker response")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, response)
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

func containsEncryptedTestSecret(monitorType models.MonitorType, config json.RawMessage) bool {
	var fields map[string]any
	if json.Unmarshal(config, &fields) != nil {
		return false // The type validator reports malformed config separately.
	}
	for _, field := range secrets.MonitorSecretFields[string(monitorType)] {
		if value, ok := fields[field].(string); ok && secrets.LooksLikeEnvelope(value) {
			return true
		}
	}
	for _, field := range secrets.MonitorSecretMapFields[string(monitorType)] {
		values, _ := fields[field].(map[string]any)
		for _, raw := range values {
			if value, ok := raw.(string); ok && secrets.LooksLikeEnvelope(value) {
				return true
			}
		}
	}
	return false
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

// RunMonitorNow handles POST /api/v1/monitors/{id}/run
func (h *Handlers) RunMonitorNow(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	if h.jobPublisher == nil {
		apierrors.WriteInternalError(w, "on-demand monitor runs are unavailable")
		return
	}

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
			"monitor_id": monitorID.String(),
		}).Error("Failed to fetch monitor for on-demand run")
		apierrors.WriteInternalError(w, "failed to fetch monitor")
		return
	}

	if sharedmodels.IsPassiveMonitorType(string(monitor.Type)) {
		apierrors.WriteValidationError(w, fmt.Sprintf("%s monitors cannot be run on-demand", monitor.Type))
		return
	}
	// Running a test-call monitor places a real call from every selected
	// location.
	if err := monitorservice.RequireCallPermission(r.Context(), monitor.Type, monitor.Config); err != nil {
		apierrors.WriteForbiddenError(w, err.Error())
		return
	}

	// Optional body: {"location_id": "..."} narrows the run to one of the
	// monitor's locations. Default: fan out exactly like the scheduler (all
	// selected locations, or the default fleet when none).
	var runReq RunMonitorNowRequest
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
			apierrors.WriteValidationError(w, "invalid location_id")
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
			apierrors.WriteValidationError(w, "location is not selected on this monitor")
			return
		}
		locationIDs = []string{requested.String()}
	}

	// GetMonitor masks secret config fields ("***") for API responses; resolve
	// the placeholders back to the stored (still encrypted) values so the
	// worker can actually authenticate.
	checkConfig, err := h.service.ResolveTestConfig(r.Context(), tenantUUID, &monitorID, monitor.Type, monitor.Config)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID.String(),
		}).Error("Failed to resolve monitor config for on-demand run")
		apierrors.WriteInternalError(w, "failed to queue monitor run")
		return
	}

	now := time.Now()
	timeoutSeconds := monitor.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	deadline := now.Add(time.Duration(2*timeoutSeconds) * time.Second)

	var jobID string
	for _, locationID := range locationIDs {
		locationConfig := checkConfig
		if locationID != "" {
			if h.locationProtector == nil {
				apierrors.WriteInternalError(w, "private location security is unavailable")
				return
			}
			parsedLocationID, err := uuid.Parse(locationID)
			if err != nil {
				apierrors.WriteInternalError(w, "invalid stored location ID")
				return
			}
			locationConfig, err = h.locationProtector.ProtectMonitorConfigForWorker(
				r.Context(), tenantUUID, parsedLocationID, string(monitor.Type), checkConfig,
			)
			if err != nil {
				h.logger.WithFields(map[string]interface{}{"error": err.Error(), "location_id": locationID}).Error("Failed to protect private-location monitor config")
				apierrors.WriteInternalError(w, "failed to protect monitor config")
				return
			}
		}
		payload := sharedmodels.CheckJobPayload{
			MonitorID:      monitor.ID.String(),
			Type:           string(monitor.Type),
			Config:         locationConfig,
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
			apierrors.WriteInternalError(w, "failed to queue monitor run")
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
			apierrors.WriteInternalError(w, "failed to queue monitor run")
			return
		}
	}

	resp := models.RunMonitorNowResponse{
		JobID:     jobID,
		MonitorID: monitor.ID,
		QueuedAt:  now,
		Deadline:  deadline,
	}

	httpx.WriteJSON(w, http.StatusAccepted, resp)
}

// GetSyntheticBrowserScreenshot handles GET /api/v1/monitors/{id}/artifacts/screenshot?path=<relative-path>[&tenant_id=<uuid>]
func (h *Handlers) GetSyntheticBrowserScreenshot(w http.ResponseWriter, r *http.Request) {
	monitorIDStr := chi.URLParam(r, "id")
	monitorID, err := uuid.Parse(monitorIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	tenantUUID, ok := artifactTenantID(w, r)
	if !ok {
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
			"tenant_id":  tenantUUID.String(),
			"monitor_id": monitorID.String(),
		}).Error("Failed to get monitor for artifact request")
		apierrors.WriteInternalError(w, "failed to get monitor")
		return
	}

	if monitor.Type != models.MonitorTypeSyntheticBrowser {
		apierrors.WriteValidationError(w, "artifacts are only available for synthetic_browser monitors")
		return
	}

	rawPath := r.URL.Query().Get("path")
	if strings.TrimSpace(rawPath) == "" {
		apierrors.WriteValidationError(w, "artifact path is required")
		return
	}

	artifactPath, err := sanitizeArtifactPath(rawPath, monitorID)
	if err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}

	fullPath := filepath.Join(h.artifactsDir, artifactPath)
	fullPath = filepath.Clean(fullPath)
	basePath := filepath.Clean(h.artifactsDir)
	if fullPath != basePath && !strings.HasPrefix(fullPath, basePath+string(filepath.Separator)) {
		apierrors.WriteValidationError(w, "invalid artifact path")
		return
	}

	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			apierrors.WriteNotFoundError(w, "artifact not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantUUID.String(),
			"monitor_id": monitorID.String(),
			"path":       fullPath,
		}).Error("Failed to stat synthetic browser artifact")
		apierrors.WriteInternalError(w, "failed to read artifact")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, fullPath)
}

// artifactTenantID is httpx.TenantID plus a fallback the other handlers do not
// have: when authentication attached no tenant, it reads ?tenant_id=.
func artifactTenantID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if _, err := middleware.GetTenantID(r.Context()); err == nil {
		return httpx.TenantID(w, r)
	}

	fallback := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if fallback == "" {
		apierrors.WriteUnauthorizedError(w, "tenant ID not found")
		return uuid.Nil, false
	}

	tenantUUID, err := uuid.Parse(fallback)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid tenant ID")
		return uuid.Nil, false
	}
	return tenantUUID, true
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
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	groupIDStr := chi.URLParam(r, "id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid group ID")
		return
	}

	var req models.AddMonitorsToGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if len(req.MonitorIDs) == 0 {
		apierrors.WriteValidationError(w, "monitor_ids is required and cannot be empty")
		return
	}

	// Convert string IDs to UUIDs
	monitorIDs := make([]uuid.UUID, len(req.MonitorIDs))
	for i, idStr := range req.MonitorIDs {
		monitorID, err := uuid.Parse(idStr)
		if err != nil {
			apierrors.WriteValidationError(w, fmt.Sprintf("invalid monitor ID: %s", idStr))
			return
		}
		monitorIDs[i] = monitorID
	}

	err = h.groupService.AddMonitorsToGroup(r.Context(), tenantUUID, groupID, monitorIDs)
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorNotFound) || errors.Is(err, groupservice.ErrNotGroup) {
			apierrors.WriteNotFoundError(w, err.Error())
			return
		}
		if errors.Is(err, groupservice.ErrGroupCycleDetected) {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"group_id":  groupID,
		}).Error("Failed to add monitors to group")
		apierrors.WriteInternalError(w, err.Error())
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
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	groupIDStr := chi.URLParam(r, "id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid group ID")
		return
	}

	var req models.RemoveMonitorsFromGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if len(req.MonitorIDs) == 0 {
		apierrors.WriteValidationError(w, "monitor_ids is required and cannot be empty")
		return
	}

	// Convert string IDs to UUIDs
	monitorIDs := make([]uuid.UUID, len(req.MonitorIDs))
	for i, idStr := range req.MonitorIDs {
		monitorID, err := uuid.Parse(idStr)
		if err != nil {
			apierrors.WriteValidationError(w, fmt.Sprintf("invalid monitor ID: %s", idStr))
			return
		}
		monitorIDs[i] = monitorID
	}

	err = h.groupService.RemoveMonitorsFromGroup(r.Context(), tenantUUID, groupID, monitorIDs)
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorNotFound) || errors.Is(err, groupservice.ErrNotGroup) {
			apierrors.WriteNotFoundError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"group_id":  groupID,
		}).Error("Failed to remove monitors from group")
		apierrors.WriteInternalError(w, "failed to remove monitors from group")
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
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	groupIDStr := chi.URLParam(r, "id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid group ID")
		return
	}

	members, err := h.groupService.GetGroupMembers(r.Context(), tenantUUID, groupID)
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorNotFound) || errors.Is(err, groupservice.ErrNotGroup) {
			apierrors.WriteNotFoundError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"group_id":  groupID,
		}).Error("Failed to get group members")
		apierrors.WriteInternalError(w, "failed to get group members")
		return
	}

	for i := range members {
		members[i] = monitorForResponse(r.Context(), members[i])
	}
	httpx.WriteJSON(w, http.StatusOK, members)
}

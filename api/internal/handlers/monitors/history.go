package monitors

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/api/internal/validation"
)

// monitorVersionStore is the config version history the history routes
// read (monitorservice.VersionStore).
type monitorVersionStore interface {
	List(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.MonitorVersionListResponse, error)
	Get(ctx context.Context, tenantID, monitorID uuid.UUID, version int) (*models.MonitorVersion, error)
	Diff(ctx context.Context, tenantID, monitorID uuid.UUID, version int, against string) (*models.MonitorVersionDiffResponse, error)
	LiveReferences(ctx context.Context, tenantID uuid.UUID, snapshot models.MonitorConfigSnapshot) (models.MonitorConfigSnapshot, []string, error)
}

// versionRecorder is implemented by the monitors service; mocks without it
// record nothing.
type versionRecorder interface {
	RecordVersion(ctx context.Context, tenantID, monitorID uuid.UUID)
}

// ConfigureVersions sets the store behind the /monitors/{id}/versions routes.
func (h *Handlers) ConfigureVersions(store monitorVersionStore) {
	h.versions = store
}

// deferVersions makes the request's monitor writes queue their config
// version until the handler calls Flush, so a create or update that also
// syncs members and dependencies records one version, not two.
func (h *Handlers) deferVersions(r *http.Request) (*http.Request, *monitorservice.VersionBatch) {
	ctx, batch := monitorservice.DeferVersions(r.Context())
	return r.WithContext(ctx), batch
}

// recordVersion records (or queues) a version after a write that does not
// go through the service's create/update: member and dependency edges.
func (h *Handlers) recordVersion(ctx context.Context, tenantID, monitorID uuid.UUID) {
	if recorder, ok := h.service.(versionRecorder); ok {
		recorder.RecordVersion(ctx, tenantID, monitorID)
	}
}

// syncGroupMembersFromConfig makes a group's monitor_groups rows match the
// config.monitor_ids of an update (membership is the junction table, never
// the config). Failures are logged, not returned: the group update itself
// has been applied.
func (h *Handlers) syncGroupMembersFromConfig(ctx context.Context, tenantUUID, monitorID uuid.UUID, config json.RawMessage) {
	var groupConfig models.GroupConfig
	if err := json.Unmarshal(config, &groupConfig); err != nil {
		return
	}
	// Convert string IDs to UUIDs
	newMonitorIDs := make([]uuid.UUID, 0, len(groupConfig.MonitorIDs))
	for _, idStr := range groupConfig.MonitorIDs {
		if memberID, err := uuid.Parse(idStr); err == nil {
			newMonitorIDs = append(newMonitorIDs, memberID)
		}
	}

	currentMembers, err := h.groupService.GetGroupMembers(ctx, tenantUUID, monitorID)
	if err != nil {
		return
	}
	currentIDs := make(map[uuid.UUID]bool)
	for _, member := range currentMembers {
		currentIDs[member.ID] = true
	}
	newIDs := make(map[uuid.UUID]bool)
	for _, id := range newMonitorIDs {
		newIDs[id] = true
	}

	// In new but not in current
	var toAdd []uuid.UUID
	for _, id := range newMonitorIDs {
		if !currentIDs[id] {
			toAdd = append(toAdd, id)
		}
	}
	// In current but not in new
	var toRemove []uuid.UUID
	for _, member := range currentMembers {
		if !newIDs[member.ID] {
			toRemove = append(toRemove, member.ID)
		}
	}

	if len(toAdd) > 0 {
		if err := h.groupService.AddMonitorsToGroup(ctx, tenantUUID, monitorID, toAdd); err != nil {
			h.logger.WithFields(map[string]interface{}{
				"error":      err.Error(),
				"tenant_id":  tenantUUID,
				"monitor_id": monitorID,
			}).Warn("Failed to add monitors to group during update")
		}
	}
	if len(toRemove) > 0 {
		if err := h.groupService.RemoveMonitorsFromGroup(ctx, tenantUUID, monitorID, toRemove); err != nil {
			h.logger.WithFields(map[string]interface{}{
				"error":      err.Error(),
				"tenant_id":  tenantUUID,
				"monitor_id": monitorID,
			}).Warn("Failed to remove monitors from group during update")
		}
	}
}

// historyRequest resolves the tenant and monitor of a /versions route and
// checks the monitor exists. ok is false once an error has been written.
func (h *Handlers) historyRequest(w http.ResponseWriter, r *http.Request) (tenantUUID uuid.UUID, monitor *models.Monitor, ok bool) {
	if h.versions == nil {
		errors.WriteInternalError(w, "monitor version history is not configured")
		return uuid.Nil, nil, false
	}
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return uuid.Nil, nil, false
	}
	tenantUUID, err = uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return uuid.Nil, nil, false
	}
	monitorID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		errors.WriteValidationError(w, "invalid monitor ID")
		return uuid.Nil, nil, false
	}
	monitor, err = h.service.GetMonitor(r.Context(), tenantUUID, monitorID)
	if err != nil {
		if err.Error() == "monitor not found" {
			errors.WriteNotFoundError(w, "monitor not found")
			return uuid.Nil, nil, false
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to get monitor")
		errors.WriteInternalError(w, "failed to get monitor")
		return uuid.Nil, nil, false
	}
	return tenantUUID, monitor, true
}

func versionParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || version < 1 {
		errors.WriteValidationError(w, "invalid version")
		return 0, false
	}
	return version, true
}

func (h *Handlers) writeVersionError(w http.ResponseWriter, err error, monitorID uuid.UUID) {
	switch {
	case stderrors.Is(err, monitorservice.ErrVersionNotFound):
		errors.WriteNotFoundError(w, "version not found (it may have been pruned: each monitor keeps its last "+
			strconv.Itoa(monitorservice.MaxVersionsPerMonitor)+")")
	case stderrors.Is(err, monitorservice.ErrInvalidAgainst):
		errors.WriteValidationError(w, err.Error())
	default:
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"monitor_id": monitorID,
		}).Error("Failed to read monitor versions")
		errors.WriteInternalError(w, "failed to read monitor versions")
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ListMonitorVersions handles GET /api/v1/monitors/{id}/versions.
func (h *Handlers) ListMonitorVersions(w http.ResponseWriter, r *http.Request) {
	tenantUUID, monitor, ok := h.historyRequest(w, r)
	if !ok {
		return
	}
	resp, err := h.versions.List(r.Context(), tenantUUID, monitor.ID)
	if err != nil {
		h.writeVersionError(w, err, monitor.ID)
		return
	}
	writeJSON(w, resp)
}

// GetMonitorVersion handles GET /api/v1/monitors/{id}/versions/{version}.
func (h *Handlers) GetMonitorVersion(w http.ResponseWriter, r *http.Request) {
	tenantUUID, monitor, ok := h.historyRequest(w, r)
	if !ok {
		return
	}
	version, ok := versionParam(w, r)
	if !ok {
		return
	}
	resp, err := h.versions.Get(r.Context(), tenantUUID, monitor.ID, version)
	if err != nil {
		h.writeVersionError(w, err, monitor.ID)
		return
	}
	writeJSON(w, resp)
}

// DiffMonitorVersion handles GET /api/v1/monitors/{id}/versions/{version}/diff
// (?against=<version>|current; default the previous version).
func (h *Handlers) DiffMonitorVersion(w http.ResponseWriter, r *http.Request) {
	tenantUUID, monitor, ok := h.historyRequest(w, r)
	if !ok {
		return
	}
	version, ok := versionParam(w, r)
	if !ok {
		return
	}
	resp, err := h.versions.Diff(r.Context(), tenantUUID, monitor.ID, version, r.URL.Query().Get("against"))
	if err != nil {
		h.writeVersionError(w, err, monitor.ID)
		return
	}
	writeJSON(w, resp)
}

// RestoreMonitorVersion handles POST /api/v1/monitors/{id}/versions/{version}/restore.
//
// It re-applies the version's snapshot through the same path as a PATCH:
// validation, UpdateMonitor (test-call gate, write-only secret merge), then
// the group member and dependency syncs. The snapshot is masked, so every
// secret keeps its current value, including secret map keys added since the
// version: a restore never restores or removes a secret. References that no
// longer exist are dropped and reported (a deleted escalation policy falls
// back to notification_mode 'default').
func (h *Handlers) RestoreMonitorVersion(w http.ResponseWriter, r *http.Request) {
	tenantUUID, existing, ok := h.historyRequest(w, r)
	if !ok {
		return
	}
	version, ok := versionParam(w, r)
	if !ok {
		return
	}
	stored, err := h.versions.Get(r.Context(), tenantUUID, existing.ID, version)
	if err != nil {
		h.writeVersionError(w, err, existing.ID)
		return
	}
	snapshot, dropped, err := h.versions.LiveReferences(r.Context(), tenantUUID, stored.Snapshot)
	if err != nil {
		h.writeVersionError(w, err, existing.ID)
		return
	}
	req, err := monitorservice.RestoreRequest(snapshot, existing.Type, existing.Config)
	if err != nil {
		h.writeVersionError(w, err, existing.ID)
		return
	}
	if err := validation.ValidateMonitorUpdate(req, existing); err != nil {
		errors.WriteValidationError(w, fmt.Sprintf("version %d cannot be restored: %s", version, err.Error()))
		return
	}

	r = r.WithContext(monitorservice.WithVersionRestore(r.Context(), version))
	r, versions := h.deferVersions(r)
	defer versions.Flush(r.Context())

	if _, err := h.service.UpdateMonitor(r.Context(), tenantUUID, existing.ID, req); err != nil {
		switch {
		case stderrors.Is(err, monitorservice.ErrCallMonitorForbidden):
			errors.WriteForbiddenError(w, err.Error())
		case stderrors.Is(err, monitorservice.ErrSecretConflict),
			stderrors.Is(err, monitorservice.ErrEscalationPolicyNotFound):
			errors.WriteValidationError(w, fmt.Sprintf("version %d cannot be restored: %s", version, err.Error()))
		case err.Error() == "timeout_seconds must be less than interval_seconds":
			errors.WriteValidationError(w, err.Error())
		default:
			h.logger.WithFields(map[string]interface{}{
				"error":      err.Error(),
				"tenant_id":  tenantUUID,
				"monitor_id": existing.ID,
				"version":    version,
			}).Error("Failed to restore monitor version")
			errors.WriteInternalError(w, "failed to restore monitor version")
		}
		return
	}

	if snapshot.Type == models.MonitorTypeGroup {
		h.syncGroupMembersFromConfig(r.Context(), tenantUUID, existing.ID, req.Config)
	}
	if req.DependsOnIDs != nil {
		if err := h.syncDependencies(r.Context(), tenantUUID, existing.ID, *req.DependsOnIDs); err != nil {
			h.writeDependencyError(w, tenantUUID, existing.ID, err)
			return
		}
	}
	h.recordVersion(r.Context(), tenantUUID, existing.ID)
	versions.Flush(r.Context())

	monitor, err := h.service.GetMonitor(r.Context(), tenantUUID, existing.ID)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantUUID,
			"monitor_id": existing.ID,
		}).Error("Failed to reload restored monitor")
		errors.WriteInternalError(w, "version restored, but the monitor could not be reloaded")
		return
	}
	restored := monitorForResponse(r.Context(), *monitor)
	writeJSON(w, models.RestoreMonitorVersionResponse{
		Monitor:           &restored,
		RestoredFrom:      version,
		DroppedReferences: dropped,
	})
}

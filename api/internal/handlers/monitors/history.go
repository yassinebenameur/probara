// Configuration history: listing, comparing and restoring a monitor's
// recorded versions (api/internal/services/monitors/versions.go).

package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/api/internal/validation"
)

// versionHistory is implemented by the Postgres-backed monitors service;
// test doubles without it record and serve no history.
type versionHistory interface {
	RecordVersion(ctx context.Context, tenantID, monitorID uuid.UUID)
	Versions() *monitorservice.VersionStore
}

func (h *Handlers) versionStore() *monitorservice.VersionStore {
	if history, ok := h.service.(versionHistory); ok {
		return history.Versions()
	}
	return nil
}

// deferVersions makes the request's monitor saves queue their version until
// the handler flushes, so a create or update that also syncs members and
// dependencies records one version, not two.
func (h *Handlers) deferVersions(r *http.Request) (*http.Request, *monitorservice.VersionBatch) {
	ctx, batch := monitorservice.DeferVersions(r.Context())
	return r.WithContext(ctx), batch
}

// recordVersion records (or queues) a version after a write that does not
// go through the service's create/update: member and dependency edges.
func (h *Handlers) recordVersion(ctx context.Context, tenantID, monitorID uuid.UUID) {
	if history, ok := h.service.(versionHistory); ok {
		history.RecordVersion(ctx, tenantID, monitorID)
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
// checks the monitor exists in the tenant. ok is false once an error has
// been written.
func (h *Handlers) historyRequest(w http.ResponseWriter, r *http.Request) (store *monitorservice.VersionStore, tenantUUID uuid.UUID, monitor *models.Monitor, ok bool) {
	store = h.versionStore()
	if store == nil {
		apierrors.WriteInternalError(w, "monitor configuration history is not configured")
		return nil, uuid.Nil, nil, false
	}
	tenantUUID, ok = httpx.TenantID(w, r)
	if !ok {
		return nil, uuid.Nil, nil, false
	}
	monitorID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid monitor ID")
		return nil, uuid.Nil, nil, false
	}
	monitor, err = h.service.GetMonitor(r.Context(), tenantUUID, monitorID)
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorNotFound) {
			apierrors.WriteNotFoundError(w, "monitor not found")
			return nil, uuid.Nil, nil, false
		}
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantUUID,
			"monitor_id": monitorID,
		}).Error("Failed to get monitor")
		apierrors.WriteInternalError(w, "failed to get monitor")
		return nil, uuid.Nil, nil, false
	}
	return store, tenantUUID, monitor, true
}

func versionParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || version < 1 {
		apierrors.WriteValidationError(w, "invalid version")
		return 0, false
	}
	return version, true
}

func (h *Handlers) writeVersionError(w http.ResponseWriter, err error, monitorID uuid.UUID) {
	switch {
	case errors.Is(err, monitorservice.ErrVersionNotFound):
		apierrors.WriteNotFoundError(w, "version not found (each monitor keeps its last "+
			strconv.Itoa(monitorservice.MaxVersionsPerMonitor)+" versions)")
	case errors.Is(err, monitorservice.ErrInvalidAgainst):
		apierrors.WriteValidationError(w, err.Error())
	default:
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"monitor_id": monitorID,
		}).Error("Failed to read monitor versions")
		apierrors.WriteInternalError(w, "failed to read monitor versions")
	}
}

// ListMonitorVersions handles GET /api/v1/monitors/{id}/versions (paginated,
// newest first).
func (h *Handlers) ListMonitorVersions(w http.ResponseWriter, r *http.Request) {
	store, tenantUUID, monitor, ok := h.historyRequest(w, r)
	if !ok {
		return
	}
	page, pageSize := httpx.Page(r)
	resp, err := store.List(r.Context(), tenantUUID, monitor.ID, page, pageSize)
	if err != nil {
		h.writeVersionError(w, err, monitor.ID)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// GetMonitorVersion handles GET /api/v1/monitors/{id}/versions/{version}.
func (h *Handlers) GetMonitorVersion(w http.ResponseWriter, r *http.Request) {
	store, tenantUUID, monitor, ok := h.historyRequest(w, r)
	if !ok {
		return
	}
	version, ok := versionParam(w, r)
	if !ok {
		return
	}
	resp, err := store.Get(r.Context(), tenantUUID, monitor.ID, version)
	if err != nil {
		h.writeVersionError(w, err, monitor.ID)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// DiffMonitorVersion handles GET /api/v1/monitors/{id}/versions/{version}/diff
// (?against=<version>|current; default the previous version).
func (h *Handlers) DiffMonitorVersion(w http.ResponseWriter, r *http.Request) {
	store, tenantUUID, monitor, ok := h.historyRequest(w, r)
	if !ok {
		return
	}
	version, ok := versionParam(w, r)
	if !ok {
		return
	}
	resp, err := store.Diff(r.Context(), tenantUUID, monitor.ID, version, r.URL.Query().Get("against"))
	if err != nil {
		h.writeVersionError(w, err, monitor.ID)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// RestoreMonitorVersion handles POST /api/v1/monitors/{id}/versions/{version}/restore.
//
// It re-applies the version through the same path as a PATCH: validation,
// UpdateMonitor (test-call gate, write-only secret merge, credential header
// normalization), then the group member and dependency syncs, and records a
// new version that names the one restored. The snapshot is masked, so every
// secret keeps its current value, including secret map keys added since the
// version; a secret the version had and the monitor no longer holds is listed
// in secrets_to_reenter. References that no longer exist are dropped and
// listed in dropped_references. The pause state is not part of a version and
// is left as it is.
func (h *Handlers) RestoreMonitorVersion(w http.ResponseWriter, r *http.Request) {
	store, tenantUUID, existing, ok := h.historyRequest(w, r)
	if !ok {
		return
	}
	version, ok := versionParam(w, r)
	if !ok {
		return
	}
	stored, err := store.Get(r.Context(), tenantUUID, existing.ID, version)
	if err != nil {
		h.writeVersionError(w, err, existing.ID)
		return
	}
	current, err := store.CurrentSnapshot(r.Context(), tenantUUID, existing.ID)
	if err != nil {
		h.writeVersionError(w, err, existing.ID)
		return
	}
	snapshot, dropped, err := store.LiveReferences(r.Context(), tenantUUID, stored.Snapshot)
	if err != nil {
		h.writeVersionError(w, err, existing.ID)
		return
	}
	req, changed, err := monitorservice.RestoreRequest(snapshot, current)
	if err != nil {
		h.writeVersionError(w, err, existing.ID)
		return
	}
	resp := models.RestoreMonitorVersionResponse{
		RestoredFrom:      version,
		DroppedReferences: dropped,
		SecretsToReenter:  monitorservice.SecretsToReenter(snapshot, existing.Config),
	}

	if changed {
		if err := validation.ValidateMonitorUpdate(req, existing); err != nil {
			apierrors.WriteValidationError(w, fmt.Sprintf("version %d cannot be restored: %s", version, err.Error()))
			return
		}

		r = r.WithContext(monitorservice.WithVersionRestore(r.Context(), version))
		var versions *monitorservice.VersionBatch
		r, versions = h.deferVersions(r)
		defer versions.Flush(r.Context())

		if _, err := h.service.UpdateMonitor(r.Context(), tenantUUID, existing.ID, req); err != nil {
			switch {
			case errors.Is(err, monitorservice.ErrCallMonitorForbidden):
				apierrors.WriteForbiddenError(w, err.Error())
			case errors.Is(err, monitorservice.ErrSecretConflict),
				errors.Is(err, monitorservice.ErrTimeoutNotBelowInterval):
				apierrors.WriteValidationError(w, fmt.Sprintf("version %d cannot be restored: %s", version, err.Error()))
			default:
				h.logger.WithFields(map[string]interface{}{
					"error":      err.Error(),
					"tenant_id":  tenantUUID,
					"monitor_id": existing.ID,
					"version":    version,
				}).Error("Failed to restore monitor version")
				apierrors.WriteInternalError(w, "failed to restore monitor version")
			}
			return
		}
		if snapshot.Type == models.MonitorTypeGroup && len(req.Config) > 0 {
			h.syncGroupMembersFromConfig(r.Context(), tenantUUID, existing.ID, req.Config)
		}
		if req.DependsOnIDs != nil {
			if err := h.syncDependencies(r.Context(), tenantUUID, existing.ID, *req.DependsOnIDs); err != nil {
				h.writeDependencyError(w, tenantUUID, existing.ID, err)
				return
			}
		}
		h.recordVersion(r.Context(), tenantUUID, existing.ID)
		if recorded, ok := versions.Flush(r.Context())[existing.ID]; ok {
			resp.Version = &recorded
		}
	}

	monitor, err := h.service.GetMonitor(r.Context(), tenantUUID, existing.ID)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantUUID,
			"monitor_id": existing.ID,
		}).Error("Failed to reload restored monitor")
		apierrors.WriteInternalError(w, "version restored, but the monitor could not be reloaded")
		return
	}
	restored := monitorForResponse(r.Context(), *monitor)
	resp.Monitor = &restored
	httpx.WriteJSON(w, http.StatusOK, resp)
}

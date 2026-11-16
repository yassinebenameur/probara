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
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	depservice "github.com/yassinebenameur/probara/api/internal/services/dependencies"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/api/internal/validation"
)

// versionHistory is implemented by the Postgres-backed monitors service;
// test doubles without it record and serve no history and save without a
// transaction.
type versionHistory interface {
	RecordVersion(ctx context.Context, tenantID, monitorID uuid.UUID)
	Versions() *monitorservice.VersionStore
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
	PlanRestore(ctx context.Context, tenantID, monitorID uuid.UUID, version models.MonitorConfigSnapshot, current *models.MonitorConfigSnapshot) (*monitorservice.RestorePlan, error)
	ValidateMergedConfig(ctx context.Context, tenantID, monitorID uuid.UUID, monitorType models.MonitorType, config json.RawMessage) error
}

// groupCycleChecker is implemented by the groups service.
type groupCycleChecker interface {
	WouldCreateCycle(ctx context.Context, tenantID, parentGroupID, candidateMemberID uuid.UUID) (bool, error)
}

func (h *Handlers) history() versionHistory {
	if history, ok := h.service.(versionHistory); ok {
		return history
	}
	return nil
}

func (h *Handlers) versionStore() *monitorservice.VersionStore {
	if history := h.history(); history != nil {
		return history.Versions()
	}
	return nil
}

// inSave runs fn as one save: the monitor write, its member and dependency
// syncs and its configuration version commit together or not at all, and
// the version is recorded inside the transaction, so it credits this save
// and no concurrent one. fn's ctx queues the version (DeferVersions); it is
// flushed before the commit; the versions recorded are returned per monitor.
func (h *Handlers) inSave(ctx context.Context, fn func(ctx context.Context) error) (map[uuid.UUID]int, error) {
	var recorded map[uuid.UUID]int
	run := func(ctx context.Context) error {
		ctx, versions := monitorservice.DeferVersions(ctx)
		if err := fn(ctx); err != nil {
			return err
		}
		recorded = versions.Flush(ctx)
		return nil
	}
	var err error
	if history := h.history(); history != nil {
		err = history.InTx(ctx, run)
	} else {
		err = run(ctx)
	}
	if err != nil {
		return nil, err
	}
	return recorded, nil
}

// recordVersion records (or queues) a version after a write that does not
// go through the service's create/update: member and dependency edges.
func (h *Handlers) recordVersion(ctx context.Context, tenantID, monitorID uuid.UUID) {
	if history := h.history(); history != nil {
		history.RecordVersion(ctx, tenantID, monitorID)
	}
}

// syncGroupMembersFromConfig makes a group's monitor_groups rows match the
// config.monitor_ids of an update (membership is the junction table, never
// the config). It stops at the first failure; a PATCH logs it and keeps the
// update, a restore fails as a whole.
func (h *Handlers) syncGroupMembersFromConfig(ctx context.Context, tenantUUID, monitorID uuid.UUID, config json.RawMessage) error {
	var groupConfig models.GroupConfig
	if err := json.Unmarshal(config, &groupConfig); err != nil {
		return nil
	}
	newMonitorIDs := make([]uuid.UUID, 0, len(groupConfig.MonitorIDs))
	for _, idStr := range groupConfig.MonitorIDs {
		if memberID, err := uuid.Parse(idStr); err == nil {
			newMonitorIDs = append(newMonitorIDs, memberID)
		}
	}

	currentMembers, err := h.groupService.GetGroupMembers(ctx, tenantUUID, monitorID)
	if err != nil {
		return err
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
			return fmt.Errorf("add monitors to group: %w", err)
		}
	}
	if len(toRemove) > 0 {
		if err := h.groupService.RemoveMonitorsFromGroup(ctx, tenantUUID, monitorID, toRemove); err != nil {
			return fmt.Errorf("remove monitors from group: %w", err)
		}
	}
	return nil
}

// dropCycles leaves out the dependencies and group members of a version that
// would now close a cycle (B came to depend on A after the version that made
// A depend on B), reporting them like references that no longer exist.
func (h *Handlers) dropCycles(ctx context.Context, tenantUUID, monitorID uuid.UUID, snapshot *models.MonitorConfigSnapshot) ([]string, error) {
	dropped := []string{}
	if h.dependencyService != nil && len(snapshot.DependsOnIDs) > 0 {
		kept := []string{}
		for _, id := range snapshot.DependsOnIDs {
			dependsOn, err := uuid.Parse(id)
			if err != nil {
				continue
			}
			cycle, err := h.dependencyService.WouldCycle(ctx, monitorID, dependsOn)
			if err != nil {
				return nil, err
			}
			if cycle {
				dropped = append(dropped, "dependency_cycle:"+id)
				continue
			}
			kept = append(kept, id)
		}
		snapshot.DependsOnIDs = kept
	}
	if checker, ok := h.groupService.(groupCycleChecker); ok && snapshot.Type == models.MonitorTypeGroup && len(snapshot.MemberIDs) > 0 {
		kept := []string{}
		for _, id := range snapshot.MemberIDs {
			member, err := uuid.Parse(id)
			if err != nil {
				continue
			}
			cycle, err := checker.WouldCreateCycle(ctx, tenantUUID, monitorID, member)
			if err != nil {
				return nil, err
			}
			if cycle {
				dropped = append(dropped, "member_cycle:"+id)
				continue
			}
			kept = append(kept, id)
		}
		snapshot.MemberIDs = kept
	}
	return dropped, nil
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

// restoreRefusal is a restore the request cannot have, answered with a
// status and message instead of a 500.
type restoreRefusal struct {
	status int
	code   string
	msg    string
}

func (e *restoreRefusal) Error() string { return e.msg }

// RestoreMonitorVersion handles POST /api/v1/monitors/{id}/versions/{version}/restore.
//
// All or nothing, in one transaction: it drops references that no longer
// exist and dependencies or members that would now close a cycle (listed in
// dropped_references), then re-applies the version through the same path as
// a PATCH — validation, UpdateMonitor (test-call gate, write-only secret
// merge, credential header normalization), the group member and dependency
// syncs — and records a new version naming the one restored. Any failure
// rolls back everything, version included.
//
// No secret value from a version is ever written: every secret keeps its
// current value, including secret map keys added since the version, and a
// secret the version had that the monitor no longer holds is listed in
// secrets_to_reenter. When the version cannot be valid without such a
// secret (a connection string, say), or conflicts with a secret the monitor
// holds now, the restore is refused with what to do first. The pause state
// is not part of a version and is left as it is.
func (h *Handlers) RestoreMonitorVersion(w http.ResponseWriter, r *http.Request) {
	store, tenantUUID, existing, ok := h.historyRequest(w, r)
	if !ok {
		return
	}
	version, ok := versionParam(w, r)
	if !ok {
		return
	}
	history := h.history()
	resp := models.RestoreMonitorVersionResponse{RestoredFrom: version}

	recorded, err := h.inSave(monitorservice.WithVersionRestore(r.Context(), version), func(ctx context.Context) error {
		stored, err := store.Get(ctx, tenantUUID, existing.ID, version)
		if err != nil {
			return err
		}
		current, err := store.CurrentSnapshot(ctx, tenantUUID, existing.ID)
		if err != nil {
			return err
		}
		snapshot, dropped, err := store.LiveReferences(ctx, tenantUUID, stored.Snapshot)
		if err != nil {
			return err
		}
		cycles, err := h.dropCycles(ctx, tenantUUID, existing.ID, &snapshot)
		if err != nil {
			return err
		}
		resp.DroppedReferences = append(dropped, cycles...)
		plan, err := history.PlanRestore(ctx, tenantUUID, existing.ID, snapshot, current)
		if err != nil {
			return err
		}
		resp.SecretsToReenter = plan.SecretsToReenter
		if !plan.Changed {
			return nil
		}
		req := plan.Request
		refuse := func(format string, args ...any) error {
			return &restoreRefusal{status: http.StatusBadRequest, code: "validation_error",
				msg: fmt.Sprintf("version %d cannot be restored: ", version) + fmt.Sprintf(format, args...)}
		}
		if err := validation.ValidateMonitorUpdate(req, existing); err != nil {
			return refuse("%s", err.Error())
		}
		if len(req.Config) > 0 {
			monitorType := existing.Type
			if req.Type != nil {
				monitorType = *req.Type
			}
			if err := history.ValidateMergedConfig(ctx, tenantUUID, existing.ID, monitorType, req.Config); err != nil {
				if errors.Is(err, monitorservice.ErrSecretConflict) {
					return refuse("it conflicts with a secret the monitor holds now, and a restore keeps current secrets "+
						"(%s). Clear the conflicting secret in Settings, then restore again", err.Error())
				}
				if len(plan.SecretsToReenter) > 0 {
					return refuse("it needs secrets the monitor no longer holds (%s); enter them in Settings first, then restore again (%s)",
						strings.Join(plan.SecretsToReenter, ", "), err.Error())
				}
				return refuse("%s", err.Error())
			}
		}

		if _, err := h.service.UpdateMonitor(ctx, tenantUUID, existing.ID, req); err != nil {
			switch {
			case errors.Is(err, monitorservice.ErrCallMonitorForbidden):
				return &restoreRefusal{status: http.StatusForbidden, code: "forbidden", msg: err.Error()}
			case errors.Is(err, monitorservice.ErrSecretConflict):
				return refuse("it conflicts with a secret the monitor holds now, and a restore keeps current secrets "+
					"(%s). Clear the conflicting secret in Settings, then restore again", err.Error())
			case errors.Is(err, monitorservice.ErrTimeoutNotBelowInterval):
				return refuse("%s", err.Error())
			}
			return err
		}
		if snapshot.Type == models.MonitorTypeGroup && len(req.Config) > 0 {
			if err := h.syncGroupMembersFromConfig(ctx, tenantUUID, existing.ID, req.Config); err != nil {
				return err
			}
		}
		if req.DependsOnIDs != nil {
			if err := h.syncDependencies(ctx, tenantUUID, existing.ID, *req.DependsOnIDs); err != nil {
				return err
			}
		}
		h.recordVersion(ctx, tenantUUID, existing.ID)
		return nil
	})
	if err != nil {
		var refusal *restoreRefusal
		switch {
		case errors.As(err, &refusal):
			apierrors.WriteError(w, refusal.status, refusal.code, refusal.msg)
		case errors.Is(err, monitorservice.ErrVersionNotFound):
			h.writeVersionError(w, err, existing.ID)
		case errors.Is(err, depservice.ErrDependencyCycle), errors.Is(err, depservice.ErrMonitorNotFound):
			// A dependency changed while the restore ran: nothing was written.
			apierrors.WriteError(w, http.StatusConflict, "restore_conflict",
				fmt.Sprintf("version %d was not restored: %s; nothing changed, try again", version, err.Error()))
		default:
			h.logger.WithFields(map[string]interface{}{
				"error":      err.Error(),
				"tenant_id":  tenantUUID,
				"monitor_id": existing.ID,
				"version":    version,
			}).Error("Failed to restore monitor version")
			apierrors.WriteInternalError(w, "failed to restore monitor version; nothing changed")
		}
		return
	}
	if resp.DroppedReferences == nil {
		resp.DroppedReferences = []string{}
	}
	if v, ok := recorded[existing.ID]; ok {
		resp.Version = &v
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

// Group membership: adding, removing and listing a group monitor's members.

package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/dbtx"
	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
)

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

	// The membership edit and the group's version, in one transaction.
	_, err = h.inSave(r.Context(), func(ctx context.Context) error {
		// The group (FOR UPDATE: two membership edits of nested groups
		// serialize, so the second one's cycle check sees the first) and its
		// members (FOR KEY SHARE) are locked in lock order before the edges
		// change (api/internal/dbtx); the group's configuration before the
		// change is kept when it has no history yet.
		if err := h.lockForSave(ctx, tenantUUID, []uuid.UUID{groupID}, req.MonitorIDs); err != nil {
			return err
		}
		h.captureBaseline(ctx, tenantUUID, groupID)
		if err := h.groupService.AddMonitorsToGroup(ctx, tenantUUID, groupID, monitorIDs); err != nil {
			return err
		}
		h.recordVersion(ctx, tenantUUID, groupID)
		return nil
	})
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorNotFound) || errors.Is(err, groupservice.ErrNotGroup) {
			apierrors.WriteNotFoundError(w, err.Error())
			return
		}
		if errors.Is(err, groupservice.ErrGroupCycleDetected) {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
		if dbtx.IsConflict(err) {
			writeConflict(w)
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

	// The membership edit and the group's version, in one transaction.
	_, err = h.inSave(r.Context(), func(ctx context.Context) error {
		// The group (FOR UPDATE: two membership edits of nested groups
		// serialize, so the second one's cycle check sees the first) and its
		// members (FOR KEY SHARE) are locked in lock order before the edges
		// change (api/internal/dbtx); the group's configuration before the
		// change is kept when it has no history yet.
		if err := h.lockForSave(ctx, tenantUUID, []uuid.UUID{groupID}, req.MonitorIDs); err != nil {
			return err
		}
		h.captureBaseline(ctx, tenantUUID, groupID)
		if err := h.groupService.RemoveMonitorsFromGroup(ctx, tenantUUID, groupID, monitorIDs); err != nil {
			return err
		}
		h.recordVersion(ctx, tenantUUID, groupID)
		return nil
	})
	if err != nil {
		if errors.Is(err, monitorservice.ErrMonitorNotFound) || errors.Is(err, groupservice.ErrNotGroup) {
			apierrors.WriteNotFoundError(w, err.Error())
			return
		}
		if dbtx.IsConflict(err) {
			writeConflict(w)
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

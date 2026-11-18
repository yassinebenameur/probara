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
	depservice "github.com/yassinebenameur/probara/api/internal/services/dependencies"
)

// GetMonitorDependencies handles GET /api/v1/monitors/{id}/dependencies —
// the upstream monitors this monitor depends on.
func (h *Handlers) GetMonitorDependencies(w http.ResponseWriter, r *http.Request) {
	h.listDependencyRelation(w, r, "dependencies")
}

// GetMonitorDependents handles GET /api/v1/monitors/{id}/dependents —
// the downstream monitors that depend on this monitor.
func (h *Handlers) GetMonitorDependents(w http.ResponseWriter, r *http.Request) {
	h.listDependencyRelation(w, r, "dependents")
}

func (h *Handlers) listDependencyRelation(w http.ResponseWriter, r *http.Request, relation string) {
	if h.dependencyService == nil {
		apierrors.WriteInternalError(w, "dependency service not configured")
		return
	}
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

	var items []models.DependencyMonitor
	if relation == "dependencies" {
		items, err = h.dependencyService.GetDependencies(r.Context(), tenantUUID, monitorID)
	} else {
		items, err = h.dependencyService.GetDependents(r.Context(), tenantUUID, monitorID)
	}
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantID,
			"monitor_id": monitorID,
		}).Error("Failed to list monitor " + relation)
		apierrors.WriteInternalError(w, "failed to list "+relation)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, models.DependencyMonitorList{Items: items})
}

// GetDependencyGraph handles GET /api/v1/monitors/dependency-graph — the
// tenant-wide dependency graph (nodes with state, directed edges).
func (h *Handlers) GetDependencyGraph(w http.ResponseWriter, r *http.Request) {
	if h.dependencyService == nil {
		apierrors.WriteInternalError(w, "dependency service not configured")
		return
	}
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	tenantID := tenantUUID.String()

	graph, err := h.dependencyService.GetDependencyGraph(r.Context(), tenantUUID)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to build dependency graph")
		apierrors.WriteInternalError(w, "failed to build dependency graph")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, graph)
}

// AddDependencyRequest is the body of POST /monitors/{id}/dependencies.
type AddDependencyRequest struct {
	DependsOnID string `json:"depends_on_id"`
}

// AddMonitorDependency handles POST /api/v1/monitors/{id}/dependencies —
// creates one edge ({"depends_on_id": "..."}) for the graph editor.
func (h *Handlers) AddMonitorDependency(w http.ResponseWriter, r *http.Request) {
	h.mutateDependencyEdge(w, r, "add")
}

// RemoveMonitorDependency handles DELETE /api/v1/monitors/{id}/dependencies/{dependsOnId}.
func (h *Handlers) RemoveMonitorDependency(w http.ResponseWriter, r *http.Request) {
	h.mutateDependencyEdge(w, r, "remove")
}

func (h *Handlers) mutateDependencyEdge(w http.ResponseWriter, r *http.Request, op string) {
	if h.dependencyService == nil {
		apierrors.WriteInternalError(w, "dependency service not configured")
		return
	}
	tenantUUID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	monitorID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid monitor ID")
		return
	}

	var dependsOnID uuid.UUID
	if op == "add" {
		var body AddDependencyRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
			return
		}
		dependsOnID, err = uuid.Parse(body.DependsOnID)
	} else {
		dependsOnID, err = uuid.Parse(chi.URLParam(r, "dependsOnId"))
	}
	if err != nil {
		apierrors.WriteValidationError(w, "invalid dependency monitor ID")
		return
	}

	// The edge and the monitor's version, in one transaction.
	_, err = h.inSave(r.Context(), func(ctx context.Context) error {
		// Both ends locked in lock order before the edge changes
		// (api/internal/dbtx): the monitor whose dependencies change FOR
		// UPDATE, so two opposite edge adds serialize and the second one's
		// cycle check sees the first; the target FOR KEY SHARE.
		if err := h.lockForSave(ctx, tenantUUID, []uuid.UUID{monitorID}, []string{dependsOnID.String()}); err != nil {
			return err
		}
		h.captureBaseline(ctx, tenantUUID, monitorID)
		var err error
		if op == "add" {
			err = h.dependencyService.AddDependency(ctx, tenantUUID, monitorID, dependsOnID)
		} else {
			err = h.dependencyService.RemoveDependency(ctx, tenantUUID, monitorID, dependsOnID)
		}
		if err != nil {
			return err
		}
		h.recordVersion(ctx, tenantUUID, monitorID)
		return nil
	})
	if err != nil {
		h.writeDependencyError(w, tenantUUID, monitorID, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// syncDependencies replaces a monitor's dependency set from a create/update
// payload.
func (h *Handlers) syncDependencies(ctx context.Context, tenantUUID, monitorID uuid.UUID, dependsOnIDs []string) error {
	if h.dependencyService == nil {
		return errors.New("dependency service not configured")
	}

	ids := make([]uuid.UUID, 0, len(dependsOnIDs))
	for _, idStr := range dependsOnIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			return fmt.Errorf("%w: invalid dependency monitor ID %q", depservice.ErrMonitorNotFound, idStr)
		}
		ids = append(ids, id)
	}

	return h.dependencyService.SetDependencies(ctx, tenantUUID, monitorID, ids)
}

// writeDependencyError maps a syncDependencies failure to an HTTP response.
func (h *Handlers) writeDependencyError(w http.ResponseWriter, tenantUUID, monitorID uuid.UUID, err error) {
	switch {
	case dbtx.IsConflict(err):
		writeConflict(w)
	case errors.Is(err, depservice.ErrDependencyCycle):
		apierrors.WriteError(w, http.StatusConflict, "dependency_cycle", err.Error())
	case errors.Is(err, depservice.ErrMonitorNotFound):
		apierrors.WriteValidationError(w, err.Error())
	default:
		h.logger.WithFields(map[string]interface{}{
			"error":      err.Error(),
			"tenant_id":  tenantUUID,
			"monitor_id": monitorID,
		}).Error("Failed to set monitor dependencies")
		apierrors.WriteInternalError(w, "failed to set monitor dependencies")
	}
}

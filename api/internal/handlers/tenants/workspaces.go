package tenants

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/tenants"
	"github.com/yassinebenameur/probara/shared/logger"
)

type workspaceService interface {
	CreateWorkspace(ctx context.Context, name string) (*models.Tenant, error)
	RenameWorkspace(ctx context.Context, id uuid.UUID, name string) (*models.Tenant, string, error)
	DeleteWorkspace(ctx context.Context, id uuid.UUID, confirmName string, deletedBy *uuid.UUID) (*models.DeleteWorkspaceResponse, error)
	ListWorkspaceOverview(ctx context.Context) ([]models.WorkspaceOverview, error)
}

// auditRecorder records the explicit workspace events.
type auditRecorder interface {
	Record(event audit.Event)
}

// WorkspaceHandlers serves workspace (tenant) management. Every route sits
// behind RequireSuperadmin.
type WorkspaceHandlers struct {
	service workspaceService
	audit   auditRecorder
	logger  *logger.Logger
}

// NewWorkspaceHandlers creates the workspace management handlers. recorder
// may be nil.
func NewWorkspaceHandlers(service *tenants.Service, recorder *audit.Recorder, log *logger.Logger) *WorkspaceHandlers {
	h := &WorkspaceHandlers{service: service, logger: log}
	if recorder != nil {
		h.audit = recorder
	}
	return h
}

// ListOverview handles GET /api/v1/tenants/overview.
func (h *WorkspaceHandlers) ListOverview(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListWorkspaceOverview(r.Context())
	if err != nil {
		h.logger.WithError(err).Error("Failed to list workspaces")
		apierrors.WriteInternalError(w, "failed to list workspaces")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, models.WorkspaceOverviewResponse{Items: items})
}

// Create handles POST /api/v1/tenants.
func (h *WorkspaceHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateWorkspaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	tenant, err := h.service.CreateWorkspace(r.Context(), req.Name)
	if err != nil {
		h.writeError(w, err, "Failed to create workspace")
		return
	}
	h.record(r, "tenant.create", tenant.ID, http.StatusCreated, map[string]any{"name": tenant.Name})
	httpx.WriteJSON(w, http.StatusCreated, tenant)
}

// Rename handles PATCH /api/v1/tenants/{id}.
func (h *WorkspaceHandlers) Rename(w http.ResponseWriter, r *http.Request) {
	id, ok := workspaceID(w, r)
	if !ok {
		return
	}
	var req models.RenameWorkspaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	tenant, previous, err := h.service.RenameWorkspace(r.Context(), id, req.Name)
	if err != nil {
		h.writeError(w, err, "Failed to rename workspace")
		return
	}
	h.record(r, "tenant.rename", tenant.ID, http.StatusOK, map[string]any{"name": tenant.Name, "previous_name": previous})
	httpx.WriteJSON(w, http.StatusOK, tenant)
}

// Delete handles DELETE /api/v1/tenants/{id}. The body repeats the
// workspace's name ({"confirm_name": …}). The workspace is gone at once; the
// scheduler purges its data in the background.
func (h *WorkspaceHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := workspaceID(w, r)
	if !ok {
		return
	}
	var req models.DeleteWorkspaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "confirm_name is required: repeat the workspace name to delete it")
		return
	}
	var deletedBy *uuid.UUID
	if raw, err := middleware.GetAdminID(r.Context()); err == nil {
		if parsed, err := uuid.Parse(raw); err == nil {
			deletedBy = &parsed
		}
	}
	result, err := h.service.DeleteWorkspace(r.Context(), id, req.ConfirmName, deletedBy)
	if err != nil {
		h.writeError(w, err, "Failed to delete workspace")
		return
	}
	h.record(r, "tenant.delete", id, http.StatusOK, map[string]any{"name": result.Name})
	httpx.WriteJSON(w, http.StatusOK, result)
}

// record writes the explicit audit event of a successful workspace change,
// with the name as it was. The event is platform-level (no tenant), so
// superadmins still see it once the workspace is gone; the generic middleware
// records refusals and failures (auditItemizedPrefixes).
func (h *WorkspaceHandlers) record(r *http.Request, action string, id uuid.UUID, status int, details map[string]any) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.TenantID = nil
	event.Action = action
	event.Outcome = audit.OutcomeSuccess
	event.StatusCode = status
	event.ResourceType = "tenant"
	event.ResourceID = id.String()
	event.MergeDetails(details)
	h.audit.Record(event)
}

func (h *WorkspaceHandlers) writeError(w http.ResponseWriter, err error, logMessage string) {
	switch {
	case errors.Is(err, tenants.ErrInvalidName), errors.Is(err, tenants.ErrConfirmMismatch):
		apierrors.WriteValidationError(w, err.Error())
	case errors.Is(err, tenants.ErrWorkspaceNotFound):
		apierrors.WriteNotFoundError(w, err.Error())
	case errors.Is(err, tenants.ErrWorkspaceNameTaken),
		errors.Is(err, tenants.ErrProtectedWorkspace),
		errors.Is(err, tenants.ErrLastWorkspace):
		apierrors.WriteError(w, http.StatusConflict, "conflict", err.Error())
	default:
		h.logger.WithError(err).Error(logMessage)
		apierrors.WriteInternalError(w, "failed to update workspace")
	}
}

func workspaceID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid workspace id")
		return uuid.Nil, false
	}
	return id, true
}

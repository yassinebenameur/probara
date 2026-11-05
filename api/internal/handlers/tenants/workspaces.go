package tenants

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/tenants"
	"github.com/yassinebenameur/probara/shared/logger"
)

type workspaceService interface {
	CreateTenant(ctx context.Context, name string) (*models.Tenant, error)
	RenameTenant(ctx context.Context, id uuid.UUID, name string) (*models.Tenant, string, error)
	DeleteTenant(ctx context.Context, id uuid.UUID, confirmName string, deletedBy *uuid.UUID) (*models.DeleteTenantResponse, error)
}

// WorkspaceHandlers serves workspace (tenant) create, rename and delete.
// Every route sits behind RequireSuperadmin.
type WorkspaceHandlers struct {
	service workspaceService
	audit   *audit.Recorder
	logger  *logger.Logger
}

// NewWorkspaceHandlers creates the workspace management handlers.
func NewWorkspaceHandlers(service *tenants.Service, recorder *audit.Recorder, log *logger.Logger) *WorkspaceHandlers {
	return &WorkspaceHandlers{service: service, audit: recorder, logger: log}
}

// CreateTenant handles POST /api/v1/tenants.
func (h *WorkspaceHandlers) CreateTenant(w http.ResponseWriter, r *http.Request) {
	var req models.CreateTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	tenant, err := h.service.CreateTenant(r.Context(), req.Name)
	if err != nil {
		h.writeError(w, err, "Failed to create workspace")
		return
	}
	h.record(r, "tenant.create", tenant.ID, map[string]any{"name": tenant.Name})
	writeJSON(w, http.StatusCreated, tenant)
}

// RenameTenant handles PATCH /api/v1/tenants/{id}.
func (h *WorkspaceHandlers) RenameTenant(w http.ResponseWriter, r *http.Request) {
	id, ok := tenantIDParam(w, r)
	if !ok {
		return
	}
	var req models.RenameTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	tenant, previous, err := h.service.RenameTenant(r.Context(), id, req.Name)
	if err != nil {
		h.writeError(w, err, "Failed to rename workspace")
		return
	}
	h.record(r, "tenant.rename", tenant.ID, map[string]any{"name": tenant.Name, "previous_name": previous})
	writeJSON(w, http.StatusOK, tenant)
}

// DeleteTenant handles DELETE /api/v1/tenants/{id}. The body must re-type the
// workspace's name ({"confirm_name": …}). The workspace disappears at once;
// its data is purged in the background by the scheduler.
func (h *WorkspaceHandlers) DeleteTenant(w http.ResponseWriter, r *http.Request) {
	id, ok := tenantIDParam(w, r)
	if !ok {
		return
	}
	var req models.DeleteTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "confirm_name is required: re-type the workspace name to delete it")
		return
	}
	var deletedBy *uuid.UUID
	if raw, err := middleware.GetAdminID(r.Context()); err == nil {
		if parsed, err := uuid.Parse(raw); err == nil {
			deletedBy = &parsed
		}
	}
	result, err := h.service.DeleteTenant(r.Context(), id, req.ConfirmName, deletedBy)
	if err != nil {
		h.writeError(w, err, "Failed to delete workspace")
		return
	}
	h.record(r, "tenant.delete", id, map[string]any{"name": result.Name, "monitors": result.Monitors})
	writeJSON(w, http.StatusOK, result)
}

// record writes the explicit audit event for a successful workspace change.
// Workspace events are platform-level (no tenant), so superadmins see them
// from any workspace's audit log, including after the workspace is gone; the
// generic middleware still records refusals and failures (auditItemizedPrefixes).
func (h *WorkspaceHandlers) record(r *http.Request, action string, id uuid.UUID, details map[string]any) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.TenantID = nil
	event.Action = action
	event.Outcome = audit.OutcomeSuccess
	event.StatusCode = http.StatusOK
	event.ResourceType = "tenant"
	event.ResourceID = id.String()
	event.MergeDetails(details)
	h.audit.Record(event)
}

func (h *WorkspaceHandlers) writeError(w http.ResponseWriter, err error, logMessage string) {
	switch {
	case stderrors.Is(err, tenants.ErrInvalidName), stderrors.Is(err, tenants.ErrConfirmMismatch):
		errors.WriteValidationError(w, err.Error())
	case stderrors.Is(err, tenants.ErrTenantNotFound):
		errors.WriteNotFoundError(w, err.Error())
	case stderrors.Is(err, tenants.ErrTenantNameTaken),
		stderrors.Is(err, tenants.ErrProtectedTenant),
		stderrors.Is(err, tenants.ErrLastLiveTenant):
		errors.WriteError(w, http.StatusConflict, "conflict", err.Error())
	default:
		h.logger.WithError(err).Error(logMessage)
		errors.WriteInternalError(w, "failed to update workspace")
	}
}

func tenantIDParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		errors.WriteValidationError(w, "invalid workspace id")
		return uuid.Nil, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

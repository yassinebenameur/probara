// Package oidcmappings exposes superadmin CRUD for OIDC group→role mappings.
// Mutations are audited by the generic AuditMutations middleware
// (oidc-group-mapping.create/update/delete).
package oidcmappings

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/models"
	oidcmappingssvc "github.com/yassinebenameur/probara/api/internal/services/oidcmappings"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers handles OIDC group mapping HTTP requests.
type Handlers struct {
	service *oidcmappingssvc.Service
	oidcCfg config.OIDCConfig
	logger  *logger.Logger
}

// NewHandlers creates a new OIDC group mappings handler.
func NewHandlers(service *oidcmappingssvc.Service, oidcCfg config.OIDCConfig, log *logger.Logger) *Handlers {
	return &Handlers{service: service, oidcCfg: oidcCfg, logger: log}
}

// List handles GET /api/v1/oidc-group-mappings.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	mappings, err := h.service.List(r.Context())
	if err != nil {
		h.logger.WithError(err).Error("Failed to list OIDC group mappings")
		apierrors.WriteInternalError(w, "failed to list OIDC group mappings")
		return
	}

	seenGroups, err := h.service.SeenGroups(r.Context())
	if err != nil {
		h.logger.WithError(err).Error("Failed to list seen OIDC groups")
		apierrors.WriteInternalError(w, "failed to list OIDC group mappings")
		return
	}

	resp := models.OIDCGroupMappingListResponse{
		Mappings:             mappings,
		SeenGroups:           seenGroups,
		GroupsClaim:          h.oidcCfg.GroupsClaim,
		GroupsScopeRequested: slices.Contains(h.oidcCfg.Scopes, "groups"),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Create handles POST /api/v1/oidc-group-mappings.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateOIDCGroupMappingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	mapping, err := h.service.Create(r.Context(), &req)
	if err != nil {
		h.writeServiceError(w, err, "create")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(mapping)
}

// Update handles PATCH /api/v1/oidc-group-mappings/{id}.
func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid mapping ID")
		return
	}

	var req models.UpdateOIDCGroupMappingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	mapping, err := h.service.Update(r.Context(), id, &req)
	if err != nil {
		h.writeServiceError(w, err, "update")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mapping)
}

// Delete handles DELETE /api/v1/oidc-group-mappings/{id}.
func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid mapping ID")
		return
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		h.writeServiceError(w, err, "delete")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) writeServiceError(w http.ResponseWriter, err error, op string) {
	switch {
	case errors.Is(err, oidcmappingssvc.ErrInvalidMapping):
		apierrors.WriteValidationError(w, err.Error())
	case errors.Is(err, oidcmappingssvc.ErrTenantNotFound):
		apierrors.WriteValidationError(w, "tenant not found")
	case errors.Is(err, oidcmappingssvc.ErrDuplicate):
		apierrors.WriteError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, oidcmappingssvc.ErrNotFound):
		apierrors.WriteNotFoundError(w, "OIDC group mapping not found")
	default:
		h.logger.WithError(err).Error("Failed to " + op + " OIDC group mapping")
		apierrors.WriteInternalError(w, "failed to "+op+" OIDC group mapping")
	}
}

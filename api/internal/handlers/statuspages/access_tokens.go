package statuspages

import (
	"encoding/json"
	stderrors "errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	statuspageservice "github.com/yassinebenameur/probara/api/internal/services/statuspages"
	"github.com/yassinebenameur/probara/api/internal/validation"
)

// pageScope resolves the tenant and the {id} page of a request, answering
// the error itself when either is missing or malformed.
func pageScope(w http.ResponseWriter, r *http.Request) (tenantID, pageID uuid.UUID, ok bool) {
	tenant, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return uuid.Nil, uuid.Nil, false
	}
	if tenantID, err = uuid.Parse(tenant); err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return uuid.Nil, uuid.Nil, false
	}
	if pageID, err = uuid.Parse(chi.URLParam(r, "id")); err != nil {
		errors.WriteValidationError(w, "invalid status page ID")
		return uuid.Nil, uuid.Nil, false
	}
	return tenantID, pageID, true
}

func (h *Handlers) writeAccessTokenError(w http.ResponseWriter, err error, what string) {
	if verr := (*statuspageservice.ValidationError)(nil); stderrors.As(err, &verr) {
		errors.WriteValidationError(w, verr.Error())
		return
	}
	if stderrors.Is(err, statuspageservice.ErrAccessTokenNotFound) {
		errors.WriteNotFoundError(w, "access token not found")
		return
	}
	if err.Error() == "status page not found" {
		errors.WriteNotFoundError(w, "status page not found")
		return
	}
	h.logger.WithError(err).Error(what)
	errors.WriteInternalError(w, what)
}

// ListAccessTokens handles GET /api/v1/status-pages/{id}/access-tokens
func (h *Handlers) ListAccessTokens(w http.ResponseWriter, r *http.Request) {
	tenantID, pageID, ok := pageScope(w, r)
	if !ok {
		return
	}
	list, err := h.service.ListAccessTokens(r.Context(), tenantID, pageID)
	if err != nil {
		h.writeAccessTokenError(w, err, "failed to list access tokens")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// CreateAccessToken handles POST /api/v1/status-pages/{id}/access-tokens.
// The response is the only one that carries the token.
func (h *Handlers) CreateAccessToken(w http.ResponseWriter, r *http.Request) {
	tenantID, pageID, ok := pageScope(w, r)
	if !ok {
		return
	}
	var req models.CreateStatusPageAccessTokenRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	if err := validation.ValidateStatusPageAccessTokenLabel(req.Label); err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}
	var createdBy *uuid.UUID
	if adminID, err := middleware.GetAdminID(r.Context()); err == nil {
		if id, err := uuid.Parse(adminID); err == nil {
			createdBy = &id
		}
	}
	created, err := h.service.CreateAccessToken(r.Context(), tenantID, pageID, createdBy, req.Label)
	if err != nil {
		h.writeAccessTokenError(w, err, "failed to create access token")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(created)
}

// RevokeAccessToken handles DELETE /api/v1/status-pages/{id}/access-tokens/{accessId}
func (h *Handlers) RevokeAccessToken(w http.ResponseWriter, r *http.Request) {
	tenantID, pageID, ok := pageScope(w, r)
	if !ok {
		return
	}
	tokenID, err := uuid.Parse(chi.URLParam(r, "accessId"))
	if err != nil {
		errors.WriteValidationError(w, "invalid access token ID")
		return
	}
	if err := h.service.RevokeAccessToken(r.Context(), tenantID, pageID, tokenID); err != nil {
		h.writeAccessTokenError(w, err, "failed to revoke access token")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

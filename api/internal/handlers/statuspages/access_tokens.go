package statuspages

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	statuspageservice "github.com/yassinebenameur/probara/api/internal/services/statuspages"
	"github.com/yassinebenameur/probara/api/internal/validation"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// maxAccessTokenRequestBytes bounds a create request (a label).
const maxAccessTokenRequestBytes = 4096

func (h *Handlers) writeAccessTokenError(w http.ResponseWriter, err error, action string, tenantID, pageID uuid.UUID) {
	switch {
	case errors.Is(err, statuspageservice.ErrStatusPageNotFound),
		errors.Is(err, statuspageservice.ErrAccessTokenNotFound):
		apierrors.WriteNotFoundError(w, err.Error())
	case errors.Is(err, statuspageservice.ErrTooManyAccessTokens):
		apierrors.WriteValidationError(w, err.Error())
	default:
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID.String(),
			"page_id":   pageID.String(),
		}).Error("Failed to " + action)
		apierrors.WriteInternalError(w, "failed to "+action)
	}
}

// tokenCreator is the caller recorded as a kiosk token's creator.
func tokenCreator(r *http.Request) statuspageservice.TokenCreator {
	var by statuspageservice.TokenCreator
	if raw, ok := ctxpkg.GetAdminID(r.Context()); ok {
		by.UserID, _ = uuid.Parse(raw)
	}
	if raw, ok := ctxpkg.GetAPIKeyID(r.Context()); ok {
		by.APIKeyID, _ = uuid.Parse(raw)
	}
	return by
}

// ListAccessTokens handles GET /api/v1/status-pages/{id}/access-tokens
func (h *Handlers) ListAccessTokens(w http.ResponseWriter, r *http.Request) {
	tenantID, pageID, ok := h.templateRequestScope(w, r)
	if !ok {
		return
	}
	list, err := h.service.ListAccessTokens(r.Context(), tenantID, pageID)
	if err != nil {
		h.writeAccessTokenError(w, err, "list access tokens", tenantID, pageID)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// CreateAccessToken handles POST /api/v1/status-pages/{id}/access-tokens.
// Its response is the only one that ever carries the token.
func (h *Handlers) CreateAccessToken(w http.ResponseWriter, r *http.Request) {
	tenantID, pageID, ok := h.templateRequestScope(w, r)
	if !ok {
		return
	}
	var req models.CreateStatusPageAccessTokenRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAccessTokenRequestBytes)).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	if err := validation.ValidateStatusPageAccessTokenLabel(req.Label); err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}
	created, err := h.service.CreateAccessToken(r.Context(), tenantID, pageID, tokenCreator(r), req.Label)
	if err != nil {
		h.writeAccessTokenError(w, err, "create access token", tenantID, pageID)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusCreated, created)
}

// RevokeAccessToken handles POST /api/v1/status-pages/{id}/access-tokens/{accessTokenId}/revoke
func (h *Handlers) RevokeAccessToken(w http.ResponseWriter, r *http.Request) {
	tenantID, pageID, ok := h.templateRequestScope(w, r)
	if !ok {
		return
	}
	tokenID, err := uuid.Parse(chi.URLParam(r, "accessTokenId"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid access token ID")
		return
	}
	if err := h.service.RevokeAccessToken(r.Context(), tenantID, pageID, tokenID); err != nil {
		h.writeAccessTokenError(w, err, "revoke access token", tenantID, pageID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

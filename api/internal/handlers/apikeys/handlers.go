package apikeys

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	apikeysservice "github.com/yassinebenameur/probara/api/internal/services/apikeys"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers handles API key HTTP requests.
type Handlers struct {
	service *apikeysservice.Service
	logger  *logger.Logger
	audit   *audit.Recorder
}

// NewHandlers creates a new API key handler.
func NewHandlers(service *apikeysservice.Service, log *logger.Logger) *Handlers {
	return &Handlers{
		service: service,
		logger:  log,
	}
}

// WithAudit attaches an audit recorder for explicit key-management events
// (the /api-keys subtree is excluded from the generic mutation middleware).
func (h *Handlers) WithAudit(recorder *audit.Recorder) *Handlers {
	h.audit = recorder
	return h
}

func (h *Handlers) recordKeyEvent(r *http.Request, action string, keyID string, details map[string]any) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.Action = action
	event.Outcome = audit.OutcomeSuccess
	event.ResourceType = "api_key"
	event.ResourceID = keyID
	event.Details = details
	h.audit.Record(event)
}

// ListAPIKeys handles GET /api/v1/api-keys
func (h *Handlers) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		apierrors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	page := 1
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	pageSize := 20
	if pageSizeStr := r.URL.Query().Get("page_size"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 && ps <= 100 {
			pageSize = ps
		}
	}

	result, err := h.service.ListAPIKeys(r.Context(), tenantUUID, page, pageSize)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to list api keys")
		apierrors.WriteInternalError(w, "failed to list api keys")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// CreateAPIKey handles POST /api/v1/api-keys
func (h *Handlers) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	var req models.CreateApiKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		apierrors.WriteValidationError(w, "name is required")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		apierrors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	apiKey, err := h.service.CreateAPIKey(r.Context(), tenantUUID, &req)
	if err != nil {
		if errors.Is(err, apikeysservice.ErrInvalidScope) || errors.Is(err, apikeysservice.ErrExpiryInPast) {
			apierrors.WriteValidationError(w, err.Error())
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
		}).Error("Failed to create api key")
		apierrors.WriteInternalError(w, "failed to create api key")
		return
	}

	details := map[string]any{"name": apiKey.Name, "scope": apiKey.Scope}
	if apiKey.ExpiresAt != nil {
		details["expires_at"] = apiKey.ExpiresAt
	}
	h.recordKeyEvent(r, "apikey.create", apiKey.ID.String(), details)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(apiKey)
}

// RevokeAPIKey handles DELETE /api/v1/api-keys/{id}
func (h *Handlers) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}

	keyIDStr := chi.URLParam(r, "id")
	keyID, err := uuid.Parse(keyIDStr)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid api key ID")
		return
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		apierrors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	if err := h.service.RevokeAPIKey(r.Context(), tenantUUID, keyID); err != nil {
		if errors.Is(err, apikeysservice.ErrAPIKeyNotFound) {
			apierrors.WriteNotFoundError(w, "api key not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"key_id":    keyIDStr,
		}).Error("Failed to revoke api key")
		apierrors.WriteInternalError(w, "failed to revoke api key")
		return
	}

	h.recordKeyEvent(r, "apikey.revoke", keyID.String(), nil)

	w.WriteHeader(http.StatusNoContent)
}

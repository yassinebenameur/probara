// Package depsuggest exposes the AI dependency-suggestion endpoint.
package depsuggest

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	svc "github.com/yassinebenameur/probara/api/internal/services/depsuggest"
	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers serves dependency suggestions.
type Handlers struct {
	service *svc.Service
	logger  *logger.Logger
}

// NewHandlers creates the handler.
func NewHandlers(service *svc.Service, log *logger.Logger) *Handlers {
	return &Handlers{service: service, logger: log}
}

// Suggest handles POST /api/v1/monitors/dependency-suggestions.
func (h *Handlers) Suggest(w http.ResponseWriter, r *http.Request) {
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

	result, err := h.service.Suggest(r.Context(), tenantUUID)
	if err != nil {
		if errors.Is(err, ai.ErrNotConfigured) {
			apierrors.WriteError(w, http.StatusServiceUnavailable, "not_configured", "AI is not configured. Set it up in Settings → AI root cause analysis.")
			return
		}
		h.logger.WithError(err).Error("Failed to suggest dependencies")
		apierrors.WriteInternalError(w, "failed to suggest dependencies")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

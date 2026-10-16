// Package depsuggest exposes the AI dependency-suggestion endpoint.
package depsuggest

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

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
		writeSuggestError(w, err, h.logger)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// unavailableRetryAfter is the Retry-After sent when the local AI answered
// without one (its own 429 carries 5 s).
const unavailableRetryAfter = 5 * time.Second

// writeSuggestError maps a Suggest failure. A local AI that is busy (queue
// full or queue deadline, HTTP 429), loading or restarting is
// ai.ErrUnavailable: a retryable 503 with Retry-After, not a 500, since the
// same request succeeds once the executing batch finishes.
func writeSuggestError(w http.ResponseWriter, err error, log *logger.Logger) {
	switch {
	case errors.Is(err, ai.ErrNotConfigured):
		apierrors.WriteError(w, http.StatusServiceUnavailable, "not_configured", "Local AI is disabled for this workspace or installation. Check Settings → AI analysis.")
	case errors.Is(err, ai.ErrUnavailable):
		retry := ai.RetryAfterHint(err)
		if retry <= 0 {
			retry = unavailableRetryAfter
		}
		w.Header().Set("Retry-After", strconv.Itoa(int((retry+time.Second-1)/time.Second)))
		apierrors.WriteError(w, http.StatusServiceUnavailable, "unavailable", "Local AI is busy or restarting; retry shortly.")
	default:
		log.WithError(err).Error("Failed to suggest dependencies")
		apierrors.WriteInternalError(w, "failed to suggest dependencies")
	}
}

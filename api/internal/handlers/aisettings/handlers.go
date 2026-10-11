// Package aisettings exposes workspace enablement for local AI endpoints, including
// a connectivity test used by the settings UI.
package aisettings

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	svc "github.com/yassinebenameur/probara/api/internal/services/aisettings"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers handles AI settings HTTP requests.
type Handlers struct {
	service *svc.Service
	logger  *logger.Logger
}

// NewHandlers creates a new AI settings handler.
func NewHandlers(service *svc.Service, log *logger.Logger) *Handlers {
	// The service logs detailed probe errors (internal dial addresses) that
	// never reach a response; give it the API logger.
	if service != nil {
		service.SetLogger(log)
	}
	return &Handlers{service: service, logger: log}
}

func (h *Handlers) tenant(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "tenant ID not found")
		return uuid.Nil, false
	}
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		apierrors.WriteInternalError(w, "invalid tenant ID")
		return uuid.Nil, false
	}
	return tenantUUID, true
}

// GetSettings handles GET /api/v1/ai-settings.
func (h *Handlers) GetSettings(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenant(w, r)
	if !ok {
		return
	}
	settings, err := h.service.Get(r.Context(), tenantUUID)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get AI settings")
		apierrors.WriteInternalError(w, "failed to load AI settings")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(settings)
}

// UpdateSettings handles PUT /api/v1/ai-settings.
func (h *Handlers) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenant(w, r)
	if !ok {
		return
	}
	var req svc.UpdateRequest
	if err := decodeSettings(w, r, &req); err != nil {
		apierrors.WriteValidationError(w, "Only enabled is accepted for updates; connection tests accept an empty object. External provider fields are retired: "+err.Error())
		return
	}
	settings, err := h.service.Update(r.Context(), tenantUUID, req)
	if err != nil {
		// Validation-style errors surface to the user; everything else is 500.
		apierrors.WriteValidationError(w, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(settings)
}

// TestConnection handles POST /api/v1/ai-settings/test. It always returns 200
// with a {ok, model?, message} body — failures are reported in the body, not
// as HTTP errors, so the UI can render the message inline.
func (h *Handlers) TestConnection(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenant(w, r)
	if !ok {
		return
	}
	var req svc.TestRequest
	if err := decodeSettings(w, r, &req); err != nil {
		apierrors.WriteValidationError(w, "Only enabled is accepted for updates; connection tests accept an empty object. External provider fields are retired: "+err.Error())
		return
	}
	result := h.service.Test(r.Context(), tenantUUID, req)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func decodeSettings(w http.ResponseWriter, r *http.Request, out any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if dec.Decode(new(any)) != io.EOF {
		return io.ErrUnexpectedEOF
	}
	return nil
}

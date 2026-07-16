// Package locations exposes the private-locations CRUD and deploy-snippet
// endpoints.
package locations

import (
	"encoding/json"
	stderrors "errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	locationservice "github.com/yassinebenameur/probara/api/internal/services/locations"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers handles location HTTP requests.
type Handlers struct {
	service       *locationservice.Service
	publicNATSURL string
	logger        *logger.Logger
}

// NewHandlers creates location handlers. publicNATSURL is the externally
// reachable NATS address baked into deploy snippets (empty = placeholder).
func NewHandlers(service *locationservice.Service, publicNATSURL string, log *logger.Logger) *Handlers {
	return &Handlers{service: service, publicNATSURL: publicNATSURL, logger: log}
}

func (h *Handlers) tenantUUID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return uuid.Nil, false
	}
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return uuid.Nil, false
	}
	return tenantUUID, true
}

func locationID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		errors.WriteValidationError(w, "invalid location ID")
		return uuid.Nil, false
	}
	return id, true
}

// CreateLocation handles POST /api/v1/locations.
func (h *Handlers) CreateLocation(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenantUUID(w, r)
	if !ok {
		return
	}

	var req models.CreateLocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	location, err := h.service.Create(r.Context(), tenantUUID, &req)
	if err != nil {
		errors.WriteValidationError(w, err.Error())
		return
	}

	h.logger.WithFields(map[string]interface{}{
		"tenant_id":   tenantUUID,
		"location_id": location.ID,
		"name":        location.Name,
	}).Info("Location created")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(location)
}

// ListLocations handles GET /api/v1/locations.
func (h *Handlers) ListLocations(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenantUUID(w, r)
	if !ok {
		return
	}

	page := 1
	if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v > 0 {
		page = v
	}
	pageSize := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("page_size")); err == nil && v > 0 {
		pageSize = v
	}

	result, err := h.service.List(r.Context(), tenantUUID, page, pageSize)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "tenant_id": tenantUUID}).Error("Failed to list locations")
		errors.WriteInternalError(w, "failed to list locations")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// GetLocation handles GET /api/v1/locations/{id}.
func (h *Handlers) GetLocation(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenantUUID(w, r)
	if !ok {
		return
	}
	id, ok := locationID(w, r)
	if !ok {
		return
	}

	location, err := h.service.Get(r.Context(), tenantUUID, id)
	if err != nil {
		if stderrors.Is(err, locationservice.ErrNotFound) {
			errors.WriteNotFoundError(w, "location not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "location_id": id}).Error("Failed to get location")
		errors.WriteInternalError(w, "failed to get location")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(location)
}

// UpdateLocation handles PATCH /api/v1/locations/{id}.
func (h *Handlers) UpdateLocation(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenantUUID(w, r)
	if !ok {
		return
	}
	id, ok := locationID(w, r)
	if !ok {
		return
	}

	var req models.UpdateLocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}

	location, err := h.service.Update(r.Context(), tenantUUID, id, &req)
	if err != nil {
		if stderrors.Is(err, locationservice.ErrNotFound) {
			errors.WriteNotFoundError(w, "location not found")
			return
		}
		errors.WriteValidationError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(location)
}

// DeleteLocation handles DELETE /api/v1/locations/{id}. Monitors using the
// location are detached (quorum clamped); the response reports how many.
func (h *Handlers) DeleteLocation(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenantUUID(w, r)
	if !ok {
		return
	}
	id, ok := locationID(w, r)
	if !ok {
		return
	}

	affected, err := h.service.Delete(r.Context(), tenantUUID, id)
	if err != nil {
		if stderrors.Is(err, locationservice.ErrNotFound) {
			errors.WriteNotFoundError(w, "location not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "location_id": id}).Error("Failed to delete location")
		errors.WriteInternalError(w, "failed to delete location")
		return
	}

	h.logger.WithFields(map[string]interface{}{
		"tenant_id":         tenantUUID,
		"location_id":       id,
		"monitors_detached": affected,
	}).Info("Location deleted")

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"monitors_detached": affected})
}

// GetDeployInfo handles GET /api/v1/locations/{id}/deploy.
func (h *Handlers) GetDeployInfo(w http.ResponseWriter, r *http.Request) {
	tenantUUID, ok := h.tenantUUID(w, r)
	if !ok {
		return
	}
	id, ok := locationID(w, r)
	if !ok {
		return
	}

	info, err := h.service.GenerateDeployInfo(r.Context(), tenantUUID, id, h.publicNATSURL)
	if err != nil {
		if stderrors.Is(err, locationservice.ErrNotFound) {
			errors.WriteNotFoundError(w, "location not found")
			return
		}
		h.logger.WithFields(map[string]interface{}{"error": err.Error(), "location_id": id}).Error("Failed to build deploy info")
		errors.WriteInternalError(w, "failed to build deploy info")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

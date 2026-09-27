// Package metricsquery exposes the metric store to the operator UI:
// GET  /api/v1/monitors/{id}/metrics/series  — series discovery
// POST /api/v1/monitors/{id}/metrics/query   — batched range query
// (the POST is read-only and allowlisted in middleware/readonly.go).
package metricsquery

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	metricsqueryservice "github.com/yassinebenameur/probara/api/internal/services/metricsquery"
	"github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handler serves metric discovery and range queries.
type Handler struct {
	service *metricsqueryservice.Service
	log     *logger.Logger
}

// NewHandler creates the handler.
func NewHandler(service *metricsqueryservice.Service, log *logger.Logger) *Handler {
	return &Handler{service: service, log: log}
}

func (h *Handler) scope(w http.ResponseWriter, r *http.Request) (monitorID, tenantID uuid.UUID, ok bool) {
	tenantIDStr, found := context.GetTenantID(r.Context())
	if !found {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return uuid.Nil, uuid.Nil, false
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.log.WithError(err).Error("invalid tenant_id in context")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return uuid.Nil, uuid.Nil, false
	}
	monitorID, err = uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid monitor ID", http.StatusBadRequest)
		return uuid.Nil, uuid.Nil, false
	}
	return monitorID, tenantID, true
}

// HandleListSeries handles GET /api/v1/monitors/{id}/metrics/series.
func (h *Handler) HandleListSeries(w http.ResponseWriter, r *http.Request) {
	monitorID, tenantID, ok := h.scope(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListSeries(r.Context(), monitorID, tenantID)
	if err != nil {
		if errors.Is(err, metricsqueryservice.ErrMonitorNotFound) {
			http.Error(w, "monitor not found", http.StatusNotFound)
			return
		}
		h.log.WithError(err).Error("list metric series failed")
		http.Error(w, "failed to list metric series", http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []metricsqueryservice.SeriesItem{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": items})
}

// HandleQuery handles POST /api/v1/monitors/{id}/metrics/query.
func (h *Handler) HandleQuery(w http.ResponseWriter, r *http.Request) {
	monitorID, tenantID, ok := h.scope(w, r)
	if !ok {
		return
	}
	var req metricsqueryservice.QueryRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	resp, err := h.service.Query(r.Context(), monitorID, tenantID, req)
	if err != nil {
		switch {
		case errors.Is(err, metricsqueryservice.ErrMonitorNotFound):
			http.Error(w, "monitor not found", http.StatusNotFound)
		case metricsqueryservice.IsValidationError(err):
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		default:
			h.log.WithError(err).Error("metric query failed")
			http.Error(w, "metric query failed", http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

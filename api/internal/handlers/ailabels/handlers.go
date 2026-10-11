// Package ailabels exposes the AI label routes: operator verdicts on incident
// analyses and dependency suggestions (writes, behind RequireWrite) and the
// tenant-admin training export.
package ailabels

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	svc "github.com/yassinebenameur/probara/api/internal/services/ailabels"
	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/logger"
)

// maxBodyBytes bounds label and export request bodies.
const maxBodyBytes = 64 << 10

// Handlers serves the AI label routes.
type Handlers struct {
	service *svc.Service
	logger  *logger.Logger
	now     func() time.Time
}

// NewHandlers creates the handlers.
func NewHandlers(service *svc.Service, log *logger.Logger) *Handlers {
	return &Handlers{service: service, logger: log, now: time.Now}
}

// LabelIncidentAnalysis handles POST /api/v1/incidents/{id}/ai-analysis/label.
func (h *Handlers) LabelIncidentAnalysis(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromContext(w, r)
	if !ok {
		return
	}
	incidentID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid incident ID")
		return
	}
	var req models.IncidentAILabelRequest
	if !decodeBody(w, r, &req, false) {
		return
	}
	label, err := h.service.LabelIncidentAnalysis(r.Context(), tenantID, incidentID, req, labeledBy(r))
	if err != nil {
		h.writeError(w, err, "failed to save the analysis label")
		return
	}
	writeJSON(w, label)
}

// LabelDependencySuggestion handles POST
// /api/v1/monitors/dependency-suggestions/label.
func (h *Handlers) LabelDependencySuggestion(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromContext(w, r)
	if !ok {
		return
	}
	var req models.DependencyLabelRequest
	if !decodeBody(w, r, &req, false) {
		return
	}
	label, err := h.service.LabelDependencySuggestion(r.Context(), tenantID, req, labeledBy(r))
	if err != nil {
		h.writeError(w, err, "failed to save the suggestion label")
		return
	}
	writeJSON(w, label)
}

// Export handles POST /api/v1/ai-labels/export (tenant admins). The body is
// buffered so the row counts can go in headers.
func (h *Handlers) Export(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromContext(w, r)
	if !ok {
		return
	}
	var filter models.AILabelExportFilter
	if !decodeBody(w, r, &filter, true) {
		return
	}
	result, err := h.service.Export(r.Context(), tenantID, filter)
	if err != nil {
		h.writeError(w, err, "failed to export AI labels")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="`+svc.ExportFilename(h.now())+`"`)
	w.Header().Set("X-Probara-Export-Rows", strconv.Itoa(result.Rows))
	w.Header().Set("X-Probara-Export-Skipped", strconv.Itoa(result.Skipped))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Body)
}

func (h *Handlers) writeError(w http.ResponseWriter, err error, internal string) {
	var validation *svc.ValidationError
	var labelValidation *ai.LabelValidationError
	switch {
	case errors.As(err, &validation):
		apierrors.WriteValidationError(w, validation.Msg)
	case errors.As(err, &labelValidation):
		apierrors.WriteValidationError(w, labelValidation.Msg)
	case errors.Is(err, svc.ErrNotFound):
		apierrors.WriteNotFoundError(w, err.Error())
	case errors.Is(err, svc.ErrAnalysisNotReady):
		apierrors.WriteError(w, http.StatusConflict, "analysis_not_ready", "This incident has no completed analysis to label yet.")
	case errors.Is(err, svc.ErrAnalysisNotLabelable):
		apierrors.WriteError(w, http.StatusConflict, "analysis_not_labelable", "This analysis was produced before labels were recorded, or sent no decisions to the model, so it cannot be labelled. Run a new analysis to label it.")
	case errors.Is(err, svc.ErrPairUnavailable):
		apierrors.WriteError(w, http.StatusConflict, "pair_evidence_unavailable", "These monitors are no longer a dependency candidate (disabled, a group, or no remaining shared signal), so the suggestion cannot be recorded.")
	case errors.Is(err, svc.ErrExportTooLarge):
		apierrors.WriteError(w, http.StatusBadRequest, "export_too_large", "More than 5,000 labels or 20,000 training rows match; narrow since/until.")
	default:
		h.logger.WithError(err).Error("AI label request failed")
		apierrors.WriteInternalError(w, internal)
	}
}

// decodeBody strictly decodes a bounded JSON body: unknown fields (an
// "evidence" field, say) and trailing data are rejected. allowEmpty accepts
// an empty body as the zero value.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return true
		}
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return false
	}
	if dec.Decode(new(any)) != io.EOF {
		apierrors.WriteValidationError(w, "invalid request body: trailing data")
		return false
	}
	return true
}

func tenantFromContext(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "tenant ID not found")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(tenantID)
	if err != nil {
		apierrors.WriteInternalError(w, "invalid tenant ID")
		return uuid.Nil, false
	}
	return id, true
}

// labeledBy is the signed-in user, or nil for API keys.
func labeledBy(r *http.Request) *uuid.UUID {
	adminID, err := middleware.GetAdminID(r.Context())
	if err != nil {
		return nil
	}
	id, err := uuid.Parse(adminID)
	if err != nil {
		return nil
	}
	return &id
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

package statuspages

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	statuspageservice "github.com/yassinebenameur/probara/api/internal/services/statuspages"
)

func (h *Handlers) libraryRequestScope(w http.ResponseWriter, r *http.Request, withID bool) (tenantID, templateID uuid.UUID, ok bool) {
	tenantIDStr, err := middleware.GetTenantID(r.Context())
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "tenant ID not found")
		return uuid.Nil, uuid.Nil, false
	}
	tenantID, err = uuid.Parse(tenantIDStr)
	if err != nil {
		apierrors.WriteInternalError(w, "invalid tenant ID")
		return uuid.Nil, uuid.Nil, false
	}
	if withID {
		templateID, err = uuid.Parse(chi.URLParam(r, "templateId"))
		if err != nil {
			apierrors.WriteValidationError(w, "invalid template ID")
			return uuid.Nil, uuid.Nil, false
		}
	}
	return tenantID, templateID, true
}

func (h *Handlers) writeLibraryError(w http.ResponseWriter, err error, action string, tenantID uuid.UUID) {
	switch {
	case err.Error() == "library template not found":
		apierrors.WriteNotFoundError(w, err.Error())
	case errors.Is(err, statuspageservice.ErrInvalidTemplate),
		err.Error() == "template name already exists":
		apierrors.WriteValidationError(w, err.Error())
	default:
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID.String(),
		}).Error("Failed to " + action)
		apierrors.WriteInternalError(w, "failed to "+action)
	}
}

// ListLibraryTemplates handles GET /api/v1/status-page-templates
func (h *Handlers) ListLibraryTemplates(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.libraryRequestScope(w, r, false)
	if !ok {
		return
	}
	result, err := h.service.ListLibraryTemplates(r.Context(), tenantID)
	if err != nil {
		h.writeLibraryError(w, err, "list library templates", tenantID)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// CreateLibraryTemplate handles POST /api/v1/status-page-templates
func (h *Handlers) CreateLibraryTemplate(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.libraryRequestScope(w, r, false)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTemplateUploadBytes)
	var req models.CreateStatusPageLibraryTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	item, err := h.service.CreateLibraryTemplate(r.Context(), tenantID, &req)
	if err != nil {
		h.writeLibraryError(w, err, "create library template", tenantID)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(item)
}

// GetLibraryTemplate handles GET /api/v1/status-page-templates/{templateId}
func (h *Handlers) GetLibraryTemplate(w http.ResponseWriter, r *http.Request) {
	tenantID, templateID, ok := h.libraryRequestScope(w, r, true)
	if !ok {
		return
	}
	item, err := h.service.GetLibraryTemplate(r.Context(), tenantID, templateID)
	if err != nil {
		h.writeLibraryError(w, err, "get library template", tenantID)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(item)
}

// GetLibraryTemplateSource handles GET /api/v1/status-page-templates/{templateId}/source
func (h *Handlers) GetLibraryTemplateSource(w http.ResponseWriter, r *http.Request) {
	tenantID, templateID, ok := h.libraryRequestScope(w, r, true)
	if !ok {
		return
	}
	item, err := h.service.GetLibraryTemplate(r.Context(), tenantID, templateID)
	if err != nil {
		h.writeLibraryError(w, err, "get library template", tenantID)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+item.Name+`.gohtml"`)
	w.Write([]byte(item.Source))
}

// UpdateLibraryTemplate handles PATCH /api/v1/status-page-templates/{templateId}
func (h *Handlers) UpdateLibraryTemplate(w http.ResponseWriter, r *http.Request) {
	tenantID, templateID, ok := h.libraryRequestScope(w, r, true)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTemplateUploadBytes)
	var req models.UpdateStatusPageLibraryTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	item, err := h.service.UpdateLibraryTemplate(r.Context(), tenantID, templateID, &req)
	if err != nil {
		h.writeLibraryError(w, err, "update library template", tenantID)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(item)
}

// DeleteLibraryTemplate handles DELETE /api/v1/status-page-templates/{templateId}
func (h *Handlers) DeleteLibraryTemplate(w http.ResponseWriter, r *http.Request) {
	tenantID, templateID, ok := h.libraryRequestScope(w, r, true)
	if !ok {
		return
	}
	if err := h.service.DeleteLibraryTemplate(r.Context(), tenantID, templateID); err != nil {
		h.writeLibraryError(w, err, "delete library template", tenantID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

package statuspages

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	statuspageservice "github.com/yassinebenameur/probara/api/internal/services/statuspages"
	"github.com/yassinebenameur/probara/shared/statustemplate"
)

// previewSecretEnvVar mirrors the status-page service: when both deployments
// share this secret, GetTemplateState mints a token that authorizes the
// public draft-preview route.
const previewSecretEnvVar = "STATUS_PAGE_PREVIEW_SECRET"

const previewTokenTTL = time.Hour

// maxTemplateUploadBytes bounds template upload request bodies: the source
// cap plus slack for JSON encoding overhead.
const maxTemplateUploadBytes = statustemplate.MaxSourceSize + 64*1024

func (h *Handlers) templateRequestScope(w http.ResponseWriter, r *http.Request) (tenantID, pageID uuid.UUID, ok bool) {
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
	pageID, err = uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid status page ID")
		return uuid.Nil, uuid.Nil, false
	}
	return tenantID, pageID, true
}

func (h *Handlers) writeTemplateError(w http.ResponseWriter, r *http.Request, err error, action string, tenantID, pageID uuid.UUID) {
	switch {
	case err.Error() == "status page not found" || err.Error() == "template version not found":
		apierrors.WriteNotFoundError(w, err.Error())
	case errors.Is(err, statuspageservice.ErrInvalidTemplate),
		err.Error() == "no draft template to publish",
		strings.HasSuffix(err.Error(), "is already published"):
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

// writeTemplateState responds with the page's current template state,
// including a fresh preview token when the deployment configures the shared
// preview secret.
func (h *Handlers) writeTemplateState(w http.ResponseWriter, r *http.Request, tenantID, pageID uuid.UUID) {
	state, err := h.service.GetTemplateState(r.Context(), tenantID, pageID)
	if err != nil {
		h.writeTemplateError(w, r, err, "get template state", tenantID, pageID)
		return
	}
	if secret := strings.TrimSpace(os.Getenv(previewSecretEnvVar)); secret != "" {
		state.PreviewToken = statustemplate.MintPreviewToken(secret, pageID.String(), time.Now().Add(previewTokenTTL))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

// GetTemplateState handles GET /api/v1/status-pages/{id}/template
func (h *Handlers) GetTemplateState(w http.ResponseWriter, r *http.Request) {
	tenantID, pageID, ok := h.templateRequestScope(w, r)
	if !ok {
		return
	}
	h.writeTemplateState(w, r, tenantID, pageID)
}

// GetDefaultTemplate handles GET /api/v1/status-pages/{id}/template/default.
// It exports the built-in template source as the starting point for
// customization.
func (h *Handlers) GetDefaultTemplate(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.templateRequestScope(w, r); !ok {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="status-page-template.gohtml"`)
	w.Write([]byte(statustemplate.DefaultSource))
}

// GetTemplateVersionSource handles GET /api/v1/status-pages/{id}/template/versions/{version}/source
func (h *Handlers) GetTemplateVersionSource(w http.ResponseWriter, r *http.Request) {
	tenantID, pageID, ok := h.templateRequestScope(w, r)
	if !ok {
		return
	}
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid template version")
		return
	}
	source, err := h.service.GetTemplateVersionSource(r.Context(), tenantID, pageID, version)
	if err != nil {
		h.writeTemplateError(w, r, err, "get template version", tenantID, pageID)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="status-page-template-v`+strconv.Itoa(version)+`.gohtml"`)
	w.Write([]byte(source))
}

// SaveTemplateDraft handles PUT /api/v1/status-pages/{id}/template/draft
func (h *Handlers) SaveTemplateDraft(w http.ResponseWriter, r *http.Request) {
	tenantID, pageID, ok := h.templateRequestScope(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTemplateUploadBytes)
	var req models.SaveStatusPageTemplateDraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	if err := h.service.SaveTemplateDraft(r.Context(), tenantID, pageID, req.Source); err != nil {
		h.writeTemplateError(w, r, err, "save template draft", tenantID, pageID)
		return
	}
	h.writeTemplateState(w, r, tenantID, pageID)
}

// DiscardTemplateDraft handles DELETE /api/v1/status-pages/{id}/template/draft
func (h *Handlers) DiscardTemplateDraft(w http.ResponseWriter, r *http.Request) {
	tenantID, pageID, ok := h.templateRequestScope(w, r)
	if !ok {
		return
	}
	if err := h.service.DiscardTemplateDraft(r.Context(), tenantID, pageID); err != nil {
		h.writeTemplateError(w, r, err, "discard template draft", tenantID, pageID)
		return
	}
	h.writeTemplateState(w, r, tenantID, pageID)
}

// PublishTemplate handles POST /api/v1/status-pages/{id}/template/publish
func (h *Handlers) PublishTemplate(w http.ResponseWriter, r *http.Request) {
	tenantID, pageID, ok := h.templateRequestScope(w, r)
	if !ok {
		return
	}
	if err := h.service.PublishTemplateDraft(r.Context(), tenantID, pageID); err != nil {
		h.writeTemplateError(w, r, err, "publish template", tenantID, pageID)
		return
	}
	h.writeTemplateState(w, r, tenantID, pageID)
}

// RevertTemplate handles POST /api/v1/status-pages/{id}/template/revert
func (h *Handlers) RevertTemplate(w http.ResponseWriter, r *http.Request) {
	tenantID, pageID, ok := h.templateRequestScope(w, r)
	if !ok {
		return
	}
	var req models.RevertStatusPageTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	if err := h.service.RevertTemplate(r.Context(), tenantID, pageID, req.Version); err != nil {
		h.writeTemplateError(w, r, err, "revert template", tenantID, pageID)
		return
	}
	h.writeTemplateState(w, r, tenantID, pageID)
}

// ResetTemplate handles DELETE /api/v1/status-pages/{id}/template.
// The page goes back to the built-in template; history is preserved.
func (h *Handlers) ResetTemplate(w http.ResponseWriter, r *http.Request) {
	tenantID, pageID, ok := h.templateRequestScope(w, r)
	if !ok {
		return
	}
	if err := h.service.ResetTemplate(r.Context(), tenantID, pageID); err != nil {
		h.writeTemplateError(w, r, err, "reset template", tenantID, pageID)
		return
	}
	h.writeTemplateState(w, r, tenantID, pageID)
}

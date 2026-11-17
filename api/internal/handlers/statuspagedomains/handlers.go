// Package statuspagedomains serves status pages' custom domains:
//
//	GET    /api/v1/status-page-domains[?status_page_id=]  the tenant's domains (or one page's), with the CNAME target
//	POST   /api/v1/status-page-domains                    add one to a page (pending until verified)
//	DELETE /api/v1/status-page-domains/{domainId}         remove one; it stops being served at once
//	POST   /api/v1/status-page-domains/{domainId}/verify  check its DNS records now
//
// Top-level rather than under /status-pages/{id} so the generic audit names
// the writes status-page-domain.create/delete/verify, with the domain as the
// resource. They sit in the protected group: RequireWrite gates the writes.
package statuspagedomains

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	svc "github.com/yassinebenameur/probara/api/internal/services/statuspagedomains"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/pagedomains"
)

// maxRequestBytes bounds a create request (a page ID and a hostname).
const maxRequestBytes = 4096

type Handlers struct {
	Service *svc.Service
	Log     *logger.Logger
}

// Routes mounts /status-page-domains.
func (h *Handlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Delete("/{domainId}", h.remove)
	r.Post("/{domainId}/verify", h.verify)
}

func (h *Handlers) fail(w http.ResponseWriter, err error, action string) {
	var recent *svc.CheckedRecentlyError
	switch {
	case errors.Is(err, svc.ErrNotFound), errors.Is(err, svc.ErrStatusPageNotFound):
		apierrors.WriteNotFoundError(w, err.Error())
	case errors.Is(err, svc.ErrHostnameTaken), errors.Is(err, svc.ErrHostnameReserved):
		apierrors.WriteError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, svc.ErrDisabled), errors.Is(err, svc.ErrTooManyDomains):
		apierrors.WriteValidationError(w, err.Error())
	case errors.As(err, &recent):
		w.Header().Set("Retry-After", strconv.Itoa(int(recent.RetryAfter.Seconds()+0.999)))
		apierrors.WriteError(w, http.StatusTooManyRequests, "rate_limited", err.Error())
	default:
		if h.Log != nil {
			h.Log.WithError(err).Error("Failed to " + action)
		}
		apierrors.WriteInternalError(w, "failed to "+action)
	}
}

func domainID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "domainId"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid domain ID")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	var pageID *uuid.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("status_page_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			apierrors.WriteValidationError(w, "invalid status_page_id")
			return
		}
		pageID = &id
	}
	resp, err := h.Service.List(r.Context(), tenantID, pageID)
	if err != nil {
		h.fail(w, err, "list custom domains")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	var req models.CreateStatusPageDomainRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		apierrors.WriteValidationError(w, "expected one JSON object")
		return
	}
	pageID, err := uuid.Parse(strings.TrimSpace(req.StatusPageID))
	if err != nil {
		apierrors.WriteValidationError(w, "status_page_id must be a status page ID")
		return
	}
	hostname, err := pagedomains.NormalizeHostname(req.Hostname)
	if err != nil {
		apierrors.WriteValidationError(w, err.Error())
		return
	}
	created, err := h.Service.Create(r.Context(), tenantID, pageID, hostname)
	if err != nil {
		h.fail(w, err, "add custom domain")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (h *Handlers) remove(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	id, ok := domainID(w, r)
	if !ok {
		return
	}
	if err := h.Service.Delete(r.Context(), tenantID, id); err != nil {
		h.fail(w, err, "remove custom domain")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) verify(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	id, ok := domainID(w, r)
	if !ok {
		return
	}
	domain, err := h.Service.Verify(r.Context(), tenantID, id)
	if err != nil {
		h.fail(w, err, "verify custom domain")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, domain)
}

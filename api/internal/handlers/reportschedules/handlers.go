// Package reportschedules serves an SLA's emailed report schedule and the
// public unsubscribe link its emails carry:
//
//	GET    /api/v1/sla-report-schedules/{id}  the SLA's schedule and whether mail is configured
//	PUT    /api/v1/sla-report-schedules/{id}  create or replace it
//	DELETE /api/v1/sla-report-schedules/{id}  delete it
//	GET    /api/v1/report-unsubscribe          describe an unsubscribe link, never act
//	POST   /api/v1/report-unsubscribe          unsubscribe
//
// The schedule routes sit in the protected group (RequireWrite, generic
// audit as sla-report-schedule.*). The unsubscribe routes are public: the
// signed token (shared/actionlink) is the credential. The confirm page
// sends it in the TokenHeader; the RFC 8058 one-click POST that mail
// clients make cannot set headers, so a POST also takes it from the token
// query parameter. The request log records the path only, and every
// refusal answers the same, so probing learns nothing.
package reportschedules

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	svc "github.com/yassinebenameur/probara/api/internal/services/reportschedules"
	"github.com/yassinebenameur/probara/shared/logger"
)

// TokenHeader carries the unsubscribe token from the confirm page; the same
// header the acknowledge links use.
const TokenHeader = "X-Action-Token"

type Handlers struct {
	Service *svc.Service
	Log     *logger.Logger
}

// Routes mounts /sla-report-schedules, keyed by SLA ID.
func (h *Handlers) Routes(r chi.Router) {
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.put)
	r.Delete("/{id}", h.remove)
}

// PublicRoutes mounts /report-unsubscribe.
func (h *Handlers) PublicRoutes(r chi.Router) {
	r.Get("/", h.describe)
	r.Post("/", h.unsubscribe)
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")
}

func (h *Handlers) fail(w http.ResponseWriter, err error, action string) {
	var bad svc.Invalid
	switch {
	case errors.Is(err, svc.ErrNotFound):
		apierrors.WriteNotFoundError(w, "not found")
	case errors.As(err, &bad):
		apierrors.WriteValidationError(w, bad.Error())
	default:
		if h.Log != nil {
			h.Log.WithError(err).Error("Failed to " + action)
		}
		apierrors.WriteInternalError(w, "failed to "+action)
	}
}

func slaID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid SLA ID")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	id, ok := slaID(w, r)
	if !ok {
		return
	}
	resp, err := h.Service.Get(r.Context(), t, id)
	if err != nil {
		h.fail(w, err, "get report schedule")
		return
	}
	noStore(w)
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handlers) put(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	id, ok := slaID(w, r)
	if !ok {
		return
	}
	var req models.PutSLAReportScheduleRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		apierrors.WriteValidationError(w, "expected one JSON object")
		return
	}
	resp, err := h.Service.Put(r.Context(), t, id, &req)
	if err != nil {
		h.fail(w, err, "save report schedule")
		return
	}
	noStore(w)
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handlers) remove(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	id, ok := slaID(w, r)
	if !ok {
		return
	}
	if err := h.Service.Delete(r.Context(), t, id); err != nil {
		h.fail(w, err, "delete report schedule")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// describe handles GET. A browser that opened the List-Unsubscribe URL
// itself (it carries the token as a query parameter) is sent to the confirm
// page with the token moved into the fragment; nothing is changed by a GET.
func (h *Handlers) describe(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.Header.Get(TokenHeader))
	if token == "" {
		if q := strings.TrimSpace(r.URL.Query().Get("token")); q != "" {
			noStore(w)
			http.Redirect(w, r, svc.UnsubscribePagePath+"#"+url.PathEscape(q), http.StatusSeeOther)
			return
		}
	}
	resp, err := h.Service.DescribeUnsubscribe(r.Context(), token)
	if err != nil {
		h.internal(w, err)
		return
	}
	writeState(w, resp)
}

// unsubscribe handles POST, from the confirm page (header) or a mail
// client's one-click unsubscribe (query, RFC 8058). The body, when there is
// one, is not needed.
func (h *Handlers) unsubscribe(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.Header.Get(TokenHeader))
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	resp, err := h.Service.Unsubscribe(r.Context(), token, audit.FromRequest(r))
	if err != nil {
		h.internal(w, err)
		return
	}
	writeState(w, resp)
}

func (h *Handlers) internal(w http.ResponseWriter, err error) {
	if h.Log != nil {
		h.Log.WithError(err).Error("Report unsubscribe failed")
	}
	noStore(w)
	apierrors.WriteInternalError(w, "unsubscribe failed")
}

// writeState answers 404 for an invalid link and 410 for an expired one, 200
// otherwise.
func writeState(w http.ResponseWriter, resp models.ReportUnsubscribeResponse) {
	status := http.StatusOK
	switch resp.State {
	case models.ReportUnsubscribeInvalid:
		status = http.StatusNotFound
	case models.ReportUnsubscribeExpired:
		status = http.StatusGone
	}
	noStore(w)
	httpx.WriteJSON(w, status, resp)
}

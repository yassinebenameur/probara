// Package statuspagesubscribers serves a status page's email subscribers:
//
//	GET  /api/v1/status-pages/{id}/subscribers/summary                 counts, mail readiness, recent notices (any member)
//	GET  /api/v1/status-pages/{id}/subscribers                         the addresses (write access)
//	GET  /api/v1/status-pages/{id}/subscribers/export                  CSV of every subscriber (write access)
//	POST /api/v1/status-pages/{id}/subscribers/{subscriberId}/remove   delete one
//	POST /api/v1/status-pages/{id}/subscribers/remove                  delete all, or those at a domain
//	POST /api/v1/public/status-pages/{slug}/subscribe                  a visitor's subscribe form (status-page service only)
//	POST /api/v1/public/status-page-subscriptions/confirm              follow a confirmation link (status-page service only)
//	GET  /api/v1/public/status-page-subscriptions/unsubscribe          a browser on the one-click URL: redirect, never act
//	POST /api/v1/public/status-page-subscriptions/unsubscribe          unsubscribe (link or RFC 8058 one-click), anyone
//
// The operator routes sit in the protected group (RequireWrite, generic
// audit for the removes; the export records its own event). Subscriber
// addresses are personal data, so reading them needs write access even on a
// GET; the summary is for every member. The subscribe and confirm routes
// answer only the status-page service (InternalTokenHeader, its
// STATUS_PAGE_INTERNAL_TOKEN), so its visibility gate and per-client
// throttle cannot be bypassed; anyone else gets 404. The token routes take
// the signed token (shared/actionlink) as the credential: in the
// TokenHeader, the JSON body, or (one-click, which cannot set either) the
// token query parameter, which the request log never records.
package statuspagesubscribers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/httpx"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	svc "github.com/yassinebenameur/probara/api/internal/services/pagesubscribers"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/pageaccess"
)

// TokenHeader carries a confirmation or unsubscribe token; the header the
// acknowledge and report unsubscribe links use.
const TokenHeader = "X-Action-Token"

// InternalTokenHeader carries STATUS_PAGE_INTERNAL_TOKEN on the requests
// the status-page service forwards (the shared contract).
const InternalTokenHeader = pageaccess.ForwardTokenHeader

// maxPublicBody bounds a public request body.
const maxPublicBody = 4096

// Handlers serves the routes above.
type Handlers struct {
	Service *svc.Service
	Audit   svc.AuditRecorder
	Log     *logger.Logger
}

// Routes mounts a page's subscriber routes under /status-pages.
func (h *Handlers) Routes(r chi.Router) {
	r.Route("/{id}/subscribers", func(r chi.Router) {
		r.Get("/summary", h.summary)
		r.Get("/", h.list)
		r.Get("/export", h.export)
		// POST …/remove rather than DELETE, like the kiosk tokens' revoke:
		// the generic audit then names it status-page.remove.
		r.Post("/remove", h.removeMany)
		r.Post("/{subscriberId}/remove", h.remove)
	})
}

// PublicRoutes mounts the visitor routes under /public.
func (h *Handlers) PublicRoutes(r chi.Router) {
	r.Post("/status-pages/{slug}/subscribe", h.subscribe)
	r.Post("/status-page-subscriptions/confirm", h.confirm)
	r.Get("/status-page-subscriptions/unsubscribe", h.unsubscribeRedirect)
	r.Post("/status-page-subscriptions/unsubscribe", h.unsubscribe)
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
	case errors.Is(err, svc.ErrLimit):
		w.Header().Set("Retry-After", "3600")
		apierrors.WriteError(w, http.StatusTooManyRequests, "limit_reached", err.Error())
	case errors.As(err, &bad):
		apierrors.WriteValidationError(w, bad.Error())
	default:
		if h.Log != nil {
			h.Log.WithError(err).Error("Failed to " + action)
		}
		apierrors.WriteInternalError(w, "failed to "+action)
	}
}

func pageID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid status page ID")
		return uuid.Nil, false
	}
	return id, true
}

// writeAccess refuses a caller without write access: subscriber addresses
// are personal data, not workspace configuration a viewer reads.
func writeAccess(w http.ResponseWriter, r *http.Request) bool {
	if !ctxpkg.CanWrite(r.Context()) {
		apierrors.WriteForbiddenError(w, "subscriber addresses require write permission; the summary shows the counts")
		return false
	}
	return true
}

func (h *Handlers) summary(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	id, ok := pageID(w, r)
	if !ok {
		return
	}
	resp, err := h.Service.Summary(r.Context(), t, id)
	if err != nil {
		h.fail(w, err, "read subscriber summary")
		return
	}
	noStore(w)
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handlers) removeMany(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	id, ok := pageID(w, r)
	if !ok {
		return
	}
	var req models.RemoveStatusPageSubscribersRequest
	if !decode(w, r, &req) {
		return
	}
	n, err := h.Service.RemoveMany(r.Context(), t, id, &req)
	if err != nil {
		h.fail(w, err, "remove subscribers")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, models.RemoveStatusPageSubscribersResponse{Removed: n})
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	if !writeAccess(w, r) {
		return
	}
	t, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	id, ok := pageID(w, r)
	if !ok {
		return
	}
	page, size := httpx.Page(r)
	resp, err := h.Service.List(r.Context(), t, id, page, size)
	if err != nil {
		h.fail(w, err, "list subscribers")
		return
	}
	noStore(w)
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// export writes the CSV and records who exported which page's addresses (a
// read, so the generic audit would not).
func (h *Handlers) export(w http.ResponseWriter, r *http.Request) {
	if !writeAccess(w, r) {
		return
	}
	t, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	id, ok := pageID(w, r)
	if !ok {
		return
	}
	var buf bytes.Buffer
	n, err := h.Service.ExportCSV(r.Context(), t, id, &buf)
	if err != nil {
		h.fail(w, err, "export subscribers")
		return
	}
	if h.Audit != nil {
		event := audit.FromRequest(r)
		event.TenantID = &t
		event.Action = "status-page.subscribers-export"
		event.ResourceType = "status-page"
		event.ResourceID = id.String()
		event.Outcome = audit.OutcomeSuccess
		event.MergeDetails(map[string]any{"subscribers": n})
		h.Audit.Record(event)
	}
	noStore(w)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="status-page-subscribers-%s.csv"`, time.Now().UTC().Format("2006-01-02")))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, &buf)
}

func (h *Handlers) remove(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.TenantID(w, r)
	if !ok {
		return
	}
	id, ok := pageID(w, r)
	if !ok {
		return
	}
	subscriberID, err := uuid.Parse(chi.URLParam(r, "subscriberId"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid subscriber ID")
		return
	}
	if err := h.Service.Remove(r.Context(), t, id, subscriberID); err != nil {
		h.fail(w, err, "remove subscriber")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decode reads one small JSON object.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxPublicBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		apierrors.WriteValidationError(w, "invalid request body")
		return false
	}
	return true
}

// subscribe answers 202 for every address the page accepts, whatever its
// state, so the endpoint never says whether an address is subscribed.
func (h *Handlers) subscribe(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !h.Service.ForwardedByStatusPage(r.Header.Get(InternalTokenHeader)) {
		apierrors.WriteNotFoundError(w, "not found")
		return
	}
	var req models.StatusPageSubscribeRequest
	if !decode(w, r, &req) {
		return
	}
	if err := h.Service.Subscribe(r.Context(), chi.URLParam(r, "slug"), req.Email); err != nil {
		h.fail(w, err, "subscribe")
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, models.StatusPageSubscribeResponse{State: models.StatusPageSubscribeCheckInbox})
}

// token reads the token from the header, the query (one-click) or a JSON
// body, in that order.
func token(w http.ResponseWriter, r *http.Request) (string, bool) {
	if t := strings.TrimSpace(r.Header.Get(TokenHeader)); t != "" {
		return t, true
	}
	if t := strings.TrimSpace(r.URL.Query().Get("token")); t != "" {
		return t, true
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var req models.StatusPageSubscriptionTokenRequest
		if !decode(w, r, &req) {
			return "", false
		}
		return req.Token, true
	}
	return "", true
}

func (h *Handlers) confirm(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !h.Service.ForwardedByStatusPage(r.Header.Get(InternalTokenHeader)) {
		apierrors.WriteNotFoundError(w, "not found")
		return
	}
	t, ok := token(w, r)
	if !ok {
		return
	}
	resp, err := h.Service.Confirm(r.Context(), t, audit.FromRequest(r))
	if err != nil {
		h.fail(w, err, "confirm subscription")
		return
	}
	writeState(w, resp)
}

func (h *Handlers) unsubscribe(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	t, ok := token(w, r)
	if !ok {
		return
	}
	resp, err := h.Service.Unsubscribe(r.Context(), t, audit.FromRequest(r))
	if err != nil {
		h.fail(w, err, "unsubscribe")
		return
	}
	writeState(w, resp)
}

// unsubscribeRedirect handles a browser that opened the one-click URL
// itself: it is sent to the page's unsubscribe confirmation with the token
// moved into the fragment. A GET never unsubscribes (mail scanners fetch
// every link they see).
func (h *Handlers) unsubscribeRedirect(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	target, err := h.Service.UnsubscribePageURL(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		h.fail(w, err, "resolve unsubscribe link")
		return
	}
	if target == "" {
		apierrors.WriteNotFoundError(w, "this link is not valid")
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// writeState answers 404 for an invalid link and 410 for an expired one,
// 200 otherwise.
func writeState(w http.ResponseWriter, resp models.StatusPageSubscriptionResponse) {
	status := http.StatusOK
	switch resp.State {
	case models.StatusPageSubscriptionInvalid:
		status = http.StatusNotFound
	case models.StatusPageSubscriptionExpired:
		status = http.StatusGone
	}
	httpx.WriteJSON(w, status, resp)
}

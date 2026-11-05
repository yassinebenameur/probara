// Package reportschedules serves SLA report schedules (/report-schedules,
// inside the protected group) and the public unsubscribe link their emails
// carry (/report-unsubscribe/{token}).
package reportschedules

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	svc "github.com/yassinebenameur/probara/api/internal/services/reportschedules"
	"github.com/yassinebenameur/probara/shared/logger"
)

type Handlers struct {
	Service *svc.Service
	Log     *logger.Logger
}

// Routes mounts /report-schedules. Reads are open to viewers; writes and
// send go through RequireWrite like every other mutation.
func (h *Handlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Patch("/{id}", h.update)
	r.Delete("/{id}", h.remove)
	r.Post("/{id}/send", h.send)
}

// UnsubscribeRoutes mounts /report-unsubscribe. Public: the token is the
// credential. GET describes and never acts (mail scanners prefetch links);
// POST unsubscribes, for the confirm page and RFC 8058 one-click.
func (h *Handlers) UnsubscribeRoutes(r chi.Router) {
	r.Get("/{token}", h.describeUnsubscribe)
	r.Post("/{token}", h.unsubscribe)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handlers) fail(w http.ResponseWriter, err error, action string) {
	var bad svc.Invalid
	switch {
	case errors.Is(err, svc.ErrNotFound):
		apierrors.WriteNotFoundError(w, "report schedule not found")
	case errors.Is(err, svc.ErrSLANotFound):
		apierrors.WriteNotFoundError(w, "SLA not found")
	case errors.As(err, &bad):
		apierrors.WriteValidationError(w, bad.Error())
	default:
		h.Log.WithError(err).Error("Failed to " + action)
		apierrors.WriteInternalError(w, "failed to "+action)
	}
}

func tenant(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	s, err := middleware.GetTenantID(r.Context())
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "tenant ID not found")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "invalid tenant ID")
		return uuid.Nil, false
	}
	return id, true
}

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid ID")
		return uuid.Nil, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		apierrors.WriteValidationError(w, "invalid request body: "+err.Error())
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		apierrors.WriteValidationError(w, "expected one JSON object")
		return false
	}
	return true
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	slaID := uuid.Nil
	if raw := r.URL.Query().Get("sla_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			apierrors.WriteValidationError(w, "invalid sla_id")
			return
		}
		slaID = id
	}
	res, err := h.Service.List(r.Context(), t, slaID)
	if err != nil {
		h.fail(w, err, "list report schedules")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	var req svc.CreateRequest
	if !decode(w, r, &req) {
		return
	}
	sc, err := h.Service.Create(r.Context(), t, &req)
	if err != nil {
		h.fail(w, err, "create report schedule")
		return
	}
	writeJSON(w, http.StatusCreated, sc)
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	sc, err := h.Service.Get(r.Context(), t, id)
	if err != nil {
		h.fail(w, err, "get report schedule")
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (h *Handlers) update(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req svc.UpdateRequest
	if !decode(w, r, &req) {
		return
	}
	sc, err := h.Service.Update(r.Context(), t, id, &req)
	if err != nil {
		h.fail(w, err, "update report schedule")
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (h *Handlers) remove(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.Service.Delete(r.Context(), t, id); err != nil {
		h.fail(w, err, "delete report schedule")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) send(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	out, err := h.Service.SendNow(r.Context(), t, id)
	if err != nil {
		h.fail(w, err, "send report")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// unsubscribeStatus: an invalid token is a 404 so a link checker sees a dead
// link; every other state is a 200.
func unsubscribeStatus(st svc.UnsubscribeStatus) int {
	if st.State == svc.StateInvalid {
		return http.StatusNotFound
	}
	return http.StatusOK
}

func (h *Handlers) describeUnsubscribe(w http.ResponseWriter, r *http.Request) {
	st, err := h.Service.DescribeUnsubscribe(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		h.Log.WithError(err).Error("Failed to describe report unsubscribe link")
		apierrors.WriteInternalError(w, "failed to load the unsubscribe link")
		return
	}
	writeJSON(w, unsubscribeStatus(st), st)
}

func (h *Handlers) unsubscribe(w http.ResponseWriter, r *http.Request) {
	// The RFC 8058 body ("List-Unsubscribe=One-Click") carries nothing the
	// token does not; drain a bounded amount and ignore it.
	_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 4<<10))
	st, err := h.Service.Unsubscribe(r.Context(), r, chi.URLParam(r, "token"))
	if err != nil {
		h.Log.WithError(err).Error("Failed to unsubscribe from report schedule")
		apierrors.WriteInternalError(w, "failed to unsubscribe")
		return
	}
	writeJSON(w, unsubscribeStatus(st), st)
}

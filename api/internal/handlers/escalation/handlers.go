// Package escalation serves escalation policies (/escalation-policies).
package escalation

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	svc "github.com/yassinebenameur/probara/api/internal/services/escalation"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers serves the policy routes.
type Handlers struct {
	Service *svc.Service
	Log     *logger.Logger
}

// Routes mounts /escalation-policies. Reads are open to every member;
// writes go through the protected group's write gate.
func (h *Handlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Post("/convert", h.convert)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.replace)
	r.Delete("/{id}", h.remove)
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	out, err := h.Service.List(r.Context(), t)
	if err != nil {
		h.fail(w, err, "list escalation policies")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	t, id, ok := scope(w, r)
	if !ok {
		return
	}
	out, err := h.Service.Get(r.Context(), t, id)
	if err != nil {
		h.fail(w, err, "load escalation policy")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	var req svc.WriteRequest
	if !decode(w, r, &req) {
		return
	}
	out, err := h.Service.Create(r.Context(), t, req)
	if err != nil {
		h.fail(w, err, "create escalation policy")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handlers) replace(w http.ResponseWriter, r *http.Request) {
	t, id, ok := scope(w, r)
	if !ok {
		return
	}
	var req svc.WriteRequest
	if !decode(w, r, &req) {
		return
	}
	out, err := h.Service.Replace(r.Context(), t, id, req)
	if err != nil {
		h.fail(w, err, "update escalation policy")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) remove(w http.ResponseWriter, r *http.Request) {
	t, id, ok := scope(w, r)
	if !ok {
		return
	}
	if err := h.Service.Delete(r.Context(), t, id); err != nil {
		h.fail(w, err, "delete escalation policy")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) convert(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	var req svc.ConvertRequest
	if !decode(w, r, &req) {
		return
	}
	out, err := h.Service.ConvertRoutes(r.Context(), t, req)
	if err != nil {
		h.fail(w, err, "convert routes to an escalation policy")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
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

func (h *Handlers) fail(w http.ResponseWriter, err error, action string) {
	var bad svc.Invalid
	var conflict svc.Conflict
	switch {
	case errors.Is(err, svc.ErrNotFound):
		apierrors.WriteNotFoundError(w, "not found")
	case errors.As(err, &bad):
		apierrors.WriteValidationError(w, bad.Error())
	case errors.As(err, &conflict):
		apierrors.WriteError(w, http.StatusConflict, "conflict", conflict.Error())
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

func scope(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	t, ok := tenant(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid id")
		return uuid.Nil, uuid.Nil, false
	}
	return t, id, true
}

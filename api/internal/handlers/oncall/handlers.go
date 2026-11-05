// Package oncall serves on-call schedules (/oncall-schedules) and their
// public iCal feed (/oncall-feeds/{token}.ics).
package oncall

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	svc "github.com/yassinebenameur/probara/api/internal/services/oncall"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers serves the schedule routes.
type Handlers struct {
	Service *svc.Service
	Log     *logger.Logger
}

// Routes mounts /oncall-schedules. Reads are open to every member, except
// /people (write access, see people); writes go through the protected
// group's write gate.
func (h *Handlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/people", h.people)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.replace)
	r.Delete("/{id}", h.remove)
	r.Get("/{id}/on-call", h.onCall)
	r.Get("/{id}/preview", h.preview)
	r.Get("/{id}/overrides", h.listOverrides)
	r.Post("/{id}/overrides", h.createOverride)
	r.Delete("/{id}/overrides/{overrideId}", h.deleteOverride)
	r.Post("/{id}/ical-token", h.rotateICal)
	r.Delete("/{id}/ical-token", h.disableICal)
}

// Feed serves GET /oncall-feeds/{token}.ics. It is unauthenticated (the
// token is the credential, stored hashed) and read-only.
func (h *Handlers) Feed(w http.ResponseWriter, r *http.Request) {
	body, err := h.Service.Feed(r.Context(), chi.URLParam(r, "token"))
	if errors.Is(err, svc.ErrNotFound) {
		http.Error(w, "feed not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.Log.WithError(err).Error("Failed to render on-call feed")
		http.Error(w, "failed to render feed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="oncall.ics"`)
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = io.WriteString(w, body)
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

func uuidParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid "+name)
		return uuid.Nil, false
	}
	return id, true
}

// scope resolves the tenant and the schedule ID of a /{id} route.
func scope(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	t, ok := tenant(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	id, ok := uuidParam(w, r, "id")
	return t, id, ok
}

// timeQuery parses an optional RFC 3339 query parameter.
func timeQuery(w http.ResponseWriter, r *http.Request, name string) (time.Time, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		apierrors.WriteValidationError(w, name+" must be an RFC 3339 time")
		return time.Time{}, false
	}
	return t, true
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	items, err := h.Service.List(r.Context(), t)
	if err != nil {
		h.fail(w, err, "list on-call schedules")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// people serves the on-call picker. Only editing needs it, so it demands
// write access like a mutation: viewers and read-scope keys see who is on
// call through the schedules, never the workspace's user list.
func (h *Handlers) people(w http.ResponseWriter, r *http.Request) {
	if !ctxpkg.CanWrite(r.Context()) {
		apierrors.WriteForbiddenError(w, "read-only access: listing people for on-call requires write permission")
		return
	}
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	items, err := h.Service.People(r.Context(), t, ctxpkg.IsSuperadmin(r.Context()))
	if err != nil {
		h.fail(w, err, "list people")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	var in svc.ScheduleInput
	if !decode(w, r, &in) {
		return
	}
	out, err := h.Service.Create(r.Context(), t, in)
	if err != nil {
		h.fail(w, err, "create on-call schedule")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	t, id, ok := scope(w, r)
	if !ok {
		return
	}
	out, err := h.Service.Get(r.Context(), t, id)
	if err != nil {
		h.fail(w, err, "get on-call schedule")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) replace(w http.ResponseWriter, r *http.Request) {
	t, id, ok := scope(w, r)
	if !ok {
		return
	}
	var in svc.ScheduleInput
	if !decode(w, r, &in) {
		return
	}
	out, err := h.Service.Replace(r.Context(), t, id, in)
	if err != nil {
		h.fail(w, err, "update on-call schedule")
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
		h.fail(w, err, "delete on-call schedule")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) onCall(w http.ResponseWriter, r *http.Request) {
	t, id, ok := scope(w, r)
	if !ok {
		return
	}
	at, ok := timeQuery(w, r, "at")
	if !ok {
		return
	}
	out, err := h.Service.OnCall(r.Context(), t, id, at)
	if err != nil {
		h.fail(w, err, "resolve on-call")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) preview(w http.ResponseWriter, r *http.Request) {
	t, id, ok := scope(w, r)
	if !ok {
		return
	}
	from, ok := timeQuery(w, r, "from")
	if !ok {
		return
	}
	to, ok := timeQuery(w, r, "to")
	if !ok {
		return
	}
	out, err := h.Service.Preview(r.Context(), t, id, from, to)
	if err != nil {
		h.fail(w, err, "preview on-call schedule")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) listOverrides(w http.ResponseWriter, r *http.Request) {
	t, id, ok := scope(w, r)
	if !ok {
		return
	}
	from, ok := timeQuery(w, r, "from")
	if !ok {
		return
	}
	to, ok := timeQuery(w, r, "to")
	if !ok {
		return
	}
	items, err := h.Service.ListOverrides(r.Context(), t, id, from, to)
	if err != nil {
		h.fail(w, err, "list overrides")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handlers) createOverride(w http.ResponseWriter, r *http.Request) {
	t, id, ok := scope(w, r)
	if !ok {
		return
	}
	var req svc.CreateOverrideRequest
	if !decode(w, r, &req) {
		return
	}
	createdBy := uuid.Nil
	if raw, err := middleware.GetAdminID(r.Context()); err == nil {
		createdBy, _ = uuid.Parse(raw)
	}
	out, err := h.Service.CreateOverride(r.Context(), t, id, createdBy, req)
	if err != nil {
		h.fail(w, err, "create override")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handlers) deleteOverride(w http.ResponseWriter, r *http.Request) {
	t, id, ok := scope(w, r)
	if !ok {
		return
	}
	overrideID, ok := uuidParam(w, r, "overrideId")
	if !ok {
		return
	}
	if err := h.Service.DeleteOverride(r.Context(), t, id, overrideID); err != nil {
		h.fail(w, err, "delete override")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) rotateICal(w http.ResponseWriter, r *http.Request) {
	t, id, ok := scope(w, r)
	if !ok {
		return
	}
	out, err := h.Service.RotateICalToken(r.Context(), t, id)
	if err != nil {
		h.fail(w, err, "create feed link")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) disableICal(w http.ResponseWriter, r *http.Request) {
	t, id, ok := scope(w, r)
	if !ok {
		return
	}
	if err := h.Service.DisableICal(r.Context(), t, id); err != nil {
		h.fail(w, err, "disable feed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

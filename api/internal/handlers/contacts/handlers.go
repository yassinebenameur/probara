// Package contacts serves the signed-in user's verified contacts
// (/account/contacts) and the workspace people settings (/people-settings).
package contacts

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	svc "github.com/yassinebenameur/probara/api/internal/services/contacts"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Handlers serves the contact and people-settings routes.
type Handlers struct {
	Service *svc.Service
	Log     *logger.Logger
}

// AccountRoutes mounts /account/contacts. Like the session routes they are
// for a signed-in browser session only (no API key, no MCP token) and sit
// outside the write gate: every role, viewers included, manages its own
// contacts.
func (h *Handlers) AccountRoutes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Patch("/{id}", h.update)
	r.Delete("/{id}", h.remove)
	r.Post("/{id}/send-code", h.sendCode)
	r.Post("/{id}/verify", h.verify)
	r.Post("/{id}/test", h.test)
}

// SettingsRoutes mounts /people-settings: readable by every member, written
// by tenant admins.
func (h *Handlers) SettingsRoutes(r chi.Router) {
	r.Get("/", h.getSettings)
	r.With(middleware.RequireTenantAdmin).Put("/", h.updateSettings)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
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
	var unavailable svc.Unavailable
	var limited svc.RateLimited
	var delivery svc.DeliveryFailed
	var wrong svc.ErrWrongCode
	switch {
	case errors.Is(err, svc.ErrNotFound):
		apierrors.WriteNotFoundError(w, "contact not found")
	case errors.As(err, &bad):
		apierrors.WriteValidationError(w, bad.Error())
	case errors.As(err, &wrong):
		apierrors.WriteError(w, http.StatusBadRequest, "incorrect_code", wrong.Error())
	case errors.As(err, &conflict):
		apierrors.WriteError(w, http.StatusConflict, "conflict", conflict.Error())
	case errors.As(err, &unavailable):
		apierrors.WriteError(w, http.StatusConflict, "unavailable", unavailable.Error())
	case errors.As(err, &limited):
		if secs := int(limited.RetryAfter.Seconds()); secs > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(secs))
		}
		apierrors.WriteError(w, http.StatusTooManyRequests, "rate_limited", limited.Error())
	case errors.As(err, &delivery):
		h.Log.WithError(err).Warn("Contact message not delivered")
		apierrors.WriteError(w, http.StatusBadGateway, "delivery_failed", delivery.Error())
	default:
		h.Log.WithError(err).Error("Failed to " + action)
		apierrors.WriteInternalError(w, "failed to "+action)
	}
}

// sessionUser returns the signed-in user of a cookie-session request and the
// selected workspace (uuid.Nil when none). API keys and MCP tokens are
// refused: contacts are a person's own, not a workspace credential's.
func sessionUser(w http.ResponseWriter, r *http.Request) (userID, tenantID uuid.UUID, ok bool) {
	ctx := r.Context()
	actorType, _ := ctxpkg.GetActorType(ctx)
	_, scoped := ctxpkg.GetAuthScope(ctx)
	via, _ := ctxpkg.GetVia(ctx)
	if actorType != ctxpkg.ActorTypeAdminUser || scoped || via == ctxpkg.ViaMCP {
		apierrors.WriteForbiddenError(w, "signed-in user session required")
		return uuid.Nil, uuid.Nil, false
	}
	raw, err := middleware.GetAdminID(ctx)
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "invalid session")
		return uuid.Nil, uuid.Nil, false
	}
	userID, err = uuid.Parse(raw)
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "invalid session")
		return uuid.Nil, uuid.Nil, false
	}
	if t, err := middleware.GetTenantID(ctx); err == nil {
		tenantID, _ = uuid.Parse(t)
	}
	return userID, tenantID, true
}

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierrors.WriteValidationError(w, "invalid contact ID")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	user, tenant, ok := sessionUser(w, r)
	if !ok {
		return
	}
	out, err := h.Service.List(r.Context(), user, tenant)
	if err != nil {
		h.fail(w, err, "list contacts")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	user, _, ok := sessionUser(w, r)
	if !ok {
		return
	}
	var req svc.CreateContactRequest
	if !decode(w, r, &req) {
		return
	}
	c, err := h.Service.Create(r.Context(), user, req)
	if err != nil {
		h.fail(w, err, "create contact")
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *Handlers) update(w http.ResponseWriter, r *http.Request) {
	user, _, ok := sessionUser(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req svc.UpdateContactRequest
	if !decode(w, r, &req) {
		return
	}
	c, err := h.Service.Update(r.Context(), user, id, req)
	if err != nil {
		h.fail(w, err, "update contact")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handlers) remove(w http.ResponseWriter, r *http.Request) {
	user, _, ok := sessionUser(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.Service.Delete(r.Context(), user, id); err != nil {
		h.fail(w, err, "delete contact")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) sendCode(w http.ResponseWriter, r *http.Request) {
	user, tenant, ok := sessionUser(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, err := h.Service.SendCode(r.Context(), user, tenant, id)
	if err != nil {
		h.fail(w, err, "send verification code")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handlers) verify(w http.ResponseWriter, r *http.Request) {
	user, _, ok := sessionUser(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req svc.VerifyContactRequest
	if !decode(w, r, &req) {
		return
	}
	c, err := h.Service.Verify(r.Context(), user, id, req.Code)
	if err != nil {
		h.fail(w, err, "verify contact")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handlers) test(w http.ResponseWriter, r *http.Request) {
	user, tenant, ok := sessionUser(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.Service.SendTest(r.Context(), user, tenant, id); err != nil {
		h.fail(w, err, "send test message")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

func tenantID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
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

func (h *Handlers) getSettings(w http.ResponseWriter, r *http.Request) {
	t, ok := tenantID(w, r)
	if !ok {
		return
	}
	out, err := h.Service.PeopleSettings(r.Context(), t)
	if err != nil {
		h.fail(w, err, "load people settings")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) updateSettings(w http.ResponseWriter, r *http.Request) {
	t, ok := tenantID(w, r)
	if !ok {
		return
	}
	var req svc.UpdatePeopleSettingsRequest
	if !decode(w, r, &req) {
		return
	}
	out, err := h.Service.UpdatePeopleSettings(r.Context(), t, req)
	if err != nil {
		h.fail(w, err, "update people settings")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

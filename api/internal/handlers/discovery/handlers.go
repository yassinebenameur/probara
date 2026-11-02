package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	svc "github.com/yassinebenameur/probara/api/internal/services/discovery"
	contract "github.com/yassinebenameur/probara/shared/discovery"
	"github.com/yassinebenameur/probara/shared/logger"
)

// auditRecorder is the slice of *audit.Recorder the operator handlers use.
type auditRecorder interface {
	Record(event audit.Event)
}

// Backend is the discovery service as the handlers use it (*svc.Service).
type Backend interface {
	List(ctx context.Context, tenant uuid.UUID) ([]svc.Integration, error)
	Create(ctx context.Context, tenant uuid.UUID, req svc.CreateRequest) (*svc.Integration, error)
	Rotate(ctx context.Context, tenant, id uuid.UUID) (string, error)
	Delete(ctx context.Context, tenant, id uuid.UUID) error
	Overview(ctx context.Context, tenant, integration uuid.UUID, q svc.OverviewQuery) (svc.Overview, error)
	Approve(ctx context.Context, tenant, service uuid.UUID, req svc.ApproveRequest) (uuid.UUID, bool, error)
	Link(ctx context.Context, tenant, service, monitor uuid.UUID, key *string) error
	Unlink(ctx context.Context, tenant, service, monitor uuid.UUID) error
	CollectorConfig(ctx context.Context, id uuid.UUID, token string) (contract.Config, error)
	AuthorizeSnapshot(ctx context.Context, id uuid.UUID, token string) error
	Snapshot(ctx context.Context, id uuid.UUID, token string, snapshot contract.Snapshot) error
}

type Handlers struct {
	Service Backend
	Log     *logger.Logger
	// Audit records each successful operator write explicitly. Its success
	// events replace the middleware's request-level ones ("/api/v1/discovery/"
	// is in auditItemizedPrefixes), which could not tell the two DELETEs apart
	// and never see the monitor an approval creates.
	Audit auditRecorder
}

func (h *Handlers) record(r *http.Request, status int, action, resourceType string, resourceID uuid.UUID, details map[string]any) {
	if h.Audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.Action = action
	event.ResourceType = resourceType
	event.ResourceID = resourceID.String()
	event.StatusCode = status
	event.Outcome = audit.OutcomeSuccess
	event.MergeDetails(details)
	h.Audit.Record(event)
}

func (h *Handlers) Routes(r chi.Router) {
	r.Get("/integrations", h.list)
	r.Post("/integrations", h.create)
	r.Delete("/integrations/{id}", h.remove)
	r.Post("/integrations/{id}/rotate-token", h.rotate)
	r.Get("/integrations/{id}/coverage", h.coverage)
	r.Post("/services/{id}/approve", h.approve)
	r.Post("/services/{id}/links", h.link)
	r.Delete("/services/{id}/links/{monitorID}", h.unlink)
}
func (h *Handlers) CollectorRoutes(r chi.Router) {
	r.Use(middlewareTimeout)
	r.Get("/{id}/config", h.config)
	r.Post("/{id}/snapshot", h.snapshot)
}
func middlewareTimeout(next http.Handler) http.Handler {
	return http.TimeoutHandler(next, 45*time.Second, `{"error":"discovery request timed out"}`)
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	if v == nil {
		// No body, so no JSON content type: clients parse on the header.
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (h *Handlers) fail(w http.ResponseWriter, err error) {
	var bad svc.Invalid
	var limited svc.RateLimited
	var full svc.InventoryFull
	switch {
	case errors.Is(err, svc.ErrNotFound):
		apierrors.WriteError(w, 404, "not_found", "discovery resource not found")
	case errors.Is(err, svc.ErrConflict):
		apierrors.WriteError(w, 409, "conflict", err.Error())
	case errors.As(err, &limited):
		w.Header().Set("Retry-After", strconv.FormatInt(int64((limited.RetryAfter+time.Second-1)/time.Second), 10))
		apierrors.WriteError(w, http.StatusTooManyRequests, "rate_limited", err.Error())
	case errors.As(err, &full):
		apierrors.WriteError(w, http.StatusUnprocessableEntity, "inventory_limit", err.Error())
	case errors.As(err, &bad):
		apierrors.WriteValidationError(w, bad.Error())
	default:
		h.Log.WithError(err).Error("Discovery request failed")
		apierrors.WriteInternalError(w, "discovery request failed")
	}
}
func tenant(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	s, err := middleware.GetTenantID(r.Context())
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "tenant required")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "invalid tenant")
		return uuid.Nil, false
	}
	return id, true
}
func id(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	v, e := uuid.Parse(chi.URLParam(r, name))
	if e != nil {
		apierrors.WriteValidationError(w, "invalid ID")
		return v, false
	}
	return v, true
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeBody(w, r, v, true)
}

// decodeBody reads one JSON object of at most 4 MiB. strict refuses unknown
// fields; the collector snapshot is decoded leniently so a collector image
// newer than the API still syncs (its known fields are validated in full by
// Snapshot.Validate).
func decodeBody(w http.ResponseWriter, r *http.Request, v any, strict bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	dec := json.NewDecoder(r.Body)
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(v); err != nil {
		apierrors.WriteValidationError(w, "invalid request body")
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
	v, e := h.Service.List(r.Context(), t)
	if e != nil {
		h.fail(w, e)
		return
	}
	write(w, 200, v)
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
	v, e := h.Service.Create(r.Context(), t, req)
	if e != nil {
		h.fail(w, e)
		return
	}
	h.record(r, 201, "discovery-integration.create", "discovery-integration", v.ID, map[string]any{"name": v.Name})
	write(w, 201, v)
}
func (h *Handlers) remove(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	i, ok := id(w, r, "id")
	if !ok {
		return
	}
	if e := h.Service.Delete(r.Context(), t, i); e != nil {
		h.fail(w, e)
		return
	}
	h.record(r, 204, "discovery-integration.delete", "discovery-integration", i, nil)
	write(w, 204, nil)
}
func (h *Handlers) rotate(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	i, ok := id(w, r, "id")
	if !ok {
		return
	}
	v, e := h.Service.Rotate(r.Context(), t, i)
	if e != nil {
		h.fail(w, e)
		return
	}
	h.record(r, 200, "discovery-integration.rotate-token", "discovery-integration", i, nil)
	write(w, 200, map[string]string{"token": v})
}
func (h *Handlers) coverage(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	i, ok := id(w, r, "id")
	if !ok {
		return
	}
	p := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		var e error
		p, e = strconv.Atoi(raw)
		if e != nil || p < 1 || p > 100000 {
			apierrors.WriteValidationError(w, "invalid page")
			return
		}
	}
	q := r.URL.Query()
	sort := q.Get("sort")
	if sort != "" && sort != "name" && sort != "gaps" {
		apierrors.WriteValidationError(w, "invalid sort")
		return
	}
	search := q.Get("q")
	if len(search) > 200 {
		apierrors.WriteValidationError(w, "search is too long")
		return
	}
	v, e := h.Service.Overview(r.Context(), t, i, svc.OverviewQuery{Page: p, Query: search, GapsOnly: q.Get("gaps") == "true", SortGaps: sort == "gaps"})
	if e != nil {
		h.fail(w, e)
		return
	}
	write(w, 200, v)
}
func (h *Handlers) approve(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	i, ok := id(w, r, "id")
	if !ok {
		return
	}
	var req svc.ApproveRequest
	if !decode(w, r, &req) {
		return
	}
	v, created, e := h.Service.Approve(r.Context(), t, i, req)
	if e != nil {
		h.fail(w, e)
		return
	}
	// Approval creates the monitor without passing through POST /monitors, so
	// the event is filed under the new monitor's own ID. A replay that only
	// returns the monitor already linked to the suggestion (retry, double
	// click, earlier manual link) wrote nothing and is not audited.
	if created {
		h.record(r, 200, "discovery.approve", "monitor", v, map[string]any{"discovered_service_id": i.String(), "suggestion_key": req.Key})
	}
	write(w, 200, map[string]any{"monitor_id": v, "created": created})
}
func (h *Handlers) link(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	i, ok := id(w, r, "id")
	if !ok {
		return
	}
	var req svc.LinkRequest
	if !decode(w, r, &req) {
		return
	}
	if e := h.Service.Link(r.Context(), t, i, req.MonitorID, req.Key); e != nil {
		h.fail(w, e)
		return
	}
	h.record(r, 204, "discovery-link.create", "discovery-service", i, map[string]any{"monitor_id": req.MonitorID.String()})
	write(w, 204, nil)
}
func (h *Handlers) unlink(w http.ResponseWriter, r *http.Request) {
	t, ok := tenant(w, r)
	if !ok {
		return
	}
	i, ok := id(w, r, "id")
	if !ok {
		return
	}
	m, ok := id(w, r, "monitorID")
	if !ok {
		return
	}
	if e := h.Service.Unlink(r.Context(), t, i, m); e != nil {
		h.fail(w, e)
		return
	}
	h.record(r, 204, "discovery-link.delete", "discovery-service", i, map[string]any{"monitor_id": m.String()})
	write(w, 204, nil)
}
func collectorToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || len(token) != 43 {
		apierrors.WriteUnauthorizedError(w, "invalid collector credential")
		return "", false
	}
	return token, true
}
func (h *Handlers) config(w http.ResponseWriter, r *http.Request) {
	i, ok := id(w, r, "id")
	if !ok {
		return
	}
	token, ok := collectorToken(w, r)
	if !ok {
		return
	}
	v, e := h.Service.CollectorConfig(r.Context(), i, token)
	if e != nil {
		h.fail(w, e)
		return
	}
	write(w, 200, v)
}
func (h *Handlers) snapshot(w http.ResponseWriter, r *http.Request) {
	i, ok := id(w, r, "id")
	if !ok {
		return
	}
	token, ok := collectorToken(w, r)
	if !ok {
		return
	}
	// Authenticate and rate-limit before reading a body of up to 4 MiB.
	if e := h.Service.AuthorizeSnapshot(r.Context(), i, token); e != nil {
		h.fail(w, e)
		return
	}
	var req contract.Snapshot
	if !decodeBody(w, r, &req, false) {
		return
	}
	if e := h.Service.Snapshot(r.Context(), i, token, req); e != nil {
		h.fail(w, e)
		return
	}
	write(w, 204, nil)
}

package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
)

// auditRecorder is the slice of *audit.Recorder the bulk handlers use.
// RecordBatch writes synchronously: the middleware skips a successful bulk
// request (middleware.itemizedSuccess), so these per-alert events are its only
// record and must not go through Record's drop-when-full buffer. Record is
// the fallback when the synchronous write fails.
type auditRecorder interface {
	Record(event audit.Event)
	RecordBatch(ctx context.Context, events []audit.Event) error
}

// maxBulkBodyBytes caps the request body before decoding: MaxBulkAlerts
// quoted UUIDs are about 20 KB, so anything far past that is refused unread.
const maxBulkBodyBytes = 64 << 10

// WithAudit attaches the recorder the bulk endpoints write their per-alert
// audit events to.
func (h *Handlers) WithAudit(recorder auditRecorder) *Handlers {
	h.audit = recorder
	return h
}

// BulkAlertsRequest is the body of POST /alerts/bulk/{acknowledge,resolve}.
type BulkAlertsRequest struct {
	IDs []string `json:"ids"`
}

// BulkAcknowledgeAlerts handles POST /api/v1/alerts/bulk/acknowledge.
func (h *Handlers) BulkAcknowledgeAlerts(w http.ResponseWriter, r *http.Request) {
	h.bulkAction(w, r, "acknowledge", h.service.BulkAcknowledge, h.hub.BroadcastAlertAcknowledged)
}

// BulkResolveAlerts handles POST /api/v1/alerts/bulk/resolve.
func (h *Handlers) BulkResolveAlerts(w http.ResponseWriter, r *http.Request) {
	h.bulkAction(w, r, "resolve", h.service.BulkResolve, h.hub.BroadcastAlertResolved)
}

// bulkAction applies one lifecycle action to many alerts. The response is
// 200 with per-alert results whenever the request itself is valid: alerts
// that changed state meanwhile land in `failed` with a reason, they do not
// fail the batch. Each requested alert gets its own audit event, shaped like
// the single endpoint's (action alert.<verb>, resource alert/<id>, 200 or
// 404), plus details.bulk so the two paths stay distinguishable.
func (h *Handlers) bulkAction(
	w http.ResponseWriter,
	r *http.Request,
	verb string,
	apply func(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) (*alertservice.BulkResult, error),
	broadcast func(tenantID uuid.UUID, alert *models.AlertWithDetails),
) {
	tenantID, err := middleware.GetTenantID(r.Context())
	if err != nil {
		errors.WriteUnauthorizedError(w, "tenant ID not found")
		return
	}
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		errors.WriteInternalError(w, "invalid tenant ID")
		return
	}

	var req BulkAlertsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBulkBodyBytes)).Decode(&req); err != nil {
		errors.WriteValidationError(w, "invalid request body: "+err.Error())
		return
	}
	if len(req.IDs) == 0 {
		errors.WriteValidationError(w, "ids is required and cannot be empty")
		return
	}
	if len(req.IDs) > alertservice.MaxBulkAlerts {
		errors.WriteValidationError(w, fmt.Sprintf("at most %d alerts per request", alertservice.MaxBulkAlerts))
		return
	}
	ids := make([]uuid.UUID, len(req.IDs))
	for i, raw := range req.IDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			errors.WriteValidationError(w, "invalid alert ID: "+raw)
			return
		}
		ids[i] = id
	}

	result, err := apply(r.Context(), tenantUUID, ids)
	if err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"tenant_id": tenantID,
			"count":     len(ids),
		}).Error("Failed to bulk " + verb + " alerts")
		errors.WriteInternalError(w, "failed to "+verb+" alerts")
		return
	}

	events := make([]audit.Event, 0, len(result.Items)+len(result.Failed))
	for i := range result.Items {
		alert := &result.Items[i]
		if h.hub != nil {
			broadcast(tenantUUID, alert)
		}
		events = append(events, bulkItemEvent(r, verb, alert.ID, http.StatusOK, ""))
	}
	for _, failure := range result.Failed {
		events = append(events, bulkItemEvent(r, verb, failure.ID, http.StatusNotFound, failure.Reason))
	}
	h.recordBulkEvents(r, events)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// recordBulkEvents persists a bulk request's per-alert events before the
// response is written. The state change is already committed, so a client
// that hangs up must not cancel its audit trail (WithoutCancel), and a failed
// write falls back to the buffered path rather than failing the request.
func (h *Handlers) recordBulkEvents(r *http.Request, events []audit.Event) {
	if h.audit == nil || len(events) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancel()
	if err := h.audit.RecordBatch(ctx, events); err != nil {
		h.logger.WithFields(map[string]interface{}{
			"error": err.Error(),
			"count": len(events),
		}).Error("Failed to persist bulk alert audit events; retrying through the buffer")
		for _, event := range events {
			h.audit.Record(event)
		}
	}
}

// bulkItemEvent builds one alert's audit event for a bulk request. The
// single endpoints answer 404 for an alert they cannot move (missing or in
// the wrong state), so a skipped item records as that failure.
func bulkItemEvent(r *http.Request, verb string, alertID uuid.UUID, status int, reason string) audit.Event {
	event := audit.FromRequest(r)
	event.Action = "alert." + verb
	event.ResourceType = "alert"
	event.ResourceID = alertID.String()
	event.StatusCode = status
	event.Outcome = audit.OutcomeSuccess
	details := map[string]any{"bulk": true}
	if status >= 400 {
		event.Outcome = audit.OutcomeFailure
		details["reason"] = reason
	}
	event.MergeDetails(details)
	return event
}

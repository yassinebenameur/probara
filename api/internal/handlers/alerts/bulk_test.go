package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// fakeBulkService answers the bulk calls; every other method panics through
// the nil embedded interface (the bulk handlers must not call them).
type fakeBulkService struct {
	alertservice.AlertService
	gotTenant uuid.UUID
	gotIDs    []uuid.UUID
	result    *alertservice.BulkResult
}

func (f *fakeBulkService) BulkAcknowledge(_ context.Context, tenantID uuid.UUID, ids []uuid.UUID) (*alertservice.BulkResult, error) {
	f.gotTenant, f.gotIDs = tenantID, ids
	return f.result, nil
}

func (f *fakeBulkService) BulkResolve(_ context.Context, tenantID uuid.UUID, ids []uuid.UUID) (*alertservice.BulkResult, error) {
	f.gotTenant, f.gotIDs = tenantID, ids
	return f.result, nil
}

// captureRecorder records which path each event took: the synchronous batch
// (the normal path) or the buffered Record fallback.
type captureRecorder struct {
	events    []audit.Event
	batches   int
	buffered  int
	batchErr  error
	ctxCancel bool
}

func (c *captureRecorder) Record(event audit.Event) {
	c.buffered++
	c.events = append(c.events, event)
}

func (c *captureRecorder) RecordBatch(ctx context.Context, events []audit.Event) error {
	c.batches++
	c.ctxCancel = ctx.Err() != nil
	if c.batchErr != nil {
		return c.batchErr
	}
	c.events = append(c.events, events...)
	return nil
}

func bulkRequest(t *testing.T, tenantID uuid.UUID, path, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	ctx := ctxpkg.WithTenantID(r.Context(), tenantID.String())
	ctx = ctxpkg.WithAdminID(ctx, uuid.NewString())
	return r.WithContext(ctx)
}

func TestBulkAcknowledge_PartialResultAuditedPerAlert(t *testing.T) {
	tenantID := uuid.New()
	changed, skipped := uuid.New(), uuid.New()
	svc := &fakeBulkService{result: &alertservice.BulkResult{
		Items:  []models.AlertWithDetails{{Alert: models.Alert{ID: changed, Status: models.AlertStatusAcknowledged}}},
		Failed: []alertservice.BulkFailure{{ID: skipped, Reason: alertservice.BulkReasonAlreadyResolved}},
	}}
	rec := &captureRecorder{}
	h := NewHandlers(svc, alertservice.NewHub(), logger.New("test", "error")).WithAudit(rec)

	body, _ := json.Marshal(BulkAlertsRequest{IDs: []string{changed.String(), skipped.String()}})
	w := httptest.NewRecorder()
	h.BulkAcknowledgeAlerts(w, bulkRequest(t, tenantID, "/api/v1/alerts/bulk/acknowledge", string(body)))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (partial success is not an error): %s", w.Code, w.Body)
	}
	if svc.gotTenant != tenantID || len(svc.gotIDs) != 2 {
		t.Fatalf("service got tenant %s ids %v", svc.gotTenant, svc.gotIDs)
	}
	var resp struct {
		Items  []map[string]any `json:"items"`
		Failed []map[string]any `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 || len(resp.Failed) != 1 || resp.Failed[0]["reason"] != "already_resolved" {
		t.Fatalf("unexpected body: %s", w.Body)
	}

	if len(rec.events) != 2 {
		t.Fatalf("audit events = %d, want one per requested alert", len(rec.events))
	}
	if rec.batches != 1 || rec.buffered != 0 {
		t.Fatalf("audit went through %d batch writes and %d buffered events, want one synchronous batch", rec.batches, rec.buffered)
	}
	ok, failed := rec.events[0], rec.events[1]
	if ok.Action != "alert.acknowledge" || ok.ResourceType != "alert" || ok.ResourceID != changed.String() ||
		ok.Outcome != audit.OutcomeSuccess || ok.StatusCode != http.StatusOK || ok.Details["bulk"] != true {
		t.Errorf("success event = %+v", ok)
	}
	if ok.TenantID == nil || *ok.TenantID != tenantID || ok.ActorType != audit.ActorAdminUser {
		t.Errorf("success event lost the request's tenant/actor: %+v", ok)
	}
	if failed.ResourceID != skipped.String() || failed.Outcome != audit.OutcomeFailure ||
		failed.StatusCode != http.StatusNotFound || failed.Details["reason"] != "already_resolved" {
		t.Errorf("failure event = %+v", failed)
	}
}

func TestBulkResolve_RejectsInvalidRequests(t *testing.T) {
	tooMany := make([]string, alertservice.MaxBulkAlerts+1)
	for i := range tooMany {
		tooMany[i] = uuid.NewString()
	}
	tooManyBody, _ := json.Marshal(BulkAlertsRequest{IDs: tooMany})

	for name, body := range map[string]string{
		"malformed": `{"ids":`,
		"empty":     `{"ids":[]}`,
		"bad id":    `{"ids":["not-a-uuid"]}`,
		"too many":  string(tooManyBody),
		// Over the body cap: refused before the IDs are decoded.
		"oversized": `{"ids":["` + strings.Repeat("a", maxBulkBodyBytes) + `"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeBulkService{}
			rec := &captureRecorder{}
			h := NewHandlers(svc, alertservice.NewHub(), logger.New("test", "error")).WithAudit(rec)
			w := httptest.NewRecorder()
			h.BulkResolveAlerts(w, bulkRequest(t, uuid.New(), "/api/v1/alerts/bulk/resolve", body))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if svc.gotIDs != nil || len(rec.events) != 0 {
				t.Fatalf("an invalid request reached the service or the per-alert audit")
			}
		})
	}
}

func TestListAlerts_RejectsUnknownSort(t *testing.T) {
	h := NewHandlers(&fakeBulkService{}, alertservice.NewHub(), logger.New("test", "error"))
	for _, query := range []string{"sort=bogus", "sort=id", "dir=sideways", "sort=status&dir=up"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/alerts?"+query, nil)
		r = r.WithContext(ctxpkg.WithTenantID(r.Context(), uuid.NewString()))
		w := httptest.NewRecorder()
		h.ListAlerts(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", query, w.Code)
		}
	}
}

func TestBulkResolve_AuditFallsBackToBufferWhenBatchWriteFails(t *testing.T) {
	changed := uuid.New()
	svc := &fakeBulkService{result: &alertservice.BulkResult{
		Items: []models.AlertWithDetails{{Alert: models.Alert{ID: changed, Status: models.AlertStatusResolved}}},
	}}
	rec := &captureRecorder{batchErr: errors.New("db down")}
	h := NewHandlers(svc, alertservice.NewHub(), logger.New("test", "error")).WithAudit(rec)

	body, _ := json.Marshal(BulkAlertsRequest{IDs: []string{changed.String()}})
	r := bulkRequest(t, uuid.New(), "/api/v1/alerts/bulk/resolve", string(body))
	// A client that already hung up must not cancel the audit write.
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	w := httptest.NewRecorder()
	h.BulkResolveAlerts(w, r.WithContext(ctx))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a failed audit write never fails the request)", w.Code)
	}
	if rec.ctxCancel {
		t.Errorf("the synchronous audit write ran on the cancelled request context")
	}
	if rec.buffered != 1 || len(rec.events) != 1 || rec.events[0].ResourceID != changed.String() {
		t.Fatalf("fallback recorded %d buffered events %+v, want the one resolved alert", rec.buffered, rec.events)
	}
}

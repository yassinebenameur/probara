package incidents

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/ai"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

type fakeAIPublisher struct {
	subject string
	payload interface{}
	err     error
	calls   int
}

func (f *fakeAIPublisher) PublishJSON(_ context.Context, subject string, v interface{}, _ map[string][]string) error {
	f.calls++
	f.subject = subject
	f.payload = v
	return f.err
}

type fakeAIChecker struct {
	enabled bool
	err     error
}

func (f *fakeAIChecker) EffectiveEnabled(context.Context, uuid.UUID) (bool, error) {
	return f.enabled, f.err
}

func TestHandlers_RequestIncidentAIAnalysis_NotConfigured(t *testing.T) {
	h := NewHandlers(&mockIncidentService{}, logger.New("test", "debug"))
	// ConfigureAIAnalysis not called → feature disabled.

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/x/ai-analysis", nil)
	req = withIncidentID(withTenantID(req), uuid.New())
	w := httptest.NewRecorder()

	h.RequestIncidentAIAnalysis(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (%s)", w.Code, w.Body.String())
	}
}

func TestHandlers_RequestIncidentAIAnalysis_Success(t *testing.T) {
	incidentID := uuid.New()
	analysisID := uuid.New()
	svc := &mockIncidentService{
		requestAIFn: func(ctx context.Context, tenantID, incID uuid.UUID, requestedBy *uuid.UUID) (*models.IncidentAIAnalysis, error) {
			return &models.IncidentAIAnalysis{ID: analysisID, IncidentID: incID, Status: models.IncidentAIAnalysisStatusPending}, nil
		},
	}
	pub := &fakeAIPublisher{}
	h := NewHandlers(svc, logger.New("test", "debug"))
	h.ConfigureAIAnalysis(pub, "ai.rca.jobs", &fakeAIChecker{enabled: true})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/x/ai-analysis", nil)
	req = withIncidentID(withTenantID(req), incidentID)
	w := httptest.NewRecorder()

	h.RequestIncidentAIAnalysis(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", w.Code, w.Body.String())
	}
	if pub.calls != 1 || pub.subject != "ai.rca.jobs" {
		t.Fatalf("publisher calls = %d subject = %q", pub.calls, pub.subject)
	}
	// The enqueued job must reference the created analysis row.
	raw, _ := json.Marshal(pub.payload)
	var job ai.AnalysisJob
	if err := json.Unmarshal(raw, &job); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if job.AnalysisID != analysisID || job.IncidentID != incidentID || job.V != ai.AnalysisJobVersion {
		t.Fatalf("job = %+v", job)
	}
}

func TestHandlers_GetIncidentAIAnalysis_Null(t *testing.T) {
	svc := &mockIncidentService{
		getLatestAIFn: func(ctx context.Context, tenantID, incidentID uuid.UUID) (*models.IncidentAIAnalysis, error) {
			return nil, nil
		},
	}
	h := NewHandlers(svc, logger.New("test", "debug"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents/x/ai-analysis", nil)
	req = withIncidentID(withTenantID(req), uuid.New())
	w := httptest.NewRecorder()

	h.GetIncidentAIAnalysis(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if strings.TrimSpace(w.Body.String()) != "null" {
		t.Fatalf("body = %q, want null", w.Body.String())
	}
}

type mockIncidentService struct {
	createFn         func(ctx context.Context, tenantID uuid.UUID, req *models.CreateIncidentRequest) (*models.IncidentDetail, error)
	getFn            func(ctx context.Context, tenantID, incidentID uuid.UUID) (*models.IncidentDetail, error)
	listFn           func(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.IncidentListResponse, error)
	updateFn         func(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.UpdateIncidentRequest) (*models.IncidentDetail, error)
	transitionFn     func(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.TransitionIncidentStateRequest) (*models.IncidentDetail, error)
	createTimelineFn func(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.CreateIncidentTimelineEntryRequest) (*models.IncidentDetail, error)
	attachAlertFn    func(ctx context.Context, tenantID, incidentID, alertID uuid.UUID) (*models.IncidentDetail, error)
	detachAlertFn    func(ctx context.Context, tenantID, incidentID, alertID uuid.UUID) (*models.IncidentDetail, error)
	attachMonitorFn  func(ctx context.Context, tenantID, incidentID, monitorID uuid.UUID) (*models.IncidentDetail, error)
	detachMonitorFn  func(ctx context.Context, tenantID, incidentID, monitorID uuid.UUID) (*models.IncidentDetail, error)
	publishFn        func(ctx context.Context, tenantID, incidentID, statusPageID uuid.UUID, req *models.UpsertIncidentPublicationRequest) (*models.IncidentDetail, error)
	unpublishFn      func(ctx context.Context, tenantID, incidentID, statusPageID uuid.UUID) (*models.IncidentDetail, error)
	requestAIFn      func(ctx context.Context, tenantID, incidentID uuid.UUID, requestedBy *uuid.UUID) (*models.IncidentAIAnalysis, error)
	getLatestAIFn    func(ctx context.Context, tenantID, incidentID uuid.UUID) (*models.IncidentAIAnalysis, error)
}

func (m *mockIncidentService) RequestAIAnalysis(ctx context.Context, tenantID, incidentID uuid.UUID, requestedBy *uuid.UUID) (*models.IncidentAIAnalysis, error) {
	if m.requestAIFn != nil {
		return m.requestAIFn(ctx, tenantID, incidentID, requestedBy)
	}
	return nil, nil
}

func (m *mockIncidentService) GetLatestAIAnalysis(ctx context.Context, tenantID, incidentID uuid.UUID) (*models.IncidentAIAnalysis, error) {
	if m.getLatestAIFn != nil {
		return m.getLatestAIFn(ctx, tenantID, incidentID)
	}
	return nil, nil
}

func (m *mockIncidentService) CreateIncident(ctx context.Context, tenantID uuid.UUID, req *models.CreateIncidentRequest) (*models.IncidentDetail, error) {
	if m.createFn != nil {
		return m.createFn(ctx, tenantID, req)
	}
	return nil, nil
}

func (m *mockIncidentService) GetIncident(ctx context.Context, tenantID, incidentID uuid.UUID) (*models.IncidentDetail, error) {
	if m.getFn != nil {
		return m.getFn(ctx, tenantID, incidentID)
	}
	return nil, nil
}

func (m *mockIncidentService) ListIncidents(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.IncidentListResponse, error) {
	if m.listFn != nil {
		return m.listFn(ctx, tenantID, page, pageSize)
	}
	return nil, nil
}

func (m *mockIncidentService) UpdateIncident(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.UpdateIncidentRequest) (*models.IncidentDetail, error) {
	if m.updateFn != nil {
		return m.updateFn(ctx, tenantID, incidentID, req)
	}
	return nil, nil
}

func (m *mockIncidentService) TransitionIncidentState(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.TransitionIncidentStateRequest) (*models.IncidentDetail, error) {
	if m.transitionFn != nil {
		return m.transitionFn(ctx, tenantID, incidentID, req)
	}
	return nil, nil
}

func (m *mockIncidentService) CreateIncidentTimelineEntry(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.CreateIncidentTimelineEntryRequest) (*models.IncidentDetail, error) {
	if m.createTimelineFn != nil {
		return m.createTimelineFn(ctx, tenantID, incidentID, req)
	}
	return nil, nil
}

func (m *mockIncidentService) AttachAlert(ctx context.Context, tenantID, incidentID, alertID uuid.UUID) (*models.IncidentDetail, error) {
	if m.attachAlertFn != nil {
		return m.attachAlertFn(ctx, tenantID, incidentID, alertID)
	}
	return nil, nil
}

func (m *mockIncidentService) DetachAlert(ctx context.Context, tenantID, incidentID, alertID uuid.UUID) (*models.IncidentDetail, error) {
	if m.detachAlertFn != nil {
		return m.detachAlertFn(ctx, tenantID, incidentID, alertID)
	}
	return nil, nil
}

func (m *mockIncidentService) AttachMonitor(ctx context.Context, tenantID, incidentID, monitorID uuid.UUID) (*models.IncidentDetail, error) {
	if m.attachMonitorFn != nil {
		return m.attachMonitorFn(ctx, tenantID, incidentID, monitorID)
	}
	return nil, nil
}

func (m *mockIncidentService) DetachMonitor(ctx context.Context, tenantID, incidentID, monitorID uuid.UUID) (*models.IncidentDetail, error) {
	if m.detachMonitorFn != nil {
		return m.detachMonitorFn(ctx, tenantID, incidentID, monitorID)
	}
	return nil, nil
}

func (m *mockIncidentService) PublishIncidentToStatusPage(ctx context.Context, tenantID, incidentID, statusPageID uuid.UUID, req *models.UpsertIncidentPublicationRequest) (*models.IncidentDetail, error) {
	if m.publishFn != nil {
		return m.publishFn(ctx, tenantID, incidentID, statusPageID, req)
	}
	return nil, nil
}

func (m *mockIncidentService) UnpublishIncidentFromStatusPage(ctx context.Context, tenantID, incidentID, statusPageID uuid.UUID) (*models.IncidentDetail, error) {
	if m.unpublishFn != nil {
		return m.unpublishFn(ctx, tenantID, incidentID, statusPageID)
	}
	return nil, nil
}

func (m *mockIncidentService) EnsureIncidentForAlert(context.Context, uuid.UUID, *models.AlertWithDetails) error {
	return nil
}

func (m *mockIncidentService) RecordAlertRecoveryIfNeeded(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (m *mockIncidentService) EnsureIncidentForAlertTx(context.Context, *sql.Tx, uuid.UUID, *models.AlertWithDetails) error {
	return nil
}

func (m *mockIncidentService) RecordAlertRecoveryIfNeededTx(context.Context, *sql.Tx, uuid.UUID, uuid.UUID) error {
	return nil
}

func withTenantID(req *http.Request) *http.Request {
	return req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))
}

func withIncidentID(req *http.Request, incidentID uuid.UUID) *http.Request {
	return withRouteParams(req, map[string]string{"id": incidentID.String()})
}

func withRouteParams(req *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for key, value := range params {
		rctx.URLParams.Add(key, value)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestHandlers_CreateIncident(t *testing.T) {
	log := logger.New("test", "debug")
	ownerID := uuid.New()
	alertID := uuid.New()
	monitorID := uuid.New()
	svc := &mockIncidentService{
		createFn: func(ctx context.Context, tenantID uuid.UUID, req *models.CreateIncidentRequest) (*models.IncidentDetail, error) {
			if req.Severity != models.IncidentSeverityCritical {
				t.Fatalf("severity = %q, want %q", req.Severity, models.IncidentSeverityCritical)
			}
			if req.OwnerUserID != ownerID.String() {
				t.Fatalf("owner_user_id = %q, want %q", req.OwnerUserID, ownerID.String())
			}
			if req.AlertID != alertID.String() {
				t.Fatalf("alert_id = %q, want %q", req.AlertID, alertID.String())
			}
			if req.MonitorID != monitorID.String() {
				t.Fatalf("monitor_id = %q, want %q", req.MonitorID, monitorID.String())
			}
			return &models.IncidentDetail{
				Incident: models.Incident{
					ID:            uuid.New(),
					Title:         req.Title,
					State:         models.IncidentStateInvestigating,
					Severity:      req.Severity,
					OwnerUserID:   &ownerID,
					OwnerUsername: "incident-owner",
				},
			}, nil
		},
	}
	h := NewHandlers(svc, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(`{"title":"API outage","summary":"Requests are failing.","severity":"critical","owner_user_id":"`+ownerID.String()+`","alert_id":"`+alertID.String()+`","monitor_id":"`+monitorID.String()+`"}`))
	req = withTenantID(req)
	w := httptest.NewRecorder()

	h.CreateIncident(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusCreated, w.Body.String())
	}

	var got models.IncidentDetail
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Title != "API outage" {
		t.Fatalf("title = %q, want %q", got.Title, "API outage")
	}
	if got.Severity != models.IncidentSeverityCritical {
		t.Fatalf("severity = %q, want %q", got.Severity, models.IncidentSeverityCritical)
	}
	if got.OwnerUserID == nil || *got.OwnerUserID != ownerID {
		t.Fatalf("owner_user_id = %v, want %s", got.OwnerUserID, ownerID)
	}
	if got.OwnerUsername != "incident-owner" {
		t.Fatalf("owner_username = %q, want %q", got.OwnerUsername, "incident-owner")
	}
}

func TestHandlers_CreateIncident_ValidationError(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{}, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(`{"summary":"Requests are failing."}`))
	req = withTenantID(req)
	w := httptest.NewRecorder()

	h.CreateIncident(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "title is required") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_CreateIncident_ServiceTitleRequired(t *testing.T) {
	log := logger.New("test", "debug")
	called := false
	h := NewHandlers(&mockIncidentService{
		createFn: func(ctx context.Context, tenantID uuid.UUID, req *models.CreateIncidentRequest) (*models.IncidentDetail, error) {
			called = true
			return nil, errors.New("title is required")
		},
	}, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(`{"title":"API outage","summary":"Requests are failing."}`))
	req = withTenantID(req)
	w := httptest.NewRecorder()

	h.CreateIncident(w, req)

	if !called {
		t.Fatal("expected service to be called")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "title is required") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_GetIncidentIncludesEmptyLinkedResourceArrays(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.MustParse("12345678-1234-1234-1234-123456789012")
	h := NewHandlers(&mockIncidentService{
		getFn: func(ctx context.Context, tenantID, gotIncidentID uuid.UUID) (*models.IncidentDetail, error) {
			if gotIncidentID != incidentID {
				t.Fatalf("incidentID = %s, want %s", gotIncidentID, incidentID)
			}
			return &models.IncidentDetail{
				Incident: models.Incident{
					ID:      incidentID,
					Title:   "API outage",
					Summary: "Requests are failing.",
					State:   models.IncidentStateInvestigating,
				},
				Alerts:       []models.IncidentAlertSummary{},
				Monitors:     []models.IncidentMonitorSummary{},
				Publications: []models.IncidentPublication{},
				Timeline:     []models.IncidentTimelineEntry{},
			}, nil
		},
	}, log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents/"+incidentID.String(), nil)
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.GetIncident(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusOK, w.Body.String())
	}
	for _, want := range []string{`"alerts":[]`, `"monitors":[]`, `"publications":[]`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("body = %s, want %s", w.Body.String(), want)
		}
	}
}

func TestHandlers_AttachIncidentAlert(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.MustParse("12345678-1234-1234-1234-123456789012")
	alertID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	called := false

	h := NewHandlers(&mockIncidentService{
		attachAlertFn: func(ctx context.Context, tenantID, gotIncidentID, gotAlertID uuid.UUID) (*models.IncidentDetail, error) {
			called = true
			if gotIncidentID != incidentID {
				t.Fatalf("incidentID = %s, want %s", gotIncidentID, incidentID)
			}
			if gotAlertID != alertID {
				t.Fatalf("alertID = %s, want %s", gotAlertID, alertID)
			}
			return &models.IncidentDetail{
				Incident: models.Incident{
					ID:    incidentID,
					Title: "API outage",
					State: models.IncidentStateInvestigating,
				},
			}, nil
		},
	}, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incidentID.String()+"/alerts", bytes.NewBufferString(`{"alert_id":"11111111-1111-1111-1111-111111111111"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.AttachIncidentAlert(w, req)

	if !called {
		t.Fatal("expected service to be called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandlers_AttachIncidentAlert_InvalidAlertID(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{}, log)

	incidentID := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incidentID.String()+"/alerts", bytes.NewBufferString(`{"alert_id":"bad-id"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.AttachIncidentAlert(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid alert ID") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_AttachIncidentAlert_NotFound(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{
		attachAlertFn: func(ctx context.Context, tenantID, incidentID, alertID uuid.UUID) (*models.IncidentDetail, error) {
			return nil, errors.New("alert not found")
		},
	}, log)

	incidentID := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incidentID.String()+"/alerts", bytes.NewBufferString(`{"alert_id":"11111111-1111-1111-1111-111111111111"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.AttachIncidentAlert(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusNotFound, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "alert not found") {
		t.Fatalf("body = %s, want not-found message", w.Body.String())
	}
}

func TestHandlers_DetachIncidentAlert(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	alertID := uuid.New()
	called := false

	h := NewHandlers(&mockIncidentService{
		detachAlertFn: func(ctx context.Context, tenantID, gotIncidentID, gotAlertID uuid.UUID) (*models.IncidentDetail, error) {
			called = true
			if gotIncidentID != incidentID {
				t.Fatalf("incidentID = %s, want %s", gotIncidentID, incidentID)
			}
			if gotAlertID != alertID {
				t.Fatalf("alertID = %s, want %s", gotAlertID, alertID)
			}
			return &models.IncidentDetail{
				Incident: models.Incident{ID: incidentID, State: models.IncidentStateInvestigating},
			}, nil
		},
	}, log)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/incidents/"+incidentID.String()+"/alerts/"+alertID.String(), nil)
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "alertId": alertID.String()})
	w := httptest.NewRecorder()

	h.DetachIncidentAlert(w, req)

	if !called {
		t.Fatal("expected service to be called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandlers_DetachIncidentAlert_InvalidAlertIDParam(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{}, log)

	incidentID := uuid.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/incidents/"+incidentID.String()+"/alerts/bad-id", nil)
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "alertId": "bad-id"})
	w := httptest.NewRecorder()

	h.DetachIncidentAlert(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid alert ID") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_DetachIncidentAlert_AlertIDRequiredParam(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{}, log)

	incidentID := uuid.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/incidents/"+incidentID.String()+"/alerts/", nil)
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "alertId": ""})
	w := httptest.NewRecorder()

	h.DetachIncidentAlert(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "alert ID is required") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_AttachIncidentMonitor_InvalidMonitorID(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{}, log)

	incidentID := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incidentID.String()+"/monitors", bytes.NewBufferString(`{"monitor_id":"bad-id"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.AttachIncidentMonitor(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid monitor ID") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_AttachIncidentMonitor_NotFound(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{
		attachMonitorFn: func(ctx context.Context, tenantID, incidentID, monitorID uuid.UUID) (*models.IncidentDetail, error) {
			return nil, errors.New("monitor not found")
		},
	}, log)

	incidentID := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incidentID.String()+"/monitors", bytes.NewBufferString(`{"monitor_id":"11111111-1111-1111-1111-111111111111"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.AttachIncidentMonitor(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusNotFound, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "monitor not found") {
		t.Fatalf("body = %s, want not-found message", w.Body.String())
	}
}

func TestHandlers_DetachIncidentMonitor(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	monitorID := uuid.New()
	called := false

	h := NewHandlers(&mockIncidentService{
		detachMonitorFn: func(ctx context.Context, tenantID, gotIncidentID, gotMonitorID uuid.UUID) (*models.IncidentDetail, error) {
			called = true
			if gotIncidentID != incidentID {
				t.Fatalf("incidentID = %s, want %s", gotIncidentID, incidentID)
			}
			if gotMonitorID != monitorID {
				t.Fatalf("monitorID = %s, want %s", gotMonitorID, monitorID)
			}
			return &models.IncidentDetail{
				Incident: models.Incident{ID: incidentID, State: models.IncidentStateInvestigating},
			}, nil
		},
	}, log)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/incidents/"+incidentID.String()+"/monitors/"+monitorID.String(), nil)
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "monitorId": monitorID.String()})
	w := httptest.NewRecorder()

	h.DetachIncidentMonitor(w, req)

	if !called {
		t.Fatal("expected service to be called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandlers_DetachIncidentMonitor_InvalidMonitorIDParam(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{}, log)

	incidentID := uuid.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/incidents/"+incidentID.String()+"/monitors/bad-id", nil)
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "monitorId": "bad-id"})
	w := httptest.NewRecorder()

	h.DetachIncidentMonitor(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid monitor ID") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_DetachIncidentMonitor_MonitorIDRequiredParam(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{}, log)

	incidentID := uuid.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/incidents/"+incidentID.String()+"/monitors/", nil)
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "monitorId": ""})
	w := httptest.NewRecorder()

	h.DetachIncidentMonitor(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "monitor ID is required") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_PublishIncidentToStatusPage_ServiceValidationError(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{
		publishFn: func(ctx context.Context, tenantID, incidentID, statusPageID uuid.UUID, req *models.UpsertIncidentPublicationRequest) (*models.IncidentDetail, error) {
			return nil, errors.New("selected monitors must be linked to the incident and status page")
		},
	}, log)

	incidentID := uuid.New()
	statusPageID := uuid.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/incidents/"+incidentID.String()+"/status-pages/"+statusPageID.String(), bytes.NewBufferString(`{"monitor_ids":["11111111-1111-1111-1111-111111111111"]}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{
		"id":           incidentID.String(),
		"statusPageId": statusPageID.String(),
	})
	w := httptest.NewRecorder()

	h.PublishIncidentToStatusPage(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "selected monitors must be linked to the incident and status page") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_PublishIncidentToStatusPage(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	statusPageID := uuid.New()
	monitorID := uuid.New()
	called := false

	h := NewHandlers(&mockIncidentService{
		publishFn: func(ctx context.Context, tenantID, gotIncidentID, gotStatusPageID uuid.UUID, req *models.UpsertIncidentPublicationRequest) (*models.IncidentDetail, error) {
			called = true
			if gotIncidentID != incidentID {
				t.Fatalf("incidentID = %s, want %s", gotIncidentID, incidentID)
			}
			if gotStatusPageID != statusPageID {
				t.Fatalf("statusPageID = %s, want %s", gotStatusPageID, statusPageID)
			}
			if len(req.MonitorIDs) != 1 || req.MonitorIDs[0] != monitorID.String() {
				t.Fatalf("monitor IDs = %v, want [%s]", req.MonitorIDs, monitorID.String())
			}
			return &models.IncidentDetail{
				Incident: models.Incident{ID: incidentID, State: models.IncidentStateInvestigating},
			}, nil
		},
	}, log)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/incidents/"+incidentID.String()+"/status-pages/"+statusPageID.String(), bytes.NewBufferString(`{"monitor_ids":["`+monitorID.String()+`"]}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "statusPageId": statusPageID.String()})
	w := httptest.NewRecorder()

	h.PublishIncidentToStatusPage(w, req)

	if !called {
		t.Fatal("expected service to be called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandlers_PublishIncidentToStatusPage_InvalidStatusPageIDParam(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{}, log)

	incidentID := uuid.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/incidents/"+incidentID.String()+"/status-pages/bad-id", bytes.NewBufferString(`{"monitor_ids":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "statusPageId": "bad-id"})
	w := httptest.NewRecorder()

	h.PublishIncidentToStatusPage(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid status page ID") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_PublishIncidentToStatusPage_StatusPageIDRequiredParam(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{}, log)

	incidentID := uuid.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/incidents/"+incidentID.String()+"/status-pages/", bytes.NewBufferString(`{"monitor_ids":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "statusPageId": ""})
	w := httptest.NewRecorder()

	h.PublishIncidentToStatusPage(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "status page ID is required") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_PublishIncidentToStatusPage_ServiceRequestRequired(t *testing.T) {
	log := logger.New("test", "debug")
	called := false
	h := NewHandlers(&mockIncidentService{
		publishFn: func(ctx context.Context, tenantID, incidentID, statusPageID uuid.UUID, req *models.UpsertIncidentPublicationRequest) (*models.IncidentDetail, error) {
			called = true
			return nil, errors.New("request is required")
		},
	}, log)

	incidentID := uuid.New()
	statusPageID := uuid.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/incidents/"+incidentID.String()+"/status-pages/"+statusPageID.String(), bytes.NewBufferString(`{"monitor_ids":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "statusPageId": statusPageID.String()})
	w := httptest.NewRecorder()

	h.PublishIncidentToStatusPage(w, req)

	if !called {
		t.Fatal("expected service to be called")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "request is required") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_PublishIncidentToStatusPage_ServiceMonitorIDRequired(t *testing.T) {
	log := logger.New("test", "debug")
	called := false
	h := NewHandlers(&mockIncidentService{
		publishFn: func(ctx context.Context, tenantID, incidentID, statusPageID uuid.UUID, req *models.UpsertIncidentPublicationRequest) (*models.IncidentDetail, error) {
			called = true
			return nil, errors.New("monitor ID is required")
		},
	}, log)

	incidentID := uuid.New()
	statusPageID := uuid.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/incidents/"+incidentID.String()+"/status-pages/"+statusPageID.String(), bytes.NewBufferString(`{"monitor_ids":[""]}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "statusPageId": statusPageID.String()})
	w := httptest.NewRecorder()

	h.PublishIncidentToStatusPage(w, req)

	if !called {
		t.Fatal("expected service to be called")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "monitor ID is required") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_PublishIncidentToStatusPage_ServiceInvalidMonitorID(t *testing.T) {
	log := logger.New("test", "debug")
	called := false
	h := NewHandlers(&mockIncidentService{
		publishFn: func(ctx context.Context, tenantID, incidentID, statusPageID uuid.UUID, req *models.UpsertIncidentPublicationRequest) (*models.IncidentDetail, error) {
			called = true
			return nil, errors.New("invalid monitor ID")
		},
	}, log)

	incidentID := uuid.New()
	statusPageID := uuid.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/incidents/"+incidentID.String()+"/status-pages/"+statusPageID.String(), bytes.NewBufferString(`{"monitor_ids":["not-a-uuid"]}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "statusPageId": statusPageID.String()})
	w := httptest.NewRecorder()

	h.PublishIncidentToStatusPage(w, req)

	if !called {
		t.Fatal("expected service to be called")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid monitor ID") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_PublishIncidentToStatusPage_NotFound(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{
		publishFn: func(ctx context.Context, tenantID, incidentID, statusPageID uuid.UUID, req *models.UpsertIncidentPublicationRequest) (*models.IncidentDetail, error) {
			return nil, errors.New("status page not found")
		},
	}, log)

	incidentID := uuid.New()
	statusPageID := uuid.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/incidents/"+incidentID.String()+"/status-pages/"+statusPageID.String(), bytes.NewBufferString(`{"monitor_ids":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{
		"id":           incidentID.String(),
		"statusPageId": statusPageID.String(),
	})
	w := httptest.NewRecorder()

	h.PublishIncidentToStatusPage(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusNotFound, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "status page not found") {
		t.Fatalf("body = %s, want not-found message", w.Body.String())
	}
}

func TestHandlers_UnpublishIncidentFromStatusPage_NotFound(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{
		unpublishFn: func(ctx context.Context, tenantID, incidentID, statusPageID uuid.UUID) (*models.IncidentDetail, error) {
			return nil, errors.New("status page not found")
		},
	}, log)

	incidentID := uuid.New()
	statusPageID := uuid.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/incidents/"+incidentID.String()+"/status-pages/"+statusPageID.String(), nil)
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{
		"id":           incidentID.String(),
		"statusPageId": statusPageID.String(),
	})
	w := httptest.NewRecorder()

	h.UnpublishIncidentFromStatusPage(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusNotFound, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "status page not found") {
		t.Fatalf("body = %s, want not-found message", w.Body.String())
	}
}

func TestHandlers_UnpublishIncidentFromStatusPage(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	statusPageID := uuid.New()
	called := false

	h := NewHandlers(&mockIncidentService{
		unpublishFn: func(ctx context.Context, tenantID, gotIncidentID, gotStatusPageID uuid.UUID) (*models.IncidentDetail, error) {
			called = true
			if gotIncidentID != incidentID {
				t.Fatalf("incidentID = %s, want %s", gotIncidentID, incidentID)
			}
			if gotStatusPageID != statusPageID {
				t.Fatalf("statusPageID = %s, want %s", gotStatusPageID, statusPageID)
			}
			return &models.IncidentDetail{
				Incident: models.Incident{ID: incidentID, State: models.IncidentStateInvestigating},
			}, nil
		},
	}, log)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/incidents/"+incidentID.String()+"/status-pages/"+statusPageID.String(), nil)
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "statusPageId": statusPageID.String()})
	w := httptest.NewRecorder()

	h.UnpublishIncidentFromStatusPage(w, req)

	if !called {
		t.Fatal("expected service to be called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandlers_UnpublishIncidentFromStatusPage_InvalidStatusPageIDParam(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{}, log)

	incidentID := uuid.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/incidents/"+incidentID.String()+"/status-pages/bad-id", nil)
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "statusPageId": "bad-id"})
	w := httptest.NewRecorder()

	h.UnpublishIncidentFromStatusPage(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid status page ID") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_UnpublishIncidentFromStatusPage_StatusPageIDRequiredParam(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{}, log)

	incidentID := uuid.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/incidents/"+incidentID.String()+"/status-pages/", nil)
	req = withTenantID(req)
	req = withRouteParams(req, map[string]string{"id": incidentID.String(), "statusPageId": ""})
	w := httptest.NewRecorder()

	h.UnpublishIncidentFromStatusPage(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "status page ID is required") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_ListIncidents(t *testing.T) {
	log := logger.New("test", "debug")
	var gotPage, gotPageSize int
	svc := &mockIncidentService{
		listFn: func(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.IncidentListResponse, error) {
			gotPage, gotPageSize = page, pageSize
			return &models.IncidentListResponse{
				Items: []models.IncidentListItem{
					{ID: uuid.New(), Title: "API outage", State: models.IncidentStateInvestigating},
				},
				Page:     page,
				PageSize: pageSize,
				Total:    1,
			}, nil
		},
	}
	h := NewHandlers(svc, log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents?page=2&page_size=50", nil)
	req = withTenantID(req)
	w := httptest.NewRecorder()

	h.ListIncidents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusOK, w.Body.String())
	}
	if gotPage != 2 || gotPageSize != 50 {
		t.Fatalf("page/page_size = %d/%d, want 2/50", gotPage, gotPageSize)
	}
}

func TestHandlers_ListIncidents_InternalFailure(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{
		listFn: func(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.IncidentListResponse, error) {
			return nil, errors.New("database unavailable")
		},
	}, log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents", nil)
	req = withTenantID(req)
	w := httptest.NewRecorder()

	h.ListIncidents(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusInternalServerError, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "failed to list incidents") {
		t.Fatalf("body = %s, want internal error", w.Body.String())
	}
}

func TestHandlers_GetIncident(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	svc := &mockIncidentService{
		getFn: func(ctx context.Context, tenantID, gotIncidentID uuid.UUID) (*models.IncidentDetail, error) {
			if gotIncidentID != incidentID {
				t.Fatalf("incidentID = %s, want %s", gotIncidentID, incidentID)
			}
			return &models.IncidentDetail{
				Incident: models.Incident{
					ID:    incidentID,
					Title: "API outage",
					State: models.IncidentStateInvestigating,
				},
			}, nil
		},
	}
	h := NewHandlers(svc, log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents/"+incidentID.String(), nil)
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.GetIncident(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandlers_GetIncident_NotFound(t *testing.T) {
	log := logger.New("test", "debug")
	h := NewHandlers(&mockIncidentService{
		getFn: func(ctx context.Context, tenantID, incidentID uuid.UUID) (*models.IncidentDetail, error) {
			return nil, errors.New("incident not found")
		},
	}, log)

	incidentID := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents/"+incidentID.String(), nil)
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.GetIncident(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusNotFound, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "incident not found") {
		t.Fatalf("body = %s, want not-found message", w.Body.String())
	}
}

func TestHandlers_UpdateIncident(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	newTitle := "Updated outage"
	svc := &mockIncidentService{
		updateFn: func(ctx context.Context, tenantID, gotIncidentID uuid.UUID, req *models.UpdateIncidentRequest) (*models.IncidentDetail, error) {
			if gotIncidentID != incidentID {
				t.Fatalf("incidentID = %s, want %s", gotIncidentID, incidentID)
			}
			if req.Title == nil || *req.Title != newTitle {
				t.Fatalf("title = %v, want %q", req.Title, newTitle)
			}
			return &models.IncidentDetail{
				Incident: models.Incident{
					ID:    incidentID,
					Title: *req.Title,
					State: models.IncidentStateInvestigating,
				},
			}, nil
		},
	}
	h := NewHandlers(svc, log)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/incidents/"+incidentID.String(), bytes.NewBufferString(`{"title":"Updated outage"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.UpdateIncident(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandlers_UpdateIncident_InternalFailure(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	h := NewHandlers(&mockIncidentService{
		updateFn: func(ctx context.Context, tenantID, gotIncidentID uuid.UUID, req *models.UpdateIncidentRequest) (*models.IncidentDetail, error) {
			return nil, errors.New("database write failed")
		},
	}, log)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/incidents/"+incidentID.String(), bytes.NewBufferString(`{"title":"Updated outage"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.UpdateIncident(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusInternalServerError, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "failed to process incident") {
		t.Fatalf("body = %s, want internal error", w.Body.String())
	}
}

func TestHandlers_UpdateIncident_ServiceSummaryRequired(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	called := false
	h := NewHandlers(&mockIncidentService{
		updateFn: func(ctx context.Context, tenantID, gotIncidentID uuid.UUID, req *models.UpdateIncidentRequest) (*models.IncidentDetail, error) {
			called = true
			return nil, errors.New("summary is required")
		},
	}, log)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/incidents/"+incidentID.String(), bytes.NewBufferString(`{"title":"Updated outage"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.UpdateIncident(w, req)

	if !called {
		t.Fatal("expected service to be called")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "summary is required") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_UpdateIncident_NotFound(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	h := NewHandlers(&mockIncidentService{
		updateFn: func(ctx context.Context, tenantID, gotIncidentID uuid.UUID, req *models.UpdateIncidentRequest) (*models.IncidentDetail, error) {
			return nil, errors.New("incident not found")
		},
	}, log)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/incidents/"+incidentID.String(), bytes.NewBufferString(`{"title":"Updated outage"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.UpdateIncident(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusNotFound, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "incident not found") {
		t.Fatalf("body = %s, want not-found message", w.Body.String())
	}
}

func TestHandlers_TransitionIncidentState(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	svc := &mockIncidentService{
		transitionFn: func(ctx context.Context, tenantID, gotIncidentID uuid.UUID, req *models.TransitionIncidentStateRequest) (*models.IncidentDetail, error) {
			if gotIncidentID != incidentID {
				t.Fatalf("incidentID = %s, want %s", gotIncidentID, incidentID)
			}
			if req.State != models.IncidentStateResolved {
				t.Fatalf("state = %s, want %s", req.State, models.IncidentStateResolved)
			}
			return &models.IncidentDetail{
				Incident: models.Incident{
					ID:    incidentID,
					State: models.IncidentStateResolved,
				},
			}, nil
		},
	}
	h := NewHandlers(svc, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incidentID.String()+"/state", bytes.NewBufferString(`{"state":"resolved"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.TransitionIncidentState(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestHandlers_TransitionIncidentState_ServiceInvalidState(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	called := false
	h := NewHandlers(&mockIncidentService{
		transitionFn: func(ctx context.Context, tenantID, gotIncidentID uuid.UUID, req *models.TransitionIncidentStateRequest) (*models.IncidentDetail, error) {
			called = true
			return nil, errors.New("invalid incident state")
		},
	}, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incidentID.String()+"/state", bytes.NewBufferString(`{"state":"monitoring"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.TransitionIncidentState(w, req)

	if !called {
		t.Fatal("expected service to be called")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid incident state") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_TransitionIncidentState_NotFound(t *testing.T) {
	var logBuf bytes.Buffer
	log := logger.New("test", "debug")
	log.SetOutput(&logBuf)

	incidentID := uuid.New()
	h := NewHandlers(&mockIncidentService{
		transitionFn: func(ctx context.Context, tenantID, gotIncidentID uuid.UUID, req *models.TransitionIncidentStateRequest) (*models.IncidentDetail, error) {
			return nil, errors.New("incident not found")
		},
	}, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incidentID.String()+"/state", bytes.NewBufferString(`{"state":"resolved"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.TransitionIncidentState(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusNotFound, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "incident not found") {
		t.Fatalf("body = %s, want not-found message", w.Body.String())
	}
	if strings.Contains(logBuf.String(), "Failed to transition incident state") {
		t.Fatalf("log = %s, did not expect error-level transition log", logBuf.String())
	}
}

func TestHandlers_TransitionIncidentState_ResolvedCannotReopen(t *testing.T) {
	var logBuf bytes.Buffer
	log := logger.New("test", "debug")
	log.SetOutput(&logBuf)

	incidentID := uuid.New()
	h := NewHandlers(&mockIncidentService{
		transitionFn: func(ctx context.Context, tenantID, gotIncidentID uuid.UUID, req *models.TransitionIncidentStateRequest) (*models.IncidentDetail, error) {
			return nil, errors.New("resolved incidents cannot be reopened")
		},
	}, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incidentID.String()+"/state", bytes.NewBufferString(`{"state":"investigating"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.TransitionIncidentState(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "resolved incidents cannot be reopened") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
	if strings.Contains(logBuf.String(), "Failed to transition incident state") {
		t.Fatalf("log = %s, did not expect error-level transition log", logBuf.String())
	}
}

func TestHandlers_TransitionIncidentState_InternalFailureLogsContext(t *testing.T) {
	var logBuf bytes.Buffer
	log := logger.New("test", "debug")
	log.SetOutput(&logBuf)

	incidentID := uuid.New()
	tenantID := uuid.New()
	h := NewHandlers(&mockIncidentService{
		transitionFn: func(ctx context.Context, gotTenantID, gotIncidentID uuid.UUID, req *models.TransitionIncidentStateRequest) (*models.IncidentDetail, error) {
			return nil, errors.New("state transition failed")
		},
	}, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incidentID.String()+"/state", bytes.NewBufferString(`{"state":"resolved"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID.String()))
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.TransitionIncidentState(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusInternalServerError, w.Body.String())
	}
	if !strings.Contains(logBuf.String(), "Failed to transition incident state") {
		t.Fatalf("log = %s, want transition failure entry", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), tenantID.String()) {
		t.Fatalf("log = %s, want tenant ID", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), incidentID.String()) {
		t.Fatalf("log = %s, want incident ID", logBuf.String())
	}
}

func TestHandlers_CreateTimelineEntry(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	svc := &mockIncidentService{
		createTimelineFn: func(ctx context.Context, tenantID, gotIncidentID uuid.UUID, req *models.CreateIncidentTimelineEntryRequest) (*models.IncidentDetail, error) {
			if gotIncidentID != incidentID {
				t.Fatalf("incidentID = %s, want %s", gotIncidentID, incidentID)
			}
			if req.EntryType != models.IncidentTimelineEntryTypePublicUpdate {
				t.Fatalf("entry_type = %s, want %s", req.EntryType, models.IncidentTimelineEntryTypePublicUpdate)
			}
			if req.Message != "We are investigating." {
				t.Fatalf("message = %q, want %q", req.Message, "We are investigating.")
			}
			return &models.IncidentDetail{
				Incident: models.Incident{
					ID:    incidentID,
					State: models.IncidentStateInvestigating,
				},
			}, nil
		},
	}
	h := NewHandlers(svc, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incidentID.String()+"/timeline", bytes.NewBufferString(`{"entry_type":"public_update","message":"We are investigating."}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.CreateTimelineEntry(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusCreated, w.Body.String())
	}
}

func TestHandlers_CreateTimelineEntry_ServiceInvalidType(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	called := false
	h := NewHandlers(&mockIncidentService{
		createTimelineFn: func(ctx context.Context, tenantID, gotIncidentID uuid.UUID, req *models.CreateIncidentTimelineEntryRequest) (*models.IncidentDetail, error) {
			called = true
			return nil, errors.New("invalid incident timeline entry type")
		},
	}, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incidentID.String()+"/timeline", bytes.NewBufferString(`{"entry_type":"public_update","message":"We are investigating."}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.CreateTimelineEntry(w, req)

	if !called {
		t.Fatal("expected service to be called")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid incident timeline entry type") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_CreateTimelineEntry_ServiceMessageRequired(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	called := false
	h := NewHandlers(&mockIncidentService{
		createTimelineFn: func(ctx context.Context, tenantID, gotIncidentID uuid.UUID, req *models.CreateIncidentTimelineEntryRequest) (*models.IncidentDetail, error) {
			called = true
			return nil, errors.New("message is required")
		},
	}, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incidentID.String()+"/timeline", bytes.NewBufferString(`{"entry_type":"public_update","message":"We are investigating."}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.CreateTimelineEntry(w, req)

	if !called {
		t.Fatal("expected service to be called")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "message is required") {
		t.Fatalf("body = %s, want validation message", w.Body.String())
	}
}

func TestHandlers_CreateTimelineEntry_NotFound(t *testing.T) {
	log := logger.New("test", "debug")
	incidentID := uuid.New()
	h := NewHandlers(&mockIncidentService{
		createTimelineFn: func(ctx context.Context, tenantID, gotIncidentID uuid.UUID, req *models.CreateIncidentTimelineEntryRequest) (*models.IncidentDetail, error) {
			return nil, errors.New("incident not found")
		},
	}, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incidentID.String()+"/timeline", bytes.NewBufferString(`{"entry_type":"public_update","message":"We are investigating."}`))
	req.Header.Set("Content-Type", "application/json")
	req = withTenantID(req)
	req = withIncidentID(req, incidentID)
	w := httptest.NewRecorder()

	h.CreateTimelineEntry(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusNotFound, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "incident not found") {
		t.Fatalf("body = %s, want not-found message", w.Body.String())
	}
}

func TestHandlers_ResponseIncludesCreatedAt(t *testing.T) {
	log := logger.New("test", "debug")
	svc := &mockIncidentService{
		createFn: func(ctx context.Context, tenantID uuid.UUID, req *models.CreateIncidentRequest) (*models.IncidentDetail, error) {
			now := time.Now().UTC()
			return &models.IncidentDetail{
				Incident: models.Incident{
					ID:        uuid.New(),
					Title:     req.Title,
					Summary:   req.Summary,
					State:     models.IncidentStateInvestigating,
					CreatedAt: now,
					UpdatedAt: now,
				},
			}, nil
		},
	}
	h := NewHandlers(svc, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(`{"title":"API outage","summary":"Requests are failing."}`))
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))
	w := httptest.NewRecorder()

	h.CreateIncident(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (%s)", w.Code, http.StatusCreated, w.Body.String())
	}

	var got models.IncidentDetail
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.CreatedAt.IsZero() {
		t.Fatalf("created_at should be set in response")
	}
}

package incidents

import (
	"bytes"
	"context"
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
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

type mockIncidentService struct {
	createFn         func(ctx context.Context, tenantID uuid.UUID, req *models.CreateIncidentRequest) (*models.IncidentDetail, error)
	getFn            func(ctx context.Context, tenantID, incidentID uuid.UUID) (*models.IncidentDetail, error)
	listFn           func(ctx context.Context, tenantID uuid.UUID, page, pageSize int) (*models.IncidentListResponse, error)
	updateFn         func(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.UpdateIncidentRequest) (*models.IncidentDetail, error)
	transitionFn     func(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.TransitionIncidentStateRequest) (*models.IncidentDetail, error)
	createTimelineFn func(ctx context.Context, tenantID, incidentID uuid.UUID, req *models.CreateIncidentTimelineEntryRequest) (*models.IncidentDetail, error)
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

func withTenantID(req *http.Request) *http.Request {
	return req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))
}

func withIncidentID(req *http.Request, incidentID uuid.UUID) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", incidentID.String())
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestHandlers_CreateIncident(t *testing.T) {
	log := logger.New("test", "debug")
	svc := &mockIncidentService{
		createFn: func(ctx context.Context, tenantID uuid.UUID, req *models.CreateIncidentRequest) (*models.IncidentDetail, error) {
			return &models.IncidentDetail{
				Incident: models.Incident{
					ID:    uuid.New(),
					Title: req.Title,
					State: models.IncidentStateInvestigating,
				},
			}, nil
		},
	}
	h := NewHandlers(svc, log)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", bytes.NewBufferString(`{"title":"API outage","summary":"Requests are failing."}`))
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

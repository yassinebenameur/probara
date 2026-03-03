package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

type mockDashboardService struct {
	lastParams *models.DashboardOverviewQuery
}

func (m *mockDashboardService) GetOverview(ctx context.Context, tenantID uuid.UUID, params *models.DashboardOverviewQuery) (*models.DashboardOverviewResponse, error) {
	copied := *params
	m.lastParams = &copied
	return &models.DashboardOverviewResponse{
		Range:       params.Range,
		GeneratedAt: time.Now().UTC(),
		Stats:       models.DashboardStats{},
	}, nil
}

func TestHandlers_GetOverview_DefaultParams(t *testing.T) {
	log := logger.New("test", "debug")
	mockSvc := &mockDashboardService{}
	handlers := NewHandlers(mockSvc, log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/overview", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))

	w := httptest.NewRecorder()
	handlers.GetOverview(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	if mockSvc.lastParams == nil {
		t.Fatalf("expected params to be passed to service")
	}
	if mockSvc.lastParams.Range != models.DashboardRange24h {
		t.Fatalf("range = %s, want %s", mockSvc.lastParams.Range, models.DashboardRange24h)
	}
	if mockSvc.lastParams.FailuresLimit != 10 {
		t.Fatalf("failures_limit = %d, want 10", mockSvc.lastParams.FailuresLimit)
	}
	if mockSvc.lastParams.AlertsLimit != 10 {
		t.Fatalf("alerts_limit = %d, want 10", mockSvc.lastParams.AlertsLimit)
	}
}

func TestHandlers_GetOverview_InvalidRangeAndClampedLimits(t *testing.T) {
	log := logger.New("test", "debug")
	mockSvc := &mockDashboardService{}
	handlers := NewHandlers(mockSvc, log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/overview?range=bad&failures_limit=999&alerts_limit=-3", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))

	w := httptest.NewRecorder()
	handlers.GetOverview(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	if mockSvc.lastParams == nil {
		t.Fatalf("expected params to be passed to service")
	}
	if mockSvc.lastParams.Range != models.DashboardRange24h {
		t.Fatalf("range = %s, want %s", mockSvc.lastParams.Range, models.DashboardRange24h)
	}
	if mockSvc.lastParams.FailuresLimit != 50 {
		t.Fatalf("failures_limit = %d, want 50", mockSvc.lastParams.FailuresLimit)
	}
	if mockSvc.lastParams.AlertsLimit != 10 {
		t.Fatalf("alerts_limit = %d, want 10", mockSvc.lastParams.AlertsLimit)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
}

func TestHandlers_GetOverview_MissingTenant(t *testing.T) {
	log := logger.New("test", "debug")
	mockSvc := &mockDashboardService{}
	handlers := NewHandlers(mockSvc, log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/overview", nil)
	w := httptest.NewRecorder()
	handlers.GetOverview(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

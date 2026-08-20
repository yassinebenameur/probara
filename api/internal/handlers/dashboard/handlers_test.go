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
	lastOverviewParams        *models.DashboardOverviewQuery
	lastSummaryParams         *models.DashboardOverviewQuery
	lastProblemMonitorsParams *models.DashboardListQuery
	lastRecentFailuresParams  *models.DashboardListQuery
	lastRecentAlertsParams    *models.DashboardListQuery
	lastGroupSparklineParams  *models.DashboardGroupSparklineQuery
}

func (m *mockDashboardService) GetOverview(ctx context.Context, tenantID uuid.UUID, params *models.DashboardOverviewQuery) (*models.DashboardOverviewResponse, error) {
	copied := *params
	m.lastOverviewParams = &copied
	return &models.DashboardOverviewResponse{
		Range:           params.Range,
		GeneratedAt:     time.Now().UTC(),
		Stats:           models.DashboardStats{},
		OpsSummary:      models.DashboardOpsSummary{},
		ProblemMonitors: []models.DashboardProblemMonitor{},
	}, nil
}

func (m *mockDashboardService) GetSummary(ctx context.Context, tenantID uuid.UUID, params *models.DashboardOverviewQuery) (*models.DashboardSummaryResponse, error) {
	copied := *params
	m.lastSummaryParams = &copied
	return &models.DashboardSummaryResponse{
		Range:         params.Range,
		GeneratedAt:   time.Now().UTC(),
		Stats:         models.DashboardStats{},
		OpsSummary:    models.DashboardOpsSummary{},
		MonitorHealth: []models.DashboardMonitorHealth{},
	}, nil
}

func (m *mockDashboardService) GetProblemMonitors(ctx context.Context, tenantID uuid.UUID, params *models.DashboardListQuery) (*models.DashboardProblemMonitorsResponse, error) {
	copied := *params
	m.lastProblemMonitorsParams = &copied
	return &models.DashboardProblemMonitorsResponse{
		Range:           params.Range,
		GeneratedAt:     time.Now().UTC(),
		ProblemMonitors: []models.DashboardProblemMonitor{},
	}, nil
}

func (m *mockDashboardService) GetRecentFailures(ctx context.Context, tenantID uuid.UUID, params *models.DashboardListQuery) (*models.DashboardRecentFailuresResponse, error) {
	copied := *params
	m.lastRecentFailuresParams = &copied
	return &models.DashboardRecentFailuresResponse{
		Range:          params.Range,
		GeneratedAt:    time.Now().UTC(),
		RecentFailures: []models.DashboardFailureEvent{},
	}, nil
}

func (m *mockDashboardService) GetRecentAlerts(ctx context.Context, tenantID uuid.UUID, params *models.DashboardListQuery) (*models.DashboardRecentAlertsResponse, error) {
	copied := *params
	m.lastRecentAlertsParams = &copied
	return &models.DashboardRecentAlertsResponse{
		Range:        params.Range,
		GeneratedAt:  time.Now().UTC(),
		RecentAlerts: []models.AlertWithDetails{},
	}, nil
}

func (m *mockDashboardService) GetGroupSparkline(ctx context.Context, tenantID uuid.UUID, params *models.DashboardGroupSparklineQuery) (*models.DashboardGroupSparklineResponse, error) {
	copied := *params
	m.lastGroupSparklineParams = &copied
	buckets := make([]*float64, 12)
	for i := range buckets {
		v := 100.0
		buckets[i] = &v
	}
	return &models.DashboardGroupSparklineResponse{
		Tag:     params.Tag,
		Range:   params.Range,
		Buckets: buckets,
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

	if mockSvc.lastOverviewParams == nil {
		t.Fatalf("expected params to be passed to service")
	}
	if mockSvc.lastOverviewParams.Range != models.DashboardRange24h {
		t.Fatalf("range = %s, want %s", mockSvc.lastOverviewParams.Range, models.DashboardRange24h)
	}
	if mockSvc.lastOverviewParams.FailuresLimit != 10 {
		t.Fatalf("failures_limit = %d, want 10", mockSvc.lastOverviewParams.FailuresLimit)
	}
	if mockSvc.lastOverviewParams.AlertsLimit != 10 {
		t.Fatalf("alerts_limit = %d, want 10", mockSvc.lastOverviewParams.AlertsLimit)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, ok := body["ops_summary"]; !ok {
		t.Fatalf("expected ops_summary field in response")
	}
	if _, ok := body["problem_monitors"]; !ok {
		t.Fatalf("expected problem_monitors field in response")
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

	if mockSvc.lastOverviewParams == nil {
		t.Fatalf("expected params to be passed to service")
	}
	if mockSvc.lastOverviewParams.Range != models.DashboardRange24h {
		t.Fatalf("range = %s, want %s", mockSvc.lastOverviewParams.Range, models.DashboardRange24h)
	}
	if mockSvc.lastOverviewParams.FailuresLimit != 50 {
		t.Fatalf("failures_limit = %d, want 50", mockSvc.lastOverviewParams.FailuresLimit)
	}
	if mockSvc.lastOverviewParams.AlertsLimit != 10 {
		t.Fatalf("alerts_limit = %d, want 10", mockSvc.lastOverviewParams.AlertsLimit)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
}

func TestHandlers_GetOverview_LongRangeAccepted(t *testing.T) {
	log := logger.New("test", "debug")
	mockSvc := &mockDashboardService{}
	handlers := NewHandlers(mockSvc, log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/overview?range=365d", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))

	w := httptest.NewRecorder()
	handlers.GetOverview(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if mockSvc.lastOverviewParams == nil || mockSvc.lastOverviewParams.Range != models.DashboardRange365d {
		t.Fatalf("range = %v, want %s", mockSvc.lastOverviewParams, models.DashboardRange365d)
	}
}

func TestHandlers_GetOverview_RepeatedTagsPassedThrough(t *testing.T) {
	log := logger.New("test", "debug")
	mockSvc := &mockDashboardService{}
	handlers := NewHandlers(mockSvc, log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/overview?tag=prod&tag=api&tag=prod", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))

	w := httptest.NewRecorder()
	handlers.GetOverview(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if mockSvc.lastOverviewParams == nil {
		t.Fatalf("expected params to be passed to service")
	}
	if len(mockSvc.lastOverviewParams.Tags) != 3 {
		t.Fatalf("tags length = %d, want 3", len(mockSvc.lastOverviewParams.Tags))
	}
	if mockSvc.lastOverviewParams.Tags[0] != "prod" || mockSvc.lastOverviewParams.Tags[1] != "api" || mockSvc.lastOverviewParams.Tags[2] != "prod" {
		t.Fatalf("tags = %#v, want [prod api prod]", mockSvc.lastOverviewParams.Tags)
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

func TestHandlers_GetSummary_DefaultParams(t *testing.T) {
	log := logger.New("test", "debug")
	mockSvc := &mockDashboardService{}
	handlers := NewHandlers(mockSvc, log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/summary", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))

	w := httptest.NewRecorder()
	handlers.GetSummary(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if mockSvc.lastSummaryParams == nil {
		t.Fatalf("expected summary params to be passed to service")
	}
	if mockSvc.lastSummaryParams.Range != models.DashboardRange24h {
		t.Fatalf("range = %s, want %s", mockSvc.lastSummaryParams.Range, models.DashboardRange24h)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, ok := body["ops_summary"]; !ok {
		t.Fatalf("expected ops_summary field in response")
	}
	if _, ok := body["problem_monitors"]; ok {
		t.Fatalf("did not expect problem_monitors field in summary response")
	}
}

func TestHandlers_GetSummary_OneHourRangeAccepted(t *testing.T) {
	log := logger.New("test", "debug")
	mockSvc := &mockDashboardService{}
	handlers := NewHandlers(mockSvc, log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/summary?range=1h", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))

	w := httptest.NewRecorder()
	handlers.GetSummary(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if mockSvc.lastSummaryParams == nil {
		t.Fatalf("expected summary params to be passed to service")
	}
	if mockSvc.lastSummaryParams.Range != models.DashboardRange("1h") {
		t.Fatalf("range = %s, want 1h", mockSvc.lastSummaryParams.Range)
	}
}

func TestHandlers_GetProblemMonitors_ParsesRangeLimitAndTags(t *testing.T) {
	log := logger.New("test", "debug")
	mockSvc := &mockDashboardService{}
	handlers := NewHandlers(mockSvc, log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/problem-monitors?range=30d&limit=999&tag=prod&tag=api", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))

	w := httptest.NewRecorder()
	handlers.GetProblemMonitors(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if mockSvc.lastProblemMonitorsParams == nil {
		t.Fatalf("expected problem monitors params to be passed to service")
	}
	if mockSvc.lastProblemMonitorsParams.Range != models.DashboardRange30d {
		t.Fatalf("range = %s, want %s", mockSvc.lastProblemMonitorsParams.Range, models.DashboardRange30d)
	}
	if mockSvc.lastProblemMonitorsParams.Limit != 50 {
		t.Fatalf("limit = %d, want 50", mockSvc.lastProblemMonitorsParams.Limit)
	}
	if len(mockSvc.lastProblemMonitorsParams.Tags) != 2 {
		t.Fatalf("tags length = %d, want 2", len(mockSvc.lastProblemMonitorsParams.Tags))
	}
}

func TestHandlers_GetRecentFailures_UsesDefaultLimit(t *testing.T) {
	log := logger.New("test", "debug")
	mockSvc := &mockDashboardService{}
	handlers := NewHandlers(mockSvc, log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/recent-failures?range=7d", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))

	w := httptest.NewRecorder()
	handlers.GetRecentFailures(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if mockSvc.lastRecentFailuresParams == nil {
		t.Fatalf("expected recent failures params to be passed to service")
	}
	if mockSvc.lastRecentFailuresParams.Range != models.DashboardRange7d {
		t.Fatalf("range = %s, want %s", mockSvc.lastRecentFailuresParams.Range, models.DashboardRange7d)
	}
	if mockSvc.lastRecentFailuresParams.Limit != 10 {
		t.Fatalf("limit = %d, want 10", mockSvc.lastRecentFailuresParams.Limit)
	}
}

func TestHandlers_GetRecentAlerts_UsesLimitAndTags(t *testing.T) {
	log := logger.New("test", "debug")
	mockSvc := &mockDashboardService{}
	handlers := NewHandlers(mockSvc, log)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/recent-alerts?range=365d&limit=25&tag=prod", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))

	w := httptest.NewRecorder()
	handlers.GetRecentAlerts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if mockSvc.lastRecentAlertsParams == nil {
		t.Fatalf("expected recent alerts params to be passed to service")
	}
	if mockSvc.lastRecentAlertsParams.Range != models.DashboardRange365d {
		t.Fatalf("range = %s, want %s", mockSvc.lastRecentAlertsParams.Range, models.DashboardRange365d)
	}
	if mockSvc.lastRecentAlertsParams.Limit != 25 {
		t.Fatalf("limit = %d, want 25", mockSvc.lastRecentAlertsParams.Limit)
	}
	if len(mockSvc.lastRecentAlertsParams.Tags) != 1 || mockSvc.lastRecentAlertsParams.Tags[0] != "prod" {
		t.Fatalf("tags = %#v, want [prod]", mockSvc.lastRecentAlertsParams.Tags)
	}
}

func TestHandlers_GetGroupSparkline_HappyPathWithTag(t *testing.T) {
	log := logger.New("test", "debug")
	mockSvc := &mockDashboardService{}
	handlers := NewHandlers(mockSvc, log)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/group-sparkline?group=api&range=7d&tag=prod", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))

	w := httptest.NewRecorder()
	handlers.GetGroupSparkline(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if mockSvc.lastGroupSparklineParams == nil {
		t.Fatalf("expected group sparkline params to be passed to service")
	}
	if mockSvc.lastGroupSparklineParams.Tag == nil {
		t.Fatalf("expected Tag to be non-nil")
	}
	if *mockSvc.lastGroupSparklineParams.Tag != "api" {
		t.Fatalf("Tag = %q, want %q", *mockSvc.lastGroupSparklineParams.Tag, "api")
	}
	if mockSvc.lastGroupSparklineParams.Range != models.DashboardRange7d {
		t.Fatalf("Range = %s, want %s", mockSvc.lastGroupSparklineParams.Range, models.DashboardRange7d)
	}
	if len(mockSvc.lastGroupSparklineParams.Tags) != 1 || mockSvc.lastGroupSparklineParams.Tags[0] != "prod" {
		t.Fatalf("Tags = %#v, want [prod]", mockSvc.lastGroupSparklineParams.Tags)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["tag"] != "api" {
		t.Fatalf("response tag = %v, want %q", body["tag"], "api")
	}
	if body["range"] != "7d" {
		t.Fatalf("response range = %v, want %q", body["range"], "7d")
	}
	buckets, ok := body["buckets"].([]interface{})
	if !ok {
		t.Fatalf("expected buckets array in response, got %T", body["buckets"])
	}
	if len(buckets) != 12 {
		t.Fatalf("buckets length = %d, want 12", len(buckets))
	}
}

func TestHandlers_GetGroupSparkline_UngroupedSentinel(t *testing.T) {
	log := logger.New("test", "debug")
	mockSvc := &mockDashboardService{}
	handlers := NewHandlers(mockSvc, log)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/group-sparkline?range=30d", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.New().String()))

	w := httptest.NewRecorder()
	handlers.GetGroupSparkline(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if mockSvc.lastGroupSparklineParams == nil {
		t.Fatalf("expected group sparkline params to be passed to service")
	}
	if mockSvc.lastGroupSparklineParams.Tag != nil {
		t.Fatalf("expected Tag to be nil (ungrouped), got %q", *mockSvc.lastGroupSparklineParams.Tag)
	}
	if mockSvc.lastGroupSparklineParams.Range != models.DashboardRange30d {
		t.Fatalf("Range = %s, want %s", mockSvc.lastGroupSparklineParams.Range, models.DashboardRange30d)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, hasTag := body["tag"]; !hasTag {
		t.Fatalf("expected tag field in response (even if null)")
	}
	if body["tag"] != nil {
		t.Fatalf("response tag = %v, want null", body["tag"])
	}
}

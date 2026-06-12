package monitors

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

type groupCall struct {
	groupID    uuid.UUID
	monitorIDs []uuid.UUID
}

// MockMonitorService implements the MonitorService interface for testing
type MockMonitorService struct {
	monitors            map[uuid.UUID]*models.Monitor
	deleteHistoryErr    error
	deletedHistoryCalls []uuid.UUID
}

func NewMockMonitorService() *MockMonitorService {
	return &MockMonitorService{
		monitors: make(map[uuid.UUID]*models.Monitor),
	}
}

func (m *MockMonitorService) CreateMonitor(ctx context.Context, tenantID uuid.UUID, req *models.CreateMonitorRequest) (*models.Monitor, error) {
	monitor := &models.Monitor{
		ID:              uuid.New(),
		TenantID:        tenantID,
		Name:            req.Name,
		Type:            req.Type,
		Config:          req.Config,
		IntervalSeconds: req.IntervalSeconds,
		TimeoutSeconds:  req.TimeoutSeconds,
		Enabled:         true,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	m.monitors[monitor.ID] = monitor
	return monitor, nil
}

func (m *MockMonitorService) ResolveTestConfig(ctx context.Context, tenantID uuid.UUID, monitorID *uuid.UUID, monitorType models.MonitorType, config json.RawMessage) (json.RawMessage, error) {
	return config, nil
}

func (m *MockMonitorService) GetMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Monitor, error) {
	monitor, ok := m.monitors[monitorID]
	if !ok {
		return nil, &mockNotFoundError{}
	}
	return monitor, nil
}

func (m *MockMonitorService) ListMonitors(ctx context.Context, tenantID uuid.UUID, tag *string, enabled *bool, page, pageSize int) (*models.MonitorListResponse, error) {
	var items []models.Monitor
	for _, monitor := range m.monitors {
		if monitor.TenantID == tenantID {
			items = append(items, *monitor)
		}
	}
	return &models.MonitorListResponse{
		Items:    items,
		Page:     page,
		PageSize: pageSize,
		Total:    len(items),
	}, nil
}

func (m *MockMonitorService) UpdateMonitor(ctx context.Context, tenantID, monitorID uuid.UUID, req *models.UpdateMonitorRequest) (*models.Monitor, error) {
	monitor, ok := m.monitors[monitorID]
	if !ok {
		return nil, &mockNotFoundError{}
	}
	if req.Name != nil {
		monitor.Name = *req.Name
	}
	monitor.UpdatedAt = time.Now()
	return monitor, nil
}

func (m *MockMonitorService) DeleteMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) error {
	if _, ok := m.monitors[monitorID]; !ok {
		return &mockNotFoundError{}
	}
	delete(m.monitors, monitorID)
	return nil
}

func (m *MockMonitorService) DeleteMonitorHistory(ctx context.Context, tenantID, monitorID uuid.UUID) error {
	if m.deleteHistoryErr != nil {
		return m.deleteHistoryErr
	}
	if _, ok := m.monitors[monitorID]; !ok {
		return &mockNotFoundError{}
	}
	m.deletedHistoryCalls = append(m.deletedHistoryCalls, monitorID)
	return nil
}

func (m *MockMonitorService) BulkDeleteMonitors(
	ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID,
) (int64, error) {
	var deleted int64
	for _, id := range monitorIDs {
		if monitor, ok := m.monitors[id]; ok && monitor.TenantID == tenantID {
			delete(m.monitors, id)
			deleted++
		}
	}
	return deleted, nil
}

func (m *MockMonitorService) BulkUpdateAlerting(
	ctx context.Context,
	tenantID uuid.UUID,
	monitorIDs []uuid.UUID,
	threshold *int,
	mode *string,
	channels []models.MonitorChannelAssignment,
) (int, error) {
	return len(monitorIDs), nil
}

type mockNotFoundError struct{}

func (e *mockNotFoundError) Error() string {
	return "monitor not found"
}

// MockGroupService implements the handler group membership dependency for testing.
type MockGroupService struct {
	currentMembers []models.Monitor
	addCalls       []groupCall
	removeCalls    []groupCall
}

func (m *MockGroupService) AddMonitorsToGroup(ctx context.Context, tenantID, groupID uuid.UUID, monitorIDs []uuid.UUID) error {
	ids := append([]uuid.UUID(nil), monitorIDs...)
	m.addCalls = append(m.addCalls, groupCall{groupID: groupID, monitorIDs: ids})
	return nil
}

func (m *MockGroupService) RemoveMonitorsFromGroup(ctx context.Context, tenantID, groupID uuid.UUID, monitorIDs []uuid.UUID) error {
	ids := append([]uuid.UUID(nil), monitorIDs...)
	m.removeCalls = append(m.removeCalls, groupCall{groupID: groupID, monitorIDs: ids})
	return nil
}

func (m *MockGroupService) GetGroupMembers(ctx context.Context, tenantID, groupID uuid.UUID) ([]models.Monitor, error) {
	return append([]models.Monitor(nil), m.currentMembers...), nil
}

// MockResultsService implements the ResultsService interface for testing
type MockResultsService struct{}

func (m *MockResultsService) GetMonitorResults(ctx context.Context, tenantID, monitorID uuid.UUID, limit int, since *time.Time) (*models.MonitorResultsResponse, error) {
	return &models.MonitorResultsResponse{
		MonitorID: monitorID,
		Results:   []models.CheckResult{},
	}, nil
}

func (m *MockResultsService) GetMonitorAnalytics(ctx context.Context, tenantID, monitorID uuid.UUID, rangeValue models.MonitorAnalyticsRange) (*models.MonitorAnalyticsResponse, error) {
	return &models.MonitorAnalyticsResponse{
		MonitorID: monitorID,
		Range:     rangeValue,
		Source:    models.AnalyticsSourceRaw,
		Summary:   models.MonitorAnalyticsSummary{},
	}, nil
}

type MockCheckJobPublisher struct {
	subject string
	job     *sharedmodels.Job
	err     error
}

func (m *MockCheckJobPublisher) PublishJSON(ctx context.Context, subject string, v interface{}, headers map[string][]string) error {
	if m.err != nil {
		return m.err
	}
	m.subject = subject
	switch j := v.(type) {
	case *sharedmodels.Job:
		m.job = j
	case sharedmodels.Job:
		copied := j
		m.job = &copied
	}
	return nil
}

func TestHandlers_CreateMonitor(t *testing.T) {
	log := logger.New("test", "debug")
	monitorSvc := NewMockMonitorService()
	groupSvc := &MockGroupService{}
	resultSvc := &MockResultsService{}
	handlers := NewHandlers(monitorSvc, groupSvc, resultSvc, log, t.TempDir())

	tenantID := uuid.New()

	reqBody := `{
		"name": "Test Monitor",
		"type": "http",
		"config": {"url": "https://example.com", "method": "GET"},
		"interval_seconds": 60,
		"timeout_seconds": 30
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	// Add tenant ID to context
	ctx := ctxpkg.WithTenantID(req.Context(), tenantID.String())
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handlers.CreateMonitor(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status %d, got %d: %s", http.StatusCreated, w.Code, w.Body.String())
	}

	var monitor models.Monitor
	if err := json.NewDecoder(w.Body).Decode(&monitor); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if monitor.Name != "Test Monitor" {
		t.Errorf("Expected name 'Test Monitor', got '%s'", monitor.Name)
	}
}

func TestHandlers_CreateMonitor_GroupSyncsMembersViaGroupService(t *testing.T) {
	log := logger.New("test", "debug")
	monitorSvc := NewMockMonitorService()
	groupSvc := &MockGroupService{}
	resultSvc := &MockResultsService{}
	handlers := NewHandlers(monitorSvc, groupSvc, resultSvc, log, t.TempDir())

	tenantID := uuid.New()
	memberA := uuid.New()
	memberB := uuid.New()

	reqBody := `{
		"name": "Grouped Monitor",
		"type": "group",
		"config": {"monitor_ids": ["` + memberA.String() + `", "` + memberB.String() + `"]},
		"interval_seconds": 60,
		"timeout_seconds": 30
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID.String()))

	w := httptest.NewRecorder()
	handlers.CreateMonitor(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusCreated, w.Code, w.Body.String())
	}

	if len(groupSvc.addCalls) != 1 {
		t.Fatalf("expected 1 add call, got %d", len(groupSvc.addCalls))
	}
	if len(groupSvc.addCalls[0].monitorIDs) != 2 {
		t.Fatalf("expected 2 monitor IDs, got %d", len(groupSvc.addCalls[0].monitorIDs))
	}
}

func TestHandlers_GetMonitor(t *testing.T) {
	log := logger.New("test", "debug")
	monitorSvc := NewMockMonitorService()
	groupSvc := &MockGroupService{}
	resultSvc := &MockResultsService{}
	handlers := NewHandlers(monitorSvc, groupSvc, resultSvc, log, t.TempDir())

	tenantID := uuid.New()
	monitorID := uuid.New()

	// Pre-populate a monitor
	monitorSvc.monitors[monitorID] = &models.Monitor{
		ID:              monitorID,
		TenantID:        tenantID,
		Name:            "Test Monitor",
		Type:            models.MonitorTypeHTTP,
		IntervalSeconds: 60,
		TimeoutSeconds:  30,
	}

	// Create router with URL params
	r := chi.NewRouter()
	r.Get("/{id}", handlers.GetMonitor)

	req := httptest.NewRequest(http.MethodGet, "/"+monitorID.String(), nil)
	ctx := ctxpkg.WithTenantID(req.Context(), tenantID.String())
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}
}

func TestHandlers_DeleteMonitorHistory(t *testing.T) {
	log := logger.New("test", "debug")
	monitorSvc := NewMockMonitorService()
	groupSvc := &MockGroupService{}
	resultSvc := &MockResultsService{}
	handlers := NewHandlers(monitorSvc, groupSvc, resultSvc, log, t.TempDir())

	tenantID := uuid.New()
	monitorID := uuid.New()
	monitorSvc.monitors[monitorID] = &models.Monitor{
		ID:       monitorID,
		TenantID: tenantID,
		Name:     "History Monitor",
		Type:     models.MonitorTypeHTTP,
	}

	r := chi.NewRouter()
	r.Delete("/{id}/history", handlers.DeleteMonitorHistory)

	req := httptest.NewRequest(http.MethodDelete, "/"+monitorID.String()+"/history", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID.String()))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d: %s", http.StatusNoContent, w.Code, w.Body.String())
	}
	if len(monitorSvc.deletedHistoryCalls) != 1 || monitorSvc.deletedHistoryCalls[0] != monitorID {
		t.Fatalf("deletedHistoryCalls = %v, want [%s]", monitorSvc.deletedHistoryCalls, monitorID)
	}
}

func TestHandlers_ListMonitors(t *testing.T) {
	log := logger.New("test", "debug")
	monitorSvc := NewMockMonitorService()
	groupSvc := &MockGroupService{}
	resultSvc := &MockResultsService{}
	handlers := NewHandlers(monitorSvc, groupSvc, resultSvc, log, t.TempDir())

	tenantID := uuid.New()

	// Pre-populate some monitors
	for i := 0; i < 3; i++ {
		id := uuid.New()
		monitorSvc.monitors[id] = &models.Monitor{
			ID:       id,
			TenantID: tenantID,
			Name:     "Test Monitor",
			Type:     models.MonitorTypeHTTP,
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
	ctx := ctxpkg.WithTenantID(req.Context(), tenantID.String())
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handlers.ListMonitors(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var response models.MonitorListResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Total != 3 {
		t.Errorf("Expected 3 monitors, got %d", response.Total)
	}
}

func TestHandlers_GetMonitorAnalytics(t *testing.T) {
	log := logger.New("test", "debug")
	monitorSvc := NewMockMonitorService()
	groupSvc := &MockGroupService{}
	resultSvc := &MockResultsService{}
	handlers := NewHandlers(monitorSvc, groupSvc, resultSvc, log, t.TempDir())

	tenantID := uuid.New()
	monitorID := uuid.New()
	monitorSvc.monitors[monitorID] = &models.Monitor{
		ID:              monitorID,
		TenantID:        tenantID,
		Name:            "Analytics Monitor",
		Type:            models.MonitorTypeHTTP,
		IntervalSeconds: 60,
		TimeoutSeconds:  30,
	}

	r := chi.NewRouter()
	r.Get("/{id}/analytics", handlers.GetMonitorAnalytics)

	req := httptest.NewRequest(http.MethodGet, "/"+monitorID.String()+"/analytics?range=90d", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID.String()))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var response models.MonitorAnalyticsResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if response.Range != models.MonitorAnalyticsRange90d {
		t.Fatalf("range = %s, want %s", response.Range, models.MonitorAnalyticsRange90d)
	}
}

func TestHandlers_DeleteMonitor(t *testing.T) {
	log := logger.New("test", "debug")
	monitorSvc := NewMockMonitorService()
	groupSvc := &MockGroupService{}
	resultSvc := &MockResultsService{}
	handlers := NewHandlers(monitorSvc, groupSvc, resultSvc, log, t.TempDir())

	tenantID := uuid.New()
	monitorID := uuid.New()

	// Pre-populate a monitor
	monitorSvc.monitors[monitorID] = &models.Monitor{
		ID:       monitorID,
		TenantID: tenantID,
	}

	// Create router with URL params
	r := chi.NewRouter()
	r.Delete("/{id}", handlers.DeleteMonitor)

	req := httptest.NewRequest(http.MethodDelete, "/"+monitorID.String(), nil)
	ctx := ctxpkg.WithTenantID(req.Context(), tenantID.String())
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status %d, got %d: %s", http.StatusNoContent, w.Code, w.Body.String())
	}

	// Verify monitor is deleted
	if _, ok := monitorSvc.monitors[monitorID]; ok {
		t.Error("Expected monitor to be deleted")
	}
}

func TestHandlers_UpdateMonitor_GroupSyncsMembershipDiff(t *testing.T) {
	log := logger.New("test", "debug")
	monitorSvc := NewMockMonitorService()
	groupID := uuid.New()
	memberA := uuid.New()
	memberB := uuid.New()
	memberC := uuid.New()
	tenantID := uuid.New()

	monitorSvc.monitors[groupID] = &models.Monitor{
		ID:              groupID,
		TenantID:        tenantID,
		Name:            "Group Monitor",
		Type:            models.MonitorTypeGroup,
		Config:          json.RawMessage(`{"monitor_ids":[]}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  30,
	}

	groupSvc := &MockGroupService{
		currentMembers: []models.Monitor{
			{ID: memberA},
			{ID: memberB},
		},
	}
	resultSvc := &MockResultsService{}
	handlers := NewHandlers(monitorSvc, groupSvc, resultSvc, log, t.TempDir())

	r := chi.NewRouter()
	r.Patch("/{id}", handlers.UpdateMonitor)

	reqBody := `{
		"config": {"monitor_ids": ["` + memberB.String() + `", "` + memberC.String() + `"]}
	}`
	req := httptest.NewRequest(http.MethodPatch, "/"+groupID.String(), bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID.String()))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	if len(groupSvc.addCalls) != 1 {
		t.Fatalf("expected 1 add call, got %d", len(groupSvc.addCalls))
	}
	if len(groupSvc.removeCalls) != 1 {
		t.Fatalf("expected 1 remove call, got %d", len(groupSvc.removeCalls))
	}
	if got := groupSvc.addCalls[0].monitorIDs; len(got) != 1 || got[0] != memberC {
		t.Fatalf("expected add call for %s, got %+v", memberC, got)
	}
	if got := groupSvc.removeCalls[0].monitorIDs; len(got) != 1 || got[0] != memberA {
		t.Fatalf("expected remove call for %s, got %+v", memberA, got)
	}
}

func TestSanitizeArtifactPath(t *testing.T) {
	monitorID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "valid monitor-scoped path",
			input: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/screenshot-1.png",
			want:  filepath.Clean("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/screenshot-1.png"),
		},
		{
			name:    "path traversal blocked",
			input:   "../etc/passwd",
			wantErr: true,
		},
		{
			name:    "monitor mismatch blocked",
			input:   "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb/screenshot-1.png",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sanitizeArtifactPath(tc.input, monitorID)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestHandlers_RunMonitorNow(t *testing.T) {
	log := logger.New("test", "debug")
	monitorSvc := NewMockMonitorService()
	groupSvc := &MockGroupService{}
	resultSvc := &MockResultsService{}
	handlers := NewHandlers(monitorSvc, groupSvc, resultSvc, log, t.TempDir())

	mockPublisher := &MockCheckJobPublisher{}
	handlers.ConfigureCheckJobs(mockPublisher, "check.jobs")

	tenantID := uuid.New()
	monitorID := uuid.New()
	monitorSvc.monitors[monitorID] = &models.Monitor{
		ID:              monitorID,
		TenantID:        tenantID,
		Name:            "Synthetic API",
		Type:            models.MonitorTypeSyntheticAPI,
		Config:          json.RawMessage(`{"steps":[{"id":"health","request":{"method":"GET","url":"/health"}}]}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  30,
		Enabled:         true,
	}

	r := chi.NewRouter()
	r.Post("/{id}/run", handlers.RunMonitorNow)

	req := httptest.NewRequest(http.MethodPost, "/"+monitorID.String()+"/run", nil)
	ctx := ctxpkg.WithTenantID(req.Context(), tenantID.String())
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusAccepted, w.Code, w.Body.String())
	}

	var resp models.RunMonitorNowResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.MonitorID != monitorID {
		t.Fatalf("Expected monitor ID %s, got %s", monitorID, resp.MonitorID)
	}
	if resp.JobID == "" {
		t.Fatal("Expected non-empty job ID")
	}
	if mockPublisher.subject != "check.jobs" {
		t.Fatalf("Expected publish subject check.jobs, got %s", mockPublisher.subject)
	}
	if mockPublisher.job == nil {
		t.Fatal("Expected published job")
	}
	if mockPublisher.job.TenantID != tenantID.String() {
		t.Fatalf("Expected job tenant ID %s, got %s", tenantID, mockPublisher.job.TenantID)
	}
}

func TestHandlers_RunMonitorNow_QueueUnavailable(t *testing.T) {
	log := logger.New("test", "debug")
	monitorSvc := NewMockMonitorService()
	groupSvc := &MockGroupService{}
	resultSvc := &MockResultsService{}
	handlers := NewHandlers(monitorSvc, groupSvc, resultSvc, log, t.TempDir())

	tenantID := uuid.New()
	monitorID := uuid.New()
	monitorSvc.monitors[monitorID] = &models.Monitor{
		ID:              monitorID,
		TenantID:        tenantID,
		Name:            "Synthetic Browser",
		Type:            models.MonitorTypeSyntheticBrowser,
		Config:          json.RawMessage(`{"start_url":"https://example.com","steps":[{"id":"open","action":"goto","url":"https://example.com"}]}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  30,
		Enabled:         true,
	}

	r := chi.NewRouter()
	r.Post("/{id}/run", handlers.RunMonitorNow)

	req := httptest.NewRequest(http.MethodPost, "/"+monitorID.String()+"/run", nil)
	ctx := ctxpkg.WithTenantID(req.Context(), tenantID.String())
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusInternalServerError, w.Code, w.Body.String())
	}
}

func TestHandlers_BulkDeleteMonitors(t *testing.T) {
	log := logger.New("test", "debug")
	monitorSvc := NewMockMonitorService()
	groupSvc := &MockGroupService{}
	resultSvc := &MockResultsService{}
	handlers := NewHandlers(monitorSvc, groupSvc, resultSvc, log, t.TempDir())

	tenantID := uuid.New()
	idA, idB := uuid.New(), uuid.New()
	monitorSvc.monitors[idA] = &models.Monitor{ID: idA, TenantID: tenantID}
	monitorSvc.monitors[idB] = &models.Monitor{ID: idB, TenantID: tenantID}

	r := chi.NewRouter()
	r.Post("/bulk/delete", handlers.BulkDeleteMonitors)

	body := `{"monitor_ids":["` + idA.String() + `","` + idB.String() + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/bulk/delete", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID.String()))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var resp models.BulkDeleteMonitorsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Deleted != 2 {
		t.Errorf("Deleted = %d, want 2", resp.Deleted)
	}
	if _, ok := monitorSvc.monitors[idA]; ok {
		t.Error("monitor A should be removed from mock")
	}
	if _, ok := monitorSvc.monitors[idB]; ok {
		t.Error("monitor B should be removed from mock")
	}
}

func TestHandlers_BulkDeleteMonitors_RejectsEmpty(t *testing.T) {
	log := logger.New("test", "debug")
	handlers := NewHandlers(NewMockMonitorService(), &MockGroupService{}, &MockResultsService{}, log, t.TempDir())

	r := chi.NewRouter()
	r.Post("/bulk/delete", handlers.BulkDeleteMonitors)

	req := httptest.NewRequest(http.MethodPost, "/bulk/delete",
		bytes.NewBufferString(`{"monitor_ids":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.NewString()))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}

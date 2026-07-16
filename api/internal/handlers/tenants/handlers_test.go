package tenants

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

type mockTenantService struct {
	settings *models.TenantSettings
	err      error
	lastReq  *models.UpdateTenantSettingsRequest
}

func (m *mockTenantService) ListTenants(ctx context.Context) ([]models.Tenant, error) {
	return []models.Tenant{}, nil
}

func (m *mockTenantService) GetTenantSettings(ctx context.Context, tenantID uuid.UUID) (*models.TenantSettings, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.settings == nil {
		return &models.TenantSettings{DataRetentionDays: 0}, nil
	}
	return m.settings, nil
}

func (m *mockTenantService) UpdateTenantSettings(ctx context.Context, tenantID uuid.UUID, req *models.UpdateTenantSettingsRequest) (*models.TenantSettings, error) {
	m.lastReq = req
	if m.err != nil {
		return nil, m.err
	}
	if req != nil && req.DataRetentionDays != nil {
		return &models.TenantSettings{DataRetentionDays: *req.DataRetentionDays}, nil
	}
	if req != nil && req.DashboardGroupTags != nil {
		return &models.TenantSettings{DashboardGroupTags: *req.DashboardGroupTags}, nil
	}
	if m.settings == nil {
		return &models.TenantSettings{DataRetentionDays: 0}, nil
	}
	return m.settings, nil
}

func TestHandlers_GetTenantSettings(t *testing.T) {
	tenantID := uuid.New().String()
	mockSvc := &mockTenantService{settings: &models.TenantSettings{DataRetentionDays: 0}}
	h := &Handlers{service: mockSvc, logger: logger.New("test", "debug")}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tenant-settings", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID))
	w := httptest.NewRecorder()

	h.GetTenantSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var got models.TenantSettings
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.DataRetentionDays != 0 {
		t.Fatalf("data_retention_days = %d, want 0", got.DataRetentionDays)
	}
}

func TestHandlers_UpdateTenantSettings_AllowsUnlimitedZero(t *testing.T) {
	tenantID := uuid.New().String()
	mockSvc := &mockTenantService{}
	h := &Handlers{service: mockSvc, logger: logger.New("test", "debug")}

	body := []byte(`{"data_retention_days":0}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tenant-settings", bytes.NewReader(body))
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID))
	w := httptest.NewRecorder()

	h.UpdateTenantSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if mockSvc.lastReq == nil || mockSvc.lastReq.DataRetentionDays == nil {
		t.Fatal("expected service to receive data_retention_days")
	}
	if *mockSvc.lastReq.DataRetentionDays != 0 {
		t.Fatalf("data_retention_days = %d, want 0", *mockSvc.lastReq.DataRetentionDays)
	}
}

func TestHandlers_UpdateTenantSettings_RejectsInvalidRange(t *testing.T) {
	tenantID := uuid.New().String()
	mockSvc := &mockTenantService{}
	h := &Handlers{service: mockSvc, logger: logger.New("test", "debug")}

	body := []byte(`{"data_retention_days":29}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tenant-settings", bytes.NewReader(body))
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID))
	w := httptest.NewRecorder()

	h.UpdateTenantSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
	if mockSvc.lastReq != nil {
		t.Fatal("service should not be called for invalid retention days")
	}
}

func TestHandlers_UpdateTenantSettings_DashboardGroupTags_HappyPath(t *testing.T) {
	tenantID := uuid.New().String()
	mockSvc := &mockTenantService{}
	h := &Handlers{service: mockSvc, logger: logger.New("test", "debug")}

	body := []byte(`{"dashboard_group_tags":["api","payments"]}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tenant-settings", bytes.NewReader(body))
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID))
	w := httptest.NewRecorder()

	h.UpdateTenantSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if mockSvc.lastReq == nil || mockSvc.lastReq.DashboardGroupTags == nil {
		t.Fatal("expected service to receive dashboard_group_tags")
	}
	got := *mockSvc.lastReq.DashboardGroupTags
	if len(got) != 2 || got[0] != "api" || got[1] != "payments" {
		t.Fatalf("dashboard_group_tags = %v, want [api payments]", got)
	}

	var resp models.TenantSettings
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.DashboardGroupTags) != 2 || resp.DashboardGroupTags[0] != "api" || resp.DashboardGroupTags[1] != "payments" {
		t.Fatalf("response dashboard_group_tags = %v, want [api payments]", resp.DashboardGroupTags)
	}
}

func TestHandlers_UpdateTenantSettings_DashboardGroupTags_RejectsDuplicates(t *testing.T) {
	tenantID := uuid.New().String()
	mockSvc := &mockTenantService{}
	h := &Handlers{service: mockSvc, logger: logger.New("test", "debug")}

	body := []byte(`{"dashboard_group_tags":["api","api"]}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tenant-settings", bytes.NewReader(body))
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID))
	w := httptest.NewRecorder()

	h.UpdateTenantSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
	if mockSvc.lastReq != nil {
		t.Fatal("service should not be called for invalid dashboard_group_tags")
	}
}

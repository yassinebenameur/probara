package monitors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// fakeBulkSvc embeds MockMonitorService (satisfying the full MonitorService interface)
// and overrides BulkUpdateAlertPolicy so tests can inspect captured arguments.
type fakeBulkSvc struct {
	MockMonitorService
	bulkResp  *models.BulkUpdateAlertPolicyResponse
	bulkErr   error
	gotOp     models.BulkAlertPolicyOp
	gotIDs    []uuid.UUID
	gotPolicy uuid.UUID
}

func (f *fakeBulkSvc) BulkUpdateAlertPolicy(
	ctx context.Context,
	tenantID uuid.UUID,
	monitorIDs []uuid.UUID,
	policyID uuid.UUID,
	op models.BulkAlertPolicyOp,
) (*models.BulkUpdateAlertPolicyResponse, error) {
	f.gotOp = op
	f.gotIDs = monitorIDs
	f.gotPolicy = policyID
	return f.bulkResp, f.bulkErr
}

func (f *fakeBulkSvc) BulkUpdateAlerting(
	ctx context.Context,
	tenantID uuid.UUID,
	monitorIDs []uuid.UUID,
	threshold *int,
	mode *string,
	channels []models.MonitorChannelAssignment,
) (int, error) {
	return len(monitorIDs), nil
}

func tenantCtx(t *testing.T) context.Context {
	t.Helper()
	tenantID := uuid.New()
	return ctxpkg.WithTenantID(context.Background(), tenantID.String())
}

func TestBulkUpdateAlertPolicy_HappyAttach(t *testing.T) {
	ctx := tenantCtx(t)
	m1, m2 := uuid.New(), uuid.New()
	policy := uuid.New()

	svc := &fakeBulkSvc{
		MockMonitorService: MockMonitorService{monitors: make(map[uuid.UUID]*models.Monitor)},
		bulkResp: &models.BulkUpdateAlertPolicyResponse{
			Updated:           2,
			Unchanged:         0,
			MonitorIDsUpdated: []uuid.UUID{m1, m2},
		},
	}
	h := &Handlers{service: svc}

	body, _ := json.Marshal(models.BulkUpdateAlertPolicyRequest{
		MonitorIDs: []string{m1.String(), m2.String()},
		PolicyID:   policy.String(),
		Op:         models.BulkAlertPolicyOpAttach,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/bulk/alert-policy", bytes.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()

	h.BulkUpdateAlertPolicy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp models.BulkUpdateAlertPolicyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Updated != 2 {
		t.Errorf("expected updated=2, got %d", resp.Updated)
	}
	if svc.gotOp != models.BulkAlertPolicyOpAttach {
		t.Errorf("expected attach op, got %v", svc.gotOp)
	}
}

func TestBulkUpdateAlertPolicy_EmptyMonitorIDs(t *testing.T) {
	ctx := tenantCtx(t)
	svc := &fakeBulkSvc{MockMonitorService: MockMonitorService{monitors: make(map[uuid.UUID]*models.Monitor)}}
	h := &Handlers{service: svc}

	body, _ := json.Marshal(models.BulkUpdateAlertPolicyRequest{
		MonitorIDs: []string{},
		PolicyID:   uuid.New().String(),
		Op:         models.BulkAlertPolicyOpAttach,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/bulk/alert-policy", bytes.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()

	h.BulkUpdateAlertPolicy(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBulkUpdateAlertPolicy_UnknownOp(t *testing.T) {
	ctx := tenantCtx(t)
	svc := &fakeBulkSvc{MockMonitorService: MockMonitorService{monitors: make(map[uuid.UUID]*models.Monitor)}}
	h := &Handlers{service: svc}

	body := []byte(fmt.Sprintf(`{"monitor_ids":["%s"],"policy_id":"%s","op":"sideways"}`, uuid.New(), uuid.New()))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/bulk/alert-policy", bytes.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()

	h.BulkUpdateAlertPolicy(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestBulkUpdateAlertPolicy_CrossTenantMonitor(t *testing.T) {
	ctx := tenantCtx(t)
	svc := &fakeBulkSvc{
		MockMonitorService: MockMonitorService{monitors: make(map[uuid.UUID]*models.Monitor)},
		bulkErr:            fmt.Errorf("one or more monitors not found or do not belong to tenant"),
	}
	h := &Handlers{service: svc}

	body, _ := json.Marshal(models.BulkUpdateAlertPolicyRequest{
		MonitorIDs: []string{uuid.New().String()},
		PolicyID:   uuid.New().String(),
		Op:         models.BulkAlertPolicyOpAttach,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/bulk/alert-policy", bytes.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()

	h.BulkUpdateAlertPolicy(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBulkUpdateAlertPolicy_HappyDetach(t *testing.T) {
	ctx := tenantCtx(t)
	m1, m2 := uuid.New(), uuid.New()
	policy := uuid.New()

	svc := &fakeBulkSvc{
		MockMonitorService: MockMonitorService{monitors: make(map[uuid.UUID]*models.Monitor)},
		bulkResp: &models.BulkUpdateAlertPolicyResponse{
			Updated:           2,
			Unchanged:         0,
			MonitorIDsUpdated: []uuid.UUID{m1, m2},
		},
	}
	h := &Handlers{service: svc}

	body, _ := json.Marshal(models.BulkUpdateAlertPolicyRequest{
		MonitorIDs: []string{m1.String(), m2.String()},
		PolicyID:   policy.String(),
		Op:         models.BulkAlertPolicyOpDetach,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/bulk/alert-policy", bytes.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()

	h.BulkUpdateAlertPolicy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp models.BulkUpdateAlertPolicyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Updated != 2 {
		t.Errorf("expected updated=2, got %d", resp.Updated)
	}
	if svc.gotOp != models.BulkAlertPolicyOpDetach {
		t.Errorf("expected detach op, got %v", svc.gotOp)
	}
}

func TestBulkUpdateAlertPolicy_PolicyNotFound(t *testing.T) {
	ctx := tenantCtx(t)
	svc := &fakeBulkSvc{
		MockMonitorService: MockMonitorService{monitors: make(map[uuid.UUID]*models.Monitor)},
		bulkErr:            fmt.Errorf("alert policy not found or does not belong to tenant"),
	}
	h := &Handlers{service: svc}

	body, _ := json.Marshal(models.BulkUpdateAlertPolicyRequest{
		MonitorIDs: []string{uuid.New().String()},
		PolicyID:   uuid.New().String(),
		Op:         models.BulkAlertPolicyOpAttach,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/bulk/alert-policy", bytes.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()

	h.BulkUpdateAlertPolicy(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

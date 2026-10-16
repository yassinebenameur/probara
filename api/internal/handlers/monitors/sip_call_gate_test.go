package monitors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

const handlerCallConfig = `{"host":"trunk.example.com","method":"invite","call":{"number":"+33123456789"}}`

// sipCallCallers maps each identity to whether it may place a test call.
func sipCallCallers(tenantID string) map[string]struct {
	ctx     context.Context
	allowed bool
} {
	bg := ctxpkg.WithTenantID(context.Background(), tenantID)
	user := ctxpkg.WithAdminID(bg, uuid.NewString())
	return map[string]struct {
		ctx     context.Context
		allowed bool
	}{
		"tenant admin":  {ctxpkg.WithRole(user, "admin"), true},
		"superadmin":    {ctxpkg.WithPlatformRole(user, "superadmin"), true},
		"tenant editor": {ctxpkg.WithRole(user, "editor"), false},
		"write API key": {ctxpkg.WithAuthScope(ctxpkg.WithAPIKeyID(bg, "k"), "write"), false},
	}
}

// Testing a test-call config places a real call, so only a tenant admin may.
func TestTestMonitorConfig_SIPCallRequiresTenantAdmin(t *testing.T) {
	for name, caller := range sipCallCallers(uuid.NewString()) {
		t.Run(name, func(t *testing.T) {
			h := NewHandlers(NewMockMonitorService(), nil, nil, logger.New("test", "error"), t.TempDir())
			queue := &testCheckRequester{}
			h.jobRequester = queue
			body := `{"type":"sip","timeout_seconds":10,"config":` + handlerCallConfig + `}`
			w := httptest.NewRecorder()
			h.TestMonitorConfig(w, httptest.NewRequest(http.MethodPost, "/api/v1/monitors/test", strings.NewReader(body)).WithContext(caller.ctx))
			if caller.allowed {
				if w.Code != http.StatusOK || queue.calls != 1 {
					t.Fatalf("status %d, worker calls %d; want 200 and one call: %s", w.Code, queue.calls, w.Body.String())
				}
				return
			}
			if w.Code != http.StatusForbidden || queue.calls != 0 {
				t.Fatalf("status %d, worker calls %d; want 403 and no call: %s", w.Code, queue.calls, w.Body.String())
			}
		})
	}

	// An OPTIONS probe is still testable by an editor.
	h := NewHandlers(NewMockMonitorService(), nil, nil, logger.New("test", "error"), t.TempDir())
	h.jobRequester = &testCheckRequester{}
	ctx := sipCallCallers(uuid.NewString())["tenant editor"].ctx
	w := httptest.NewRecorder()
	h.TestMonitorConfig(w, httptest.NewRequest(http.MethodPost, "/api/v1/monitors/test",
		strings.NewReader(`{"type":"sip","config":{"host":"trunk.example.com"}}`)).WithContext(ctx))
	if w.Code != http.StatusOK {
		t.Fatalf("options test by editor: status %d, want 200: %s", w.Code, w.Body.String())
	}
}

// Run-now of a test-call monitor places a call from every selected location.
func TestRunMonitorNow_SIPCallRequiresTenantAdmin(t *testing.T) {
	tenantID := uuid.New()
	for name, caller := range sipCallCallers(tenantID.String()) {
		t.Run(name, func(t *testing.T) {
			monitorSvc := NewMockMonitorService()
			h := NewHandlers(monitorSvc, &MockGroupService{}, &MockResultsService{}, logger.New("test", "error"), t.TempDir())
			publisher := &MockCheckJobPublisher{}
			h.ConfigureCheckJobs(publisher, "check.jobs")
			monitorID := uuid.New()
			monitorSvc.monitors[monitorID] = &models.Monitor{
				ID: monitorID, TenantID: tenantID, Name: "call", Type: models.MonitorTypeSIP,
				Config: json.RawMessage(handlerCallConfig), IntervalSeconds: 60, TimeoutSeconds: 15, Enabled: true,
			}
			r := chi.NewRouter()
			r.Post("/{id}/run", h.RunMonitorNow)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/"+monitorID.String()+"/run", nil).WithContext(caller.ctx))
			if caller.allowed {
				if w.Code != http.StatusAccepted || publisher.job == nil {
					t.Fatalf("status %d, published %v; want 202 and a job: %s", w.Code, publisher.job != nil, w.Body.String())
				}
				return
			}
			if w.Code != http.StatusForbidden || publisher.job != nil {
				t.Fatalf("status %d, published %v; want 403 and no job: %s", w.Code, publisher.job != nil, w.Body.String())
			}
		})
	}
}

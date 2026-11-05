package monitors

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// conflictingMonitorService fails every write the way the real service does
// when the merged config pairs a kept secret with something the request added.
type conflictingMonitorService struct {
	*MockMonitorService
}

func (conflictingMonitorService) UpdateMonitor(context.Context, uuid.UUID, uuid.UUID, *models.UpdateMonitorRequest) (*models.Monitor, error) {
	return nil, fmt.Errorf("%w: basic auth and an Authorization header cannot both be set", monitorservice.ErrSecretConflict)
}

func TestUpdateMonitor_SecretConflictIs400(t *testing.T) {
	tenantID := uuid.New()
	mock := NewMockMonitorService()
	monitorID := uuid.New()
	mock.monitors[monitorID] = &models.Monitor{ID: monitorID, TenantID: tenantID, Name: "api", Type: models.MonitorTypeHTTP,
		Config: []byte(`{"url":"https://x.test","method":"GET"}`), IntervalSeconds: 60, TimeoutSeconds: 10, CreatedAt: time.Now()}
	h := NewHandlers(conflictingMonitorService{mock}, nil, nil, logger.New("test", "error"), t.TempDir())

	body := `{"config":{"url":"https://x.test","method":"GET","basic_auth_username":"ops","basic_auth_password":"pw"}}`
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", monitorID.String())
	ctx := context.WithValue(ctxpkg.WithTenantID(context.Background(), tenantID.String()), chi.RouteCtxKey, routeCtx)
	w := httptest.NewRecorder()
	h.UpdateMonitor(w, httptest.NewRequest(http.MethodPut, "/api/v1/monitors/"+monitorID.String(), strings.NewReader(body)).WithContext(ctx))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Authorization header") {
		t.Fatalf("status %d body %s, want 400 naming the conflict", w.Code, w.Body.String())
	}
}

package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	svc "github.com/yassinebenameur/probara/api/internal/services/discovery"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	contract "github.com/yassinebenameur/probara/shared/discovery"
)

func TestReadOnlyCannotMutateDiscovery(t *testing.T) {
	h := &Handlers{}
	r := chi.NewRouter()
	r.Use(middleware.RequireWrite)
	r.Route("/api/v1/discovery", h.Routes)
	for _, ctx := range []context.Context{ctxpkg.WithRole(context.Background(), "viewer"), ctxpkg.WithAuthScope(context.Background(), "read")} {
		for _, route := range []struct{ method, path string }{{"POST", "/integrations"}, {"POST", "/integrations/id/rotate-token"}, {"DELETE", "/integrations/id"}, {"POST", "/services/id/approve"}, {"POST", "/services/id/links"}, {"DELETE", "/services/id/links/monitor"}} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(route.method, "/api/v1/discovery"+route.path, nil).WithContext(ctx))
			require.Equal(t, http.StatusForbidden, w.Code, route.path)
		}
	}
}
func TestCollectorRejectsMissingAndMalformedCredentialsBeforeDBAccess(t *testing.T) {
	h := &Handlers{}
	r := chi.NewRouter()
	r.Route("/api/v1/discovery-collectors", h.CollectorRoutes)
	for _, token := range []string{"", "Bearer short", "Basic secret"} {
		req := httptest.NewRequest("POST", "/api/v1/discovery-collectors/c952c53e-d8c8-4e16-8e94-a9ec57690c11/snapshot", strings.NewReader(`{}`))
		req.Header.Set("Authorization", token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 401, w.Code)
	}
}
func TestDecodeRequiresOneBoundedStrictObject(t *testing.T) {
	for _, body := range []string{`{"extra":1}`, `{} {}`, strings.Repeat(" ", 4<<20) + `{}`} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		require.False(t, decode(w, req, &struct{}{}))
		require.Equal(t, 400, w.Code)
	}
}

// fakeBackend stands in for *svc.Service: it records what reached it and
// returns canned results.
type fakeBackend struct {
	approveCreated bool
	authorizeErr   error
	snapshotErr    error
	snapshotToken  string
	snapshot       *contract.Snapshot
}

func (f *fakeBackend) List(context.Context, uuid.UUID) ([]svc.Integration, error) { return nil, nil }
func (f *fakeBackend) Create(_ context.Context, _ uuid.UUID, req svc.CreateRequest) (*svc.Integration, error) {
	return &svc.Integration{ID: integrationID, Name: req.Name}, nil
}
func (f *fakeBackend) Rotate(context.Context, uuid.UUID, uuid.UUID) (string, error) { return "t", nil }
func (f *fakeBackend) Delete(context.Context, uuid.UUID, uuid.UUID) error           { return nil }
func (f *fakeBackend) Overview(context.Context, uuid.UUID, uuid.UUID, svc.OverviewQuery) (svc.Overview, error) {
	return svc.Overview{}, nil
}
func (f *fakeBackend) Approve(context.Context, uuid.UUID, uuid.UUID, svc.ApproveRequest) (uuid.UUID, bool, error) {
	return monitorID, f.approveCreated, nil
}
func (f *fakeBackend) Link(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, *string) error {
	return nil
}
func (f *fakeBackend) Unlink(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeBackend) CollectorConfig(context.Context, uuid.UUID, string) (contract.Config, error) {
	return contract.Config{}, nil
}
func (f *fakeBackend) AuthorizeSnapshot(context.Context, uuid.UUID, string) error {
	return f.authorizeErr
}
func (f *fakeBackend) Snapshot(_ context.Context, _ uuid.UUID, token string, s contract.Snapshot) error {
	f.snapshotToken, f.snapshot = token, &s
	return f.snapshotErr
}

type fakeAudit struct{ events []audit.Event }

func (a *fakeAudit) Record(e audit.Event) { a.events = append(a.events, e) }

var (
	tenantID      = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	integrationID = uuid.MustParse("c952c53e-d8c8-4e16-8e94-a9ec57690c11")
	serviceID     = uuid.MustParse("3f2a6c1e-0000-0000-0000-000000000001")
	monitorID     = uuid.MustParse("3f2a6c1e-0000-0000-0000-000000000002")
	collectorTok  = strings.Repeat("a", 43)
)

func operatorRequest(t *testing.T, h *Handlers, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/api/v1/discovery", h.Routes)
	req := httptest.NewRequest(method, "/api/v1/discovery"+path, strings.NewReader(body))
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenantID.String()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestOperatorWritesRecordAuditEvents(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
		status             int
		action, resType    string
		resID              uuid.UUID
	}{
		{"POST", "/integrations", `{"name":"prod","namespaces":["prod"]}`, 201, "discovery-integration.create", "discovery-integration", integrationID},
		{"DELETE", "/integrations/" + integrationID.String(), "", 204, "discovery-integration.delete", "discovery-integration", integrationID},
		{"POST", "/integrations/" + integrationID.String() + "/rotate-token", "", 200, "discovery-integration.rotate-token", "discovery-integration", integrationID},
		{"POST", "/services/" + serviceID.String() + "/approve", `{"key":"k","name":"Web","interval_seconds":60}`, 200, "discovery.approve", "monitor", monitorID},
		{"POST", "/services/" + serviceID.String() + "/links", `{"monitor_id":"` + monitorID.String() + `"}`, 204, "discovery-link.create", "discovery-service", serviceID},
		{"DELETE", "/services/" + serviceID.String() + "/links/" + monitorID.String(), "", 204, "discovery-link.delete", "discovery-service", serviceID},
	} {
		rec := &fakeAudit{}
		h := &Handlers{Service: &fakeBackend{approveCreated: true}, Audit: rec}
		w := operatorRequest(t, h, tc.method, tc.path, tc.body)
		require.Equal(t, tc.status, w.Code, tc.path)
		require.Len(t, rec.events, 1, tc.path)
		e := rec.events[0]
		require.Equal(t, tc.action, e.Action)
		require.Equal(t, tc.resType, e.ResourceType)
		require.Equal(t, tc.resID.String(), e.ResourceID)
		require.Equal(t, tc.status, e.StatusCode)
		require.Equal(t, audit.OutcomeSuccess, e.Outcome)
		require.Equal(t, &tenantID, e.TenantID)
	}
}

func TestApproveReplayIsNotAudited(t *testing.T) {
	rec := &fakeAudit{}
	h := &Handlers{Service: &fakeBackend{approveCreated: false}, Audit: rec}
	w := operatorRequest(t, h, "POST", "/services/"+serviceID.String()+"/approve", `{"key":"k","name":"Web","interval_seconds":60}`)
	require.Equal(t, 200, w.Code)
	require.JSONEq(t, `{"monitor_id":"`+monitorID.String()+`","created":false}`, w.Body.String())
	require.Empty(t, rec.events, "returning an already-linked monitor writes nothing")
}

func collectorSnapshot(h *Handlers, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Route("/api/v1/discovery-collectors", h.CollectorRoutes)
	req := httptest.NewRequest("POST", "/api/v1/discovery-collectors/"+integrationID.String()+"/snapshot", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+collectorTok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestValidCollectorTokenReachesSnapshot(t *testing.T) {
	backend := &fakeBackend{}
	// An unknown field (a newer collector) is tolerated on this route.
	w := collectorSnapshot(&Handlers{Service: backend}, `{"revision":3,"namespaces":["prod"],"resources":[],"collector_version":"9.9"}`)
	require.Equal(t, 204, w.Code, w.Body.String())
	require.Equal(t, collectorTok, backend.snapshotToken)
	require.NotNil(t, backend.snapshot)
	require.Equal(t, int64(3), backend.snapshot.Revision)
	require.Equal(t, []string{"prod"}, backend.snapshot.Namespaces)
}

func TestSnapshotRefusalsMapToStatus(t *testing.T) {
	backend := &fakeBackend{authorizeErr: svc.RateLimited{RetryAfter: 1500 * time.Millisecond}}
	w := collectorSnapshot(&Handlers{Service: backend}, `{}`)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "2", w.Header().Get("Retry-After"))
	require.Nil(t, backend.snapshot, "a rate-limited request is refused before its body is read")

	backend = &fakeBackend{snapshotErr: svc.InventoryFull{Stored: 10001, Limit: 10000}}
	w = collectorSnapshot(&Handlers{Service: backend}, `{"revision":0,"namespaces":["prod"],"resources":[]}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Contains(t, w.Body.String(), "inventory_limit")
	require.Contains(t, w.Body.String(), "10000")

	backend = &fakeBackend{authorizeErr: svc.ErrNotFound}
	w = collectorSnapshot(&Handlers{Service: backend}, `{}`)
	require.Equal(t, http.StatusNotFound, w.Code)
}

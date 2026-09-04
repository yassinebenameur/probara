package monitors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	testcontainers "github.com/testcontainers/testcontainers-go"
	importhandlers "github.com/yassinebenameur/probara/api/internal/handlers/import"
	locationhandlers "github.com/yassinebenameur/probara/api/internal/handlers/locations"
	pushhandlers "github.com/yassinebenameur/probara/api/internal/handlers/push"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/models"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	importservice "github.com/yassinebenameur/probara/api/internal/services/import"
	locationservice "github.com/yassinebenameur/probara/api/internal/services/locations"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	pushservice "github.com/yassinebenameur/probara/api/internal/services/push"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestCredentialBoundaries_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenant := testutil.InsertTenant(ctx, t, db, "credential-boundaries")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "saved-redis")
	pushID := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "push-monitor")
	if _, err := db.ExecContext(ctx, `UPDATE monitors SET type='redis', config='{"host":"redis.internal","password":"test-saved-password"}' WHERE id=$1`, monitorID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE monitors SET type='push', config='{"push_token":"legacy-config-credential"}', push_token='integration-push-token' WHERE id=$1`, pushID); err != nil {
		t.Fatal(err)
	}
	log := logger.New("test", "error")
	h := NewHandlers(monitorservice.NewService(monitorservice.NewPostgresRepository(db)), groupservice.NewService(db), nil, log, t.TempDir())
	queue := &testCheckRequester{}
	h.jobRequester = queue
	locationSvc := locationservice.NewService(db)
	location, err := locationSvc.Create(ctx, tenant, &models.CreateLocationRequest{Name: "integration-location"})
	if err != nil {
		t.Fatal(err)
	}
	locationHandler := locationhandlers.NewHandlers(locationSvc, "tls://nats.example:4222", log)
	pushHandler := pushhandlers.NewHandler(pushservice.NewService(db.DB, nil), log, "https://api.example")
	r := chi.NewRouter()
	r.Use(middleware.RequireWrite)
	r.Get("/api/v1/monitors", h.ListMonitors)
	r.Get("/api/v1/monitors/export", importhandlers.NewHandlers(importservice.NewService(db, h.service, groupservice.NewService(db)), log).Export)
	r.Get("/api/v1/monitors/{id}", h.GetMonitor)
	r.Post("/api/v1/monitors/test", h.TestMonitorConfig)
	r.Get("/api/v1/monitors/{id}/push/info", pushHandler.HandleGetPushInfo)
	r.Get("/api/v1/locations/{id}/deploy", locationHandler.GetDeployInfo)
	for _, identity := range []string{"viewer", "read-key", "editor", "superadmin"} {
		t.Run(identity, func(t *testing.T) {
			requestCtx := ctxpkg.WithTenantID(ctx, tenant.String())
			switch identity {
			case "read-key":
				requestCtx = ctxpkg.WithAuthScope(requestCtx, "read")
			case "superadmin":
				requestCtx = ctxpkg.WithPlatformRole(requestCtx, "superadmin")
			default:
				requestCtx = ctxpkg.WithRole(requestCtx, identity)
			}
			canWrite := identity == "editor" || identity == "superadmin"
			exported := httptest.NewRecorder()
			r.ServeHTTP(exported, httptest.NewRequest("GET", "/api/v1/monitors/export", nil).WithContext(requestCtx))
			if exported.Code != http.StatusOK {
				t.Fatalf("export status %d: %s", exported.Code, exported.Body.String())
			}
			for _, secret := range []string{"legacy-config-credential", "integration-push-token", "test-saved-password"} {
				if strings.Contains(exported.Body.String(), secret) {
					t.Fatal("export exposed monitor credential")
				}
			}
			for _, path := range []string{"/api/v1/monitors", "/api/v1/monitors/" + pushID.String()} {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("GET", path, nil).WithContext(requestCtx))
				if w.Code != http.StatusOK {
					t.Fatalf("%s: status %d: %s", path, w.Code, w.Body.String())
				}
				if strings.Contains(w.Body.String(), "integration-push-token") != canWrite {
					t.Fatal("incorrect push token visibility")
				}
				if strings.Contains(w.Body.String(), "test-saved-password") {
					t.Fatal("saved password exposed by monitor read")
				}
				if strings.Contains(w.Body.String(), "legacy-config-credential") {
					t.Fatal("config exposed legacy push token")
				}
			}
			for _, path := range []string{"/api/v1/monitors/" + pushID.String() + "/push/info", "/api/v1/locations/" + location.ID.String() + "/deploy"} {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("GET", path, nil).WithContext(requestCtx))
				want := http.StatusForbidden
				if canWrite {
					want = http.StatusOK
				}
				if w.Code != want {
					t.Fatalf("%s: status %d, want %d: %s", path, w.Code, want, w.Body.String())
				}
			}
			body := `{"type":"redis","monitor_id":"` + monitorID.String() + `","config":{"host":"attacker.example","password":"***"}}`
			before := queue.calls
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/monitors/test", strings.NewReader(body)).WithContext(requestCtx))
			want := http.StatusForbidden
			if canWrite {
				want = http.StatusOK
			}
			if w.Code != want {
				t.Fatalf("saved secret test: status %d, want %d: %s", w.Code, want, w.Body.String())
			}
			if !canWrite && queue.calls != before {
				t.Fatal("read-only saved secret test reached worker")
			}
		})
	}
}

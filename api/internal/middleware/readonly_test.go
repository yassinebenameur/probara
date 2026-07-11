package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// identity constructors matching what AuthMiddleware injects per credential.
func superadminCtx() context.Context {
	ctx := ctxpkg.WithAdminID(context.Background(), "00000000-0000-0000-0000-00000000000a")
	ctx = ctxpkg.WithPlatformRole(ctx, "superadmin")
	return ctxpkg.WithRole(ctx, "admin")
}

func memberCtx(role string) context.Context {
	ctx := ctxpkg.WithAdminID(context.Background(), "00000000-0000-0000-0000-00000000000b")
	ctx = ctxpkg.WithPlatformRole(ctx, "member")
	return ctxpkg.WithRole(ctx, role)
}

func apiKeyCtx(scope string) context.Context {
	ctx := ctxpkg.WithTenantID(context.Background(), "00000000-0000-0000-0000-000000000001")
	ctx = ctxpkg.WithActorType(ctx, ctxpkg.ActorTypeAPIKey)
	return ctxpkg.WithAuthScope(ctx, scope)
}

func TestRequireWriteMatrix(t *testing.T) {
	cases := []struct {
		name   string
		ctx    context.Context
		method string
		path   string
		want   int
	}{
		{"superadmin GET", superadminCtx(), http.MethodGet, "/api/v1/monitors", http.StatusOK},
		{"superadmin POST", superadminCtx(), http.MethodPost, "/api/v1/monitors", http.StatusOK},
		{"superadmin DELETE", superadminCtx(), http.MethodDelete, "/api/v1/monitors/x", http.StatusOK},

		{"member admin POST", memberCtx("admin"), http.MethodPost, "/api/v1/monitors", http.StatusOK},
		{"member editor POST", memberCtx("editor"), http.MethodPost, "/api/v1/monitors", http.StatusOK},
		{"member editor DELETE", memberCtx("editor"), http.MethodDelete, "/api/v1/monitors/x", http.StatusOK},

		{"viewer GET", memberCtx("viewer"), http.MethodGet, "/api/v1/monitors", http.StatusOK},
		{"viewer POST", memberCtx("viewer"), http.MethodPost, "/api/v1/monitors", http.StatusForbidden},
		{"viewer PATCH", memberCtx("viewer"), http.MethodPatch, "/api/v1/monitors/x", http.StatusForbidden},
		{"viewer DELETE", memberCtx("viewer"), http.MethodDelete, "/api/v1/monitors/x", http.StatusForbidden},
		{"viewer POST test allowlisted", memberCtx("viewer"), http.MethodPost, "/api/v1/monitors/test", http.StatusOK},
		{"viewer POST import preview allowlisted", memberCtx("viewer"), http.MethodPost, "/api/v1/monitors/import/preview", http.StatusOK},
		{"viewer POST mesh probe allowlisted", memberCtx("viewer"), http.MethodPost, "/api/v1/mesh/probe", http.StatusOK},
		{"viewer POST import NOT allowlisted", memberCtx("viewer"), http.MethodPost, "/api/v1/monitors/import", http.StatusForbidden},
		{"viewer POST run NOT allowlisted", memberCtx("viewer"), http.MethodPost, "/api/v1/monitors/x/run", http.StatusForbidden},
		{"viewer POST alert-channel test NOT allowlisted", memberCtx("viewer"), http.MethodPost, "/api/v1/alert-channels/x/test", http.StatusForbidden},

		{"write key POST", apiKeyCtx("write"), http.MethodPost, "/api/v1/monitors", http.StatusOK},
		{"read key GET", apiKeyCtx("read"), http.MethodGet, "/api/v1/monitors", http.StatusOK},
		{"read key POST", apiKeyCtx("read"), http.MethodPost, "/api/v1/monitors", http.StatusForbidden},
		{"read key POST test allowlisted", apiKeyCtx("read"), http.MethodPost, "/api/v1/monitors/test", http.StatusOK},
		{"read key DELETE", apiKeyCtx("read"), http.MethodDelete, "/api/v1/monitors/x", http.StatusForbidden},
	}

	handler := RequireWrite(okHandler())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil).WithContext(tc.ctx)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("%s %s: expected %d, got %d", tc.method, tc.path, tc.want, w.Code)
			}
		})
	}
}

func TestRequireTenantAdmin(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want int
	}{
		{"superadmin", superadminCtx(), http.StatusOK},
		{"member admin", memberCtx("admin"), http.StatusOK},
		{"member editor", memberCtx("editor"), http.StatusForbidden},
		{"member viewer", memberCtx("viewer"), http.StatusForbidden},
		{"api key write", apiKeyCtx("write"), http.StatusForbidden},
	}

	handler := RequireTenantAdmin(okHandler())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/api-keys", nil).WithContext(tc.ctx)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, w.Code)
			}
		})
	}
}

// TestPostReadAllowlistDrift catches new compute-only POST endpoints that were
// added to the router but not to postReadPaths: any POST route whose final
// segment matches a "read-ish" verb must either be in the allowlist or be
// consciously excluded below.
func TestPostReadAllowlistDrift(t *testing.T) {
	// Endpoints that look read-only by name but intentionally require write
	// (they mutate state or have external side effects).
	writeGatedExceptions := map[string]struct{}{
		"/api/v1/alert-channels/{id}/test": {}, // sends a real notification
	}

	readVerbs := map[string]struct{}{"test": {}, "preview": {}, "probe": {}, "suggestions": {}, "dependency-suggestions": {}}

	// Mirror of the mutation routes in server.go that end in a read-ish verb.
	// Walking the real router would need the full Server dependency graph;
	// keep this list in sync when adding such routes.
	postRoutes := []string{
		"/api/v1/ai-settings/test",
		"/api/v1/mesh/probe",
		"/api/v1/monitors/test",
		"/api/v1/monitors/import/preview",
		"/api/v1/monitors/dependency-suggestions",
		"/api/v1/alert-channels/{id}/test",
	}

	for _, route := range postRoutes {
		if _, excluded := writeGatedExceptions[route]; excluded {
			if _, inAllowlist := postReadPaths[route]; inAllowlist {
				t.Errorf("route %s is both write-gated exception and allowlisted", route)
			}
			continue
		}

		last := route[len("/api/v1/"):]
		if idx := lastSlash(last); idx >= 0 {
			last = last[idx+1:]
		}
		if _, ok := readVerbs[last]; !ok {
			continue
		}
		if _, ok := postReadPaths[route]; !ok {
			t.Errorf("POST route %s looks compute-only but is missing from postReadPaths (or add it to writeGatedExceptions)", route)
		}
	}
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

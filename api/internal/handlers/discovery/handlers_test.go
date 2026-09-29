package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
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

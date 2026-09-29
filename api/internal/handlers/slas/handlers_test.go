package slas

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/middleware"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

func TestReadOnlyCannotMutateSLAs(t *testing.T) {
	h := &Handlers{}
	r := chi.NewRouter()
	r.Use(middleware.RequireWrite)
	r.Route("/api/v1/slas", h.Routes)
	id := "c952c53e-d8c8-4e16-8e94-a9ec57690c11"
	for _, ctx := range []context.Context{ctxpkg.WithRole(context.Background(), "viewer"), ctxpkg.WithAuthScope(context.Background(), "read")} {
		for _, route := range []struct{ method, path string }{
			{"POST", ""}, {"PATCH", "/" + id}, {"DELETE", "/" + id}, {"POST", "/" + id + "/reports"},
		} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(route.method, "/api/v1/slas"+route.path, nil).WithContext(ctx))
			require.Equal(t, http.StatusForbidden, w.Code, route.method+" "+route.path)
		}
	}
}

func TestReportFormatValidatedBeforeServiceAccess(t *testing.T) {
	h := &Handlers{}
	r := chi.NewRouter()
	r.Route("/api/v1/slas", h.Routes)
	req := httptest.NewRequest("GET", "/api/v1/slas/c952c53e-d8c8-4e16-8e94-a9ec57690c11/report?format=xlsx", nil)
	req = req.WithContext(ctxpkg.WithTenantID(req.Context(), "00000000-0000-0000-0000-000000000001"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

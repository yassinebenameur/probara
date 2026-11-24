package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// The delivery routes are full paths on the parent router, beside the
// /alerts and /alert-channels subrouters: the delivery path must reach the
// delivery handler, and every other path must still reach its subrouter.
func TestDeliveryRoutesBesideSubrouters(t *testing.T) {
	sqlDB, err := sql.Open("postgres", "postgres://unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	stub := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(name)) }
	}
	r := chi.NewRouter()
	r.Route("/alerts", func(r chi.Router) {
		r.Get("/{id}", stub("alert"))
		r.Post("/{id}/acknowledge", stub("acknowledge"))
	})
	r.Route("/alert-channels", func(r chi.Router) {
		r.Get("/{id}", stub("channel"))
		r.Post("/{id}/test", stub("test"))
	})
	mountDeliveryRoutes(r, sqlDB, logger.New("api-test", "error"))

	id := uuid.NewString()
	for _, tc := range []struct{ method, path, want string }{
		{http.MethodGet, "/alerts/" + id, "alert"},
		{http.MethodPost, "/alerts/" + id + "/acknowledge", "acknowledge"},
		{http.MethodGet, "/alert-channels/" + id, "channel"},
		{http.MethodPost, "/alert-channels/" + id + "/test", "test"},
		// The delivery handler answers (500: the database is unreachable).
		{http.MethodGet, "/alerts/" + id + "/deliveries", "failed to list deliveries"},
		{http.MethodGet, "/alert-channels/" + id + "/deliveries", "failed to list deliveries"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req = req.WithContext(ctxpkg.WithTenantID(req.Context(), uuid.NewString()))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s %s -> %d %q, want %q", tc.method, tc.path, rec.Code, rec.Body.String(), tc.want)
		}
	}
}

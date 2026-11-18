package statuspagesubscribers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	svc "github.com/yassinebenameur/probara/api/internal/services/pagesubscribers"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/pageaccess"
)

// Subscriber addresses are personal data: a viewer or a read-scope key gets
// 403 on the list and the export before anything is read (the service here
// has no database, so a call that got through would panic).
func TestAddressesRequireWrite(t *testing.T) {
	h := &Handlers{Service: svc.NewService(nil, svc.Config{}), Log: logger.New("test", "error")}
	r := chi.NewRouter()
	r.Route("/status-pages", h.Routes)
	for _, path := range []string{"/subscribers", "/subscribers/export"} {
		for _, tc := range []struct{ role, scope string }{{"viewer", ""}, {"", "read"}} {
			ctx := ctxpkg.WithTenantID(context.Background(), uuid.NewString())
			if tc.role != "" {
				ctx = ctxpkg.WithRole(ctx, tc.role)
			}
			if tc.scope != "" {
				ctx = ctxpkg.WithAuthScope(ctx, tc.scope)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/status-pages/"+uuid.NewString()+path, nil).WithContext(ctx))
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s as %+v = %d, want 403", path, tc, w.Code)
			}
		}
	}
}

// The subscribe and confirm routes answer only the status-page service;
// anyone calling the API directly gets 404, whatever they send. The
// one-click unsubscribe stays public.
func TestPublicRoutesNeedTheInternalToken(t *testing.T) {
	const token = "internal-token-0123456789abcdef0123456789"
	for _, configured := range []string{"", token} {
		h := &Handlers{Service: svc.NewService(nil, svc.Config{InternalToken: configured}), Log: logger.New("test", "error")}
		r := chi.NewRouter()
		r.Route("/public", h.PublicRoutes)
		for _, path := range []string{"/public/status-pages/acme/subscribe", "/public/status-page-subscriptions/confirm"} {
			for _, presented := range []string{"", "wrong-token", strings.ToUpper(token)} {
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"email":"a@example.com"}`))
				req.Header.Set("Content-Type", "application/json")
				if presented != "" {
					req.Header.Set(pageaccess.ForwardTokenHeader, presented)
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != http.StatusNotFound {
					t.Fatalf("%s with %q (configured %q) = %d, want 404", path, presented, configured, w.Code)
				}
			}
		}
		// No database behind it: an unsigned token is refused before any read.
		req := httptest.NewRequest(http.MethodPost, "/public/status-page-subscriptions/unsubscribe?token=x", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "invalid") {
			t.Fatalf("one-click unsubscribe = %d %s, want the public invalid-link answer", w.Code, w.Body.String())
		}
	}
	if !svc.NewService(nil, svc.Config{InternalToken: token}).ForwardedByStatusPage(token) {
		t.Fatal("the right token was refused")
	}
}

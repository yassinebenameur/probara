package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
)

// newEdgeTestServer builds the real router against an unreachable database.
// Every request below is answered without touching it (health, or 401 for a
// missing credential), so this checks the wiring, not the handlers.
func newEdgeTestServer(t *testing.T, mutate func(*config.APIConfig)) http.Handler {
	t.Helper()
	sqlDB, err := sql.Open("postgres", "postgres://unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	cfg := &config.APIConfig{}
	cfg.ServiceName = "api-test"
	cfg.NATSURL = "nats://127.0.0.1:1"
	mutate(cfg)
	s := NewServer(cfg, logger.New("api-test", "error"), metrics.NewRegistry("api-test"), &db.Client{DB: sqlDB})
	return s.http.Handler
}

func TestAPISecurityHeadersOnEveryResponse(t *testing.T) {
	h := newEdgeTestServer(t, func(*config.APIConfig) {})
	for _, path := range []string{"/healthz", "/api/v1/monitors", "/api/v1/no-such-route"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		for name, want := range map[string]string{
			"X-Content-Type-Options":  "nosniff",
			"Referrer-Policy":         "no-referrer",
			"X-Frame-Options":         "DENY",
			"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
		} {
			if got := rec.Header().Get(name); got != want {
				t.Errorf("%s %s = %q, want %q", path, name, got, want)
			}
		}
	}
}

func TestAPICORSAllowlistWired(t *testing.T) {
	h := newEdgeTestServer(t, func(cfg *config.APIConfig) {
		cfg.AllowedOrigins = []string{"https://ops.example.com"}
	})
	for origin, want := range map[string]string{
		"https://ops.example.com": "https://ops.example.com",
		"https://evil.example":    "",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Errorf("Origin %s: ACAO = %q, want %q", origin, got, want)
		}
	}
}

func edgeGet(h http.Handler, path, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "198.51.100.4:5000"
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAPIRateLimitWired(t *testing.T) {
	h := newEdgeTestServer(t, func(cfg *config.APIConfig) {
		cfg.RateLimitPerMinute = 60
		cfg.RateLimitBurst = 2
	})

	// Authenticated route: failures spend from the address failure bucket
	// (the unreachable database fails every key), then turn into 429.
	codes := []int{}
	for i := 0; i < 3; i++ {
		codes = append(codes, edgeGet(h, "/api/v1/monitors", "Bearer junk").Code)
	}
	if codes[0] != http.StatusUnauthorized || codes[1] != http.StatusUnauthorized || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("auth failures = %v, want [401 401 429]", codes)
	}

	// Unauthenticated route: the address bucket is separate from the
	// failure bucket, and a bogus Authorization header does not skip it.
	codes = codes[:0]
	for i := 0; i < 3; i++ {
		rec := edgeGet(h, "/api/v1/auth/oidc/status", "Bearer junk")
		codes = append(codes, rec.Code)
		if i == 2 && rec.Header().Get("Retry-After") == "" {
			t.Fatal("429 without Retry-After")
		}
	}
	if codes[0] != http.StatusOK || codes[1] != http.StatusOK || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("anonymous route with a bogus header = %v, want [200 200 429]", codes)
	}

	// Health probes are never limited.
	for i := 0; i < 5; i++ {
		if rec := edgeGet(h, "/healthz", ""); rec.Code != http.StatusOK {
			t.Fatalf("healthz %d: %d", i, rec.Code)
		}
	}
}

// TestAuthRouteClassifierMatchesRouter checks the limiter's route split
// against the real router: routes behind AuthMiddleware are charged per
// credential, everything else per address.
func TestAuthRouteClassifierMatchesRouter(t *testing.T) {
	h := newEdgeTestServer(t, func(cfg *config.APIConfig) {
		cfg.RateLimitPerMinute = 60
		cfg.RateLimitBurst = 2
	})
	mux, ok := h.(*chi.Mux)
	if !ok {
		t.Fatalf("handler is %T, want *chi.Mux", h)
	}
	isAuth := authRouteClassifier(mux, apimiddleware.NewRateLimiter(60, 1))
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/api/v1/monitors", true},
		{http.MethodGet, "/api/v1/monitors/7d0c1c55-0000-0000-0000-000000000000", true},
		{http.MethodPost, "/api/v1/otlp/v1/metrics", true},
		{http.MethodGet, "/api/v1/alerts/stream", true},
		{http.MethodGet, "/api/v1/oauth/grants", true},
		{http.MethodPost, "/api/v1/mcp", true},
		{http.MethodPost, "/api/v1/oauth/register", false},
		{http.MethodPost, "/api/v1/oauth/token", false},
		{http.MethodGet, "/api/v1/auth/me", false},
		{http.MethodPost, "/api/v1/auth/refresh", false},
		{http.MethodGet, "/api/v1/users/bootstrap/status", false},
		{http.MethodGet, "/api/v1/push/abc", false},
		{http.MethodGet, "/.well-known/oauth-authorization-server", false},
		{http.MethodGet, "/no/such/route", false},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if got := isAuth(req); got != tc.want {
			t.Errorf("%s %s authenticated = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

// Every route the walk sees behind Authenticate must classify as
// authenticated when requested, whatever its pattern shape (sub-router
// roots, parameters), or its traffic would fall into the address bucket.
func TestAuthRouteClassifierCoversEveryAuthenticatedRoute(t *testing.T) {
	h := newEdgeTestServer(t, func(cfg *config.APIConfig) {
		cfg.RateLimitPerMinute = 60
		cfg.RateLimitBurst = 2
	})
	mux := h.(*chi.Mux)
	limiter := apimiddleware.NewRateLimiter(60, 1)
	isAuth := authRouteClassifier(mux, limiter)
	param := regexp.MustCompile(`\{[^}]*\}`)
	checked := 0
	_ = chi.Walk(mux, func(method, route string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
		authed := false
		for _, mw := range mws {
			authed = authed || limiter.IsAuthenticate(mw)
		}
		if !authed {
			return nil
		}
		path := strings.ReplaceAll(param.ReplaceAllString(normalizeRoutePattern(route), "1"), "*", "x")
		if !isAuth(httptest.NewRequest(method, path, nil)) {
			t.Errorf("%s %s (route %s) is behind auth but classified anonymous", method, path, route)
		}
		checked++
		return nil
	})
	if checked < 100 {
		t.Fatalf("only %d authenticated routes walked; the walk is not seeing the protected group", checked)
	}
}

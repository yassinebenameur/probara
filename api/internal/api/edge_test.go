package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

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

func TestAPIRateLimitWired(t *testing.T) {
	h := newEdgeTestServer(t, func(cfg *config.APIConfig) {
		cfg.RateLimitPerMinute = 60
		cfg.RateLimitBurst = 2
	})
	codes := []int{}
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/monitors", nil)
		req.RemoteAddr = "198.51.100.4:5000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
		if i == 2 && rec.Header().Get("Retry-After") == "" {
			t.Fatal("429 without Retry-After")
		}
	}
	if codes[0] != http.StatusUnauthorized || codes[1] != http.StatusUnauthorized || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v, want [401 401 429]", codes)
	}
	// Health probes are never limited.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "198.51.100.4:5000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("healthz %d: %d", i, rec.Code)
		}
	}
}

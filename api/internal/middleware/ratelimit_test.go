package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/clientip"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestLimiter(perMinute, burst int) (*RateLimiter, *fakeClock) {
	l := NewRateLimiter(perMinute, burst)
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	l.now = clock.now
	return l, clock
}

func statusHandler(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
}

func anonRequest(path, ip string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	return req.WithContext(clientip.WithClientIP(req.Context(), ip))
}

func bearer(path, ip, token string) *http.Request {
	req := anonRequest(path, ip)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func do(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// fakeAuth accepts "Bearer good-<id>" as API key <id> and answers anything
// else 401, like AuthMiddleware.
func fakeAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		id, ok := strings.CutPrefix(token, "good-")
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		next.ServeHTTP(w, r.WithContext(ctxpkg.WithAPIKeyID(r.Context(), id)))
	})
}

// testStack is the API's arrangement: Anonymous router-wide, Authenticate
// around the auth middleware on routes under /api/v1/ except the
// unauthenticated groups.
func testStack(l *RateLimiter) http.Handler {
	isAuth := func(r *http.Request) bool {
		p := r.URL.Path
		for _, anon := range []string{"/api/v1/auth/", "/api/v1/oauth/", "/api/v1/push/", "/api/v1/discovery-collectors/"} {
			if strings.HasPrefix(p, anon) {
				return false
			}
		}
		return strings.HasPrefix(p, "/api/v1/")
	}
	l.SetRouteClassifier(isAuth)
	authed := l.Authenticate(fakeAuth)(statusHandler(http.StatusOK))
	return l.Anonymous(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAuth(r) {
			authed.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func TestRateLimiterDisabled(t *testing.T) {
	var l *RateLimiter = NewRateLimiter(0, 10)
	if l != nil {
		t.Fatal("perMinute=0 must disable the limiter")
	}
	h := l.Anonymous(l.Authenticate(fakeAuth)(statusHandler(http.StatusOK)))
	for i := 0; i < 50; i++ {
		if rec := do(h, bearer("/api/v1/monitors", "198.51.100.1", "good-k")); rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
}

func TestRateLimiterAnonymousKeyedByClientIP(t *testing.T) {
	l, clock := newTestLimiter(60, 2)
	h := testStack(l)

	for i := 0; i < 2; i++ {
		if rec := do(h, anonRequest("/api/v1/auth/refresh", "198.51.100.1")); rec.Code != http.StatusOK {
			t.Fatalf("burst request %d: %d", i, rec.Code)
		}
	}
	rec := do(h, anonRequest("/api/v1/auth/refresh", "198.51.100.1"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over burst: %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1 (one token a second)", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if rec := do(h, anonRequest("/api/v1/auth/refresh", "198.51.100.2")); rec.Code != http.StatusOK {
		t.Fatalf("second address: %d", rec.Code)
	}
	clock.t = clock.t.Add(time.Second)
	if rec := do(h, anonRequest("/api/v1/auth/refresh", "198.51.100.1")); rec.Code != http.StatusOK {
		t.Fatalf("after refill: %d", rec.Code)
	}
}

func TestRateLimiterRetryAfterRoundsUp(t *testing.T) {
	l, _ := newTestLimiter(6, 1) // one token every 10s
	h := testStack(l)
	do(h, anonRequest("/x", "198.51.100.1"))
	rec := do(h, anonRequest("/x", "198.51.100.1"))
	if got, _ := strconv.Atoi(rec.Header().Get("Retry-After")); rec.Code != http.StatusTooManyRequests || got != 10 {
		t.Fatalf("code = %d Retry-After = %q, want 429 and 10", rec.Code, rec.Header().Get("Retry-After"))
	}
}

// Regression: a bogus credential header must not lift the address limit
// on unauthenticated routes (OAuth registration, token, bootstrap...).
func TestRateLimiterBogusCredentialStillLimitedOnAnonymousRoutes(t *testing.T) {
	l, _ := newTestLimiter(60, 2)
	h := testStack(l)
	for i := 0; i < 2; i++ {
		req := bearer("/api/v1/oauth/register", "198.51.100.1", "x")
		req.AddCookie(&http.Cookie{Name: "admin_access", Value: "junk"})
		if rec := do(h, req); rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	if rec := do(h, bearer("/api/v1/oauth/register", "198.51.100.1", "x")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("bogus Authorization bypassed the address limit: %d", rec.Code)
	}
}

func TestRateLimiterVerifiedSessionKeyedByUserOnAnonymousRoutes(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	l.SetSessionSecret("secret")
	h := testStack(l)
	session := func(id string) *http.Request {
		token, err := auth.CreateAdminToken(id, "secret", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		req := anonRequest("/api/v1/auth/me", "198.51.100.1")
		req.AddCookie(&http.Cookie{Name: "admin_access", Value: token})
		return req
	}
	// Two users behind one address each get their own bucket.
	if rec := do(h, session("u1")); rec.Code != http.StatusOK {
		t.Fatalf("u1: %d", rec.Code)
	}
	if rec := do(h, session("u2")); rec.Code != http.StatusOK {
		t.Fatalf("u2 shares u1's address bucket: %d", rec.Code)
	}
	if rec := do(h, session("u1")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("u1 second request: %d, want 429", rec.Code)
	}
}

// Regression: a flood of bad credentials from a shared address (an ingress
// without TRUSTED_PROXY_CIDRS) must never refuse valid credentials.
func TestRateLimiterFailuresNeverBlockValidCredentials(t *testing.T) {
	l, _ := newTestLimiter(60, 2)
	h := testStack(l)
	const shared = "10.0.0.1"

	for i := 0; i < 2; i++ {
		if rec := do(h, bearer("/api/v1/monitors", shared, "junk")); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d", i, rec.Code)
		}
	}
	rec := do(h, bearer("/api/v1/monitors", shared, "junk"))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("after failures: %d, want 429 with Retry-After", rec.Code)
	}
	// Exhaust the anonymous bucket of the same address as well.
	for i := 0; i < 3; i++ {
		do(h, anonRequest("/api/v1/auth/refresh", shared))
	}
	for _, key := range []string{"good-a", "good-b"} {
		if rec := do(h, bearer("/api/v1/monitors", shared, key)); rec.Code != http.StatusOK {
			t.Fatalf("valid key %s refused from the busy address: %d", key, rec.Code)
		}
	}
}

func TestRateLimiterAuthenticateKeysByCredential(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	h := testStack(l)
	if rec := do(h, bearer("/api/v1/monitors", "198.51.100.1", "good-k1")); rec.Code != http.StatusOK {
		t.Fatalf("k1: %d", rec.Code)
	}
	if rec := do(h, bearer("/api/v1/monitors", "198.51.100.1", "good-k2")); rec.Code != http.StatusOK {
		t.Fatalf("k2: %d", rec.Code)
	}
	if rec := do(h, bearer("/api/v1/monitors", "203.0.113.50", "good-k1")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("k1 from another address: %d, want 429", rec.Code)
	}
}

// Regression: OTLP is exempt only for keys that validate; bad keys spend
// from the failure bucket like anywhere else.
func TestRateLimiterExemptRoutesStillChargeFailures(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	h := testStack(l)
	for i := 0; i < 5; i++ {
		if rec := do(h, bearer("/api/v1/otlp/v1/metrics", "198.51.100.1", "good-k")); rec.Code != http.StatusOK {
			t.Fatalf("validated OTLP export %d: %d", i, rec.Code)
		}
	}
	do(h, bearer("/api/v1/otlp/v1/metrics", "198.51.100.9", "junk"))
	if rec := do(h, bearer("/api/v1/otlp/v1/metrics", "198.51.100.9", "junk")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("bad-key OTLP flood: %d, want 429", rec.Code)
	}
	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/static/collector/x", "/api/v1/push/tok", "/api/v1/discovery-collectors/abc/config"} {
		for i := 0; i < 5; i++ {
			if rec := do(h, anonRequest(path, "198.51.100.1")); rec.Code != http.StatusOK {
				t.Fatalf("%s request %d: %d", path, i, rec.Code)
			}
		}
	}
}

func TestRateLimiterMCPEndpointNotDoubleCharged(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	h := testStack(l)
	for i := 0; i < 3; i++ {
		if rec := do(h, bearer("/api/v1/mcp", "198.51.100.1", "good-k")); rec.Code != http.StatusOK {
			t.Fatalf("outer MCP request %d: %d", i, rec.Code)
		}
	}
}

func TestIdentityKey(t *testing.T) {
	base := anonRequest("/", "198.51.100.7")
	tests := []struct {
		name string
		req  *http.Request
		want string
	}{
		{"api key", base.WithContext(ctxpkg.WithAPIKeyID(base.Context(), "k")), "key:k"},
		{"session", base.WithContext(ctxpkg.WithAdminID(base.Context(), "u")), "user:u"},
		{"mcp grant", base.WithContext(ctxpkg.WithVia(ctxpkg.WithAdminID(base.Context(), "u"), ctxpkg.ViaMCP)), "mcp:u"},
		{"no identity", base, "ip:198.51.100.7"},
	}
	for _, tt := range tests {
		if got := identityKey(tt.req); got != tt.want {
			t.Errorf("%s: identityKey = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestIsAuthenticate(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	if !l.IsAuthenticate(l.Authenticate(fakeAuth)) {
		t.Fatal("Authenticate wrapper not recognized")
	}
	if l.IsAuthenticate(fakeAuth) || l.IsAuthenticate(l.Anonymous) {
		t.Fatal("other middleware recognized as Authenticate")
	}
}

func TestRateLimiterSweepsRefilledBuckets(t *testing.T) {
	l, clock := newTestLimiter(60, 2)
	l.take("ip:a")
	l.take("ip:b")
	clock.t = clock.t.Add(5 * time.Minute)
	l.take("ip:c")
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) != 1 {
		t.Fatalf("buckets after sweep = %d, want 1", len(l.buckets))
	}
}

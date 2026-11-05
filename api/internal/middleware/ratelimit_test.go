package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

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

func do(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRateLimiterDisabled(t *testing.T) {
	var l *RateLimiter = NewRateLimiter(0, 10)
	if l != nil {
		t.Fatal("perMinute=0 must disable the limiter")
	}
	h := l.Anonymous(l.Identified(statusHandler(http.StatusOK)))
	for i := 0; i < 50; i++ {
		if rec := do(h, anonRequest("/api/v1/auth/me", "198.51.100.1")); rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
}

func TestRateLimiterAnonymousKeyedByClientIP(t *testing.T) {
	l, clock := newTestLimiter(60, 2)
	h := l.Anonymous(statusHandler(http.StatusOK))

	for i := 0; i < 2; i++ {
		if rec := do(h, anonRequest("/api/v1/users/bootstrap/status", "198.51.100.1")); rec.Code != http.StatusOK {
			t.Fatalf("burst request %d: %d", i, rec.Code)
		}
	}
	rec := do(h, anonRequest("/api/v1/users/bootstrap/status", "198.51.100.1"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over burst: %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1 (one token a second)", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}

	// Another address has its own bucket.
	if rec := do(h, anonRequest("/api/v1/users/bootstrap/status", "198.51.100.2")); rec.Code != http.StatusOK {
		t.Fatalf("second address: %d", rec.Code)
	}

	// The bucket refills.
	clock.t = clock.t.Add(time.Second)
	if rec := do(h, anonRequest("/api/v1/users/bootstrap/status", "198.51.100.1")); rec.Code != http.StatusOK {
		t.Fatalf("after refill: %d", rec.Code)
	}
}

func TestRateLimiterRetryAfterRoundsUp(t *testing.T) {
	l, _ := newTestLimiter(6, 1) // one token every 10s
	h := l.Anonymous(statusHandler(http.StatusOK))
	do(h, anonRequest("/x", "198.51.100.1"))
	rec := do(h, anonRequest("/x", "198.51.100.1"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("code = %d", rec.Code)
	}
	if got, _ := strconv.Atoi(rec.Header().Get("Retry-After")); got != 10 {
		t.Fatalf("Retry-After = %q, want 10", rec.Header().Get("Retry-After"))
	}
}

func TestRateLimiterIdentifiedKeyedByCredential(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	h := l.Identified(statusHandler(http.StatusOK))

	withKey := func(id string) *http.Request {
		req := anonRequest("/api/v1/monitors", "198.51.100.1")
		return req.WithContext(ctxpkg.WithAPIKeyID(req.Context(), id))
	}
	withUser := func(id string, mcp bool) *http.Request {
		req := anonRequest("/api/v1/monitors", "198.51.100.1")
		ctx := ctxpkg.WithAdminID(req.Context(), id)
		if mcp {
			ctx = ctxpkg.WithVia(ctx, ctxpkg.ViaMCP)
		}
		return req.WithContext(ctx)
	}

	// Same address, different credentials: independent buckets.
	for _, req := range []*http.Request{withKey("k1"), withKey("k2"), withUser("u1", false), withUser("u1", true)} {
		if rec := do(h, req); rec.Code != http.StatusOK {
			t.Fatalf("first request for %s: %d", identityKey(req), rec.Code)
		}
	}
	// Same credential again: its own bucket is spent, whatever the address.
	again := withKey("k1")
	again = again.WithContext(clientip.WithClientIP(again.Context(), "203.0.113.50"))
	if rec := do(h, again); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request for key k1 from another address: %d, want 429", rec.Code)
	}
	if rec := do(h, withUser("u1", false)); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second session request: %d, want 429", rec.Code)
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

func TestRateLimiterExemptRoutes(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	h := l.Anonymous(l.Identified(statusHandler(http.StatusOK)))
	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/static/collector/x", "/api/v1/push/tok", "/api/v1/otlp/v1/metrics", "/api/v1/discovery-collectors/abc/config", "/api/v1/alerts/stream"} {
		for i := 0; i < 5; i++ {
			if rec := do(h, anonRequest(path, "198.51.100.1")); rec.Code != http.StatusOK {
				t.Fatalf("%s request %d: %d", path, i, rec.Code)
			}
		}
	}
}

func TestRateLimiterAuthFailuresThrottleTheAddress(t *testing.T) {
	l, _ := newTestLimiter(60, 2)
	rejecting := l.Anonymous(statusHandler(http.StatusUnauthorized))
	accepting := l.Anonymous(statusHandler(http.StatusOK))
	bearer := func(ip string) *http.Request {
		req := anonRequest("/api/v1/monitors", ip)
		req.Header.Set("Authorization", "Bearer garbage")
		return req
	}

	// Credentialed requests are not charged up front...
	for i := 0; i < 5; i++ {
		if rec := do(accepting, bearer("198.51.100.1")); rec.Code != http.StatusOK {
			t.Fatalf("authenticated request %d: %d", i, rec.Code)
		}
	}
	// ...but each 401 is, and once the failure bucket is empty the address
	// is refused before authentication runs.
	for i := 0; i < 2; i++ {
		if rec := do(rejecting, bearer("198.51.100.9")); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d", i, rec.Code)
		}
	}
	rec := do(rejecting, bearer("198.51.100.9"))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("after failures: %d Retry-After=%q, want 429 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
	// Another address is unaffected.
	if rec := do(accepting, bearer("198.51.100.10")); rec.Code != http.StatusOK {
		t.Fatalf("other address: %d", rec.Code)
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

package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/clientip"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

const (
	testSessionSecret   = "secret"
	testRefreshLifetime = 30 * 24 * time.Hour
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestLimiter(perMinute, burst int) (*RateLimiter, *fakeClock) {
	l := NewRateLimiter(perMinute, burst)
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	l.now = clock.now
	l.SetSessionSecret(testSessionSecret, testRefreshLifetime)
	return l, clock
}

func statusHandler(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
}

// anonRequest is a request whose resolved client address is ip, as the TCP
// peer (not forwarded by a trusted proxy).
func anonRequest(path, ip string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	return req.WithContext(clientip.WithClientIP(req.Context(), ip))
}

func bearer(path, ip, token string) *http.Request {
	req := anonRequest(path, ip)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func withSession(t *testing.T, req *http.Request, user string, ttl time.Duration) *http.Request {
	t.Helper()
	token, err := auth.CreateAdminToken(user, "", testSessionSecret, ttl)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	return req
}

func do(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// revokedKeys lists "good-<id>" keys fakeAuth now refuses.
var revokedKeys sync.Map

// fakeAuth mirrors AuthMiddleware: a valid session cookie, else "Bearer
// good-<id>" (not revoked) as API key <id>, else 401.
func fakeAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookieName); err == nil {
			if claims, err := auth.ParseAdminToken(c.Value, testSessionSecret); err == nil {
				next.ServeHTTP(w, r.WithContext(ctxpkg.WithAdminID(r.Context(), claims.AdminID)))
				return
			}
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		id, ok := strings.CutPrefix(token, "good-")
		if _, revoked := revokedKeys.Load(token); !ok || revoked {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		next.ServeHTTP(w, r.WithContext(ctxpkg.WithAPIKeyID(r.Context(), id)))
	})
}

// fakeAnonymous answers the unauthenticated routes: push and discovery
// tokens other than "good" are unknown (404/401), refresh needs the
// "good-refresh" cookie, a sign-in (POST) needs the "right" password header,
// everything else is 200.
func fakeAnonymous(w http.ResponseWriter, r *http.Request) {
	switch p := r.URL.Path; {
	case p == loginPath && r.Method == http.MethodPost:
		if r.Header.Get("X-Test-Password") != "right" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	case strings.HasPrefix(p, "/api/v1/push/"):
		if strings.TrimPrefix(p, "/api/v1/push/") != "good" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
	case strings.HasPrefix(p, "/api/v1/discovery-collectors/"):
		if r.Header.Get("Authorization") != "Bearer collector" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	case p == refreshPath:
		c, err := r.Cookie(refreshCookieName)
		if err != nil || (c.Value != "good-refresh" && !strings.HasPrefix(c.Value, "rotated-")) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Refresh rotates the cookie, as the real handler does.
		http.SetCookie(w, &http.Cookie{Name: refreshCookieName, Value: "rotated-" + c.Value, MaxAge: 3600})
	}
	w.WriteHeader(http.StatusOK)
}

// testStack is the API's arrangement: Anonymous router-wide, Authenticate
// around the auth middleware on routes under /api/v1/ except the
// unauthenticated groups.
func testStack(l *RateLimiter) http.Handler {
	h, _ := countingTestStack(l)
	return h
}

// countingTestStack is testStack plus a count of the requests that reached
// a credential check: the auth middleware or an unauthenticated handler.
func countingTestStack(l *RateLimiter) (http.Handler, *int) {
	reached := new(int)
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
	countedAuth := func(next http.Handler) http.Handler {
		inner := fakeAuth(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*reached++
			inner.ServeHTTP(w, r)
		})
	}
	authed := l.Authenticate(countedAuth)(statusHandler(http.StatusOK))
	return l.Anonymous(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAuth(r) {
			authed.ServeHTTP(w, r)
			return
		}
		*reached++
		fakeAnonymous(w, r)
	})), reached
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
		if rec := do(h, anonRequest("/api/v1/auth/login", "198.51.100.1")); rec.Code != http.StatusOK {
			t.Fatalf("burst request %d: %d", i, rec.Code)
		}
	}
	rec := do(h, anonRequest("/api/v1/auth/login", "198.51.100.1"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over burst: %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1 (one token a second)", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if rec := do(h, anonRequest("/api/v1/auth/login", "198.51.100.2")); rec.Code != http.StatusOK {
		t.Fatalf("second address: %d", rec.Code)
	}
	clock.t = clock.t.Add(time.Second)
	if rec := do(h, anonRequest("/api/v1/auth/login", "198.51.100.1")); rec.Code != http.StatusOK {
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

// A bogus credential header must not lift the address limit on
// unauthenticated routes (OAuth registration, token, bootstrap...).
func TestRateLimiterBogusCredentialStillLimitedOnAnonymousRoutes(t *testing.T) {
	l, _ := newTestLimiter(60, 2)
	h := testStack(l)
	for i := 0; i < 2; i++ {
		req := bearer("/api/v1/oauth/register", "198.51.100.1", fmt.Sprintf("x%d", i))
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "junk"})
		req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: fmt.Sprintf("junk%d", i)})
		if rec := do(h, req); rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	if rec := do(h, bearer("/api/v1/oauth/register", "198.51.100.1", "x")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("bogus credentials bypassed the address limit: %d", rec.Code)
	}
}

func TestRateLimiterSignedSessionKeyedByUserOnAnonymousRoutes(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	h := testStack(l)
	// Two users behind one address each get their own bucket, expired
	// cookies included.
	if rec := do(h, withSession(t, anonRequest("/api/v1/auth/me", "198.51.100.1"), "u1", time.Hour)); rec.Code != http.StatusOK {
		t.Fatalf("u1: %d", rec.Code)
	}
	if rec := do(h, withSession(t, anonRequest("/api/v1/auth/me", "198.51.100.1"), "u2", -time.Minute)); rec.Code != http.StatusOK {
		t.Fatalf("u2 (expired cookie) shares u1's address bucket: %d", rec.Code)
	}
	if rec := do(h, withSession(t, anonRequest("/api/v1/auth/me", "198.51.100.1"), "u1", time.Hour)); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("u1 second request: %d, want 429", rec.Code)
	}
}

// The shared-address attack: one client floods sign-in and junk keys from an
// address many users share. Everyone else's sign-in from that address is
// limited (it has no other key), and so, until the failure bucket refills,
// is every credential only a lookup can verify. A valid session and an
// expired session cookie's 401 -- which is what makes the client refresh --
// still go through.
func TestRateLimiterSharedAddressFloodSparesSessions(t *testing.T) {
	l, clock := newTestLimiter(60, 3)
	h := testStack(l)
	const shared = "198.51.100.77"

	for i := 0; i < 20; i++ {
		do(h, anonRequest("/api/v1/auth/login", shared))
		do(h, bearer("/api/v1/monitors", shared, fmt.Sprintf("junk%d", i)))
	}
	if rec := do(h, anonRequest("/api/v1/auth/login", shared)); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the flood itself is not limited: %d", rec.Code)
	}
	if rec := do(h, bearer("/api/v1/monitors", shared, "junk")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("junk keys are not limited after the failure bucket empties: %d", rec.Code)
	}

	if rec := do(h, withSession(t, anonRequest("/api/v1/monitors", shared), "victim", time.Hour)); rec.Code != http.StatusOK {
		t.Fatalf("valid session refused from the flooded address: %d", rec.Code)
	}
	for i := 0; i < 3; i++ {
		rec := do(h, withSession(t, anonRequest("/api/v1/monitors", shared), "victim", -time.Minute))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expired session cookie got %d, want 401 so the client refreshes", rec.Code)
		}
	}

	// Keys and refresh cookies cannot be told from guesses without a
	// lookup, so they wait with the flood ...
	if rec := do(h, bearer("/api/v1/monitors", shared, "good-key")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("API key from an address with no failure budget left: %d, want 429", rec.Code)
	}
	refresh := func() int {
		req := anonRequest(refreshPath, shared)
		req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "good-refresh"})
		return do(h, req).Code
	}
	if code := refresh(); code != http.StatusTooManyRequests {
		t.Fatalf("refresh from an address with no failure budget left: %d, want 429", code)
	}
	// ... and go through once a failure token has refilled.
	clock.t = clock.t.Add(time.Second)
	if rec := do(h, bearer("/api/v1/monitors", shared, "good-key")); rec.Code != http.StatusOK {
		t.Fatalf("valid API key after the refill: %d", rec.Code)
	}
	clock.t = clock.t.Add(time.Second)
	if code := refresh(); code != http.StatusOK {
		t.Fatalf("valid refresh after the refill: %d", code)
	}
}

// Once an address has emptied its failure bucket, its guesses stop reaching
// the handler (no refresh-session, API-key, push or collector token lookup)
// and random refresh cookies stop minting buckets; a valid session from the
// same address still reaches the auth middleware and gets through.
func TestRateLimiterFloodStopsReachingHandler(t *testing.T) {
	const addr = "198.51.100.90"
	floods := map[string]func(i int) *http.Request{
		"refresh cookies": func(i int) *http.Request {
			req := anonRequest(refreshPath, addr)
			req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: fmt.Sprintf("guess%d", i)})
			return req
		},
		"push tokens": func(i int) *http.Request {
			return anonRequest(fmt.Sprintf("/api/v1/push/guess%d", i), addr)
		},
		"collector tokens": func(i int) *http.Request {
			req := anonRequest("/api/v1/discovery-collectors/x/config", addr)
			req.Header.Set("Authorization", fmt.Sprintf("Bearer guess%d", i))
			return req
		},
		"API keys": func(i int) *http.Request {
			return bearer("/api/v1/monitors", addr, fmt.Sprintf("guess%d", i))
		},
		"sign-ins": func(i int) *http.Request {
			req := anonRequest(loginPath, addr)
			req.Method = http.MethodPost
			return req
		},
	}
	for name, guess := range floods {
		t.Run(name, func(t *testing.T) {
			l, _ := newTestLimiter(60, 3)
			h, reached := countingTestStack(l)
			for i := 0; i < 50; i++ {
				do(h, guess(i))
			}
			if *reached != 3 {
				t.Fatalf("%d of 50 guesses reached the handler, want the burst of 3", *reached)
			}
			l.mu.Lock()
			buckets := len(l.buckets)
			l.mu.Unlock()
			if buckets > 5 {
				t.Fatalf("the flood left %d buckets, want only those of the guesses that ran", buckets)
			}
			before := *reached
			if rec := do(h, withSession(t, anonRequest("/api/v1/monitors", addr), "victim", time.Hour)); rec.Code != http.StatusOK {
				t.Fatalf("valid session from the flooding address: %d", rec.Code)
			}
			if *reached != before+1 {
				t.Fatal("the valid session did not reach the auth middleware")
			}
		})
	}
}

// A signed but expired session cookie must not shield a bearer credential:
// the auth middleware falls through from the cookie to the key and looks it
// up, so the key's failures are charged and, once the bucket is empty,
// refused before the lookup.
func TestRateLimiterExpiredCookieDoesNotShieldBearer(t *testing.T) {
	l, _ := newTestLimiter(60, 2)
	h, reached := countingTestStack(l)
	codes := []int{}
	for i := 0; i < 5; i++ {
		req := withSession(t, bearer("/api/v1/monitors", "198.51.100.61", fmt.Sprintf("guess%d", i)), "ex-member", -time.Minute)
		codes = append(codes, do(h, req).Code)
	}
	if fmt.Sprint(codes) != fmt.Sprint([]int{401, 401, 429, 429, 429}) {
		t.Fatalf("expired cookie + random keys = %v, want [401 401 429 429 429]", codes)
	}
	if *reached != 2 {
		t.Fatalf("%d key lookups ran, want 2", *reached)
	}
	// The same goes for X-API-Key.
	req := withSession(t, anonRequest("/api/v1/monitors", "198.51.100.61"), "ex-member", -time.Minute)
	req.Header.Set("X-API-Key", "guess")
	if rec := do(h, req); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expired cookie + X-API-Key: %d, want 429", rec.Code)
	}
}

// A cookie that expired longer ago than the refresh lifetime can no longer
// be refreshed: its 401s are ordinary failures.
func TestRateLimiterLongExpiredCookieIsCharged(t *testing.T) {
	l, _ := newTestLimiter(60, 2)
	h := testStack(l)
	codes := []int{}
	for i := 0; i < 3; i++ {
		req := withSession(t, anonRequest("/api/v1/monitors", "198.51.100.62"), "ex-member", -(testRefreshLifetime + time.Hour))
		codes = append(codes, do(h, req).Code)
	}
	if fmt.Sprint(codes) != fmt.Sprint([]int{401, 401, 429}) {
		t.Fatalf("long-expired cookie = %v, want [401 401 429]", codes)
	}
	// Within the lifetime it keeps its 401 however often it is sent.
	for i := 0; i < 5; i++ {
		req := withSession(t, anonRequest("/api/v1/monitors", "198.51.100.63"), "member", -(testRefreshLifetime - time.Hour))
		if rec := do(h, req); rec.Code != http.StatusUnauthorized {
			t.Fatalf("refreshable cookie request %d: %d, want 401", i, rec.Code)
		}
	}
}

// Failed sign-ins spend the failure bucket, so password guessing is bounded
// by it as well as by the address bucket, and a sign-in waits while it is
// empty.
func TestRateLimiterFailedSignInChargesFailures(t *testing.T) {
	l, _ := newTestLimiter(60, 3)
	h := testStack(l)
	signIn := func(ip, password string) int {
		req := anonRequest(loginPath, ip)
		req.Method = http.MethodPost
		req.Header.Set("X-Test-Password", password)
		return do(h, req).Code
	}
	// Junk keys empty the failure bucket while the address bucket is full.
	for i := 0; i < 3; i++ {
		do(h, bearer("/api/v1/monitors", "198.51.100.64", fmt.Sprintf("junk%d", i)))
	}
	if code := signIn("198.51.100.64", "right"); code != http.StatusTooManyRequests {
		t.Fatalf("sign-in while the failure bucket is empty: %d, want 429", code)
	}
	// Failed sign-ins empty it too.
	codes := []int{}
	for i := 0; i < 3; i++ {
		codes = append(codes, signIn("198.51.100.65", "wrong"))
	}
	if fmt.Sprint(codes) != fmt.Sprint([]int{401, 401, 401}) {
		t.Fatalf("failed sign-ins = %v", codes)
	}
	if rec := do(h, bearer("/api/v1/monitors", "198.51.100.65", "good-k")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("API key after three failed sign-ins: %d, want 429", rec.Code)
	}
}

// Invalid refresh cookies are failures: rotating them buys a fresh
// credential bucket each time but spends the address's failure bucket, and
// once that is empty they stop reaching the handler.
func TestRateLimiterInvalidRefreshChargesFailures(t *testing.T) {
	l, _ := newTestLimiter(60, 2)
	h, reached := countingTestStack(l)
	codes := []int{}
	for i := 0; i < 4; i++ {
		req := anonRequest(refreshPath, "198.51.100.5")
		req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: fmt.Sprintf("guess%d", i)})
		codes = append(codes, do(h, req).Code)
	}
	if fmt.Sprint(codes) != fmt.Sprint([]int{401, 401, 429, 429}) {
		t.Fatalf("refresh guesses = %v, want [401 401 429 429]", codes)
	}
	if *reached != 2 {
		t.Fatalf("%d refresh lookups ran, want 2", *reached)
	}
}

// An unattributable peer (the frontend's /api proxy, an ingress pod, a NAT
// gateway on a private network) skips the address buckets instead of
// pooling every visitor into one; credential buckets still apply.
func TestRateLimiterPrivateUntrustedPeerSkipsAddressBuckets(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	h := testStack(l)
	for _, peer := range []string{"10.0.0.5", "127.0.0.1", "fd00::5", "100.64.3.4"} {
		for i := 0; i < 5; i++ {
			if rec := do(h, anonRequest("/api/v1/auth/login", peer)); rec.Code != http.StatusOK {
				t.Fatalf("%s login %d: %d", peer, i, rec.Code)
			}
			if rec := do(h, bearer("/api/v1/monitors", peer, "junk")); rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s junk key %d: %d, want 401", peer, i, rec.Code)
			}
			if rec := do(h, anonRequest("/api/v1/push/unknown", peer)); rec.Code != http.StatusNotFound {
				t.Fatalf("%s unknown push token %d: %d, want 404", peer, i, rec.Code)
			}
		}
	}
	do(h, bearer("/api/v1/monitors", "10.0.0.5", "good-k"))
	if rec := do(h, bearer("/api/v1/monitors", "10.0.0.5", "good-k")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("credential bucket must still apply behind a private peer: %d", rec.Code)
	}
}

// An address resolved through a trusted proxy is attributable even when it
// is private (an internal user behind the ingress), and limited.
func TestRateLimiterTrustedProxyResolvedAddressIsLimited(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	trusted, err := clientip.ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	h := clientip.NewResolver(trusted).Middleware(testStack(l))
	send := func(xff string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login", nil)
		req.RemoteAddr = "10.0.0.2:4000"
		req.Header.Set("X-Forwarded-For", xff)
		return do(h, req).Code
	}
	if send("192.168.7.7") != http.StatusOK || send("192.168.7.7") != http.StatusTooManyRequests {
		t.Fatal("a client forwarded by a trusted proxy is not limited")
	}
	if send("198.51.100.8") != http.StatusOK {
		t.Fatal("a second forwarded client shares the first's bucket")
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

// OTLP is exempt only for keys that validate; bad keys spend from the
// failure bucket like anywhere else.
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
	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/static/collector/x"} {
		for i := 0; i < 5; i++ {
			if rec := do(h, anonRequest(path, "198.51.100.1")); rec.Code != http.StatusOK {
				t.Fatalf("%s request %d: %d", path, i, rec.Code)
			}
		}
	}
}

// Token-path routes: a valid push or collector token is not limited, but an
// unknown one is a failure, so looping random tokens is bounded.
func TestRateLimiterTokenPathsChargeOnlyFailures(t *testing.T) {
	l, clock := newTestLimiter(60, 2)
	h := testStack(l)
	const addr = "198.51.100.40"
	for i := 0; i < 10; i++ {
		if rec := do(h, anonRequest("/api/v1/push/good", addr)); rec.Code != http.StatusOK {
			t.Fatalf("valid push token %d: %d", i, rec.Code)
		}
		req := anonRequest("/api/v1/discovery-collectors/x/config", addr)
		req.Header.Set("Authorization", "Bearer collector")
		if rec := do(h, req); rec.Code != http.StatusOK {
			t.Fatalf("valid collector token %d: %d", i, rec.Code)
		}
	}
	codes := []int{}
	for i := 0; i < 3; i++ {
		codes = append(codes, do(h, anonRequest(fmt.Sprintf("/api/v1/push/random%d", i), addr)).Code)
	}
	if fmt.Sprint(codes) != fmt.Sprint([]int{404, 404, 429}) {
		t.Fatalf("random push tokens = %v, want [404 404 429]", codes)
	}
	if rec := do(h, anonRequest("/api/v1/discovery-collectors/x/config", addr)); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("bad collector token after the failure bucket emptied: %d, want 429", rec.Code)
	}
	// The push token verified a moment ago, so the empty bucket does not
	// stop it (see TestRateLimiterRecentlyVerifiedCredentialsPassPreCheck).
	if rec := do(h, anonRequest("/api/v1/push/good", addr)); rec.Code != http.StatusOK {
		t.Fatalf("verified push token while the failure bucket is empty: %d, want 200", rec.Code)
	}
	clock.t = clock.t.Add(time.Second)
	if rec := do(h, anonRequest("/api/v1/push/good", addr)); rec.Code != http.StatusOK {
		t.Fatalf("valid push token after the refill: %d", rec.Code)
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

// IPv6 clients share a bucket per /64, so rotating addresses inside one
// does not buy fresh buckets.
func TestRateLimiterIPv6KeyedBySlash64(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	h := testStack(l)
	if rec := do(h, anonRequest("/api/v1/auth/login", "2001:db8:1:2::1")); rec.Code != http.StatusOK {
		t.Fatalf("first: %d", rec.Code)
	}
	if rec := do(h, anonRequest("/api/v1/auth/login", "2001:db8:1:2:ffff::9")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("another address in the same /64 got a fresh bucket: %d", rec.Code)
	}
	if rec := do(h, anonRequest("/api/v1/auth/login", "2001:db8:1:3::1")); rec.Code != http.StatusOK {
		t.Fatalf("a different /64 shares the bucket: %d", rec.Code)
	}
	for in, want := range map[string]string{
		"2001:db8:1:2::1":  "2001:db8:1:2::/64",
		"198.51.100.1":     "198.51.100.1",
		"::ffff:192.0.2.1": "192.0.2.1",
		"not-an-ip":        "not-an-ip",
	} {
		if got := bucketAddress(in); got != want {
			t.Errorf("bucketAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

// Filling the bucket map evicts refilled buckets first, then the least
// recently used: a bucket still in use keeps its spent tokens.
func TestRateLimiterEvictionKeepsActiveBuckets(t *testing.T) {
	l, clock := newTestLimiter(60, 2)
	l.maxBuckets = 10
	if ok, _ := l.take("ip:victim"); !ok {
		t.Fatal("first take")
	}
	l.take("ip:victim") // now empty
	for i := 0; i < 50; i++ {
		clock.t = clock.t.Add(10 * time.Millisecond)
		l.take(fmt.Sprintf("ip:filler%d", i))
		l.take(fmt.Sprintf("ip:filler%d", i)) // spent, so not refilled
		// The victim keeps using its bucket.
		if ok, _ := l.take("ip:victim"); ok && i < 40 {
			t.Fatalf("filling the map reset an active bucket (iteration %d)", i)
		}
		l.mu.Lock()
		n := len(l.buckets)
		l.mu.Unlock()
		if n > l.maxBuckets {
			t.Fatalf("map grew to %d past the cap of %d", n, l.maxBuckets)
		}
	}

	// Refilled buckets go before used ones.
	l2, clock2 := newTestLimiter(60, 2)
	l2.maxBuckets = 4
	l2.take("ip:spent")
	l2.take("ip:spent")
	l2.take("ip:fresh1")
	l2.take("ip:fresh2")
	clock2.t = clock2.t.Add(2 * time.Second) // fresh1/fresh2 refill; spent is half full
	l2.take("ip:spent")                      // spent again, most recently used
	l2.take("ip:other")
	l2.take("ip:new") // map full: evicts the refilled ones, not "spent"
	l2.mu.Lock()
	_, kept := l2.buckets["ip:spent"]
	_, gone1 := l2.buckets["ip:fresh1"]
	l2.mu.Unlock()
	if !kept || gone1 {
		t.Fatalf("eviction order wrong: spent kept=%v fresh1 kept=%v", kept, gone1)
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
		{"no identity", base, ""},
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

func TestClassifySessionToken(t *testing.T) {
	const lifetime = 30 * 24 * time.Hour
	now := time.Now()
	token := func(user string, ttl time.Duration) string {
		t.Helper()
		v, err := auth.CreateAdminToken(user, "", "secret", ttl)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	fresh := token("u1", time.Hour)
	expired := token("u2", -time.Minute)
	ancient := token("u3", -(lifetime + time.Hour))

	if id, state := classifySessionToken(fresh, "secret", now, lifetime); state != sessionLive || id != "u1" {
		t.Fatalf("fresh token: %q %v", id, state)
	}
	if _, err := auth.ParseAdminToken(expired, "secret"); err == nil {
		t.Fatal("expired token must not authenticate")
	}
	if id, state := classifySessionToken(expired, "secret", now, lifetime); state != sessionExpired || id != "u2" {
		t.Fatalf("expired but refreshable token: %q %v", id, state)
	}
	if id, state := classifySessionToken(ancient, "secret", now, lifetime); state != sessionNone || id != "" {
		t.Fatalf("token expired past the refresh lifetime: %q %v", id, state)
	}
	for _, bad := range []struct{ token, secret string }{{fresh, "other"}, {"junk", "secret"}, {"", "secret"}, {fresh, ""}} {
		if _, state := classifySessionToken(bad.token, bad.secret, now, lifetime); state != sessionNone {
			t.Fatalf("accepted %q with secret %q", bad.token, bad.secret)
		}
	}
}

// emptyFailureBucket spends addr's failure budget with junk keys.
func emptyFailureBucket(t *testing.T, h http.Handler, addr string, burst int) {
	t.Helper()
	for i := 0; i < burst; i++ {
		do(h, bearer("/api/v1/monitors", addr, fmt.Sprintf("junk-%d", i)))
	}
	if rec := do(h, bearer("/api/v1/monitors", addr, "junk-check")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("failure bucket not empty: %d", rec.Code)
	}
}

// With the address out of failure budget, a key or token that verified
// recently still reaches its handler; one never seen to succeed does not --
// a valid one included, which waits until the address has budget again.
func TestRateLimiterRecentlyVerifiedCredentialsPassPreCheck(t *testing.T) {
	l, _ := newTestLimiter(60, 3)
	h, reached := countingTestStack(l)
	const addr = "198.51.100.120"
	collector := func(token string) *http.Request {
		req := anonRequest("/api/v1/discovery-collectors/x/config", addr)
		req.Header.Set("Authorization", "Bearer "+token)
		return req
	}
	refresh := func(value string) *http.Request {
		req := anonRequest(refreshPath, addr)
		req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: value})
		return req
	}
	signIn := bearer(loginPath, addr, "good-k1")
	signIn.Method = http.MethodPost

	// Verified while the address still had budget.
	for name, req := range map[string]*http.Request{
		"API key":         bearer("/api/v1/monitors", addr, "good-k1"),
		"push token":      anonRequest("/api/v1/push/good", addr),
		"collector token": collector("collector"),
	} {
		if rec := do(h, req); rec.Code != http.StatusOK {
			t.Fatalf("%s while the address has budget: %d", name, rec.Code)
		}
	}
	rec := do(h, refresh("good-refresh"))
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh while the address has budget: %d", rec.Code)
	}
	rotated := issuedRefreshCookie(rec.Header())
	if rotated == "" {
		t.Fatal("the fake refresh did not rotate the cookie")
	}

	emptyFailureBucket(t, h, addr, 3)

	for name, req := range map[string]*http.Request{
		"verified API key":         bearer("/api/v1/monitors", addr, "good-k1"),
		"verified push token":      anonRequest("/api/v1/push/good", addr),
		"verified collector token": collector("collector"),
		"rotated refresh cookie":   refresh(rotated),
	} {
		before := *reached
		if rec := do(h, req); rec.Code != http.StatusOK || *reached != before+1 {
			t.Fatalf("%s from the flooded address: %d (reached handler: %v), want 200 through the handler", name, rec.Code, *reached == before+1)
		}
	}
	for name, req := range map[string]*http.Request{
		"unknown API key":          bearer("/api/v1/monitors", addr, "guess"),
		"never-verified valid key": bearer("/api/v1/monitors", addr, "good-k2"),
		"unknown push token":       anonRequest("/api/v1/push/guess", addr),
		"unknown collector token":  collector("guess"),
		"unseen refresh cookie":    refresh("rotated-unseen"),
		"sign-in":                  signIn,
	} {
		before := *reached
		if rec := do(h, req); rec.Code != http.StatusTooManyRequests || *reached != before {
			t.Fatalf("%s from the flooded address: %d, want 429 before the handler", name, rec.Code)
		}
	}
}

// One success vouches for a credential for the TTL; after that it waits for
// the address's budget like any unknown credential.
func TestRateLimiterVerifiedCredentialExpires(t *testing.T) {
	l, clock := newTestLimiter(60, 3)
	h := testStack(l)
	const addr = "198.51.100.121"
	if rec := do(h, bearer("/api/v1/monitors", addr, "good-k")); rec.Code != http.StatusOK {
		t.Fatalf("verify: %d", rec.Code)
	}
	clock.t = clock.t.Add(verifiedCredentialTTL - time.Second)
	emptyFailureBucket(t, h, addr, 3)
	if rec := do(h, bearer("/api/v1/monitors", addr, "good-k")); rec.Code != http.StatusOK {
		t.Fatalf("within the TTL: %d, want 200", rec.Code)
	}
	// That success restarted the TTL; let it lapse.
	clock.t = clock.t.Add(verifiedCredentialTTL)
	emptyFailureBucket(t, h, addr, 3)
	if rec := do(h, bearer("/api/v1/monitors", addr, "good-k")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after the TTL: %d, want 429", rec.Code)
	}
}

// A verified key that is then revoked fails once and is forgotten: from then
// on it is refused before the lookup like any unknown key.
func TestRateLimiterRevokedCredentialIsForgotten(t *testing.T) {
	l, _ := newTestLimiter(60, 3)
	h, reached := countingTestStack(l)
	const addr = "198.51.100.122"
	const key = "good-revoked"
	t.Cleanup(func() { revokedKeys.Delete(key) })
	if rec := do(h, bearer("/api/v1/monitors", addr, key)); rec.Code != http.StatusOK {
		t.Fatalf("verify: %d", rec.Code)
	}
	emptyFailureBucket(t, h, addr, 3)
	revokedKeys.Store(key, true)
	before := *reached
	if rec := do(h, bearer("/api/v1/monitors", addr, key)); *reached != before+1 || rec.Code == http.StatusOK {
		t.Fatalf("first use after revocation: %d, want it looked up and refused", rec.Code)
	}
	before = *reached
	if rec := do(h, bearer("/api/v1/monitors", addr, key)); rec.Code != http.StatusTooManyRequests || *reached != before {
		t.Fatalf("revoked key after its failure: %d, want 429 before the handler", rec.Code)
	}
}

// The set is bounded with least-recently-verified eviction, expires entries,
// and keys its digests per process.
func TestVerifiedCredentialsBoundedLRU(t *testing.T) {
	v := newVerifiedCredentials(time.Minute, 3)
	now := time.Unix(1_700_000_000, 0)
	for _, c := range []string{"a", "b", "c"} {
		v.remember(c, now)
	}
	v.remember("a", now) // most recent again
	v.remember("d", now) // evicts b, the least recently verified
	if v.len() != 3 {
		t.Fatalf("size = %d, want 3", v.len())
	}
	if v.known("b", now) || !v.known("a", now) || !v.known("c", now) || !v.known("d", now) {
		t.Fatal("eviction did not drop the least recently verified entry")
	}
	if v.known("a", now.Add(time.Minute)) {
		t.Fatal("entry outlived its TTL")
	}
	v.forget("c")
	if v.known("c", now) {
		t.Fatal("forget kept the entry")
	}
	if v.known("", now) {
		t.Fatal("empty credential is known")
	}
	if newVerifiedCredentials(time.Minute, 3).digest("d") == v.digest("d") {
		t.Fatal("digests are not keyed per process")
	}
}

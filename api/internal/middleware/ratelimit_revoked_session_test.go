package middleware

import (
	"net/http"
	"testing"
)

// A session's refresh cookie that the limiter remembers as verified is never
// accepted on that memory once the session is revoked: the verified set only
// lifts the address pre-check, the refresh handler still checks the session,
// so the first request after the revocation is looked up and refused, and
// that refusal drops the cookie from the set. From then on it waits for the
// address's budget like any unknown credential.
func TestRateLimiterRevokedSessionRefreshCookieIsNeverVouchedFor(t *testing.T) {
	l, _ := newTestLimiter(60, 3)
	const addr = "198.51.100.130"
	revoked := map[string]bool{}
	reached := 0
	isAuth := func(r *http.Request) bool { return r.URL.Path == "/api/v1/monitors" }
	l.SetRouteClassifier(isAuth)
	authed := l.Authenticate(fakeAuth)(statusHandler(http.StatusOK))
	h := l.Anonymous(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAuth(r) {
			authed.ServeHTTP(w, r)
			return
		}
		reached++
		c, err := r.Cookie(refreshCookieName)
		if r.URL.Path != refreshPath || err != nil || revoked[c.Value] {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK) // no rotation: the same cookie stays in use
	}))
	refresh := func() *http.Request {
		req := anonRequest(refreshPath, addr)
		req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "session-cookie"})
		return req
	}

	if rec := do(h, refresh()); rec.Code != http.StatusOK {
		t.Fatalf("verify: %d", rec.Code)
	}
	emptyFailureBucket(t, h, addr, 3)
	if rec := do(h, refresh()); rec.Code != http.StatusOK {
		t.Fatalf("verified cookie from a flooded address: %d, want 200", rec.Code)
	}

	revoked["session-cookie"] = true
	before := reached
	if rec := do(h, refresh()); rec.Code == http.StatusOK || reached != before+1 {
		t.Fatalf("first refresh after revocation: %d (reached=%v), want refused by the handler", rec.Code, reached == before+1)
	}
	if l.verified.known(refreshCredential("session-cookie"), l.now()) {
		t.Fatal("revoked session's cookie still vouched for")
	}
	before = reached
	if rec := do(h, refresh()); rec.Code != http.StatusTooManyRequests || reached != before {
		t.Fatalf("revoked cookie after its failure: %d, want 429 before the handler", rec.Code)
	}
}

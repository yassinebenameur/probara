package middleware

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/logger"
)

const testAccessLifetime = 15 * time.Minute

func refreshRequest(addr, value string) *http.Request {
	req := anonRequest(refreshPath, addr)
	req.Method = http.MethodPost
	req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: value})
	return req
}

// A browser presents the refresh cookie it was given only once its access
// token has expired -- 15 minutes later by default, past the ten minutes a
// verified key is vouched for. The issued cookie's entry lasts the access
// lifetime plus that margin, so a flooded address does not sign the user out.
func TestRateLimiterIssuedRefreshCookieOutlivesAccessToken(t *testing.T) {
	l, clock := newTestLimiter(60, 3)
	l.SetAccessTokenLifetime(testAccessLifetime)
	h, reached := countingTestStack(l)
	const addr = "198.51.100.130"

	rec := do(h, refreshRequest(addr, "good-refresh"))
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: %d", rec.Code)
	}
	rotated := issuedRefreshCookie(rec.Header())

	// The access token has expired; the old ten-minute entry would be gone.
	clock.t = clock.t.Add(testAccessLifetime + time.Minute)
	if testAccessLifetime+time.Minute <= verifiedCredentialTTL {
		t.Fatal("the test must move the clock past verifiedCredentialTTL")
	}
	emptyFailureBucket(t, h, addr, 3)
	before := *reached
	rec = do(h, refreshRequest(addr, rotated))
	if rec.Code != http.StatusOK || *reached != before+1 {
		t.Fatalf("refresh after the access token expired, flooded address: %d, want 200 through the handler", rec.Code)
	}

	// The cookie that refresh issued, unused past its window, waits for the
	// address's budget like any unknown cookie.
	next := issuedRefreshCookie(rec.Header())
	clock.t = clock.t.Add(testAccessLifetime + verifiedCredentialTTL)
	emptyFailureBucket(t, h, addr, 3)
	if rec := do(h, refreshRequest(addr, next)); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("refresh cookie past its window: %d, want 429", rec.Code)
	}
}

// A replica that never issued the refresh cookie cannot recognise it, but it
// can check the access cookie the browser sends with it. A signed one
// (expired, within the refresh lifetime) lets the refresh through the
// pre-check, charged to the user's own refresh bucket; its 401 stays a 401.
func TestRateLimiterSignedAccessCookieVouchesForRefresh(t *testing.T) {
	l, _ := newTestLimiter(60, 3) // another replica: nothing recorded
	l.SetAccessTokenLifetime(testAccessLifetime)
	h, reached := countingTestStack(l)
	const addr = "198.51.100.131"
	emptyFailureBucket(t, h, addr, 3)

	vouched := func(value string) *http.Request {
		return withSession(t, refreshRequest(addr, value), "user-1", -time.Minute)
	}
	before := *reached
	if rec := do(h, vouched("rotated-elsewhere")); rec.Code != http.StatusOK || *reached != before+1 {
		t.Fatalf("vouched refresh from a flooded address: %d, want 200 through the handler", rec.Code)
	}
	// A bad cookie with a signed access cookie: looked up, and its 401 (the
	// session is over) is not hidden behind a 429.
	if rec := do(h, vouched("bogus")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("vouched refresh with a bad cookie, flooded address: %d, want 401", rec.Code)
	}

	// Without a signed access cookie the pre-check still refuses.
	forged, err := auth.CreateAdminToken("user-1", "", "another-secret", -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := refreshRequest(addr, "rotated-elsewhere-2")
	unsigned.AddCookie(&http.Cookie{Name: sessionCookieName, Value: forged})
	for name, req := range map[string]*http.Request{
		"no access cookie":     refreshRequest(addr, "rotated-elsewhere-2"),
		"forged access cookie": unsigned,
	} {
		before := *reached
		if rec := do(h, req); rec.Code != http.StatusTooManyRequests || *reached != before {
			t.Fatalf("%s: %d, want 429 before the handler", name, rec.Code)
		}
	}

	// The user's refresh bucket bounds the lookups one signed-in user can buy
	// with random refresh cookies (burst 3: two spent above).
	if rec := do(h, vouched("random-1")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("third vouched refresh: %d", rec.Code)
	}
	before = *reached
	if rec := do(h, vouched("random-2")); rec.Code != http.StatusTooManyRequests || *reached != before {
		t.Fatalf("fourth vouched refresh: %d, want 429 from the user's refresh bucket", rec.Code)
	}
}

// A signed-in caller whose request authenticates and is then refused -- a
// malformed or unopenable workspace -- spends from its own bucket like a
// served request, so looping random X-Tenant-ID values is limited. The real
// AuthMiddleware answers the malformed identifier without a database.
func TestRateLimiterChargesAuthenticatedRefusals(t *testing.T) {
	l, _ := newTestLimiter(60, 2)
	l.SetRouteClassifier(func(*http.Request) bool { return true })
	h := l.Authenticate(AuthMiddleware(nil, logger.New("test", "error"), testSessionSecret, nil))(statusHandler(http.StatusOK))
	refused := func(user, path string) int {
		req := withSession(t, anonRequest(path, "198.51.100.132"), user, time.Minute)
		req.Header.Set("X-Tenant-ID", "not-a-uuid")
		return do(h, req).Code
	}
	const user = "4b0c0a52-2d39-4d1b-9b8e-3f0b2a7c9e10"
	for i, path := range []string{"/api/v1/monitors", "/api/v1/alerts/stream"} {
		if code := refused(user, path); code != http.StatusBadRequest {
			t.Fatalf("refusal %d: %d, want 400", i+1, code)
		}
	}
	if code := refused(user, "/api/v1/monitors"); code != http.StatusTooManyRequests {
		t.Fatalf("third refusal: %d, want 429 from the user's bucket", code)
	}
	if code := refused("8d2e7f61-0c4b-4f0e-a1d2-5b6c7d8e9f00", "/api/v1/monitors"); code != http.StatusBadRequest {
		t.Fatalf("another user: %d, want 400", code)
	}
}

// The refusal hook names the identity a served request would be charged to.
func TestNoteAuthenticatedRefusalKeys(t *testing.T) {
	req, slot := withRefusalSlot(anonRequest("/api/v1/monitors", "198.51.100.133"))
	id := uuid.MustParse("4b0c0a52-2d39-4d1b-9b8e-3f0b2a7c9e10")
	noteAuthenticatedRefusal(req, id)
	if slot.key != "user:"+id.String() {
		t.Fatalf("key = %q", slot.key)
	}
	// Outside the limiter there is no slot, and nothing happens.
	noteAuthenticatedRefusal(anonRequest("/api/v1/monitors", "198.51.100.133"), id)
}

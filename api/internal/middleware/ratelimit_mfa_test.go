package middleware

import (
	"fmt"
	"net/http"
	"testing"
)

// The second sign-in step submits an unverified credential (a code) like a
// password: its 401s spend the address's failure budget, and while that is
// empty it is refused before the handler runs, whatever cookie it carries.
func TestRateLimiterSecondSignInStepIsASignIn(t *testing.T) {
	l, _ := newTestLimiter(60, 3)
	reached := 0
	h := l.Anonymous(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		if r.Header.Get("X-Test-Code") != "right" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	step := func(ip, code string) int {
		req := anonRequest(loginMFAPath, ip)
		req.Method = http.MethodPost
		req.Header.Set("X-Test-Code", code)
		return do(h, req).Code
	}

	codes := []int{}
	for i := 0; i < 3; i++ {
		codes = append(codes, step("198.51.100.70", "wrong"))
	}
	if fmt.Sprint(codes) != fmt.Sprint([]int{401, 401, 401}) {
		t.Fatalf("wrong codes = %v", codes)
	}
	before := reached
	if code := step("198.51.100.70", "right"); code != http.StatusTooManyRequests {
		t.Fatalf("second step while the failure budget is empty: %d, want 429", code)
	}
	if reached != before {
		t.Fatal("a refused second step reached the handler")
	}
	// A live session cookie does not lift it, as for the password step.
	req := withSession(t, anonRequest(loginMFAPath, "198.51.100.70"), "u1", 3600e9)
	req.Method = http.MethodPost
	req.Header.Set("X-Test-Code", "right")
	if rec := do(h, req); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second step with a session cookie while failing: %d, want 429", rec.Code)
	}
	// The password step shares the budget.
	login := anonRequest(loginPath, "198.51.100.70")
	login.Method = http.MethodPost
	if rec := do(h, login); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("password step after failed codes: %d, want 429", rec.Code)
	}
	// Another address is unaffected, and a GET on the path is not a sign-in.
	if code := step("198.51.100.71", "right"); code != http.StatusOK {
		t.Fatalf("other address: %d", code)
	}
	if !signInRoute(func() *http.Request { r := anonRequest(loginMFAPath, "x"); r.Method = http.MethodPost; return r }()) ||
		signInRoute(anonRequest(loginMFAPath, "x")) {
		t.Fatal("signInRoute misclassifies the second step")
	}
}

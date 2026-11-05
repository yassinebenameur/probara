package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/shared/config"
)

// The lifecycle routes sit where the rate limiter's router-derived
// classifier expects: public /auth/* flows are anonymous (charged per
// address), /account/* and the superadmin /users actions per credential.
func TestIdentityRoutesRateLimitClassification(t *testing.T) {
	h := newEdgeTestServer(t, func(cfg *config.APIConfig) {
		cfg.RateLimitPerMinute = 60
		cfg.RateLimitBurst = 2
	})
	isAuth := authRouteClassifier(h.(*chi.Mux), apimiddleware.NewRateLimiter(60, 1))
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/api/v1/auth/capabilities", false},
		{http.MethodPost, "/api/v1/auth/login/mfa", false},
		{http.MethodPost, "/api/v1/auth/password/forgot", false},
		{http.MethodPost, "/api/v1/auth/password/reset", false},
		{http.MethodPost, "/api/v1/auth/invite/inspect", false},
		{http.MethodPost, "/api/v1/auth/invite/accept", false},
		{http.MethodPost, "/api/v1/auth/email/confirm", false},
		{http.MethodPatch, "/api/v1/account/password", true},
		{http.MethodPost, "/api/v1/account/email", true},
		{http.MethodGet, "/api/v1/account/2fa", true},
		{http.MethodPost, "/api/v1/account/2fa/setup", true},
		{http.MethodPost, "/api/v1/users/7d0c1c55-0000-0000-0000-000000000000/invite", true},
		{http.MethodPost, "/api/v1/users/7d0c1c55-0000-0000-0000-000000000000/reset-2fa", true},
	} {
		if got := isAuth(httptest.NewRequest(tc.method, tc.path, nil)); got != tc.want {
			t.Errorf("%s %s authenticated = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

// Forgot-password is limited per address like every public route, and
// answers 202 without touching the database when mail is off.
func TestForgotPasswordIsRateLimited(t *testing.T) {
	h := newEdgeTestServer(t, func(cfg *config.APIConfig) {
		cfg.RateLimitPerMinute = 1
		cfg.RateLimitBurst = 2
	})
	codes := []int{}
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password/forgot", bytes.NewBufferString(`{"login":"alice"}`))
		req.RemoteAddr = "198.51.100.7:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	if codes[0] != http.StatusAccepted || codes[1] != http.StatusAccepted || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("statuses = %v, want [202 202 429]", codes)
	}
}

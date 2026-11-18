package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apimiddleware "github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/config"
)

// The cross-site request forgery defence as wired in the real router (the
// database is unreachable: a request that gets past the origin check fails
// later, with something other than the refusal). The operator UI is
// https://app.example.com; status pages are a sibling subdomain or another
// port of the same host, the cases SameSite=Lax does not cover.
func TestCookieWritesRefuseUntrustedOrigins(t *testing.T) {
	const secret = "csrf-wiring-secret"
	h := newEdgeTestServer(t, func(cfg *config.APIConfig) {
		cfg.AdminJWTSecret = secret
		cfg.AdminAccessTTLMinutes = 15
		cfg.AdminRefreshTTLDays = 30
		cfg.AppBaseURL = "https://app.example.com"
	})
	token, err := auth.CreateAdminToken("6f1c1f3e-8f39-4d6a-9d43-0b8a3c6e2b11", "", secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	type request struct {
		method, host, path, contentType, body string
		cookie                                bool
		bearer                                string
		headers                               map[string]string
	}
	send := func(rq request) *httptest.ResponseRecorder {
		req := httptest.NewRequest(rq.method, "http://"+rq.host+rq.path, strings.NewReader(rq.body))
		req.RemoteAddr = "198.51.100.40:5000"
		if rq.cookie {
			req.AddCookie(&http.Cookie{Name: "admin_access", Value: token})
		}
		if rq.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+rq.bearer)
		}
		if rq.contentType != "" {
			req.Header.Set("Content-Type", rq.contentType)
		}
		for k, v := range rq.headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	code := func(rec *httptest.ResponseRecorder) string {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body.Error
	}
	siblingStatusPage := map[string]string{"Origin": "https://status.example.com", "Sec-Fetch-Site": "same-site"}
	otherPort := map[string]string{"Origin": "http://probara.example.org:3001", "Sec-Fetch-Site": "same-site"}
	ui := map[string]string{"Origin": "https://app.example.com", "Sec-Fetch-Site": "same-origin"}
	createWorkspace := `{"name":"attacker"}`

	refused := map[string]request{
		"cross-site simple POST, JSON in text/plain": {method: http.MethodPost, host: "app.example.com", path: "/api/v1/users",
			contentType: "text/plain", body: createWorkspace, cookie: true,
			headers: map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}},
		"status page on a sibling subdomain": {method: http.MethodPost, host: "app.example.com", path: "/api/v1/tenants",
			contentType: "text/plain", body: createWorkspace, cookie: true, headers: siblingStatusPage},
		"status page on another port of the same host": {method: http.MethodDelete, host: "probara.example.org:3000",
			path: "/api/v1/account/sessions/6f1c1f3e-8f39-4d6a-9d43-0b8a3c6e2b11", cookie: true, headers: otherPort},
		"a status page's script through a same-origin proxy to the API": {method: http.MethodPatch, host: "api.internal:8080",
			path: "/api/v1/status-pages/6f1c1f3e-8f39-4d6a-9d43-0b8a3c6e2b11", contentType: "application/json", body: `{}`, cookie: true,
			headers: map[string]string{"Origin": "https://status.example.com", "Sec-Fetch-Site": "same-origin"}},
		"sign-in from a status page":  {method: http.MethodPost, host: "app.example.com", path: "/api/v1/auth/login", contentType: "text/plain", body: `{"username":"a","password":"b"}`, headers: siblingStatusPage},
		"refresh from a status page":  {method: http.MethodPost, host: "app.example.com", path: "/api/v1/auth/refresh", headers: siblingStatusPage},
		"sign-out from a status page": {method: http.MethodPost, host: "app.example.com", path: "/api/v1/auth/logout", cookie: true, headers: otherPort},
		"OAuth consent from a status page": {method: http.MethodPost, host: "app.example.com", path: "/api/v1/oauth/authorize/decision",
			contentType: "application/json", body: `{}`, cookie: true, headers: siblingStatusPage},
	}
	for name, rq := range refused {
		if rec := send(rq); rec.Code != http.StatusForbidden || code(rec) != apimiddleware.CrossOriginRefusedCode {
			t.Errorf("%s: %d %s, want 403 %s", name, rec.Code, rec.Body, apimiddleware.CrossOriginRefusedCode)
		}
	}

	// Allowed past the origin check: answered by whatever comes next (here
	// the unreachable database), never the refusal.
	allowed := map[string]request{
		"operator UI origin": {method: http.MethodPost, host: "api.internal:8080", path: "/api/v1/tenants",
			contentType: "application/json", body: createWorkspace, cookie: true, headers: ui},
		"same origin as the API": {method: http.MethodPost, host: "probara.example.org", path: "/api/v1/tenants",
			contentType: "application/json", body: createWorkspace, cookie: true,
			headers: map[string]string{"Origin": "https://probara.example.org"}},
		"cookie-jar client (no Origin, Sec-Fetch-Site or Referer)": {method: http.MethodPost, host: "api.internal:8080",
			path: "/api/v1/tenants", contentType: "application/json", body: createWorkspace, cookie: true},
		"API key from anywhere, any body": {method: http.MethodPost, host: "app.example.com", path: "/api/v1/monitors",
			contentType: "text/plain", body: `{}`, bearer: "pk_unknown", headers: siblingStatusPage},
		"sign-in from the UI":      {method: http.MethodPost, host: "api.internal:8080", path: "/api/v1/auth/login", contentType: "application/json", body: `{"username":"a","password":"b"}`, headers: ui},
		"refresh from the UI":      {method: http.MethodPost, host: "api.internal:8080", path: "/api/v1/auth/refresh", headers: ui},
		"sign-out from the UI":     {method: http.MethodPost, host: "api.internal:8080", path: "/api/v1/auth/logout", headers: ui},
		"sign-out with curl":       {method: http.MethodPost, host: "api.internal:8080", path: "/api/v1/auth/logout"},
		"cross-origin session GET": {method: http.MethodGet, host: "app.example.com", path: "/api/v1/tenants", cookie: true, headers: siblingStatusPage},
	}
	for name, rq := range allowed {
		rec := send(rq)
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnsupportedMediaType {
			t.Errorf("%s: %d %s, want it past the origin check", name, rec.Code, rec.Body)
		}
	}

	// A trusted origin still cannot smuggle JSON in a simple-request body.
	rec := send(request{method: http.MethodPost, host: "app.example.com", path: "/api/v1/tenants",
		contentType: "text/plain", body: createWorkspace, cookie: true, headers: ui})
	if rec.Code != http.StatusUnsupportedMediaType || code(rec) != apimiddleware.UnsupportedMediaTypeCode {
		t.Fatalf("text/plain body from the UI: %d %s, want 415", rec.Code, rec.Body)
	}
}

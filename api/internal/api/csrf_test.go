package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
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
		"password reset link from a status page": {method: http.MethodPost, host: "app.example.com", path: "/api/v1/auth/password/reset",
			contentType: "application/json", body: `{"password":"x"}`, headers: siblingStatusPage},
		"sign-in second step from a status page": {method: http.MethodPost, host: "app.example.com", path: "/api/v1/auth/login/mfa",
			contentType: "text/plain", body: `{}`, headers: otherPort},
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
	// The same holds on the anonymous routes that set a session or redeem a link.
	for _, path := range []string{"/api/v1/auth/login", "/api/v1/auth/login/mfa", "/api/v1/auth/password/reset", "/api/v1/auth/invite/accept"} {
		rec := send(request{method: http.MethodPost, host: "app.example.com", path: path,
			contentType: "text/plain", body: `{"password":"x"}`, headers: ui})
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("text/plain body to %s from the UI: %d %s, want 415", path, rec.Code, rec.Body)
		}
	}
}

// With no origin configuration at all (no APP_BASE_URL, PUBLIC_BASE_URL or
// API_ALLOWED_ORIGINS), as a default Helm install reached by port-forward or
// any ingress in front of the frontend: requests arrive from the UI's /api
// proxy with the API's internal Host and the browser-facing host in
// X-Forwarded-Host, from a peer that is trusted or not. A write the proxy
// vouched for (it verified the page is its own origin and dropped Origin,
// Referer and Sec-Fetch-Site, web/lib/api-proxy.ts) passes; every cross-site
// shape the proxy forwards as it came is refused.
func TestCookieWritesThroughTheUIProxyWithoutOriginConfiguration(t *testing.T) {
	const secret = "csrf-proxy-secret"
	const trustedProxy, untrustedPod = "10.20.0.5:41000", "10.42.0.7:41000"
	h := newEdgeTestServer(t, func(cfg *config.APIConfig) {
		cfg.AdminJWTSecret = secret
		cfg.AdminAccessTTLMinutes = 15
		cfg.AdminRefreshTTLDays = 30
		cfg.TrustedProxyCIDRs = []netip.Prefix{netip.MustParsePrefix("10.20.0.5/32")}
	})
	token, err := auth.CreateAdminToken("6f1c1f3e-8f39-4d6a-9d43-0b8a3c6e2b11", "", secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// send is one request as the UI proxy forwards it: internal Host,
	// X-Forwarded-Host/Proto set from what the browser used.
	send := func(peer, method, path, forwardedHost string, cookie bool, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://probara-api:8080"+path, strings.NewReader(`{"name":"x"}`))
		req.RemoteAddr = peer
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-Host", forwardedHost)
		req.Header.Set("X-Forwarded-Proto", "http")
		if cookie {
			req.AddCookie(&http.Cookie{Name: "admin_access", Value: token})
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	type write struct {
		method, path string
		cookie       bool
	}
	writes := map[string]write{
		"sign-in":          {http.MethodPost, "/api/v1/auth/login", false},
		"refresh":          {http.MethodPost, "/api/v1/auth/refresh", false},
		"sign-out":         {http.MethodPost, "/api/v1/auth/logout", true},
		"workspace create": {http.MethodPost, "/api/v1/tenants", true},
		"OAuth consent":    {http.MethodPost, "/api/v1/oauth/authorize/decision", true},
		// Account lane: the sign-in's second step and the mailed-link
		// redemptions (no cookie; they act on an account), and the signed-in
		// user's own account writes.
		"sign-in second step":    {http.MethodPost, "/api/v1/auth/login/mfa", false},
		"forgot password":        {http.MethodPost, "/api/v1/auth/password/forgot", false},
		"password reset link":    {http.MethodPost, "/api/v1/auth/password/reset", false},
		"invite accept":          {http.MethodPost, "/api/v1/auth/invite/accept", false},
		"email change confirm":   {http.MethodPost, "/api/v1/auth/email/confirm", false},
		"own password change":    {http.MethodPost, "/api/v1/account/password", true},
		"own two-factor disable": {http.MethodPost, "/api/v1/account/two-factor/disable", true},
		"own sessions revoke":    {http.MethodPost, "/api/v1/account/sessions/revoke-others", true},
		"superadmin invite":      {http.MethodPost, "/api/v1/users/6f1c1f3e-8f39-4d6a-9d43-0b8a3c6e2b11/invite", true},
	}

	for _, peer := range []string{untrustedPod, trustedProxy} {
		for _, browserHost := range []string{"localhost:3000", "localhost:18080", "127.0.0.1:3000", "192.168.1.20:3000", "probara.example.com"} {
			for name, wr := range writes {
				// Vouched for by the proxy: no Origin, Referer or Sec-Fetch-Site.
				if rec := send(peer, wr.method, wr.path, browserHost, wr.cookie, nil); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnsupportedMediaType {
					t.Errorf("%s via the UI proxy at %s (peer %s): %d %s", name, browserHost, peer, rec.Code, rec.Body)
				}
			}
		}
	}
	// A trusted proxy's forwarded host is the API's own origin, so even a
	// proxy that passes Origin through works.
	if rec := send(trustedProxy, http.MethodPost, "/api/v1/tenants", "localhost:3000", true,
		map[string]string{"Origin": "http://localhost:3000", "Sec-Fetch-Site": "same-origin"}); rec.Code == http.StatusForbidden {
		t.Errorf("trusted proxy, Origin kept: %d %s", rec.Code, rec.Body)
	}

	// What the proxy forwards unchanged: a page on another origin. The
	// forwarded host is the one the browser sent to (the UI), or -- through
	// a same-origin proxy on the attacker's origin that passes client headers
	// (the status page's /_sp_api) -- whatever the attacker's script set.
	attacks := map[string]struct {
		forwardedHost string
		headers       map[string]string
	}{
		"status page on another port of the UI host": {"localhost:3000",
			map[string]string{"Origin": "http://localhost:3001", "Sec-Fetch-Site": "same-site"}},
		"status page on a sibling subdomain": {"app.example.com",
			map[string]string{"Origin": "https://status.example.com", "Sec-Fetch-Site": "same-site"}},
		"cross-site page": {"app.example.com",
			map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}},
		"script setting X-Forwarded-Host through a same-origin proxy": {"localhost:8082",
			map[string]string{"Origin": "http://localhost:8082", "Sec-Fetch-Site": "same-origin"}},
		// The UI proxy reached through something that forwards client
		// headers: it refuses to vouch (Host is its internal name) and sends
		// that Host on as the forwarded host, never the script's value.
		"script reaching the UI proxy through a header-forwarding proxy": {"probara-frontend:3000",
			map[string]string{"Origin": "https://status.example.com", "Sec-Fetch-Site": "same-origin"}},
		"opaque origin (sandboxed frame)": {"localhost:3000",
			map[string]string{"Origin": "null", "Sec-Fetch-Site": "cross-site"}},
		"no Origin, cross-site fetch metadata": {"localhost:3000",
			map[string]string{"Sec-Fetch-Site": "same-site"}},
		"only a foreign Referer": {"localhost:3000",
			map[string]string{"Referer": "http://localhost:3001/public/status/x"}},
	}
	for name, a := range attacks {
		for _, peer := range []string{untrustedPod} {
			for wname, wr := range writes {
				rec := send(peer, wr.method, wr.path, a.forwardedHost, wr.cookie, a.headers)
				if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), apimiddleware.CrossOriginRefusedCode) {
					t.Errorf("%s, %s (peer %s): %d %s, want 403 %s", name, wname, peer, rec.Code, rec.Body, apimiddleware.CrossOriginRefusedCode)
				}
			}
		}
	}
	// Through the trusted proxy the forwarded host is the UI's, so every
	// shape with another Origin is refused too.
	for name, a := range attacks {
		if a.forwardedHost == "localhost:8082" {
			continue // the UI proxy (the trusted peer) never passes a client X-Forwarded-Host on
		}
		if rec := send(trustedProxy, http.MethodPost, "/api/v1/tenants", a.forwardedHost, true, a.headers); rec.Code != http.StatusForbidden {
			t.Errorf("%s via the trusted proxy: %d %s, want 403", name, rec.Code, rec.Body)
		}
	}
	// The refusal names the origin and the setting that would trust it.
	rec := send(untrustedPod, http.MethodPost, "/api/v1/auth/login", "localhost:3000", false,
		map[string]string{"Origin": "http://localhost:3001"})
	if !strings.Contains(rec.Body.String(), "http://localhost:3001") || !strings.Contains(rec.Body.String(), "APP_BASE_URL") {
		t.Errorf("refusal body: %s", rec.Body)
	}
}

package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/logger"
)

// The operator UI at https://app.example.com, the API reached on the same
// host through its /api path, a status page on a sibling subdomain and one on
// another port of the UI's host.
func testOriginPolicy() *OriginPolicy {
	return NewOriginPolicy("https://app.example.com/", "", []string{"https://ops.example.net"},
		[]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, nil)
}

func originRequest(method, host string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(method, "http://"+host+"/api/v1/monitors", strings.NewReader(`{}`))
	req.RemoteAddr = "198.51.100.7:4000"
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func TestOriginPolicyAllows(t *testing.T) {
	p := testOriginPolicy()
	cases := []struct {
		name    string
		host    string
		remote  string
		headers map[string]string
		want    bool
	}{
		{"operator UI origin (APP_BASE_URL)", "api.internal:8080", "", map[string]string{"Origin": "https://app.example.com"}, true},
		{"explicit default port", "api.internal:8080", "", map[string]string{"Origin": "https://app.example.com:443"}, true},
		{"API_ALLOWED_ORIGINS entry", "api.internal:8080", "", map[string]string{"Origin": "https://OPS.example.net"}, true},
		{"same origin as the request's Host", "probara.example.org", "", map[string]string{"Origin": "https://probara.example.org"}, true},
		{"same-origin to a proxy on another origin is not proof", "api.internal:8080", "", map[string]string{
			"Origin": "http://localhost:8082", "Sec-Fetch-Site": "same-origin"}, false},
		{"sibling subdomain (status page)", "app.example.com", "", map[string]string{
			"Origin": "https://status.example.com", "Sec-Fetch-Site": "same-site"}, false},
		{"same host, other port (status page)", "probara.example.org:3000", "", map[string]string{
			"Origin": "http://probara.example.org:3001", "Sec-Fetch-Site": "same-site"}, false},
		{"cross-site", "probara.example.org", "", map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}, false},
		{"opaque origin", "probara.example.org", "", map[string]string{"Origin": "null"}, false},
		{"forwarded host from a trusted proxy", "api.internal:8080", "10.1.2.3:5000", map[string]string{
			"Origin": "https://ui.example.org", "X-Forwarded-Host": "ui.example.org", "X-Forwarded-Proto": "https"}, true},
		{"forwarded host from an untrusted peer is ignored", "api.internal:8080", "", map[string]string{
			"Origin": "https://evil.example", "X-Forwarded-Host": "evil.example"}, false},
		{"known HTTPS refuses an http origin on the same host", "ui.example.org", "10.1.2.3:5000", map[string]string{
			"Origin": "http://ui.example.org", "X-Forwarded-Proto": "https"}, false},
		{"no Origin, Sec-Fetch-Site same-origin", "api.internal:8080", "", map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		{"no Origin, Sec-Fetch-Site same-site", "api.internal:8080", "", map[string]string{"Sec-Fetch-Site": "same-site"}, false},
		{"only a Referer from the UI", "api.internal:8080", "", map[string]string{"Referer": "https://app.example.com/monitors"}, true},
		{"only a Referer from a status page", "api.internal:8080", "", map[string]string{"Referer": "https://status.example.com/"}, false},
		{"none of the three: a non-browser client (curl with a cookie jar)", "api.internal:8080", "", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := originRequest(http.MethodPost, c.host, c.headers)
			if c.remote != "" {
				req.RemoteAddr = c.remote
			}
			if got := p.Allows(req); got != c.want {
				t.Fatalf("Allows = %v, want %v", got, c.want)
			}
		})
	}

	// A nil policy (no configuration) still trusts the API's own origin only.
	var none *OriginPolicy
	if !none.Allows(originRequest(http.MethodPost, "probara.example.org", map[string]string{"Origin": "https://probara.example.org"})) {
		t.Fatal("nil policy refused the request's own origin")
	}
	if none.Allows(originRequest(http.MethodPost, "probara.example.org", map[string]string{"Origin": "https://app.example.com"})) {
		t.Fatal("nil policy trusted another origin")
	}
}

func TestRequireTrustedOrigin(t *testing.T) {
	h := testOriginPolicy().RequireTrustedOrigin(statusHandler(http.StatusNoContent))
	cross := map[string]string{"Origin": "https://status.example.com", "Sec-Fetch-Site": "same-site"}
	for _, path := range []string{loginPath, refreshPath, "/api/v1/auth/logout"} {
		req := originRequest(http.MethodPost, "app.example.com", cross)
		req.URL.Path = path
		rec := do(h, req)
		if rec.Code != http.StatusForbidden || errorCodeOf(t, rec) != CrossOriginRefusedCode {
			t.Errorf("%s from a status page: %d %s, want 403 %s", path, rec.Code, rec.Body, CrossOriginRefusedCode)
		}
		req = originRequest(http.MethodPost, "api.internal", map[string]string{"Origin": "https://app.example.com"})
		req.URL.Path = path
		if rec := do(h, req); rec.Code != http.StatusNoContent {
			t.Errorf("%s from the UI: %d", path, rec.Code)
		}
	}
	// Reads are never refused.
	if rec := do(h, originRequest(http.MethodGet, "app.example.com", cross)); rec.Code != http.StatusNoContent {
		t.Fatalf("GET from a status page: %d", rec.Code)
	}
}

func errorCodeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error
}

// AuthMiddleware applies the policy to requests its session cookie
// authenticates, before any lookup. The cookie here has no session id and
// the request names a malformed workspace, so a request that passes the
// origin and body checks is answered 400 without touching the (absent)
// database -- which tells the cases apart.
func TestAuthMiddlewareRefusesCrossOriginCookieWrites(t *testing.T) {
	const secret = "csrf-test-secret"
	mw := AuthMiddleware(nil, logger.New("test", "error"), secret, testOriginPolicy())
	h := mw(statusHandler(http.StatusOK))
	token, err := auth.CreateAdminToken("6f1c1f3e-8f39-4d6a-9d43-0b8a3c6e2b11", "", secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	send := func(method, contentType, body string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://app.example.com/api/v1/tenants", strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		req.Header.Set("X-Tenant-ID", "not-a-uuid")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return do(h, req)
	}
	statusPage := map[string]string{"Origin": "https://status.example.com", "Sec-Fetch-Site": "same-site"}
	ui := map[string]string{"Origin": "https://app.example.com", "Sec-Fetch-Site": "same-origin"}

	// The attack: a status-page script's simple request, JSON in a text/plain body.
	if rec := send(http.MethodPost, "text/plain;charset=UTF-8", `{"name":"x"}`, statusPage); rec.Code != http.StatusForbidden || errorCodeOf(t, rec) != CrossOriginRefusedCode {
		t.Fatalf("cross-origin text/plain POST: %d %s", rec.Code, rec.Body)
	}
	if rec := send(http.MethodDelete, "", "", statusPage); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin DELETE: %d", rec.Code)
	}
	// The UI and a cookie-jar client pass on to the identity checks.
	if rec := send(http.MethodPost, "application/json", `{}`, ui); rec.Code != http.StatusBadRequest {
		t.Fatalf("UI JSON POST: %d %s", rec.Code, rec.Body)
	}
	if rec := send(http.MethodPost, "application/json; charset=utf-8", `{}`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("cookie-jar JSON POST: %d %s", rec.Code, rec.Body)
	}
	if rec := send(http.MethodPost, "", "", ui); rec.Code != http.StatusBadRequest {
		t.Fatalf("UI bodiless POST: %d %s", rec.Code, rec.Body)
	}
	// Even from a trusted origin, a body must be JSON.
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", ""} {
		if rec := send(http.MethodPost, ct, `{"name":"x"}`, ui); rec.Code != http.StatusUnsupportedMediaType || errorCodeOf(t, rec) != UnsupportedMediaTypeCode {
			t.Errorf("UI POST with %q: %d %s, want 415", ct, rec.Code, rec.Body)
		}
	}
	// Reads are not checked.
	if rec := send(http.MethodGet, "", "", statusPage); rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-origin GET: %d", rec.Code)
	}
}

func TestAcceptableBodyMultipartOnlyOnUploadRoutes(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, MultipartUploadPaths[0], strings.NewReader("--x--"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	if !acceptableBody(req) {
		t.Fatal("multipart refused on the import preview")
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/monitors", strings.NewReader("--x--"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	if acceptableBody(req) {
		t.Fatal("multipart accepted on a JSON route")
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/monitors", strings.NewReader("{}"))
	req.ContentLength = -1 // chunked: a body of unknown length
	if acceptableBody(req) {
		t.Fatal("a chunked body without a content type was accepted")
	}
}

// The refusal tells an operator what was refused and what would fix it,
// showing the origin normalized, never the raw header.
func TestCrossOriginRefusalNamesOriginAndSetting(t *testing.T) {
	h := NewOriginPolicy("", "", nil, nil, nil).RequireTrustedOrigin(statusHandler(http.StatusNoContent))
	for origin, want := range map[string]string{
		"http://localhost:3000":      "origin http://localhost:3000 ",
		"HTTPS://Status.Example:443": "origin https://status.example ",
		"null":                       "origin null (an opaque origin)",
		"javascript:\"<x>":           "origin (unreadable)",
	} {
		rec := do(h, originRequest(http.MethodPost, "probara-api:8080", map[string]string{"Origin": origin}))
		var body struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusForbidden || !strings.Contains(body.Message, want) || !strings.Contains(body.Message, "APP_BASE_URL") {
			t.Errorf("Origin %q: %d %q, want the message to name %q and APP_BASE_URL", origin, rec.Code, body.Message, want)
		}
	}
}

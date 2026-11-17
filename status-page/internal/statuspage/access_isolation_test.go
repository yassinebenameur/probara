package statuspage

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/pageaccess"
)

// Another status page on the same origin (a custom template runs script) must
// not read or frame a gated page with the visitor's session for it.
func TestAccess_CrossPageRequestsRefused(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	session := f.tokenSession("internal")
	const own = "http://example.com/public/status/internal"
	const other = "http://example.com/public/status/open/"
	cases := []struct {
		name             string
		site, mode, dest string
		referer          string
		wantCode         int
	}{
		{"the page's own refresh", "same-origin", "cors", "empty", own, http.StatusOK},
		{"another page's fetch", "same-origin", "cors", "empty", other, http.StatusNotFound},
		{"another page's fetch without a Referer", "same-origin", "cors", "empty", "", http.StatusNotFound},
		{"same-site load from another page", "same-site", "no-cors", "image", other, http.StatusNotFound},
		{"another page frames it", "same-origin", "navigate", "iframe", other, http.StatusNotFound},
		{"framed without a Referer", "same-origin", "navigate", "iframe", "", http.StatusNotFound},
		{"the operator UI's preview frame", "same-origin", "navigate", "iframe", "http://example.com/status-pages/x/template", http.StatusOK},
		{"a link from another page", "same-origin", "navigate", "document", other, http.StatusOK},
		{"typed or bookmarked", "none", "navigate", "document", "", http.StatusOK},
		{"an old browser, Referer another page", "", "", "", other, http.StatusNotFound},
		{"an old browser, no Referer", "", "", "", "", http.StatusOK},
		{"another host's page", "cross-site", "navigate", "document", "https://elsewhere.example/public/status/open", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/public/status/internal", nil)
			req.AddCookie(session)
			for k, v := range map[string]string{"Sec-Fetch-Site": c.site, "Sec-Fetch-Mode": c.mode, "Sec-Fetch-Dest": c.dest, "Referer": c.referer} {
				if v != "" {
					req.Header.Set(k, v)
				}
			}
			rec := f.do(req)
			if rec.Code != c.wantCode {
				t.Fatalf("= %d, want %d", rec.Code, c.wantCode)
			}
			if c.wantCode != http.StatusOK && strings.Contains(rec.Body.String(), pageSentinel) {
				t.Fatal("refused response carries page content")
			}
		})
	}

	// Every sub-route goes through the same check (asked of the gate
	// directly: no service sits behind this fixture to serve them).
	for _, row := range routeTable {
		if row.class != classGated || row.method == "" {
			continue
		}
		req := httptest.NewRequest(row.method, "/public/status/internal"+row.route, nil)
		req.AddCookie(session)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Referer", other)
		slug, rest := splitStatusPath(strings.TrimPrefix(req.URL.Path, "/public/status/"))
		if _, _, ok := f.h.authorize(httptest.NewRecorder(), req, slug, rest); ok {
			t.Errorf("another page reached %s", row.route)
		}
	}

	// A password page refuses the same way (with its own denial).
	req := httptest.NewRequest(http.MethodGet, "/public/status/locked/data", nil)
	req.AddCookie(f.passwordSession(2))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Referer", other)
	if rec := f.do(req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("password page /data from another page = %d, want 401", rec.Code)
	}

	// Public pages are readable by anyone anyway.
	req = httptest.NewRequest(http.MethodGet, "/public/status/open", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Referer", own)
	if rec := f.do(req); rec.Code != http.StatusOK {
		t.Fatalf("public page from another page = %d", rec.Code)
	}
}

func TestAccess_GatedResponseHeaders(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	f.h.config.AppBaseURL = "https://ops.example.com"
	gated := f.get("/public/status/locked", f.passwordSession(2))
	if gated.Code != http.StatusOK {
		t.Fatalf("= %d", gated.Code)
	}
	// Only the operator UI may frame it: 'self' would admit every other page
	// on this origin.
	if csp := gated.Header().Get("Content-Security-Policy"); !strings.HasSuffix(csp, "frame-ancestors https://ops.example.com") {
		t.Fatalf("gated frame-ancestors: %q", csp)
	}
	if gated.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" || gated.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("gated headers: %v", gated.Header())
	}
	if csp := f.get("/public/status/locked").Header().Get("Content-Security-Policy"); !strings.HasSuffix(csp, "frame-ancestors https://ops.example.com") {
		t.Fatalf("unlock page frame-ancestors: %q", csp)
	}
	if gatedFrameAncestors("") != nil {
		t.Fatal("without APP_BASE_URL a gated page must not be frameable at all")
	}

	// The helper add-on routes rely on: a route's own CORS grant and
	// validator never survive on a gated response.
	rec := httptest.NewRecorder()
	aw := &accessWriter{ResponseWriter: rec, noStore: true}
	aw.Header().Set("Access-Control-Allow-Origin", "*")
	aw.Header().Set("ETag", `"x"`)
	aw.Header().Set("Cache-Control", "public, max-age=60")
	aw.WriteHeader(http.StatusOK)
	if !aw.gated() || rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("ETag") != "" ||
		rec.Header().Get("Cache-Control") != "private, no-store" || rec.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Fatalf("gated writer headers: %v", rec.Header())
	}
}

func TestAccess_SessionCookieSecureSetting(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name   string
		cfg    config.StatusPageConfig
		https  bool
		secure bool
	}{
		{"https base URL behind a TLS proxy", config.StatusPageConfig{StatusPageBaseURL: "https://status.example.com"}, false, true},
		{"explicit off wins", config.StatusPageConfig{StatusPageBaseURL: "https://status.example.com", CookieSecure: &no}, true, false},
		{"explicit on", config.StatusPageConfig{CookieSecure: &yes}, false, true},
		{"detected over plain http", config.StatusPageConfig{StatusPageBaseURL: "http://localhost:8082"}, false, false},
		{"detected over TLS", config.StatusPageConfig{}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := c.cfg
			cfg.SessionSecret = testSessionSecret
			h := NewHandlers(nil, &cfg, logger.New("status-page", "error"), NewHub(), newRenderCache(0), nil)
			h.access.store = newAccessFixture(t, testSessionSecret).store
			req := httptest.NewRequest(http.MethodPost, "/public/status/locked/unlock", strings.NewReader("password=open+sesame"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if c.https {
				req.TLS = &tls.ConnectionState{}
			}
			rec := httptest.NewRecorder()
			h.HandleStatusPage(rec, req)
			if got := findCookie(t, rec, sessionCookieName).Secure; got != c.secure {
				t.Fatalf("Secure = %v, want %v", got, c.secure)
			}
		})
	}
}

// Every stream re-checks on its heartbeat: one opened while the page was
// public ends when the page turns private, even with no access event.
func TestAccess_PublicStreamEndsWhenPageTurnsPrivate(t *testing.T) {
	prev := streamHeartbeatInterval
	streamHeartbeatInterval = 20 * time.Millisecond
	defer func() { streamHeartbeatInterval = prev }()

	f := newAccessFixture(t, testSessionSecret)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/public/status/open/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		f.h.HandleStatusPage(rec, req)
		close(done)
	}()
	time.Sleep(60 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("public stream ended on its own")
	default:
	}
	f.store.set("open", func(p *pageAccess) { p.Visibility = pageaccess.Private })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream outlived the page turning private")
	}
}

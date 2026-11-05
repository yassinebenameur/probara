package statuspage

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/securityheaders"
	"github.com/yassinebenameur/probara/shared/statustemplate"
)

var headerNonce = regexp.MustCompile(`script-src 'nonce-([A-Za-z0-9+/=]+)'`)

// renderAndApply renders data and applies its CSP the way HandleStatusPage
// does, returning the response body and headers.
func renderAndApply(t *testing.T, data *StatusPageData, appBaseURL string) (string, http.Header) {
	t.Helper()
	html, _, err := renderStatusPageHTML(data, false)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	body := pageCSPFor(data, appBaseURL, strings.TrimSpace(data.CustomTemplateSource) != "").apply(h, html)
	return body, h
}

func TestStatusPageNonceMatchesHeader(t *testing.T) {
	data := customTemplateTestData()
	data.AllowedEmbedOrigins = []string{"https://intranet.example.com"}

	body, h := renderAndApply(t, data, "https://ops.example.com")
	policy := h.Get(securityheaders.HeaderCSP)
	m := headerNonce.FindStringSubmatch(policy)
	if m == nil {
		t.Fatalf("default page should be strict with a nonce, got policy %q", policy)
	}
	nonce := m[1]
	if !strings.Contains(body, `<script nonce="`+nonce+`">`) {
		t.Fatalf("rendered page does not carry the header nonce %q", nonce)
	}
	if strings.Contains(body, statustemplate.NoncePlaceholder) {
		t.Fatal("nonce placeholder leaked into the response")
	}
	if !strings.Contains(policy, "frame-ancestors 'self' https://ops.example.com https://intranet.example.com") {
		t.Fatalf("frame-ancestors missing the operator UI or embed origin: %q", policy)
	}

	// Every response gets its own nonce.
	body2, h2 := renderAndApply(t, data, "https://ops.example.com")
	m2 := headerNonce.FindStringSubmatch(h2.Get(securityheaders.HeaderCSP))
	if m2 == nil || m2[1] == nonce || !strings.Contains(body2, `nonce="`+m2[1]+`"`) {
		t.Fatalf("second response reused or mismatched the nonce: %v", m2)
	}
}

func TestStatusPageCSPModes(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*StatusPageData)
		wantMode string
	}{
		{"plain page is strict", func(*StatusPageData) {}, securityheaders.CSPModeStrict},
		{"custom head HTML is compatible", func(d *StatusPageData) { d.CustomHeadHTML = `<script>track()</script>` }, securityheaders.CSPModeCompatible},
		{"custom footer HTML is compatible", func(d *StatusPageData) { d.CustomFooterHTML = `<div>hi</div>` }, securityheaders.CSPModeCompatible},
		// Regression: a page customized only through CSS (remote @import,
		// @font-face) must not turn strict on upgrade.
		{"custom CSS is compatible", func(d *StatusPageData) {
			d.CustomCSS = `@import url(https://fonts.googleapis.com/css2?family=Inter);`
		}, securityheaders.CSPModeCompatible},
		{"custom template is compatible", func(d *StatusPageData) {
			d.CustomTemplateSource = `<html><body>{{.Title}}</body></html>`
			d.CustomTemplateVersion = 7
		}, securityheaders.CSPModeCompatible},
		{"explicit strict wins over custom HTML", func(d *StatusPageData) {
			d.CustomHeadHTML = `<script nonce="{{cspNonce}}">ok()</script>`
			d.CSPMode = "strict"
		}, securityheaders.CSPModeStrict},
		{"explicit off", func(d *StatusPageData) { d.CSPMode = "off" }, securityheaders.CSPModeOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := customTemplateTestData()
			tt.mutate(data)
			_, h := renderAndApply(t, data, "")
			policy := h.Get(securityheaders.HeaderCSP)
			switch tt.wantMode {
			case securityheaders.CSPModeStrict:
				if !headerNonce.MatchString(policy) {
					t.Fatalf("want strict, got %q", policy)
				}
			case securityheaders.CSPModeCompatible:
				if !strings.Contains(policy, "script-src 'self' 'unsafe-inline' https: http:") {
					t.Fatalf("want compatible, got %q", policy)
				}
			case securityheaders.CSPModeOff:
				if policy != "frame-ancestors 'self'" {
					t.Fatalf("want frame-ancestors only, got %q", policy)
				}
			}
		})
	}
}

func TestCustomHeadHTMLReceivesNonce(t *testing.T) {
	data := customTemplateTestData()
	data.CSPMode = "strict"
	data.CustomHeadHTML = `<script nonce="{{cspNonce}}">window.acme = 1</script>`

	body, h := renderAndApply(t, data, "")
	m := headerNonce.FindStringSubmatch(h.Get(securityheaders.HeaderCSP))
	if m == nil {
		t.Fatal("expected a strict policy")
	}
	if !strings.Contains(body, `<script nonce="`+m[1]+`">window.acme = 1</script>`) {
		t.Fatal("custom head HTML's {{cspNonce}} was not replaced with the response nonce")
	}
}

func TestCustomTemplateCSPNonceFunction(t *testing.T) {
	data := customTemplateTestData()
	data.CSPMode = "strict"
	data.CustomTemplateSource = `<html><head><style nonce="{{cspNonce}}">h1{}</style></head><body><h1>{{.Title}}</h1><script nonce="{{cspNonce}}">go()</script></body></html>`
	data.CustomTemplateVersion = 3

	body, h := renderAndApply(t, data, "")
	m := headerNonce.FindStringSubmatch(h.Get(securityheaders.HeaderCSP))
	if m == nil {
		t.Fatal("expected a strict policy")
	}
	if strings.Count(body, `nonce="`+m[1]+`"`) != 2 {
		t.Fatalf("custom template should carry the nonce twice: %s", body)
	}
}

func TestStatusPageServerSecurityHeaders(t *testing.T) {
	sqlDB, err := sql.Open("postgres", "postgres://unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	cfg := &config.StatusPageConfig{}
	cfg.ServiceName = "status-page-test"
	s := NewServer(cfg, logger.New("status-page-test", "error"), metrics.NewRegistry("status-page-test"), &db.Client{DB: sqlDB})

	for _, path := range []string{"/healthz", "/public/status/sw.js"} {
		rec := httptest.NewRecorder()
		s.http.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("%s headers = %v", path, rec.Header())
		}
	}
}

func TestRenderedPageETagCoversPolicy(t *testing.T) {
	a := renderedPage{html: "<html></html>", csp: pageCSP{mode: "strict", frameAncestors: []string{"'self'"}}}
	b := a
	b.csp.frameAncestors = []string{"'self'", "https://intranet.example.com"}
	c := a
	c.csp.mode = "off"
	if a.etag() == b.etag() || a.etag() == c.etag() {
		t.Fatal("a policy change must change the ETag, or a 304 keeps the old header")
	}
}

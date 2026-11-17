package statuspage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/pageaccess"
	"github.com/yassinebenameur/probara/shared/statustemplate"
)

const testSessionSecret = "test-session-secret-0123456789abcdef"

// addonRouteTestID is a component ID for the monitor add-on routes.
const addonRouteTestID = "00000000-0000-0000-0000-000000000001"

// allPublicAccess answers every slug as a public page.
type allPublicAccess struct{}

func (allPublicAccess) PageAccess(_ context.Context, slug, _ string, _ uuid.UUID) (*pageAccess, error) {
	return &pageAccess{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(slug)), Title: slug, Visibility: pageaccess.Public}, nil
}

func (allPublicAccess) PageVisibilityByID(context.Context, uuid.UUID) (string, error) {
	return pageaccess.Public, nil
}

// fakeAccess is an in-memory accessQuery with the same token semantics as
// the SQL: a presented hash resolves to a live token of that page, and a
// session token is live while it belongs to the page and is not revoked.
type fakeAccess struct {
	mu      sync.Mutex
	pages   map[string]*pageAccess // by slug
	tokens  map[string]fakeToken   // by hash
	failErr error
}

type fakeToken struct {
	id      uuid.UUID
	pageID  uuid.UUID
	revoked bool
}

func (f *fakeAccess) PageAccess(_ context.Context, slug, presentedHash string, sessionTokenID uuid.UUID) (*pageAccess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failErr != nil {
		return nil, f.failErr
	}
	p, ok := f.pages[slug]
	if !ok {
		return nil, ErrStatusPageNotFound
	}
	cp := *p
	if t, ok := f.tokens[presentedHash]; ok && t.pageID == p.ID && !t.revoked {
		cp.PresentedTokenID = t.id
	}
	for _, t := range f.tokens {
		if t.id == sessionTokenID && t.pageID == p.ID && !t.revoked {
			cp.SessionTokenLive = true
		}
	}
	return &cp, nil
}

func (f *fakeAccess) PageVisibilityByID(_ context.Context, id uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.pages {
		if p.ID == id {
			return p.Visibility, nil
		}
	}
	return "", ErrStatusPageNotFound
}

func (f *fakeAccess) set(slug string, mutate func(p *pageAccess)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mutate(f.pages[slug])
}

func (f *fakeAccess) revokeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for hash, t := range f.tokens {
		t.revoked = true
		f.tokens[hash] = t
	}
}

type accessFixture struct {
	h        *Handlers
	store    *fakeAccess
	password string
	token    string // a live token of "internal"
	tokenID  uuid.UUID
	logs     *bytes.Buffer
}

// pageSentinel is the cached body of every fixture page: seeing it in a
// response means the page's content was served.
const pageSentinel = "PAGE-CONTENT-SENTINEL"

// fixturePasswordHash is hashed once: bcrypt at the production cost is slow,
// and much slower under -race.
var fixturePasswordHash = sync.OnceValue(func() string {
	hash, err := pageaccess.HashPassword("open sesame")
	if err != nil {
		panic(err)
	}
	return hash
})

func newAccessFixture(t *testing.T, secret string) *accessFixture {
	t.Helper()
	hash := fixturePasswordHash()
	internalID := uuid.New()
	token, tokenHash, err := pageaccess.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	tokenID := uuid.New()
	store := &fakeAccess{
		pages: map[string]*pageAccess{
			"open":     {ID: uuid.New(), Title: "Open", Visibility: pageaccess.Public},
			"hidden":   {ID: uuid.New(), Title: "Hidden", Visibility: pageaccess.Unlisted},
			"locked":   {ID: uuid.New(), Title: "Locked <Corp>", Visibility: pageaccess.Password, PasswordHash: hash, PasswordVersion: 2, PrimaryColor: "#123abc"},
			"internal": {ID: internalID, Title: "Internal", Visibility: pageaccess.Private},
		},
		tokens: map[string]fakeToken{tokenHash: {id: tokenID, pageID: internalID}},
	}
	cfg := &config.StatusPageConfig{SessionSecret: secret}
	cfg.TrustedProxyCIDRs = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	log := logger.New("status-page", "debug")
	logs := &bytes.Buffer{}
	log.SetOutput(logs)
	h := NewHandlers(nil, cfg, log, NewHub(), newRenderCache(time.Hour), nil)
	h.access.store = store
	// Every page already rendered and cached, as after a visit while it
	// was public: the gate, not the cache, must decide.
	for slug, p := range store.pages {
		html := "<!DOCTYPE html><html><head><title>t</title></head><body><header>h</header>" + pageSentinel + "</body></html>"
		visibility := p.Visibility
		_, _, _ = h.cache.GetPage(slug, func() (renderedPage, error) {
			return renderedPage{html: html, visibility: visibility}, nil
		})
	}
	return &accessFixture{h: h, store: store, password: "open sesame", token: token, tokenID: tokenID, logs: logs}
}

func (f *accessFixture) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.h.HandleStatusPage(rec, req)
	return rec
}

func (f *accessFixture) get(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	return f.do(req)
}

func (f *accessFixture) session(slug string, cred sessionCredential, expires time.Time) *http.Cookie {
	return &http.Cookie{Name: sessionCookieName, Value: f.h.access.sessions.sign(f.store.pages[slug].ID, cred, expires)}
}

func (f *accessFixture) tokenSession(slug string) *http.Cookie {
	return f.session(slug, sessionCredential{kind: sessionKindToken, tokenID: f.tokenID}, time.Now().Add(time.Hour))
}

func (f *accessFixture) passwordSession(version int) *http.Cookie {
	return f.session("locked", sessionCredential{kind: sessionKindPassword, passwordVersion: version}, time.Now().Add(time.Hour))
}

// routeClass is how the access gate treats a route.
type routeClass string

const (
	// classGated: served only after authorize; a private page without a
	// credential answers exactly like a missing page.
	classGated routeClass = "gated"
	// classUnlock: exempt from authorize because authorize sends visitors
	// there; answers a private page and a missing one identically itself.
	classUnlock routeClass = "unlock"
	// classPreviewToken: gated, or opened by its own preview token.
	classPreviewToken routeClass = "gated-or-preview-token"
	// classNoPageData: outside the per-page dispatcher and serves nothing of
	// any page (the service worker is a static script; push is off on gated
	// pages, so it needs no exemption from the gate either).
	classNoPageData routeClass = "no-page-data"
	// classOperational: health and metrics, never page content.
	classOperational routeClass = "operational"
	// classProxyGated: the /_sp_api proxy, gated by authorizeProxy.
	classProxyGated routeClass = "proxy-gated"
	// classDispatcher: the per-page dispatcher itself.
	classDispatcher routeClass = "dispatcher"
)

// routeTable is every route the status-page service serves: the server mux
// patterns and every sub-route the per-page dispatcher matches. Adding a
// route without a row here fails TestRouteAccess_EveryRouteIsClassified.
// Rows with a method are exercised against every visibility below.
var routeTable = []struct {
	route  string
	class  routeClass
	method string
}{
	// server.go mux patterns.
	{"/healthz", classOperational, ""},
	{"/readyz", classOperational, ""},
	{"/metrics", classOperational, ""},
	{"/_sp_api/", classProxyGated, ""},
	{"/public/status/sw.js", classNoPageData, ""},
	{"/public/status/", classDispatcher, ""},

	// HandleStatusPage sub-routes (after /public/status/{slug}).
	{"", classGated, http.MethodGet},
	{"/", classGated, http.MethodGet},
	{"/data", classGated, http.MethodGet},
	{"/stream", classGated, http.MethodGet},
	{"/preview/draft", classPreviewToken, http.MethodGet},
	{"/push/subscribe", classGated, http.MethodPost},
	{"/push/unsubscribe", classGated, http.MethodPost},
	{"/unlock", classUnlock, http.MethodPost},
	// Page add-ons (addons.go pageAddonRoutes): gated like the page, and
	// private on a gated page (gatedPageRequest).
	{"/summary.json", classGated, http.MethodGet},
	{"/feed.atom", classGated, http.MethodGet},
	{"/badge.svg", classGated, http.MethodGet},
	{"/monitors/" + addonRouteTestID + "/badge.svg", classGated, http.MethodGet},
	{"/monitors/" + addonRouteTestID + "/uptime.svg", classGated, http.MethodGet},
	// A page's push worker on its custom domain (custom_host.go): gated,
	// and not found on a gated page. Every custom-domain path is one of these
	// sub-routes (customHostRouteTable).
	{"/sw.js", classGated, http.MethodGet},
	// Not routed: proves an unknown sub-route is gated too.
	{"/some-future-route.json", classGated, http.MethodGet},
}

// notRoutes are path-like literals the package compares against that are
// not routes of their own (prefixes being stripped, API paths the proxy
// forwards to).
var notRoutes = map[string]bool{
	"/public/status/":       true,
	"/_sp_api":              true,
	"/api/v1/":              true,
	"/api/v1/monitors":      true,
	"/api/v1/status-pages/": true,
	"/":                     true,
}

// TestRouteAccess_EveryRouteIsClassified scans the package's non-test source
// for every path literal a request is matched against (mux patterns, string
// prefix/suffix matchers, == comparisons and switch cases) and fails on any
// that routeTable does not classify.
func TestRouteAccess_EveryRouteIsClassified(t *testing.T) {
	classified := map[string]bool{}
	for _, row := range routeTable {
		if row.class == "" {
			t.Fatalf("route %q has no class", row.route)
		}
		classified[row.route] = true
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var found []string
	addLit := func(e ast.Expr) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil || !strings.HasPrefix(v, "/") {
			return
		}
		found = append(found, v)
	}
	matchers := map[string]bool{
		"HasSuffix": true, "CutSuffix": true, "TrimSuffix": true,
		"HasPrefix": true, "CutPrefix": true, "TrimPrefix": true,
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				sel, ok := n.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch {
				case matchers[sel.Sel.Name] && len(n.Args) == 2:
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "strings" {
						addLit(n.Args[1])
					}
				case (sel.Sel.Name == "HandleFunc" || sel.Sel.Name == "Handle") && len(n.Args) >= 1:
					addLit(n.Args[0])
				}
			case *ast.BinaryExpr:
				if n.Op == token.EQL || n.Op == token.NEQ {
					addLit(n.X)
					addLit(n.Y)
				}
			case *ast.CaseClause:
				for _, e := range n.List {
					addLit(e)
				}
			case *ast.ValueSpec:
				// Named route constants (unlockSuffix, previewSuffix).
				for i, ident := range n.Names {
					if i < len(n.Values) && strings.HasSuffix(ident.Name, "Suffix") {
						addLit(n.Values[i])
					}
				}
			}
			return true
		})
	}
	if len(found) == 0 {
		t.Fatal("found no route literals; did the dispatcher change shape?")
	}
	// Add-on routes are matched by segment (parsePageAddon), not by a path
	// literal, so their table is checked directly.
	for _, route := range pageAddonRoutes {
		concrete := strings.ReplaceAll(strings.TrimPrefix(route, "{slug}"), "{id}", addonRouteTestID)
		if _, ok := parsePageAddon("acme" + concrete); !ok {
			t.Fatalf("pageAddonRoutes entry %q does not parse", route)
		}
		found = append(found, concrete)
	}

	var missing []string
	for _, lit := range found {
		if !classified[lit] && !notRoutes[lit] {
			missing = append(missing, lit)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("routes with no access class in routeTable (decide whether the gate covers them, then add a row): %v", missing)
	}
}

func notFoundRequest(method, path string) *http.Request {
	var body *strings.Reader
	if method == http.MethodPost {
		body = strings.NewReader(`{"endpoint":"https://fcm.googleapis.com/fcm/send/x"}`)
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// A private page without a valid credential and a slug no page has must be
// indistinguishable on every route: status, headers and body.
func TestAccess_PrivateIsIndistinguishableFromMissing(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	other := newAccessFixture(t, testSessionSecret) // its token opens nothing here

	credentials := map[string]func(*http.Request){
		"none":          func(*http.Request) {},
		"forged cookie": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "eyJ2MSJ9.AAAA"}) },
		"other page's session": func(r *http.Request) {
			r.AddCookie(f.passwordSession(2))
		},
		"foreign token session": func(r *http.Request) {
			r.AddCookie(other.tokenSession("internal"))
		},
		"wrong token": func(r *http.Request) {
			q := r.URL.Query()
			q.Set("token", other.token)
			r.URL.RawQuery = q.Encode()
		},
	}
	for _, row := range routeTable {
		if row.method == "" {
			continue
		}
		for credName, apply := range credentials {
			for _, method := range []string{row.method, http.MethodGet, http.MethodPost, http.MethodDelete} {
				private := notFoundRequest(method, "/public/status/internal"+row.route)
				missing := notFoundRequest(method, "/public/status/no-such-page"+row.route)
				apply(private)
				apply(missing)
				a, b := f.do(private), f.do(missing)
				label := method + " " + row.route + " (" + credName + ")"
				if a.Code != b.Code || a.Body.String() != b.Body.String() {
					t.Errorf("%s: private %d %q vs missing %d %q", label, a.Code, a.Body.String(), b.Code, b.Body.String())
				}
				if !equalHeaders(a.Header(), b.Header()) {
					t.Errorf("%s: headers differ:\nprivate %v\nmissing %v", label, a.Header(), b.Header())
				}
				if strings.Contains(a.Body.String(), pageSentinel) {
					t.Errorf("%s: private page content served", label)
				}
				if a.Code != http.StatusNotFound {
					t.Errorf("%s: private = %d, want 404", label, a.Code)
				}
			}
		}
	}
}

func equalHeaders(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if strings.Join(v, "\x00") != strings.Join(b.Values(k), "\x00") {
			return false
		}
	}
	return true
}

// Each visibility on each gated route, without a credential.
func TestAccess_ModesOnEveryRoute(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	modes := []struct {
		slug    string
		allowed bool
		// status when refused
		deny int
	}{
		{"open", true, 0},
		{"hidden", true, 0},
		{"locked", false, http.StatusUnauthorized},
		{"internal", false, http.StatusNotFound},
	}
	for _, row := range routeTable {
		if row.class != classGated && row.class != classPreviewToken {
			continue
		}
		for _, m := range modes {
			req := httptest.NewRequest(row.method, "/public/status/"+m.slug+row.route, nil)
			rec := httptest.NewRecorder()
			slug, rest := splitStatusPath(strings.TrimPrefix(req.URL.Path, "/public/status/"))
			aw, acc, ok := f.h.authorize(rec, req, slug, rest)
			if ok != m.allowed {
				t.Fatalf("%s %s: allowed = %v, want %v", m.slug, row.route, ok, m.allowed)
			}
			if !ok {
				if rec.Code != m.deny {
					t.Errorf("%s %s: denied with %d, want %d", m.slug, row.route, rec.Code, m.deny)
				}
				if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
					t.Errorf("%s %s: denial Cache-Control = %q", m.slug, row.route, cc)
				}
				if strings.Contains(rec.Body.String(), pageSentinel) {
					t.Errorf("%s %s: denial carries page content", m.slug, row.route)
				}
				continue
			}
			if acc.Visibility == pageaccess.Unlisted {
				aw.WriteHeader(http.StatusOK)
				if rec.Header().Get("X-Robots-Tag") != robotsNoIndex {
					t.Errorf("unlisted %s: missing X-Robots-Tag", row.route)
				}
			}
		}
	}
}

// The unlock form is the password page's answer; its sub-routes get a bare
// 401 and never the form.
func TestAccess_PasswordPageShowsUnlockForm(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	rec := f.get("/public/status/locked")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`action="/public/status/locked/unlock"`, "Locked &lt;Corp&gt;", `name="robots" content="noindex, nofollow"`, "--accent: #123abc"} {
		if !strings.Contains(body, want) {
			t.Errorf("unlock page lacks %q", want)
		}
	}
	if strings.Contains(body, pageSentinel) {
		t.Fatal("unlock page carries page content")
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'nonce-") || !strings.Contains(csp, "frame-ancestors") {
		t.Fatalf("unlock page CSP = %q", csp)
	}
	if strings.Contains(body, statustemplate.NoncePlaceholder) {
		t.Fatal("nonce placeholder left in the unlock page")
	}

	data := f.get("/public/status/locked/data")
	if data.Code != http.StatusUnauthorized || strings.Contains(data.Body.String(), "<form") {
		t.Fatalf("sub-route = %d %q, want a bare 401", data.Code, data.Body.String())
	}
}

func TestAccess_PasswordUnlockFlow(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	post := func(password, remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/public/status/locked/unlock", strings.NewReader(url.Values{"password": {password}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = remote
		return f.do(req)
	}

	wrong := post("not it", "203.0.113.1:1000")
	if wrong.Code != http.StatusUnauthorized || !strings.Contains(wrong.Body.String(), "not correct") {
		t.Fatalf("wrong password = %d", wrong.Code)
	}
	if len(wrong.Result().Cookies()) != 0 {
		t.Fatal("wrong password set a cookie")
	}

	right := post(f.password, "203.0.113.2:1000")
	if right.Code != http.StatusSeeOther || right.Header().Get("Location") != "/public/status/locked" {
		t.Fatalf("right password = %d → %q", right.Code, right.Header().Get("Location"))
	}
	cookie := findCookie(t, right, sessionCookieName)
	if cookie.Path != "/public/status/locked" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Secure {
		t.Fatalf("cookie attributes = %+v", cookie)
	}
	if cookie.MaxAge != int(passwordSessionTTL.Seconds()) {
		t.Fatalf("MaxAge = %d", cookie.MaxAge)
	}
	if right.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("unlock response is cacheable")
	}

	page := f.get("/public/status/locked", cookie)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), pageSentinel) {
		t.Fatalf("unlocked page = %d", page.Code)
	}
	if page.Header().Get("Cache-Control") != "private, no-store" || page.Header().Get("ETag") != "" {
		t.Fatalf("gated page headers: %v", page.Header())
	}
	if page.Header().Get("X-Robots-Tag") != robotsNoIndex || !strings.Contains(page.Body.String(), `<head><meta name="robots" content="noindex, nofollow" />`) {
		t.Fatal("gated page is not noindex")
	}
	// A validator never turns a gated response into a 304.
	req := httptest.NewRequest(http.MethodGet, "/public/status/locked", nil)
	req.AddCookie(cookie)
	req.Header.Set("If-None-Match", "*")
	if rec := f.do(req); rec.Code != http.StatusOK {
		t.Fatalf("gated page with If-None-Match = %d, want 200", rec.Code)
	}

	// Secure over HTTPS (a trusted proxy said so).
	req = httptest.NewRequest(http.MethodPost, "/public/status/locked/unlock", strings.NewReader("password="+url.QueryEscape(f.password)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "10.1.2.3:4000"
	req.Header.Set("X-Forwarded-Proto", "https")
	if c := findCookie(t, f.do(req), sessionCookieName); !c.Secure {
		t.Fatal("cookie over HTTPS is not Secure")
	}
}

func TestAccess_UnlockOnOtherVisibilities(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	post := func(slug string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/public/status/"+slug+"/unlock", strings.NewReader("password=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return f.do(req)
	}
	if rec := post("open"); rec.Code != http.StatusSeeOther {
		t.Fatalf("unlock on a public page = %d, want a redirect to it", rec.Code)
	}
	if rec := f.get("/public/status/locked/unlock"); rec.Code != http.StatusSeeOther {
		t.Fatalf("GET unlock = %d, want a redirect to the page", rec.Code)
	}
	cross := httptest.NewRequest(http.MethodPost, "/public/status/locked/unlock", strings.NewReader("password=x"))
	cross.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	cross.Header.Set("Origin", "https://evil.example")
	if rec := f.do(cross); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin unlock = %d, want 403", rec.Code)
	}
}

func TestAccess_KioskTokenExchange(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)

	rec := f.get("/public/status/internal?mode=kiosk&token=" + url.QueryEscape(f.token) + "&theme=dark")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("token visit = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if strings.Contains(loc, "token") || !strings.HasPrefix(loc, "/public/status/internal?") ||
		!strings.Contains(loc, "mode=kiosk") || !strings.Contains(loc, "theme=dark") {
		t.Fatalf("redirect = %q, want the clean URL with the other parameters", loc)
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("redirect headers: %v", rec.Header())
	}
	if strings.Contains(rec.Body.String(), f.token) {
		t.Fatal("redirect body echoes the token")
	}
	cookie := findCookie(t, rec, sessionCookieName)
	if cookie.MaxAge != int(tokenSessionTTL.Seconds()) || cookie.Path != "/public/status/internal" {
		t.Fatalf("token cookie = %+v", cookie)
	}
	if strings.Contains(cookie.Value, f.token) {
		t.Fatal("cookie carries the token")
	}

	if page := f.get(loc, cookie); page.Code != http.StatusOK || !strings.Contains(page.Body.String(), pageSentinel) {
		t.Fatalf("page with the token session = %d", page.Code)
	}
	// Sub-routes take the same session (asked of the gate directly: no
	// service sits behind this fixture to serve them).
	for _, rest := range []string{"/data", "/stream", "/push/subscribe"} {
		req := httptest.NewRequest(http.MethodGet, "/public/status/internal"+rest, nil)
		req.AddCookie(cookie)
		if _, _, ok := f.h.authorize(httptest.NewRecorder(), req, "internal", rest); !ok {
			t.Fatalf("token session refused on %s", rest)
		}
	}

	// A token is only exchanged on the page itself.
	if sub := f.get("/public/status/internal/stream?token=" + url.QueryEscape(f.token)); sub.Code != http.StatusNotFound {
		t.Fatalf("token on a sub-route = %d, want 404", sub.Code)
	}
	// A token with a still-valid session is stripped too.
	again := f.get("/public/status/internal?token=garbage", cookie)
	if again.Code != http.StatusSeeOther || strings.Contains(again.Header().Get("Location"), "token") {
		t.Fatalf("garbage token with a session = %d → %q", again.Code, again.Header().Get("Location"))
	}
	// On a password page a wrong token gets the unlock form.
	if rec := f.get("/public/status/locked?token=garbage"); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "<form") {
		t.Fatalf("wrong token on a password page = %d", rec.Code)
	}
	// A token of another page opens nothing.
	if rec := f.get("/public/status/locked?token=" + url.QueryEscape(f.token)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("another page's token = %d, want 401", rec.Code)
	}
	// Public and unlisted pages strip it too, without a session or a lookup.
	for _, slug := range []string{"open", "hidden"} {
		rec := f.get("/public/status/" + slug + "?token=" + url.QueryEscape(f.token) + "&mode=kiosk")
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/public/status/"+slug+"?mode=kiosk" ||
			len(rec.Result().Cookies()) != 0 {
			t.Fatalf("%s with ?token= = %d → %q", slug, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// A kiosk token also opens a password page, without the password.
func TestAccess_TokenBypassesPassword(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	token, hash, _ := pageaccess.NewToken()
	f.store.tokens[hash] = fakeToken{id: uuid.New(), pageID: f.store.pages["locked"].ID}
	rec := f.get("/public/status/locked?token=" + url.QueryEscape(token))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("token on a password page = %d, want 303", rec.Code)
	}
	if page := f.get("/public/status/locked", findCookie(t, rec, sessionCookieName)); page.Code != http.StatusOK {
		t.Fatalf("password page with a token session = %d", page.Code)
	}
}

func TestAccess_SessionTamperExpiryAndVersion(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	good := f.passwordSession(2)
	if rec := f.get("/public/status/locked", good); rec.Code != http.StatusOK {
		t.Fatalf("valid session = %d", rec.Code)
	}

	payload, mac, _ := strings.Cut(good.Value, ".")
	raw, _ := decodeB64(payload)
	tampered := strings.Replace(raw, "|p:2|", "|p:3|", 1)
	cases := map[string]*http.Cookie{
		"tampered payload": {Name: sessionCookieName, Value: encodeB64(tampered) + "." + mac},
		"truncated mac":    {Name: sessionCookieName, Value: payload + "." + mac[:10]},
		"no mac":           {Name: sessionCookieName, Value: payload},
		"expired":          f.session("locked", sessionCredential{kind: sessionKindPassword, passwordVersion: 2}, time.Now().Add(-time.Second)),
		"old version":      f.passwordSession(1),
		"other page":       f.session("hidden", sessionCredential{kind: sessionKindPassword, passwordVersion: 2}, time.Now().Add(time.Hour)),
		"other secret": {Name: sessionCookieName, Value: newSessionSigner("another-secret-0123456789abcdefghij").sign(
			f.store.pages["locked"].ID, sessionCredential{kind: sessionKindPassword, passwordVersion: 2}, time.Now().Add(time.Hour))},
	}
	for name, c := range cases {
		if rec := f.get("/public/status/locked", c); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", name, rec.Code)
		}
	}

	// Changing the password (version bump) ends existing sessions.
	f.store.set("locked", func(p *pageAccess) { p.PasswordVersion = 3 })
	if rec := f.get("/public/status/locked", good); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session after a password change = %d", rec.Code)
	}
	// A password session never opens a private page, even one that was a
	// password page when it was signed.
	f.store.set("locked", func(p *pageAccess) { p.Visibility = pageaccess.Private; p.PasswordVersion = 2 })
	if rec := f.get("/public/status/locked", good); rec.Code != http.StatusNotFound {
		t.Fatalf("password session on a private page = %d, want 404", rec.Code)
	}
}

func TestAccess_RevokedTokenEndsSession(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	session := f.tokenSession("internal")
	if rec := f.get("/public/status/internal", session); rec.Code != http.StatusOK {
		t.Fatalf("token session = %d", rec.Code)
	}
	f.store.revokeAll()
	if rec := f.get("/public/status/internal", session); rec.Code != http.StatusNotFound {
		t.Fatalf("revoked token session = %d, want 404", rec.Code)
	}
	if rec := f.get("/public/status/internal?token=" + url.QueryEscape(f.token)); rec.Code != http.StatusNotFound {
		t.Fatalf("revoked token = %d, want 404", rec.Code)
	}
}

// An open stream re-checks a gated page's session on every heartbeat and
// ends once the token is revoked.
func TestAccess_GatedStreamEndsOnRevocation(t *testing.T) {
	prev := streamHeartbeatInterval
	streamHeartbeatInterval = 20 * time.Millisecond
	defer func() { streamHeartbeatInterval = prev }()

	f := newAccessFixture(t, testSessionSecret)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/public/status/internal/stream", nil).WithContext(ctx)
	req.AddCookie(f.tokenSession("internal"))
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		f.h.HandleStatusPage(rec, req)
		close(done)
	}()
	time.Sleep(60 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("stream ended while the session was valid")
	default:
	}
	f.store.revokeAll()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream outlived the revoked token")
	}
}

func TestAccess_WithoutSessionSecret(t *testing.T) {
	f := newAccessFixture(t, "")
	if rec := f.get("/public/status/locked"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("password page without a secret = %d, want 503", rec.Code)
	}
	if rec := f.get("/public/status/internal?token=" + url.QueryEscape(f.token)); rec.Code != http.StatusNotFound {
		t.Fatalf("private page without a secret = %d, want 404", rec.Code)
	}
	if rec := f.get("/public/status/open"); rec.Code != http.StatusOK {
		t.Fatalf("public page without a secret = %d", rec.Code)
	}
	f.get("/public/status/locked")
	if n := strings.Count(f.logs.String(), "STATUS_PAGE_SESSION_SECRET is not set"); n != 1 {
		t.Fatalf("missing-secret warning logged %d times, want once", n)
	}
}

// Kiosk tokens never reach a log line, whatever happens to the request.
func TestAccess_TokenNotLogged(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	f.get("/public/status/internal?token=" + url.QueryEscape(f.token))
	f.get("/public/status/internal?token=" + url.QueryEscape(f.token+"x"))
	f.get("/public/status/locked?token=" + url.QueryEscape(f.token))
	f.store.failErr = errors.New("connection refused")
	f.get("/public/status/internal?token=" + url.QueryEscape(f.token))
	if !strings.Contains(f.logs.String(), "connection refused") {
		t.Fatal("expected the failure to be logged")
	}
	if strings.Contains(f.logs.String(), f.token) || strings.Contains(f.logs.String(), pageaccess.HashToken(f.token)) {
		t.Fatalf("token in logs: %s", f.logs.String())
	}
}

func TestAccess_UnlistedAndPublicHeaders(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	unlisted := f.get("/public/status/hidden")
	if unlisted.Code != http.StatusOK || unlisted.Header().Get("X-Robots-Tag") != robotsNoIndex ||
		!strings.Contains(unlisted.Body.String(), `name="robots" content="noindex, nofollow"`) {
		t.Fatalf("unlisted page = %d %v", unlisted.Code, unlisted.Header())
	}
	public := f.get("/public/status/open")
	if public.Header().Get("X-Robots-Tag") != "" || strings.Contains(public.Body.String(), `name="robots"`) {
		t.Fatal("public page says noindex")
	}
	if public.Header().Get("Cache-Control") != "no-cache" || public.Header().Get("ETag") == "" {
		t.Fatal("public page lost its revalidation headers")
	}
	// Same rendered HTML, different bodies: the validators must differ.
	if unlisted.Header().Get("ETag") == public.Header().Get("ETag") {
		t.Fatal("unlisted and public pages share an ETag")
	}
}

func TestAccess_ProxyRefusesGatedPages(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	for slug, wantOK := range map[string]bool{"open": true, "hidden": true, "locked": false, "internal": false} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/_sp_api/api/v1/status-pages/"+f.store.pages[slug].ID.String(), nil)
		ok := f.h.authorizeProxy(rec, req, f.store.pages[slug].ID)
		if ok != wantOK {
			t.Errorf("%s: proxy allowed = %v", slug, ok)
		}
	}
	rec := httptest.NewRecorder()
	if f.h.authorizeProxy(rec, httptest.NewRequest(http.MethodGet, "/", nil), uuid.New()) || rec.Code != http.StatusNotFound {
		t.Fatal("unknown page passed the proxy gate")
	}

	id := uuid.New()
	cases := []struct {
		path, header string
		ok           bool
		want         uuid.UUID
	}{
		{"/api/v1/status-pages/" + id.String(), "", true, id},
		{"/api/v1/status-pages/" + id.String(), id.String(), true, id},
		{"/api/v1/status-pages/" + id.String(), uuid.New().String(), false, uuid.Nil},
		{"/api/v1/status-pages/not-a-uuid", "", false, uuid.Nil},
		{"/api/v1/status-pages/" + id.String() + "/../" + uuid.New().String(), "", false, uuid.Nil},
		{"/api/v1/monitors", id.String(), true, id},
		{"/api/v1/monitors", "", true, uuid.Nil},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.header != "" {
			req.Header.Set("X-Status-Page-ID", c.header)
		}
		got, ok := proxyStatusPageID(req, c.path)
		if ok != c.ok || got != c.want {
			t.Errorf("%s (header %q) = %v %v, want %v %v", c.path, c.header, got, ok, c.want, c.ok)
		}
	}
}

func TestNoIndexMeta(t *testing.T) {
	cases := map[string]string{
		"<html><head><title>x</title></head></html>":           `<html><head><meta name="robots" content="noindex, nofollow" /><title>x</title></head></html>`,
		`<html><HEAD lang="en"><title>x</title></head></html>`: `<html><HEAD lang="en"><meta name="robots" content="noindex, nofollow" /><title>x</title></head></html>`,
		"<body><header>h</header></body>":                      `<meta name="robots" content="noindex, nofollow" /><body><header>h</header></body>`,
	}
	for in, want := range cases {
		if got := noIndexMeta(in); got != want {
			t.Errorf("noIndexMeta(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHubCloseSlug(t *testing.T) {
	hub := NewHub()
	a := hub.Register("p")
	b := hub.Register("q")
	hub.CloseSlug("p")
	if _, ok := <-a; ok {
		t.Fatal("stream of the closed slug still open")
	}
	// Unregistering an already-closed stream must not double-close.
	hub.Unregister("p", a)
	c := hub.Register("p")
	hub.Unregister("p", a)
	hub.Broadcast("p", SSEEvent{Type: "update"})
	if ev := <-c; ev.Type != "update" {
		t.Fatal("new stream lost its event")
	}
	hub.Broadcast("q", SSEEvent{Type: "update"})
	if ev := <-b; ev.Type != "update" {
		t.Fatal("other slug's stream affected")
	}
}

func TestSessionPeekNeedsVerify(t *testing.T) {
	s := newSessionSigner(testSessionSecret)
	pageID, tokenID := uuid.New(), uuid.New()
	v := s.sign(pageID, sessionCredential{kind: sessionKindToken, tokenID: tokenID}, time.Now().Add(time.Hour))
	if peekSessionTokenID(v) != tokenID {
		t.Fatal("peek lost the token id")
	}
	if peekSessionTokenID("garbage") != uuid.Nil || peekSessionTokenID("") != uuid.Nil {
		t.Fatal("peek accepted garbage")
	}
	if _, ok := s.verify(uuid.New(), v, time.Now()); ok {
		t.Fatal("session verified for another page")
	}
	if newSessionSigner("  ") != nil {
		t.Fatal("blank secret made a signer")
	}
}

func findCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s cookie set (status %d)", name, rec.Code)
	return nil
}

func decodeB64(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return string(b), err
}

func encodeB64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

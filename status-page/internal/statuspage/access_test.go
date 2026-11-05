package statuspage

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/pageaccess"
	"github.com/yassinebenameur/probara/shared/statustemplate"
)

const testSessionSecret = "test-session-secret-0123456789abcdef"

// allPublicAccess answers every slug as a public page.
type allPublicAccess struct{}

func (allPublicAccess) PageAccess(_ context.Context, slug string) (*pageAccess, error) {
	return &pageAccess{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(slug)), Title: slug, Visibility: pageaccess.Public}, nil
}
func (allPublicAccess) PageAccessByID(_ context.Context, id uuid.UUID) (*pageAccess, error) {
	return &pageAccess{ID: id, Visibility: pageaccess.Public}, nil
}
func (allPublicAccess) ActiveTokenID(context.Context, uuid.UUID, string) (uuid.UUID, bool, error) {
	return uuid.Nil, false, nil
}
func (allPublicAccess) TokenActive(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}

// fakeAccess is an in-memory accessStore.
type fakeAccess struct {
	pages  map[string]*pageAccess // by slug
	tokens map[string]uuid.UUID   // active token hash -> id
}

func (f *fakeAccess) PageAccess(_ context.Context, slug string) (*pageAccess, error) {
	if p, ok := f.pages[slug]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, ErrStatusPageNotFound
}

func (f *fakeAccess) PageAccessByID(_ context.Context, id uuid.UUID) (*pageAccess, error) {
	for _, p := range f.pages {
		if p.ID == id {
			cp := *p
			return &cp, nil
		}
	}
	return nil, ErrStatusPageNotFound
}

func (f *fakeAccess) ActiveTokenID(_ context.Context, _ uuid.UUID, hash string) (uuid.UUID, bool, error) {
	id, ok := f.tokens[hash]
	return id, ok, nil
}

func (f *fakeAccess) TokenActive(_ context.Context, _ uuid.UUID, tokenID uuid.UUID) (bool, error) {
	for _, id := range f.tokens {
		if id == tokenID {
			return true, nil
		}
	}
	return false, nil
}

type accessFixture struct {
	h        *Handlers
	store    *fakeAccess
	password string
	token    string
	tokenID  uuid.UUID
}

func newAccessFixture(t *testing.T, secret string) *accessFixture {
	t.Helper()
	hash, err := pageaccess.HashPassword("open sesame")
	require.NoError(t, err)
	token, tokenHash, err := pageaccess.NewToken()
	require.NoError(t, err)
	tokenID := uuid.New()
	store := &fakeAccess{
		pages: map[string]*pageAccess{
			"open":     {ID: uuid.New(), Title: "Open", Visibility: pageaccess.Public},
			"hidden":   {ID: uuid.New(), Title: "Hidden", Visibility: pageaccess.Unlisted},
			"locked":   {ID: uuid.New(), Title: "Locked <Corp>", Visibility: pageaccess.Password, PasswordHash: hash, PasswordVersion: 2},
			"internal": {ID: uuid.New(), Title: "Internal", Visibility: pageaccess.Private},
		},
		tokens: map[string]uuid.UUID{tokenHash: tokenID},
	}
	cfg := &config.StatusPageConfig{SessionSecret: secret}
	h := NewHandlers(nil, cfg, logger.New("status-page", "error"), NewHub(), newRenderCache(0), nil)
	h.access.store = store
	return &accessFixture{h: h, store: store, password: "open sesame", token: token, tokenID: tokenID}
}

func (f *accessFixture) sessionCookie(t *testing.T, slug string, cred pageaccess.Credential) *http.Cookie {
	t.Helper()
	page := f.store.pages[slug]
	return &http.Cookie{
		Name:  pageaccess.CookieName(page.ID),
		Value: f.h.access.sessions.Sign(page.ID, cred, time.Now().Add(time.Hour)),
	}
}

// dispatchedSuffixes is every sub-route HandleStatusPage serves under
// /public/status/{slug}, with the method it answers. Add a row when you route
// a new suffix; TestAuthorize_TableCoversDispatcher fails until you do.
var dispatchedSuffixes = []struct{ suffix, method string }{
	{"", http.MethodGet},
	{"/", http.MethodGet},
	{"/data", http.MethodGet},
	{"/stream", http.MethodGet},
	{"/preview/draft", http.MethodGet},
	{"/push/subscribe", http.MethodPost},
	{"/push/unsubscribe", http.MethodPost},
	// Not routed today: proves an unknown suffix is gated too.
	{"/some-future-route.json", http.MethodGet},
}

// TestAuthorize_TableCoversDispatcher reads HandleStatusPage's source and
// fails on any path literal it matches requests against that no row of
// dispatchedSuffixes exercises.
func TestAuthorize_TableCoversDispatcher(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "handlers.go", nil, 0)
	require.NoError(t, err)

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == "HandleStatusPage" {
			fn = d
		}
	}
	require.NotNil(t, fn, "HandleStatusPage not found in handlers.go")

	matchers := map[string]bool{
		"HasSuffix": true, "CutSuffix": true, "TrimSuffix": true,
		"HasPrefix": true, "CutPrefix": true, "TrimPrefix": true, "Contains": true,
	}
	var literals []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !matchers[sel.Sel.Name] {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "strings" {
			return true
		}
		lit, ok := call.Args[1].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil || value == "/" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "/public/status") {
			return true
		}
		literals = append(literals, value)
		return true
	})
	require.NotEmpty(t, literals, "found no routed suffixes; did the dispatcher change shape?")

	var missing []string
	for _, lit := range literals {
		covered := lit == unlockSuffix
		for _, row := range dispatchedSuffixes {
			if strings.Contains(row.suffix, lit) {
				covered = true
			}
		}
		if !covered {
			missing = append(missing, lit)
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing, "HandleStatusPage routes these suffixes but dispatchedSuffixes has no row for them; add one so the gate is tested on them")
}

// Every dispatched suffix of a gated page is refused without a credential,
// before any handler (or the nil service behind them) runs.
func TestAuthorize_GatesEveryDispatchedSuffix(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	for _, row := range dispatchedSuffixes {
		for _, c := range []struct {
			slug string
			want int
		}{
			{"locked", http.StatusUnauthorized},
			{"internal", http.StatusNotFound},
			{"no-such-page", http.StatusNotFound},
		} {
			req := httptest.NewRequest(row.method, "/public/status/"+c.slug+row.suffix, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			f.h.HandleStatusPage(rec, req)
			require.Equalf(t, c.want, rec.Code, "%s %s%s", row.method, c.slug, row.suffix)
			if c.slug != "no-such-page" {
				require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"), c.slug+row.suffix)
				require.Equal(t, "noindex", rec.Header().Get("X-Robots-Tag"), c.slug+row.suffix)
			}
		}
	}
}

// Without STATUS_PAGE_SESSION_SECRET gated pages fail closed; public ones
// are untouched.
func TestAuthorize_NoSessionSecretFailsClosed(t *testing.T) {
	f := newAccessFixture(t, "")
	require.Nil(t, f.h.access.sessions)
	for _, row := range dispatchedSuffixes {
		rec := httptest.NewRecorder()
		f.h.HandleStatusPage(rec, httptest.NewRequest(row.method, "/public/status/locked"+row.suffix, nil))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code, row.suffix)

		rec = httptest.NewRecorder()
		f.h.HandleStatusPage(rec, httptest.NewRequest(row.method, "/public/status/internal"+row.suffix+"?token="+url.QueryEscape(f.token), nil))
		require.Equal(t, http.StatusNotFound, rec.Code, row.suffix)
	}
	req := httptest.NewRequest(http.MethodGet, "/public/status/open", nil)
	w, ok := f.h.authorize(httptest.NewRecorder(), req, "open", "")
	require.True(t, ok)
	require.False(t, gatedResponse(w))
}

func TestAuthorize_PasswordPageShowsUnlockForm(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	rec := httptest.NewRecorder()
	f.h.HandleStatusPage(rec, httptest.NewRequest(http.MethodGet, "/public/status/locked", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `action="/public/status/locked/unlock"`)
	require.Contains(t, body, "Locked &lt;Corp&gt;", "title is escaped")
	require.Contains(t, body, `<meta name="robots" content="noindex" />`)
	require.Contains(t, rec.Header().Get("Content-Security-Policy"), "script-src 'nonce-")
}

func TestAuthorize_Sessions(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	pw := pageaccess.Credential{Kind: pageaccess.KindPassword, PasswordVersion: 2}
	tok := pageaccess.Credential{Kind: pageaccess.KindToken, TokenID: f.tokenID}

	try := func(slug string, cookie *http.Cookie) bool {
		req := httptest.NewRequest(http.MethodGet, "/public/status/"+slug+"/data", nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w, ok := f.h.authorize(httptest.NewRecorder(), req, slug, "/data")
		if ok {
			require.True(t, gatedResponse(w))
		}
		return ok
	}

	require.True(t, try("locked", f.sessionCookie(t, "locked", pw)))
	require.True(t, try("locked", f.sessionCookie(t, "locked", tok)), "tokens open password pages")
	require.True(t, try("internal", f.sessionCookie(t, "internal", tok)))
	require.False(t, try("internal", f.sessionCookie(t, "internal", pw)), "a private page takes no password session")

	// Another page's session.
	other := f.sessionCookie(t, "locked", pw)
	other.Name = pageaccess.CookieName(f.store.pages["internal"].ID)
	require.False(t, try("internal", other))

	// A password change signs password sessions out.
	f.store.pages["locked"].PasswordVersion = 3
	require.False(t, try("locked", f.sessionCookie(t, "locked", pw)))
	// Clearing the password does too.
	f.store.pages["locked"].PasswordVersion = 2
	f.store.pages["locked"].PasswordHash = ""
	require.False(t, try("locked", f.sessionCookie(t, "locked", pw)))

	// Revoking the token ends its sessions on the next request.
	f.store.tokens = map[string]uuid.UUID{}
	require.False(t, try("internal", f.sessionCookie(t, "internal", tok)))
}

func TestAuthorize_KioskTokenBecomesSession(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	req := httptest.NewRequest(http.MethodGet, "/public/status/internal/data?token="+url.QueryEscape(f.token), nil)
	rec := httptest.NewRecorder()
	w, ok := f.h.authorize(rec, req, "internal", "/data")
	require.True(t, ok)
	w.WriteHeader(http.StatusOK)

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	c := cookies[0]
	require.Equal(t, pageaccess.CookieName(f.store.pages["internal"].ID), c.Name)
	require.True(t, c.HttpOnly)
	require.Equal(t, http.SameSiteLaxMode, c.SameSite)
	require.False(t, c.Secure, "plain-HTTP request")
	require.NotContains(t, c.Value, f.token)

	// The cookie alone now opens the page.
	req = httptest.NewRequest(http.MethodGet, "/public/status/internal/stream", nil)
	req.AddCookie(c)
	_, ok = f.h.authorize(httptest.NewRecorder(), req, "internal", "/stream")
	require.True(t, ok)

	// Wrong and revoked tokens do not.
	req = httptest.NewRequest(http.MethodGet, "/public/status/internal?token=spk_wrong", nil)
	_, ok = f.h.authorize(httptest.NewRecorder(), req, "internal", "")
	require.False(t, ok)

	// HTTPS (here TLS terminated by this process) marks the cookie Secure.
	req = httptest.NewRequest(http.MethodGet, "https://status.example.com/public/status/internal?token="+url.QueryEscape(f.token), nil)
	rec = httptest.NewRecorder()
	_, ok = f.h.authorize(rec, req, "internal", "")
	require.True(t, ok)
	require.True(t, rec.Result().Cookies()[0].Secure)
}

func TestAuthorize_PreviewKeepsItsOwnToken(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	page := f.store.pages["internal"]
	secret := "preview-secret-for-tests"
	previewToken := statustemplate.MintPreviewToken(secret, page.ID.String(), time.Now().Add(time.Hour))

	req := httptest.NewRequest(http.MethodGet, "/public/status/internal/preview/draft?token="+url.QueryEscape(previewToken), nil)
	_, ok := f.h.authorize(httptest.NewRecorder(), req, "internal", previewSuffix)
	require.False(t, ok, "without the preview secret the token means nothing")

	t.Setenv(previewSecretEnvVar, secret)
	_, ok = f.h.authorize(httptest.NewRecorder(), req, "internal", previewSuffix)
	require.True(t, ok)
	// Only for the preview route.
	req = httptest.NewRequest(http.MethodGet, "/public/status/internal/data?token="+url.QueryEscape(previewToken), nil)
	_, ok = f.h.authorize(httptest.NewRecorder(), req, "internal", "/data")
	require.False(t, ok)
}

func TestAuthorize_UnlistedSaysNoIndex(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	rec := httptest.NewRecorder()
	w, ok := f.h.authorize(rec, httptest.NewRequest(http.MethodGet, "/public/status/hidden", nil), "hidden", "")
	require.True(t, ok)
	require.False(t, gatedResponse(w))
	w.Header().Set("Cache-Control", "no-cache")
	html := noIndexHTML(w, "<!DOCTYPE html><html><head><title>x</title></head></html>")
	require.Contains(t, html, `<head><meta name="robots" content="noindex" /><title>`)
	w.WriteHeader(http.StatusOK)
	require.Equal(t, "noindex", rec.Header().Get("X-Robots-Tag"))
	require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"), "unlisted pages stay cacheable")

	// Public pages get the writer back untouched.
	rec = httptest.NewRecorder()
	w, ok = f.h.authorize(rec, httptest.NewRequest(http.MethodGet, "/public/status/open", nil), "open", "")
	require.True(t, ok)
	require.Equal(t, "<head>", noIndexHTML(w, "<head>"))
}

func TestAccessWriter_OverridesRouteCachingAndFlushes(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &accessWriter{ResponseWriter: rec, noIndex: true, noStore: true}
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("ETag", `"abc"`)
	w.Flush()
	require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	require.Empty(t, rec.Header().Get("ETag"))
	require.True(t, rec.Flushed)
	var _ http.Flusher = w
}

func postUnlock(h *Handlers, slug, password string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	form := url.Values{"password": {password}}
	req := httptest.NewRequest(http.MethodPost, "/public/status/"+slug+"/unlock", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.HandleStatusPage(rec, req)
	return rec
}

func TestUnlock(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)

	rec := postUnlock(f.h, "locked", "wrong password", nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), "not correct")
	require.Empty(t, rec.Result().Cookies())

	rec = postUnlock(f.h, "locked", f.password, nil)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "/public/status/locked", rec.Header().Get("Location"))
	require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)

	req := httptest.NewRequest(http.MethodGet, "/public/status/locked", nil)
	req.AddCookie(cookies[0])
	_, ok := f.h.authorize(httptest.NewRecorder(), req, "locked", "")
	require.True(t, ok)

	// A private page has no password to try, and does not confirm it exists.
	require.Equal(t, http.StatusNotFound, postUnlock(f.h, "internal", f.password, nil).Code)
	require.Equal(t, http.StatusNotFound, postUnlock(f.h, "no-such-page", f.password, nil).Code)
	// A public page just sends the visitor back.
	require.Equal(t, http.StatusSeeOther, postUnlock(f.h, "open", "", nil).Code)

	// Cross-origin posts are refused.
	rec = postUnlock(f.h, "locked", f.password, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") })
	require.Equal(t, http.StatusForbidden, rec.Code)

	// GET returns to the page.
	rec = httptest.NewRecorder()
	f.h.HandleStatusPage(rec, httptest.NewRequest(http.MethodGet, "/public/status/locked/unlock", nil))
	require.Equal(t, http.StatusSeeOther, rec.Code)
}

func TestUnlock_Throttled(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	var last int
	for i := 0; i < unlockPerClientBurst+1; i++ {
		last = postUnlock(f.h, "locked", "wrong password", nil).Code
	}
	require.Equal(t, http.StatusTooManyRequests, last)
	// Even the right password waits.
	require.Equal(t, http.StatusTooManyRequests, postUnlock(f.h, "locked", f.password, nil).Code)
	// Another client still gets its own attempts.
	rec := postUnlock(f.h, "locked", f.password, func(r *http.Request) { r.RemoteAddr = "198.51.100.9:1234" })
	require.Equal(t, http.StatusSeeOther, rec.Code)
}

func TestUnlock_NoSessionSecret(t *testing.T) {
	f := newAccessFixture(t, "")
	require.Equal(t, http.StatusServiceUnavailable, postUnlock(f.h, "locked", f.password, nil).Code)
}

func TestAuthorizeProxy(t *testing.T) {
	f := newAccessFixture(t, testSessionSecret)
	check := func(slug string, cookie *http.Cookie) int {
		req := httptest.NewRequest(http.MethodGet, "/_sp_api/api/v1/monitors", nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		if f.h.authorizeProxy(rec, req, f.store.pages[slug].ID) {
			return http.StatusOK
		}
		return rec.Code
	}
	require.Equal(t, http.StatusOK, check("open", nil))
	require.Equal(t, http.StatusOK, check("hidden", nil))
	require.Equal(t, http.StatusNotFound, check("locked", nil))
	require.Equal(t, http.StatusNotFound, check("internal", nil))
	require.Equal(t, http.StatusOK, check("locked", f.sessionCookie(t, "locked", pageaccess.Credential{Kind: pageaccess.KindPassword, PasswordVersion: 2})))

	// Naming no page passes; the API authenticates on its own.
	require.True(t, f.h.authorizeProxy(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/_sp_api/api/v1/monitors", nil), uuid.Nil))
}

func TestSplitStatusPath(t *testing.T) {
	cases := map[string][2]string{
		"acme":                        {"acme", ""},
		"acme/":                       {"acme", "/"},
		"acme/data":                   {"acme", "/data"},
		"acme/preview/draft":          {"acme", "/preview/draft"},
		"acme/monitors/123/badge.svg": {"acme", "/monitors/123/badge.svg"},
		"/data":                       {"", "/data"},
	}
	for in, want := range cases {
		slug, rest := splitStatusPath(in)
		require.Equal(t, want[0], slug, in)
		require.Equal(t, want[1], rest, in)
	}
}

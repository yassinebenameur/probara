package statuspage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Custom-domain hosts of the access fixture's pages (newAccessFixture).
const (
	openHost     = "status.open.example.com"
	hiddenHost   = "status.hidden.example.com"
	lockedHost   = "status.locked.example.com"
	internalHost = "status.internal.example.com"
	unknownHost  = "status.unknown.example.com"
	platformHost = "status.platform.example.com"
	// Claimed but not verified: pending, failed or superseded.
	pendingHost = "status.pending.example.com"
	failedHost  = "status.failed.example.com"
	// An extra platform host (STATUS_PAGE_PLATFORM_HOSTS).
	appHost = "probara.example.com"
)

type customHostFixture struct {
	*accessFixture
	handler http.Handler
	loads   *atomic.Int32
	routes  map[string]customHostRoute
}

// newCustomHostFixture serves the access fixture's pages through the real
// mux behind CustomHosts, each also on a custom domain.
func newCustomHostFixture(t *testing.T) *customHostFixture {
	t.Helper()
	f := newAccessFixture(t, testSessionSecret)
	routes := map[string]customHostRoute{
		openHost:     {PageID: f.store.pages["open"].ID, Slug: "open", Verified: true},
		hiddenHost:   {PageID: f.store.pages["hidden"].ID, Slug: "hidden", Verified: true},
		lockedHost:   {PageID: f.store.pages["locked"].ID, Slug: "locked", Verified: true},
		internalHost: {PageID: f.store.pages["internal"].ID, Slug: "internal", Verified: true},
		pendingHost:  {},
		failedHost:   {},
		// The platform host never serves as a custom domain, even if a row
		// named it.
		platformHost: {PageID: f.store.pages["open"].ID, Slug: "open", Verified: true},
	}
	loads := &atomic.Int32{}
	f.h.hosts = newHostTable(func(context.Context) (map[string]customHostRoute, error) {
		loads.Add(1)
		return routes, nil
	})
	f.h.config.StatusPageBaseURL = "https://" + platformHost
	f.h.config.PlatformHosts = []string{appHost}
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("METRICS")) })
	return &customHostFixture{accessFixture: f, handler: f.h.CustomHosts(f.h.routes(metrics)), loads: loads, routes: routes}
}

func (f *customHostFixture) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// on builds a request for path on host.
func on(method, host, path string) *http.Request {
	req := notFoundRequest(method, "http://"+host+path)
	req.Host = host
	return req
}

// customHostRouteTable is every path form a custom domain serves: each
// dispatcher sub-route of routeTable at the root, the worker, and paths
// that must answer not found there.
var customHostRouteTable = []struct {
	path string
	// slugRoute is the routeTable sub-route it is rewritten to ("" for the
	// rows below that are not page routes).
	slugRoute string
	class     routeClass
	method    string
}{
	{"/", "/", classGated, http.MethodGet},
	{"/data", "/data", classGated, http.MethodGet},
	{"/stream", "/stream", classGated, http.MethodGet},
	{"/preview/draft", "/preview/draft", classPreviewToken, http.MethodGet},
	{"/push/subscribe", "/push/subscribe", classGated, http.MethodPost},
	{"/push/unsubscribe", "/push/unsubscribe", classGated, http.MethodPost},
	{"/unlock", "/unlock", classUnlock, http.MethodPost},
	{"/summary.json", "/summary.json", classGated, http.MethodGet},
	{"/feed.atom", "/feed.atom", classGated, http.MethodGet},
	{"/badge.svg", "/badge.svg", classGated, http.MethodGet},
	{"/monitors/" + addonRouteTestID + "/badge.svg", "/monitors/" + addonRouteTestID + "/badge.svg", classGated, http.MethodGet},
	{"/monitors/" + addonRouteTestID + "/uptime.svg", "/monitors/" + addonRouteTestID + "/uptime.svg", classGated, http.MethodGet},
	{"/some-future-route.json", "/some-future-route.json", classGated, http.MethodGet},
	{"/sw.js", "/sw.js", classGated, http.MethodGet},
	{"/subscribe", "/subscribe", classGated, http.MethodPost},
	// The mail link routes skip the gate only on the platform host (mail
	// unsubscribe links name it); on a custom domain they are gated.
	{"/subscription", "/subscription", classGated, http.MethodGet},
	{"/subscription/confirm", "/subscription/confirm", classGated, http.MethodPost},
	{"/subscription/unsubscribe", "/subscription/unsubscribe", classGated, http.MethodPost},
}

// Paths that are something else on the platform host and nothing on a
// custom domain: health and metrics, other slugs, the slug form of the page
// itself, dot segments.
var customHostNotFound = []string{
	"/healthz",
	"/readyz",
	"/metrics",
	"/public/status/hidden",
	"/public/status/open",
	"/public/status/open/data",
	"/public/status/sw.js",
	"/public/status/",
	"/x/../data",
	"/./data",
	"//data",
	"/data/..",
}

// Every dispatcher sub-route has a custom-domain form, so the gate and the
// route classification cover custom domains too.
func TestCustomHost_RouteTableCoversEverySubRoute(t *testing.T) {
	forms := map[string]routeClass{}
	for _, row := range customHostRouteTable {
		if row.slugRoute != "" {
			forms[row.slugRoute] = row.class
		}
	}
	for _, row := range routeTable {
		if row.method == "" || row.route == "" {
			continue
		}
		class, ok := forms[row.route]
		if !ok {
			t.Errorf("sub-route %q has no custom-domain form in customHostRouteTable", row.route)
			continue
		}
		if class != row.class && !(row.class == classSubscriptionLink && class == classGated) {
			t.Errorf("sub-route %q: custom-domain class %q, slug class %q", row.route, class, row.class)
		}
	}
}

// Every route on a custom domain is its page's slug route behind the same
// gate: a password page's domain answers each one exactly as the slug route
// does without a session (the form's action aside), and a public page's
// domain serves the page at its root.
func TestCustomHost_RoutesMatchSlugRoutes(t *testing.T) {
	f := newCustomHostFixture(t)
	for _, row := range customHostRouteTable {
		if row.slugRoute == "" {
			continue
		}
		if isSubscriptionLinkRoute(row.slugRoute) {
			// Gated on the domain, open on the platform host: the domain
			// answers the gate's refusal, never the page.
			for _, method := range []string{row.method, http.MethodGet, http.MethodPost} {
				if rec := f.serve(on(method, lockedHost, row.path)); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), pageSentinel) {
					t.Errorf("%s %s on a password page's domain = %d", method, row.path, rec.Code)
				}
			}
			continue
		}
		for _, method := range []string{row.method, http.MethodGet, http.MethodPost, http.MethodDelete} {
			custom := f.serve(on(method, lockedHost, row.path))
			slug := f.serve(on(method, platformHost, "/public/status/locked"+strings.TrimSuffix(row.slugRoute, "/")))
			label := method + " " + row.path
			if custom.Code != slug.Code {
				t.Errorf("%s: custom domain %d, slug route %d", label, custom.Code, slug.Code)
			}
			switch {
			case strings.Contains(custom.Body.String(), "unlock"):
				if !strings.Contains(custom.Body.String(), `action="/unlock"`) {
					t.Errorf("%s: unlock form posts elsewhere: %q", label, custom.Body.String())
				}
			case custom.Code == http.StatusSeeOther:
				if loc := custom.Header().Get("Location"); loc != "/" {
					t.Errorf("%s: redirects to %q, want the page at /", label, loc)
				}
			case custom.Body.String() != slug.Body.String():
				t.Errorf("%s: custom domain %q, slug route %q", label, custom.Body.String(), slug.Body.String())
			}
			if strings.Contains(custom.Body.String(), pageSentinel) {
				t.Errorf("%s: served the password page's content without a session", label)
			}
		}
	}
	if rec := f.serve(on(http.MethodGet, openHost, "/")); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), pageSentinel) {
		t.Fatalf("page on its custom domain: %d %q", rec.Code, rec.Body.String())
	}

	sw := f.serve(on(http.MethodGet, openHost, "/sw.js"))
	if sw.Code != http.StatusOK || sw.Header().Get("Service-Worker-Allowed") != "/" {
		t.Fatalf("worker on a custom domain: %d, Service-Worker-Allowed %q", sw.Code, sw.Header().Get("Service-Worker-Allowed"))
	}
	// Push is off on gated pages, so their domains have no worker: a
	// private page's answers like a host that serves nothing, and a password
	// page's is not found even with a session.
	notFound := httptest.NewRecorder()
	writeNotFound(notFound)
	private := f.serve(on(http.MethodGet, internalHost, "/sw.js"))
	if private.Code != http.StatusNotFound || private.Body.String() != notFound.Body.String() || !equalHeaders(private.Header(), f.serve(on(http.MethodGet, unknownHost, "/sw.js")).Header()) {
		t.Fatalf("worker on a private page's domain = %d %v", private.Code, private.Header())
	}
	req := on(http.MethodGet, lockedHost, "/sw.js")
	req.AddCookie(f.passwordSession(2))
	if rec := f.serve(req); rec.Code != http.StatusNotFound {
		t.Fatalf("worker on a password page's domain with a session = %d", rec.Code)
	}
	// The slug form of the worker path is not a route on the platform host.
	if rec := f.serve(on(http.MethodGet, platformHost, "/public/status/open/sw.js")); rec.Code != http.StatusNotFound {
		t.Fatalf("/public/status/open/sw.js on the platform host = %d", rec.Code)
	}
	if got := f.serve(on(http.MethodGet, platformHost, "/public/status/sw.js")).Header().Get("Service-Worker-Allowed"); got != "/public/status/" {
		t.Fatalf("worker on the platform host: Service-Worker-Allowed %q", got)
	}
}

// Only the page's own routes exist on its custom domain: health and
// metrics, other pages and its own slug form are unknown sub-routes of the
// page there, answered by its gate and dispatcher exactly as the same
// unknown sub-route of the slug form (not found, 405 for a write method on a
// page that lets it through, a bare 401 on a password page), never served. Dot segments are refused before routing.
func TestCustomHost_NothingElseIsReachable(t *testing.T) {
	f := newCustomHostFixture(t)
	notFound := httptest.NewRecorder()
	writeNotFound(notFound)
	slugs := map[string]string{openHost: "open", hiddenHost: "hidden", lockedHost: "locked", internalHost: "internal"}
	for host, slug := range slugs {
		for _, p := range customHostNotFound {
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch} {
				rec := f.serve(on(method, host, p))
				label := method + " " + host + p
				if strings.Contains(rec.Body.String(), "METRICS") || strings.Contains(rec.Body.String(), pageSentinel) || rec.Code == http.StatusBadGateway {
					t.Errorf("%s reached something: %d %q", label, rec.Code, rec.Body.String())
				}
				want := notFound
				if !strings.Contains(p, "/.") && !strings.Contains(p, "//") {
					want = f.serve(on(method, platformHost, "/public/status/"+slug+p))
				}
				if rec.Code != want.Code || rec.Body.String() != want.Body.String() {
					t.Errorf("%s = %d %q, want %d %q", label, rec.Code, rec.Body.String(), want.Code, want.Body.String())
				}
				if slug != "locked" && method == http.MethodGet && rec.Code != http.StatusNotFound {
					t.Errorf("%s = %d, want 404", label, rec.Code)
				}
			}
		}
	}
	// The same paths still work on the platform host.
	if rec := f.serve(on(http.MethodGet, platformHost, "/metrics")); rec.Body.String() != "METRICS" {
		t.Fatalf("metrics on the platform host: %d", rec.Code)
	}
	if rec := f.serve(on(http.MethodGet, platformHost, "/public/status/hidden")); rec.Code != http.StatusOK {
		t.Fatalf("another page on the platform host: %d", rec.Code)
	}
}

// The platform routes exist only on platform hosts. A hostname pointed at
// the service but not verified (pending, failed, superseded, removed in the
// UI but still routed), an unknown one, and every public hostname before the
// table has loaded once get the not-found answer on every path, never a page
// under /public/status/ or metrics.
func TestCustomHost_UnverifiedHostsGetNothing(t *testing.T) {
	paths := append([]string{"/", "/public/status/open", "/public/status/hidden", "/public/status/hidden/summary.json", "/data"}, customHostNotFound...)
	check := func(t *testing.T, f *customHostFixture, hosts []string) {
		t.Helper()
		want := httptest.NewRecorder()
		writeNotFound(want)
		for _, host := range hosts {
			for _, p := range paths {
				for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch} {
					rec := f.serve(on(method, host, p))
					label := method + " " + host + p
					if rec.Code != http.StatusNotFound || rec.Body.String() != want.Body.String() || !equalHeaders(rec.Header(), want.Header()) {
						t.Errorf("%s = %d %q %v, want the not-found answer", label, rec.Code, rec.Body.String(), rec.Header())
					}
				}
			}
		}
	}

	t.Run("claimed, removed and unknown hosts", func(t *testing.T) {
		f := newCustomHostFixture(t)
		delete(f.routes, hiddenHost) // removed in the UI, still routed here
		f.h.hosts.Invalidate()
		check(t, f, []string{pendingHost, failedHost, hiddenHost, unknownHost})
	})

	t.Run("before the table has loaded once", func(t *testing.T) {
		f := newCustomHostFixture(t)
		f.h.hosts = newHostTable(func(context.Context) (map[string]customHostRoute, error) {
			return nil, context.DeadlineExceeded
		})
		check(t, f, []string{openHost, internalHost, pendingHost, unknownHost})
		// Platform and internal hosts are unaffected.
		if rec := f.serve(on(http.MethodGet, platformHost, "/public/status/open")); rec.Code != http.StatusOK {
			t.Fatalf("platform host during a failed load = %d", rec.Code)
		}
		if rec := f.serve(on(http.MethodGet, "10.1.2.3:8080", "/healthz")); rec.Code != http.StatusOK {
			t.Fatalf("probe during a failed load = %d", rec.Code)
		}
	})

	t.Run("extra platform hosts get the platform routes", func(t *testing.T) {
		f := newCustomHostFixture(t)
		if rec := f.serve(on(http.MethodGet, appHost, "/public/status/hidden")); rec.Code != http.StatusOK {
			t.Fatalf("STATUS_PAGE_PLATFORM_HOSTS host = %d", rec.Code)
		}
	})

	t.Run("without a public platform host (development) other hosts fall through", func(t *testing.T) {
		f := newCustomHostFixture(t)
		f.h.config.StatusPageBaseURL = "http://localhost:8082"
		f.h.config.PlatformHosts = nil
		if rec := f.serve(on(http.MethodGet, pendingHost, "/public/status/open")); rec.Code != http.StatusOK {
			t.Fatalf("pending host in development = %d", rec.Code)
		}
		if rec := f.serve(on(http.MethodGet, openHost, "/")); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), pageSentinel) {
			t.Fatalf("verified domain in development = %d", rec.Code)
		}
	})
}

// A private page's custom domain, without a credential, answers on every
// route exactly like a host that serves no page: status, headers and body.
func TestCustomHost_PrivateIsIndistinguishableFromUnknownHost(t *testing.T) {
	f := newCustomHostFixture(t)
	for _, row := range customHostRouteTable {
		if row.slugRoute == "" || row.class == classUnlock {
			continue
		}
		for _, method := range []string{row.method, http.MethodGet, http.MethodPost} {
			private := f.serve(on(method, internalHost, row.path))
			unknown := f.serve(on(method, unknownHost, row.path))
			pending := f.serve(on(method, pendingHost, row.path))
			if pending.Code != unknown.Code || pending.Body.String() != unknown.Body.String() || !equalHeaders(pending.Header(), unknown.Header()) {
				t.Errorf("%s %s: pending host differs from an unknown one", method, row.path)
			}
			label := method + " " + row.path
			if private.Code != http.StatusNotFound || private.Code != unknown.Code || private.Body.String() != unknown.Body.String() {
				t.Errorf("%s: private %d %q vs unknown host %d %q", label, private.Code, private.Body.String(), unknown.Code, unknown.Body.String())
			}
			if !equalHeaders(private.Header(), unknown.Header()) {
				t.Errorf("%s: headers differ:\nprivate %v\nunknown %v", label, private.Header(), unknown.Header())
			}
		}
	}
	// And the unlock route, exempt from the gate, answers it the same way.
	req := on(http.MethodPost, internalHost, "/unlock")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := f.serve(req); rec.Code != http.StatusNotFound {
		t.Fatalf("unlock on a private page's domain = %d", rec.Code)
	}
}

// Host header handling: a forged or unknown Host never selects a page it is
// not, X-Forwarded-Host counts only behind the setting and a trusted proxy,
// and the platform host is never a custom domain.
func TestCustomHost_HostHeaderTrust(t *testing.T) {
	f := newCustomHostFixture(t)
	notFound := httptest.NewRecorder()
	writeNotFound(notFound)

	cases := []struct {
		name       string
		host       string
		remote     string
		forwarded  string
		trustFwd   bool
		wantServed bool
	}{
		{name: "custom domain", host: openHost, wantServed: true},
		{name: "with port", host: openHost + ":443", wantServed: true},
		{name: "upper case and trailing dot", host: "STATUS.Open.Example.COM.", wantServed: true},
		{name: "unknown host", host: unknownHost},
		{name: "platform host in the table", host: platformHost},
		{name: "garbage host", host: "evil.example.com/../x"},
		{name: "X-Forwarded-Host without the setting", host: platformHost, forwarded: openHost, remote: "10.1.2.3:5555"},
		{name: "X-Forwarded-Host from an untrusted peer", host: platformHost, forwarded: openHost, remote: "192.0.2.7:5555", trustFwd: true},
		{name: "X-Forwarded-Host from a trusted proxy", host: platformHost, forwarded: openHost, remote: "10.1.2.3:5555", trustFwd: true, wantServed: true},
		{name: "first X-Forwarded-Host value", host: platformHost, forwarded: openHost + ", " + unknownHost, remote: "10.1.2.3:5555", trustFwd: true, wantServed: true},
		{name: "trusted proxy forwarding an unknown host", host: openHost, forwarded: unknownHost, remote: "10.1.2.3:5555", trustFwd: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f.h.config.TrustForwardedHost = c.trustFwd
			req := on(http.MethodGet, openHost, "/")
			req.Host = c.host
			if c.remote != "" {
				req.RemoteAddr = c.remote
			}
			if c.forwarded != "" {
				req.Header.Set("X-Forwarded-Host", c.forwarded)
			}
			rec := f.serve(req)
			served := rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), pageSentinel)
			if served != c.wantServed {
				t.Fatalf("served = %v (%d %q), want %v", served, rec.Code, rec.Body.String(), c.wantServed)
			}
			if !served && (rec.Code != http.StatusNotFound || rec.Body.String() != notFound.Body.String()) {
				t.Fatalf("refusal = %d %q, want the not-found answer", rec.Code, rec.Body.String())
			}
		})
	}
	f.h.config.TrustForwardedHost = false

	// A custom domain's Host with another page's slug path serves nothing.
	if rec := f.serve(on(http.MethodGet, openHost, "/public/status/locked")); rec.Code != http.StatusNotFound {
		t.Fatalf("other slug on a custom domain = %d", rec.Code)
	}
}

// The table is read once per TTL however many hosts are asked for, so a
// stream of forged hosts never reaches the database.
func TestCustomHost_ForgedHostsDoNotReload(t *testing.T) {
	f := newCustomHostFixture(t)
	for i := 0; i < 200; i++ {
		f.serve(on(http.MethodGet, uuid.NewString()+".example.com", "/"))
	}
	if n := f.loads.Load(); n != 1 {
		t.Fatalf("table loaded %d times, want 1", n)
	}
	f.h.hosts.Invalidate()
	f.serve(on(http.MethodGet, openHost, "/"))
	if n := f.loads.Load(); n != 2 {
		t.Fatalf("after invalidation: loaded %d times, want 2", n)
	}
}

// Probes and scrapes arrive by IP or a bare service name: they never touch
// the table, so a stalled reload cannot fail a liveness probe.
func TestCustomHost_ProbesSkipTheTable(t *testing.T) {
	f := newCustomHostFixture(t)
	f.h.hosts = newHostTable(func(context.Context) (map[string]customHostRoute, error) {
		t.Fatal("the table was loaded for a host that cannot be a custom domain")
		return nil, nil
	})
	for _, host := range []string{"10.1.2.3:8080", "127.0.0.1", "localhost:8082", "status-page:8080", "[2001:db8::1]:8080"} {
		if rec := f.serve(on(http.MethodGet, host, "/healthz")); rec.Code != http.StatusOK {
			t.Errorf("/healthz on %s = %d", host, rec.Code)
		}
	}
}

// A removed domain stops being served as soon as the table is invalidated
// (the API's domain event), and a stale entry never serves a page that took
// over the slug.
func TestCustomHost_RemovalAndStaleEntries(t *testing.T) {
	f := newCustomHostFixture(t)
	if rec := f.serve(on(http.MethodGet, openHost, "/")); rec.Code != http.StatusOK {
		t.Fatalf("before removal = %d", rec.Code)
	}
	delete(f.routes, openHost)
	f.h.hosts.Invalidate()
	if rec := f.serve(on(http.MethodGet, openHost, "/")); rec.Code != http.StatusNotFound {
		t.Fatalf("after removal = %d", rec.Code)
	}

	// The slug "hidden" now belongs to another page than the table says.
	f.routes[hiddenHost] = customHostRoute{PageID: uuid.New(), Slug: "hidden"}
	f.h.hosts.Invalidate()
	for _, p := range []string{"/", "/data", "/summary.json"} {
		if rec := f.serve(on(http.MethodGet, hiddenHost, p)); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), pageSentinel) {
			t.Fatalf("%s on a stale entry = %d", p, rec.Code)
		}
	}
	req := on(http.MethodPost, hiddenHost, "/unlock")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := f.serve(req); rec.Code != http.StatusNotFound {
		t.Fatalf("unlock on a stale entry = %d", rec.Code)
	}
}

// A password page on its custom domain: the form posts to /unlock, the
// session cookie is host-only at /, and the visitor lands back on /.
func TestCustomHost_PasswordUnlock(t *testing.T) {
	f := newCustomHostFixture(t)
	form := f.serve(on(http.MethodGet, lockedHost, "/"))
	if form.Code != http.StatusUnauthorized || !strings.Contains(form.Body.String(), `action="/unlock"`) {
		t.Fatalf("unlock form = %d, body %q", form.Code, form.Body.String())
	}

	post := func(origin, password string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://"+lockedHost+"/unlock", strings.NewReader(url.Values{"password": {password}}.Encode()))
		req.Host = lockedHost
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		return f.serve(req)
	}
	// A post from another origin (the platform host's page) is refused.
	if rec := post("https://"+platformHost, f.password); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin unlock = %d", rec.Code)
	}
	if rec := post("https://"+lockedHost, "wrong password"); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `action="/unlock"`) {
		t.Fatalf("wrong password = %d", rec.Code)
	}
	rec := post("https://"+lockedHost, f.password)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("unlock = %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
	cookie := sessionCookieFrom(t, rec)
	if cookie.Path != "/" || cookie.Domain != "" {
		t.Fatalf("cookie path %q domain %q, want host-only at /", cookie.Path, cookie.Domain)
	}

	req := on(http.MethodGet, lockedHost, "/")
	req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	if page := f.serve(req); page.Code != http.StatusOK || !strings.Contains(page.Body.String(), pageSentinel) {
		t.Fatalf("page with the session = %d", page.Code)
	}
	// A sub-request from the page itself carries a same-origin Referer of
	// the custom domain, not a status-page path: nothing to refuse there.
	req = on(http.MethodGet, lockedHost, "/summary.json")
	req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Referer", "https://"+lockedHost+"/")
	if rec := f.serve(req); rec.Code == http.StatusNotFound || rec.Code == http.StatusUnauthorized {
		t.Fatalf("same-origin sub-request on the custom domain refused: %d", rec.Code)
	}
	// GET /unlock goes back to the page.
	if rec := f.serve(on(http.MethodGet, lockedHost, "/unlock")); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("GET /unlock = %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
}

// The session cookie is always Secure on a custom domain, whatever
// STATUS_PAGE_COOKIE_SECURE, the base URL or the request's scheme say:
// custom domains are https only, and a kiosk following a stray http:// link
// must not send its cookie in clear.
func TestCustomHost_SessionCookieSecure(t *testing.T) {
	on, off := true, false
	cases := []struct {
		name   string
		flag   *bool
		remote string
		proto  string
	}{
		{name: "plain http, nothing configured"},
		{name: "https from a trusted proxy", remote: "10.1.2.3:4444", proto: "https"},
		{name: "https claimed by an untrusted peer", remote: "192.0.2.9:4444", proto: "https"},
		{name: "forced on", flag: &on},
		{name: "forced off", flag: &off, remote: "10.1.2.3:4444", proto: "http"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newCustomHostFixture(t)
			f.h.config.CookieSecure = c.flag
			f.h.config.StatusPageBaseURL = "http://" + platformHost
			req := httptest.NewRequest(http.MethodPost, "http://"+lockedHost+"/unlock", strings.NewReader("password="+url.QueryEscape(f.password)))
			req.Host = lockedHost
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if c.remote != "" {
				req.RemoteAddr = c.remote
			}
			if c.proto != "" {
				req.Header.Set("X-Forwarded-Proto", c.proto)
			}
			rec := f.serve(req)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("unlock = %d", rec.Code)
			}
			if !sessionCookieFrom(t, rec).Secure {
				t.Fatal("session cookie on a custom domain is not Secure")
			}
		})
	}
}

// A kiosk URL on a private page's custom domain opens a session at / and
// sends the display to the clean root URL.
func TestCustomHost_KioskToken(t *testing.T) {
	f := newCustomHostFixture(t)
	rec := f.serve(on(http.MethodGet, internalHost, "/?token="+url.QueryEscape(f.token)+"&mode=kiosk"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("kiosk URL = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/?mode=kiosk" {
		t.Fatalf("Location = %q, want the root without the token", loc)
	}
	cookie := sessionCookieFrom(t, rec)
	if cookie.Path != "/" {
		t.Fatalf("cookie path %q", cookie.Path)
	}
	req := on(http.MethodGet, internalHost, "/?mode=kiosk")
	req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	if page := f.serve(req); page.Code != http.StatusOK {
		t.Fatalf("page with the kiosk session = %d", page.Code)
	}
	// On a public page the token is still taken out of the URL.
	if rec := f.serve(on(http.MethodGet, openHost, "/?token=x")); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("token on a public page's domain = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

// A push subscription posted from another origin than the custom domain is
// refused before anything is stored.
func TestCustomHost_PushOrigin(t *testing.T) {
	f := newCustomHostFixture(t)
	for origin, want := range map[string]bool{
		"https://" + openHost:     true,
		"https://" + platformHost: false,
		"https://" + hiddenHost:   false,
	} {
		req := on(http.MethodPost, openHost, "/push/unsubscribe")
		req.Header.Set("Origin", origin)
		_, ok := customHostFrom(req)
		if ok {
			t.Fatal("request built with a custom host already")
		}
		var allowed bool
		probe := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { allowed = f.h.pushOriginOK(r) })
		f.h.CustomHosts(probe).ServeHTTP(httptest.NewRecorder(), req)
		if allowed != want {
			t.Errorf("Origin %s: allowed = %v, want %v", origin, allowed, want)
		}
	}
}

func sessionCookieFrom(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatalf("no session cookie in %v", rec.Header())
	return nil
}

func TestHostTable_ReloadAndFailure(t *testing.T) {
	var fail atomic.Bool
	var loads atomic.Int32
	table := newHostTable(func(context.Context) (map[string]customHostRoute, error) {
		loads.Add(1)
		if fail.Load() {
			return nil, context.DeadlineExceeded
		}
		return map[string]customHostRoute{openHost: {Slug: "open", Verified: true}}, nil
	})
	now := time.Now()
	table.now = func() time.Time { return now }
	// Never loaded: nothing is known, and the caller is told so.
	fail.Store(true)
	if _, known, loaded := table.Lookup(context.Background(), openHost); known || loaded {
		t.Fatal("a failed first load reported a table")
	}
	now = now.Add(hostTableRetry + time.Second)
	fail.Store(false)
	if route, known, loaded := table.Lookup(context.Background(), openHost); !known || !loaded || !route.Verified {
		t.Fatal("miss on a known host")
	}
	// Expired, and the reload fails: the previous table keeps serving.
	now = now.Add(hostTableTTL + time.Second)
	fail.Store(true)
	if _, known, loaded := table.Lookup(context.Background(), openHost); !known || !loaded {
		t.Fatal("a failed reload dropped the table")
	}
	// The failure is retried after hostTableRetry, not on every request.
	n := loads.Load()
	table.Lookup(context.Background(), openHost)
	if loads.Load() != n {
		t.Fatal("retried at once")
	}
	now = now.Add(hostTableRetry + time.Second)
	fail.Store(false)
	table.Lookup(context.Background(), openHost)
	if loads.Load() != n+1 {
		t.Fatal("not retried after the retry delay")
	}
}

// A public page's custom domain serves the subscribe form's target and the
// mail link routes at its root; the cached page's form posts to the page's
// canonical origin, which a visitor on the platform host may post to.
func TestCustomHost_Subscriptions(t *testing.T) {
	f := newCustomHostFixture(t)
	api := &fakeSubscriberAPI{status: http.StatusOK, body: `{"state":"unsubscribed","page_title":"Open"}`}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	f.h.config.InternalAPIURL = srv.URL
	f.h.config.InternalToken = testInternalToken

	landing := f.serve(on(http.MethodGet, openHost, "/subscription?action=unsubscribe"))
	if landing.Code != http.StatusOK || !strings.Contains(landing.Body.String(), `action="subscription/unsubscribe"`) {
		t.Fatalf("landing on a custom domain = %d %q", landing.Code, landing.Body.String())
	}
	req := on(http.MethodPost, openHost, "/subscription/unsubscribe")
	req.Body = io.NopCloser(strings.NewReader("token=tok"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := f.serve(req); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "You are unsubscribed") {
		t.Fatalf("unsubscribe on a custom domain = %d %q", rec.Code, rec.Body.String())
	}
	api.status, api.body = http.StatusAccepted, `{"state":"check_inbox"}`
	sub := on(http.MethodPost, openHost, "/subscribe")
	sub.Body = io.NopCloser(strings.NewReader("email=a%40example.com"))
	sub.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	sub.Header.Set("Origin", "https://"+platformHost)
	if rec := f.serve(sub); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Check your inbox") {
		t.Fatalf("subscribe posted from the platform host to the domain = %d %q", rec.Code, rec.Body.String())
	}
	if got := api.requests[len(api.requests)-1].path; got != "/api/v1/public/status-pages/open/subscribe" {
		t.Fatalf("forwarded to %q", got)
	}
}

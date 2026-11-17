package statuspage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/pageaccess"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

// gatedAddonFixture serves the add-on test page ("acme") from primed render
// and feed caches behind a fake access store, so the gate's effect on every
// add-on is tested without a database (a cache miss would panic).
func gatedAddonFixture(t *testing.T, visibility string) (*Handlers, *fakeAccess, *http.Cookie) {
	t.Helper()
	data := addonTestData()
	data.Visibility = visibility
	h := primedHandlers(t, data)
	if _, _, err := h.cache.GetPage(feedCacheKey("acme"), func() (renderedPage, error) {
		return renderedPage{html: `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><title>Acme</title></feed>`}, nil
	}); err != nil {
		t.Fatal(err)
	}
	pageID := uuid.MustParse(addonTestPageID)
	tokenID := uuid.New()
	_, tokenHash, _ := pageaccess.NewToken()
	store := &fakeAccess{
		pages:  map[string]*pageAccess{"acme": {ID: pageID, Title: "Acme", Visibility: visibility}},
		tokens: map[string]fakeToken{tokenHash: {id: tokenID, pageID: pageID}},
	}
	h.access.store = store
	h.access.sessions = newSessionSigner(testSessionSecret)
	session := &http.Cookie{Name: sessionCookieName, Value: h.access.sessions.sign(pageID,
		sessionCredential{kind: sessionKindToken, tokenID: tokenID}, time.Now().Add(time.Hour))}
	return h, store, session
}

var addonPaths = []string{
	"/summary.json",
	"/feed.atom",
	"/badge.svg",
	"/monitors/" + addonTestAPI + "/badge.svg",
	"/monitors/" + addonTestAPI + "/uptime.svg",
}

func TestGatedAddons_RefusedWithoutSession(t *testing.T) {
	for _, tc := range []struct {
		visibility string
		want       int
	}{
		{pageaccess.Private, http.StatusNotFound},
		{pageaccess.Password, http.StatusUnauthorized},
	} {
		h, _, _ := gatedAddonFixture(t, tc.visibility)
		for _, p := range addonPaths {
			rec := serveAddonTest(h, http.MethodGet, "/public/status/acme"+p, nil)
			if rec.Code != tc.want {
				t.Errorf("%s %s = %d, want %d", tc.visibility, p, rec.Code, tc.want)
			}
			if strings.Contains(rec.Body.String(), "Acme") || strings.Contains(rec.Body.String(), "<svg") {
				t.Errorf("%s %s leaked add-on content", tc.visibility, p)
			}
			if tc.visibility == pageaccess.Private {
				missing := serveAddonTest(h, http.MethodGet, "/public/status/no-such-page"+p, nil)
				if missing.Code != rec.Code || missing.Body.String() != rec.Body.String() || !equalHeaders(missing.Header(), rec.Header()) {
					t.Errorf("private %s differs from a missing page", p)
				}
			}
		}
	}
}

// With a session the add-ons work for that visitor, privately: no shared
// caching, no validator (never a 304), no CORS grant, same-origin only.
func TestGatedAddons_WorkPrivatelyWithSession(t *testing.T) {
	h, _, session := gatedAddonFixture(t, pageaccess.Private)
	for _, p := range addonPaths {
		req := httptest.NewRequest(http.MethodGet, "/public/status/acme"+p, nil)
		req.AddCookie(session)
		req.Header.Set("If-None-Match", "*")
		rec := httptest.NewRecorder()
		h.HandleStatusPage(rec, req)
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Fatalf("%s with a session = %d", p, rec.Code)
		}
		hdr := rec.Header()
		if hdr.Get("Cache-Control") != "private, no-store" || hdr.Get("ETag") != "" ||
			hdr.Get("Access-Control-Allow-Origin") != "" || hdr.Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Errorf("%s gated headers: %v", p, hdr)
		}
	}
}

// Another status page on this origin cannot read a gated page's add-ons
// with the visitor's cookie; a badge embedded on another site is a cross-site
// image request, which never carries the SameSite=Lax session cookie, so it
// is refused like any request without one.
func TestGatedAddons_OtherPagesAndSites(t *testing.T) {
	h, _, session := gatedAddonFixture(t, pageaccess.Private)
	req := httptest.NewRequest(http.MethodGet, "/public/status/acme/badge.svg", nil)
	req.AddCookie(session)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	req.Header.Set("Sec-Fetch-Dest", "image")
	req.Header.Set("Referer", "http://example.com/public/status/other")
	rec := httptest.NewRecorder()
	h.HandleStatusPage(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("another page's <img> of a gated badge = %d, want 404", rec.Code)
	}
	// The cross-site embed, as the browser sends it: no cookie (the session
	// cookie is SameSite=Lax, TestAccess_PasswordUnlockFlow).
	cross := serveAddonTest(h, http.MethodGet, "/public/status/acme/badge.svg", map[string]string{
		"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image",
		"Referer": "https://github.com/",
	})
	if cross.Code != http.StatusNotFound {
		t.Fatalf("cross-site embed of a gated badge = %d, want 404", cross.Code)
	}
}

// A public page's add-ons are cached publicly; once the page turns private
// the gate refuses them before the caches, and the access-change event drops
// the page, summary and feed entries so nothing built while public survives.
func TestGatedAddons_PublicCachesDroppedOnAccessChange(t *testing.T) {
	h, store, _ := gatedAddonFixture(t, pageaccess.Public)
	for _, p := range addonPaths {
		rec := serveAddonTest(h, http.MethodGet, "/public/status/acme"+p, nil)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Cache-Control"), "public, max-age=") {
			t.Fatalf("public %s = %d %v", p, rec.Code, rec.Header())
		}
	}

	store.set("acme", func(p *pageAccess) { p.Visibility = pageaccess.Private })
	for _, p := range addonPaths {
		if rec := serveAddonTest(h, http.MethodGet, "/public/status/acme"+p, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s after turning private = %d, want 404 before any cache", p, rec.Code)
		}
	}

	hub := NewHub()
	sub := &Subscriber{
		hub:    hub,
		cache:  h.cache,
		logger: h.logger,
		resolveSlugs: func(context.Context, statusupdates.Event) ([]string, error) {
			return []string{"acme"}, nil
		},
	}
	sub.handleEvent(statusupdates.Event{Type: statusupdates.EventStatusPageAccessChanged, StatusPageID: addonTestPageID})
	for _, key := range []string{"acme", feedCacheKey("acme")} {
		if _, ok := h.cache.lookup(key); ok {
			t.Errorf("cache entry %q survived the access change", key)
		}
	}
}

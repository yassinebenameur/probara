package statuspage

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// The status-page service forwards nothing to the operator API on a
// visitor's behalf. A custom template written for an older release may still
// call /_sp_api/…: on every host that path must answer exactly like any other
// unknown path, and no request may leave the service.
func TestOperatorAPIPath_IsAnUnknownPath(t *testing.T) {
	f := newCustomHostFixture(t)
	var upstream atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstream.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()
	// The subscription forwarding target is the one API origin the service
	// knows; nothing on these paths may reach it.
	f.h.config.InternalAPIURL = srv.URL

	notFound := httptest.NewRecorder()
	writeNotFound(notFound)

	suffixes := []string{
		"/",
		"/api/v1/monitors",
		"/api/v1/status-pages/" + f.store.pages["open"].ID.String(),
	}
	methods := []string{http.MethodGet, http.MethodPatch, http.MethodPost, http.MethodDelete}
	withOperatorCredentials := func(req *http.Request) *http.Request {
		req.AddCookie(&http.Cookie{Name: "admin_access", Value: "operator-session"})
		req.Header.Set("Authorization", "Bearer operator-key")
		req.Header.Set("X-Status-Page-ID", f.store.pages["open"].ID.String())
		return req
	}

	for _, suffix := range suffixes {
		for _, method := range methods {
			// Platform host: the mux's catch-all not-found answer.
			got := f.serve(withOperatorCredentials(on(method, platformHost, "/_sp_api"+suffix)))
			want := f.serve(withOperatorCredentials(on(method, platformHost, "/no-such-route"+suffix)))
			label := method + " " + platformHost + "/_sp_api" + suffix
			if got.Code != http.StatusNotFound || got.Body.String() != notFound.Body.String() || !equalHeaders(got.Header(), notFound.Header()) {
				t.Errorf("%s = %d %q %v, want the not-found answer", label, got.Code, got.Body.String(), got.Header())
			}
			if got.Code != want.Code || got.Body.String() != want.Body.String() || !equalHeaders(got.Header(), want.Header()) {
				t.Errorf("%s differs from an unknown path: %d %q, want %d %q", label, got.Code, got.Body.String(), want.Code, want.Body.String())
			}

			// Custom domains: an unknown sub-route of the page, behind its
			// gate like any other.
			for _, host := range []string{openHost, lockedHost, internalHost} {
				got := f.serve(withOperatorCredentials(on(method, host, "/_sp_api"+suffix)))
				want := f.serve(withOperatorCredentials(on(method, host, "/no-such-route"+suffix)))
				label := method + " " + host + "/_sp_api" + suffix
				if got.Code != want.Code || got.Body.String() != want.Body.String() {
					t.Errorf("%s = %d %q, want %d %q (an unknown path)", label, got.Code, got.Body.String(), want.Code, want.Body.String())
				}
				if strings.Contains(got.Body.String(), pageSentinel) {
					t.Errorf("%s served page content", label)
				}
			}
		}
	}
	if n := upstream.Load(); n != 0 {
		t.Fatalf("%d requests reached the API", n)
	}
}

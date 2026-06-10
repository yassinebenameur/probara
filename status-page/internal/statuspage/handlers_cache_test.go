package statuspage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestEtagMatches(t *testing.T) {
	etag := `"abc123"`
	cases := []struct {
		ifNoneMatch string
		want        bool
	}{
		{"", false},
		{`"abc123"`, true},
		{`"other"`, false},
		{"*", true},
		{`"first", "abc123"`, true},
		{`"first", "second"`, false},
		{`W/"abc123"`, true},
		{`"first" , "abc123" `, true},
		{"abc123", false}, // unquoted token is not the same validator
	}
	for _, tc := range cases {
		if got := etagMatches(tc.ifNoneMatch, etag); got != tc.want {
			t.Errorf("etagMatches(%q, %q) = %v, want %v", tc.ifNoneMatch, etag, got, tc.want)
		}
	}
	if etagMatches("*", "") {
		t.Error(`etagMatches("*", "") = true, want false (no etag to match)`)
	}
}

func TestHandleStatusPageETagRoundTrip(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-page-etag")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "etag-status", "ETag Status")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorID, 0)

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	cache := newRenderCache(time.Minute)
	h := NewHandlers(svc, nil, logger.New("status-page", "debug"), NewHub(), cache)

	get := func(ifNoneMatch string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/public/status/etag-status", nil)
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		rec := httptest.NewRecorder()
		h.HandleStatusPage(rec, req)
		return rec
	}

	// First request: full page with a strong ETag and revalidation headers.
	first := get("")
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", first.Code, first.Body.String())
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag header on 200 response")
	}
	if cc := first.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want %q", cc, "no-cache")
	}
	if first.Body.Len() == 0 {
		t.Fatal("200 response has empty body")
	}

	// Conditional request with the matching validator: 304, no body.
	second := get(etag)
	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Fatalf("304 response body = %q, want empty", second.Body.String())
	}
	if got := second.Header().Get("ETag"); got != etag {
		t.Fatalf("304 ETag = %q, want %q", got, etag)
	}

	// Stale validator: full page again, same ETag (served from cache).
	third := get(`"deadbeefdeadbeefdeadbeefdeadbeef"`)
	if third.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", third.Code)
	}
	if got := third.Header().Get("ETag"); got != etag {
		t.Fatalf("ETag changed across cached responses: %q != %q", got, etag)
	}
	if third.Body.Len() == 0 {
		t.Fatal("200 response has empty body")
	}

	// After invalidation the page is rebuilt and a conditional request with
	// the freshly rebuilt validator revalidates to a 304 again.
	cache.Invalidate("etag-status")
	rebuilt := get("")
	if rebuilt.Code != http.StatusOK {
		t.Fatalf("status after invalidation = %d, want 200", rebuilt.Code)
	}
	rebuiltETag := rebuilt.Header().Get("ETag")
	if rebuiltETag == "" {
		t.Fatal("missing ETag header after rebuild")
	}
	fourth := get(rebuiltETag)
	if fourth.Code != http.StatusNotModified {
		t.Fatalf("status for rebuilt validator = %d, want 304", fourth.Code)
	}

	// Unknown slug stays a 404 and is never cached.
	req := httptest.NewRequest(http.MethodGet, "/public/status/does-not-exist", nil)
	rec := httptest.NewRecorder()
	h.HandleStatusPage(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status for unknown slug = %d, want 404", rec.Code)
	}
}

//go:build !unit

package statuspage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/pageaccess"
	"github.com/yassinebenameur/probara/shared/statustemplate"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// The gate against the real schema and the real handlers: the access query,
// the render cache warmed while the page was public, every sub-route with a
// real service behind it (a route that skipped the gate would serve data
// here), kiosk tokens and the draft preview's own token.
func TestAccessIntegration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	const previewKey = "preview-secret-for-access-integration"
	t.Setenv(previewSecretEnvVar, previewKey)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-page-access")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Checkout API")
	privateID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "int-private", "Internal Status")
	lockedID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "int-locked", "Locked Status")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, privateID, monitorID, 0)
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, lockedID, monitorID, 0)

	hash, err := pageaccess.HashPassword("visitor-password")
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := dbClient.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`UPDATE status_pages SET visibility = 'password', password_hash = $1, password_version = 1 WHERE id = $2`, hash, lockedID)
	liveToken, liveHash, _ := pageaccess.NewToken()
	revokedToken, revokedHash, _ := pageaccess.NewToken()
	exec(`INSERT INTO status_page_access_tokens (status_page_id, label, token_hash) VALUES ($1, 'Lobby', $2)`, privateID, liveHash)
	exec(`INSERT INTO status_page_access_tokens (status_page_id, label, token_hash, revoked_at) VALUES ($1, 'Old', $2, NOW())`, privateID, revokedHash)

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	cfg := &config.StatusPageConfig{SessionSecret: testSessionSecret}
	h := NewHandlers(svc, cfg, logger.New("status-page", "error"), NewHub(), newRenderCache(time.Hour), nil)
	do := func(req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.HandleStatusPage(rec, req)
		return rec
	}
	get := func(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		return do(req)
	}

	// Rendered and cached while public; then the page goes private without
	// any cache invalidation reaching this process.
	if rec := get("/public/status/int-private"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Checkout API") {
		t.Fatalf("public render = %d", rec.Code)
	}
	// Its add-ons too: summary and badges share the page's cache entry, the
	// feed has its own, all keyed by slug.
	for _, p := range []string{"/summary.json", "/feed.atom", "/badge.svg"} {
		rec := get("/public/status/int-private" + p)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Cache-Control"), "public") {
			t.Fatalf("public %s = %d %v", p, rec.Code, rec.Header())
		}
	}
	exec(`UPDATE status_pages SET visibility = 'private' WHERE id = $1`, privateID)

	t.Run("private is indistinguishable from missing on every route", func(t *testing.T) {
		for _, row := range routeTable {
			if row.method == "" {
				continue
			}
			for _, method := range []string{row.method, http.MethodGet, http.MethodPost} {
				a := do(notFoundRequest(method, "/public/status/int-private"+row.route))
				b := do(notFoundRequest(method, "/public/status/no-such-page"+row.route))
				wantNotFound := row.class != classSubscriptionLink
				if (wantNotFound && a.Code != http.StatusNotFound) || a.Code != b.Code ||
					withoutNonce(a.Body.String()) != withoutNonce(b.Body.String()) || !equalHeaders(nonceFree(a.Header()), nonceFree(b.Header())) {
					t.Errorf("%s %s: private %d %v %q / missing %d %v %q", method, row.route,
						a.Code, a.Header(), a.Body.String(), b.Code, b.Header(), b.Body.String())
				}
				if strings.Contains(a.Body.String(), "Checkout API") {
					t.Errorf("%s %s: private content served", method, row.route)
				}
			}
		}
	})

	t.Run("kiosk token", func(t *testing.T) {
		if rec := get("/public/status/int-private?token=" + url.QueryEscape(revokedToken)); rec.Code != http.StatusNotFound {
			t.Fatalf("revoked token = %d, want 404", rec.Code)
		}
		rec := get("/public/status/int-private?token=" + url.QueryEscape(liveToken))
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/public/status/int-private" {
			t.Fatalf("live token = %d → %q", rec.Code, rec.Header().Get("Location"))
		}
		session := findCookie(t, rec, sessionCookieName)
		page := get("/public/status/int-private", session)
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Checkout API") {
			t.Fatalf("page with token session = %d", page.Code)
		}
		if page.Header().Get("Cache-Control") != "private, no-store" || page.Header().Get("X-Robots-Tag") != robotsNoIndex {
			t.Fatalf("gated page headers = %v", page.Header())
		}
		data := get("/public/status/int-private/data", session)
		var body StatusPageData
		if data.Code != http.StatusOK || json.Unmarshal(data.Body.Bytes(), &body) != nil || body.Slug != "int-private" {
			t.Fatalf("/data with token session = %d %s", data.Code, data.Body.String())
		}
		if data.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("/data of a gated page is cacheable")
		}
		for _, p := range []string{"/summary.json", "/feed.atom", "/badge.svg", "/monitors/" + monitorID.String() + "/badge.svg"} {
			rec := get("/public/status/int-private"+p, session)
			if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" ||
				rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("ETag") != "" {
				t.Fatalf("%s with a token session = %d %v", p, rec.Code, rec.Header())
			}
		}
		if !strings.Contains(get("/public/status/int-private", session).Body.String(), `content="noindex, nofollow"`) {
			t.Fatal("private page head is not noindex")
		}

		exec(`UPDATE status_page_access_tokens SET revoked_at = NOW() WHERE token_hash = $1`, liveHash)
		if rec := get("/public/status/int-private", session); rec.Code != http.StatusNotFound {
			t.Fatalf("session of a revoked token = %d, want 404", rec.Code)
		}
	})

	t.Run("password", func(t *testing.T) {
		if rec := get("/public/status/int-locked/data"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("locked /data = %d, want 401", rec.Code)
		}
		req := httptest.NewRequest(http.MethodPost, "/public/status/int-locked/unlock", strings.NewReader("password=visitor-password"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := do(req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("unlock = %d", rec.Code)
		}
		session := findCookie(t, rec, sessionCookieName)
		if data := get("/public/status/int-locked/data", session); data.Code != http.StatusOK {
			t.Fatalf("/data after unlock = %d", data.Code)
		}
		// The API changes the password: the session ends.
		exec(`UPDATE status_pages SET password_version = password_version + 1 WHERE id = $1`, lockedID)
		if data := get("/public/status/int-locked/data", session); data.Code != http.StatusUnauthorized {
			t.Fatalf("/data after a password change = %d, want 401", data.Code)
		}
	})

	t.Run("draft preview keeps its own token", func(t *testing.T) {
		if rec := get("/public/status/int-private/preview/draft"); rec.Code != http.StatusNotFound {
			t.Fatalf("preview without a token = %d, want 404", rec.Code)
		}
		token := statustemplate.MintPreviewToken(previewKey, privateID.String(), time.Now().Add(time.Hour))
		rec := get("/public/status/int-private/preview/draft?token=" + url.QueryEscape(token))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Checkout API") {
			t.Fatalf("preview with its token = %d", rec.Code)
		}
		if rec.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("gated preview is cacheable")
		}
		other := statustemplate.MintPreviewToken(previewKey, lockedID.String(), time.Now().Add(time.Hour))
		if rec := get("/public/status/int-private/preview/draft?token=" + url.QueryEscape(other)); rec.Code != http.StatusNotFound {
			t.Fatalf("another page's preview token = %d, want 404", rec.Code)
		}
	})

	t.Run("access query", func(t *testing.T) {
		acc, err := svc.PageAccess(ctx, "int-private", pageaccess.HashToken(revokedToken), uuid.New())
		if err != nil {
			t.Fatal(err)
		}
		if acc.ID != privateID || acc.Visibility != pageaccess.Private || acc.PresentedTokenID != uuid.Nil || acc.SessionTokenLive {
			t.Fatalf("access = %+v", acc)
		}
		if _, err := svc.PageAccess(ctx, "nope", "", uuid.Nil); err != ErrStatusPageNotFound {
			t.Fatalf("missing slug err = %v", err)
		}
		if v, err := svc.PageVisibilityByID(ctx, lockedID); err != nil || v != pageaccess.Password {
			t.Fatalf("visibility by id = %q %v", v, err)
		}
	})

	t.Run("pending notifications of a gated page are closed unsent", func(t *testing.T) {
		publicID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "int-public", "Public Status")
		var gatedDelivery, publicDelivery uuid.UUID
		insert := func(pageID uuid.UUID) uuid.UUID {
			var id uuid.UUID
			if err := dbClient.QueryRowContext(ctx, `
				INSERT INTO status_page_push_deliveries (status_page_id, monitor_id, interval_id, kind, monitor_name)
				VALUES ($1, $2, $3, 'down', 'Checkout API') RETURNING id`, pageID, monitorID, uuid.New()).Scan(&id); err != nil {
				t.Fatal(err)
			}
			return id
		}
		gatedDelivery = insert(lockedID)
		publicDelivery = insert(publicID)

		claimed, err := newPushStore(dbClient.DB).ClaimDeliveries(ctx, 10, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if len(claimed) != 1 || claimed[0].ID != publicDelivery {
			t.Fatalf("claimed = %+v, want only the public page's", claimed)
		}
		var status string
		var sent, failed int
		if err := dbClient.QueryRowContext(ctx, `SELECT status, sent_count, failed_count FROM status_page_push_deliveries WHERE id = $1`, gatedDelivery).
			Scan(&status, &sent, &failed); err != nil {
			t.Fatal(err)
		}
		if status != "sent" || sent != 0 || failed != 0 {
			t.Fatalf("gated delivery = %s %d/%d, want closed unsent", status, sent, failed)
		}
	})

	t.Run("push is off on gated pages", func(t *testing.T) {
		exec(`UPDATE status_pages SET settings = jsonb_set(COALESCE(settings, '{}'::jsonb), '{enable_push_notifications}', 'true') WHERE id = $1`, lockedID)
		store := newPushStore(dbClient.DB)
		id, enabled, err := store.PageIDForSlug(ctx, "int-locked")
		if err != nil || id != lockedID || enabled {
			t.Fatalf("PageIDForSlug on a password page = %v %v %v", id, enabled, err)
		}
		exec(`UPDATE status_pages SET visibility = 'unlisted' WHERE id = $1`, lockedID)
		if _, enabled, _ := store.PageIDForSlug(ctx, "int-locked"); !enabled {
			t.Fatal("push refused on an unlisted page")
		}
	})
}

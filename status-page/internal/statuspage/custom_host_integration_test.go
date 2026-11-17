//go:build !unit

package statuspage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestCustomDomains serves stored pages through the real routes on their
// verified custom domains: only verified domains route, each to its own page;
// the canonical link, feed and summary name the primary domain on every
// host, so the slug-keyed render is the same bytes on all of them; push
// subscriptions remember their origin while it serves the page; and removing
// or failing a domain, hiding or deleting the page stops it at once.
func TestCustomDomains(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantA := testutil.InsertTenant(ctx, t, dbClient, "custom-domains-a")
	tenantB := testutil.InsertTenant(ctx, t, dbClient, "custom-domains-b")
	acme := testutil.InsertStatusPage(ctx, t, dbClient, tenantA, "acme", "Acme Status")
	beta := testutil.InsertStatusPage(ctx, t, dbClient, tenantB, "beta", "Beta Status")
	monitor := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantA, "API")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, acme, monitor, 0)
	if _, err := dbClient.ExecContext(ctx, `UPDATE status_pages SET settings = '{"enable_push_notifications": true}'::jsonb WHERE id = $1`, acme); err != nil {
		t.Fatal(err)
	}

	addDomain := func(pageID uuid.UUID, host, status string, age time.Duration) {
		t.Helper()
		if _, err := dbClient.ExecContext(ctx, `
			INSERT INTO status_page_domains (status_page_id, hostname, verification_token, status, created_at)
			VALUES ($1, $2, 't', $3, NOW() - make_interval(secs => $4))`, pageID, host, status, age.Seconds()); err != nil {
			t.Fatal(err)
		}
	}
	addDomain(acme, "status.acme.example.com", "verified", 2*time.Hour)
	addDomain(acme, "www.acme.example.com", "verified", time.Hour)
	addDomain(acme, "pending.acme.example.com", "pending", 3*time.Hour)
	addDomain(acme, "failed.acme.example.com", "failed", 4*time.Hour)
	addDomain(beta, "status.beta.example.com", "verified", time.Hour)

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	cfg := &config.StatusPageConfig{StatusPageBaseURL: "https://status.platform.example.com"}
	cache := newRenderCache(time.Minute)
	h := NewHandlers(svc, cfg, logger.New("status-page", "error"), NewHub(), cache, nil)
	handler := h.CustomHosts(h.routes(http.NotFoundHandler()))
	get := func(host, path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
		req.Host = host
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	const canonical = "https://status.acme.example.com/"

	t.Run("canonical, feed and summary links name the primary domain on every host", func(t *testing.T) {
		var etags []string
		for _, target := range [][2]string{
			{"status.acme.example.com", "/"},
			{"www.acme.example.com", "/"},
			{"status.platform.example.com", "/public/status/acme"},
		} {
			rec := get(target[0], target[1])
			if rec.Code != http.StatusOK {
				t.Fatalf("%s%s = %d", target[0], target[1], rec.Code)
			}
			body := rec.Body.String()
			for _, want := range []string{
				`<link rel="canonical" href="` + canonical + `"`,
				`<meta property="og:url" content="` + canonical + `"`,
				`href="https://status.acme.example.com/feed.atom"`,
				`href="https://status.acme.example.com/summary.json"`,
				"Acme Status",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("%s%s lacks %s", target[0], target[1], want)
				}
			}
			etags = append(etags, rec.Header().Get("ETag"))
		}
		// The render never depends on the host, so one slug-keyed cache
		// entry serves every host the page is on.
		if etags[0] == "" || etags[0] != etags[1] || etags[0] != etags[2] {
			t.Fatalf("ETags differ between hosts: %v", etags)
		}

		feed := get("status.acme.example.com", "/feed.atom")
		if feed.Code != http.StatusOK ||
			!strings.Contains(feed.Body.String(), `rel="self" type="application/atom+xml" href="https://status.acme.example.com/feed.atom"`) ||
			!strings.Contains(feed.Body.String(), `rel="alternate" type="text/html" href="`+canonical+`"`) {
			t.Fatalf("feed = %d %s", feed.Code, feed.Body.String())
		}
		summary := get("www.acme.example.com", "/summary.json")
		var doc struct {
			Page struct {
				URL string `json:"url"`
			} `json:"page"`
		}
		if err := json.Unmarshal(summary.Body.Bytes(), &doc); err != nil || doc.Page.URL != canonical {
			t.Fatalf("summary url = %q (err %v): %s", doc.Page.URL, err, summary.Body.String())
		}
		if rec := get("status.acme.example.com", "/badge.svg"); rec.Code != http.StatusOK {
			t.Fatalf("badge on the custom domain = %d", rec.Code)
		}
	})

	t.Run("only verified domains route, each to its own page", func(t *testing.T) {
		// Unverified hostnames still pointed at the service get nothing:
		// not the page, not another page's slug, not the platform routes.
		for _, host := range []string{"pending.acme.example.com", "failed.acme.example.com", "nothing.example.com"} {
			for _, p := range []string{"/", "/public/status/beta", "/public/status/acme/summary.json", "/metrics"} {
				if rec := get(host, p); rec.Code != http.StatusNotFound {
					t.Errorf("%s%s = %d", host, p, rec.Code)
				}
			}
		}
		rec := get("status.beta.example.com", "/")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Beta Status") || strings.Contains(rec.Body.String(), "Acme Status") {
			t.Fatalf("beta's domain = %d", rec.Code)
		}
		if rec := get("status.beta.example.com", "/public/status/acme"); rec.Code != http.StatusNotFound {
			t.Fatalf("acme through beta's domain = %d", rec.Code)
		}
		// A page without a custom domain keeps its platform canonical URL.
		if rec := get("status.platform.example.com", "/public/status/beta"); !strings.Contains(rec.Body.String(), `href="https://status.beta.example.com/"`) {
			t.Fatalf("beta canonical missing")
		}
	})

	t.Run("push subscriptions remember their origin while it serves the page", func(t *testing.T) {
		store := newPushStore(dbClient.DB)
		for _, sub := range []pushSubscriptionRow{
			{Endpoint: "https://fcm.googleapis.com/fcm/send/platform-endpoint", P256dh: "k", Auth: "a"},
			{Endpoint: "https://fcm.googleapis.com/fcm/send/custom-endpoint", P256dh: "k", Auth: "a", OriginHost: "www.acme.example.com"},
		} {
			if ok, err := store.Subscribe(ctx, acme, sub, "test", 100); err != nil || !ok {
				t.Fatalf("Subscribe(%s) = %v %v", sub.Endpoint, ok, err)
			}
		}
		origins := func() map[string]string {
			subs, err := store.SubscriptionsForPage(ctx, acme)
			if err != nil {
				t.Fatal(err)
			}
			out := map[string]string{}
			for _, s := range subs {
				out[s.Endpoint] = s.OriginHost
			}
			return out
		}
		got := origins()
		if got["https://fcm.googleapis.com/fcm/send/platform-endpoint"] != "" || got["https://fcm.googleapis.com/fcm/send/custom-endpoint"] != "www.acme.example.com" {
			t.Fatalf("origins %v", got)
		}
		if _, err := dbClient.ExecContext(ctx, `UPDATE status_page_domains SET status = 'failed' WHERE hostname = 'www.acme.example.com'`); err != nil {
			t.Fatal(err)
		}
		if got := origins(); got["https://fcm.googleapis.com/fcm/send/custom-endpoint"] != "" {
			t.Fatalf("a failed domain still named as the deep link: %v", got)
		}
		if _, err := dbClient.ExecContext(ctx, `UPDATE status_page_domains SET status = 'verified' WHERE hostname = 'www.acme.example.com'`); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("removal, failure, privacy and deletion stop serving at once", func(t *testing.T) {
		if rec := get("www.acme.example.com", "/"); rec.Code != http.StatusOK {
			t.Fatalf("before = %d", rec.Code)
		}
		// The API's domain event invalidates the table (and the page).
		if _, err := dbClient.ExecContext(ctx, `DELETE FROM status_page_domains WHERE hostname = 'www.acme.example.com'`); err != nil {
			t.Fatal(err)
		}
		h.hosts.Invalidate()
		if rec := get("www.acme.example.com", "/"); rec.Code != http.StatusNotFound {
			t.Fatalf("removed domain = %d", rec.Code)
		}
		if _, err := dbClient.ExecContext(ctx, `UPDATE status_page_domains SET status = 'failed' WHERE hostname = 'status.acme.example.com'`); err != nil {
			t.Fatal(err)
		}
		h.hosts.Invalidate()
		cache.Invalidate("acme")
		if rec := get("status.acme.example.com", "/"); rec.Code != http.StatusNotFound {
			t.Fatalf("failed domain = %d", rec.Code)
		}
		// With no verified domain left the canonical URL is the platform's.
		if rec := get("status.platform.example.com", "/public/status/acme"); !strings.Contains(rec.Body.String(), `href="https://status.platform.example.com/public/status/acme"`) {
			t.Fatalf("canonical did not fall back to the platform URL")
		}

		// Privacy needs no event: the gate reads it on every request.
		if rec := get("status.beta.example.com", "/"); rec.Code != http.StatusOK {
			t.Fatalf("beta before = %d", rec.Code)
		}
		if _, err := dbClient.ExecContext(ctx, `UPDATE status_pages SET visibility = 'private' WHERE id = $1`, beta); err != nil {
			t.Fatal(err)
		}
		private := get("status.beta.example.com", "/")
		unknown := get("nothing.example.com", "/")
		if private.Code != http.StatusNotFound || private.Body.String() != unknown.Body.String() {
			t.Fatalf("private page's domain = %d %q", private.Code, private.Body.String())
		}
		if _, err := dbClient.ExecContext(ctx, `UPDATE status_pages SET visibility = 'public' WHERE id = $1`, beta); err != nil {
			t.Fatal(err)
		}
		// Deleting the page takes its domains with it; even before the
		// table reloads, the gate finds no page.
		if _, err := dbClient.ExecContext(ctx, `DELETE FROM status_pages WHERE id = $1`, beta); err != nil {
			t.Fatal(err)
		}
		if rec := get("status.beta.example.com", "/"); rec.Code != http.StatusNotFound {
			t.Fatalf("deleted page's domain before reload = %d", rec.Code)
		}
		h.hosts.Invalidate()
		if _, known, _ := h.hosts.Lookup(ctx, "status.beta.example.com"); known {
			t.Fatal("deleted page's domain still in the table")
		}
	})
}

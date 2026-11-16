//go:build !unit

package statuspage

import (
	"context"
	"fmt"
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

// TestPageAddons drives summary.json, the badges and the Atom feed through
// the real dispatcher against stored data: settings written in the API's
// stored shape reach the head and the robots header, the feed is bounded and
// leaves unpublished and old incidents out, badges exist only for monitors
// the page renders, and an unknown page serves none of the add-ons.
func TestPageAddons(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	now := time.Now().UTC()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-addons")
	pageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "addons", "Addons Status")
	onPage := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "On page")
	offPage := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "Off page")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, pageID, onPage, 0)

	// The keys and value types the API's statusPageSettingsStored writes.
	if _, err := dbClient.ExecContext(ctx, `UPDATE status_pages SET settings = $2 WHERE id = $1`, pageID,
		`{"show_monitor_uptime":true,"show_global_uptime":true,"seo_title":"Addons <Cloud>","seo_description":"All Addons services.","allow_search_indexing":false}`); err != nil {
		t.Fatal(err)
	}

	insertIncidentFixture(ctx, t, dbClient, incidentFixture{
		tenantID: tenantID, statusPageID: pageID, title: "Open <b>outage</b>", state: "investigating",
		updatedAt: now.Add(-time.Hour), publishedAt: now.Add(-2 * time.Hour), monitorIDs: []uuid.UUID{onPage},
		updates: []incidentUpdateFixture{{message: "Looking & digging", createdAt: now.Add(-30 * time.Minute)}},
	})
	ancient := now.Add(-120 * 24 * time.Hour)
	insertIncidentFixture(ctx, t, dbClient, incidentFixture{
		tenantID: tenantID, statusPageID: pageID, title: "Ancient resolved", state: "resolved",
		updatedAt: ancient, resolvedAt: &ancient, publishedAt: ancient,
	})
	insertIncidentFixture(ctx, t, dbClient, incidentFixture{
		tenantID: tenantID, statusPageID: pageID, title: "Withdrawn", state: "investigating",
		updatedAt: now, publishedAt: now.Add(-time.Hour),
	})
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE incident_status_page_publications SET unpublished_at = NOW()
		WHERE incident_id = (SELECT id FROM incidents WHERE title = 'Withdrawn')`); err != nil {
		t.Fatal(err)
	}
	insertTestMaintenanceWindow(ctx, t, dbClient, tenantID, "DB upgrade", now.Add(48*time.Hour), now.Add(50*time.Hour), onPage)

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	cfg := &config.StatusPageConfig{StatusPageBaseURL: "https://status.example.com/"}
	h := NewHandlers(svc, cfg, logger.New("status-page", "error"), NewHub(), newRenderCache(time.Minute), nil)
	serve := func(target string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		h.HandleStatusPage(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}

	t.Run("settings reach the head", func(t *testing.T) {
		rec := serve("/public/status/addons")
		if rec.Code != http.StatusOK {
			t.Fatalf("page = %d", rec.Code)
		}
		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, nofollow" {
			t.Fatalf("X-Robots-Tag = %q", got)
		}
		body := rec.Body.String()
		for _, want := range []string{
			"<title>Addons &lt;Cloud&gt;</title>",
			`<meta name="description" content="All Addons services." />`,
			`<meta name="robots" content="noindex, nofollow" />`,
			`<link rel="canonical" href="https://status.example.com/public/status/addons" />`,
			`href="https://status.example.com/public/status/addons/feed.atom"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("page head missing %s", want)
			}
		}
	})

	t.Run("feed", func(t *testing.T) {
		rec := serve("/public/status/addons/feed.atom")
		if rec.Code != http.StatusOK {
			t.Fatalf("feed = %d: %s", rec.Code, rec.Body)
		}
		if rec.Header().Get("Content-Type") != "application/atom+xml; charset=utf-8" || rec.Header().Get("Cache-Control") != "public, max-age=300" {
			t.Fatalf("feed headers: %v", rec.Header())
		}
		doc := decodeAtom(t, rec.Body.Bytes())
		if doc.Title != "Addons <Cloud>" {
			t.Fatalf("feed title = %q", doc.Title)
		}
		titles := make([]string, 0, len(doc.Entries))
		for _, e := range doc.Entries {
			titles = append(titles, e.Title)
		}
		joined := strings.Join(titles, " | ")
		if !strings.Contains(joined, "Investigating: Open <b>outage</b>") || !strings.Contains(joined, "Scheduled maintenance: DB upgrade") {
			t.Fatalf("feed entries: %s", joined)
		}
		if strings.Contains(joined, "Withdrawn") || strings.Contains(joined, "Ancient") {
			t.Fatalf("feed carries an unpublished or out-of-window incident: %s", joined)
		}
		again := serve("/public/status/addons/feed.atom")
		if again.Header().Get("ETag") != rec.Header().Get("ETag") {
			t.Fatal("the feed's ETag changed between polls with no change")
		}
	})

	t.Run("feed is bounded", func(t *testing.T) {
		for i := 0; i < maxFeedIncidents+5; i++ {
			at := now.Add(-time.Duration(i+1) * time.Hour)
			insertIncidentFixture(ctx, t, dbClient, incidentFixture{
				tenantID: tenantID, statusPageID: pageID, title: fmt.Sprintf("Bulk %d", i), state: "resolved",
				updatedAt: at, resolvedAt: &at, publishedAt: at,
			})
		}
		doc := decodeAtom(t, serve("/public/status/addons/feed.atom").Body.Bytes())
		incidents := 0
		for _, e := range doc.Entries {
			if !strings.Contains(e.Title, "maintenance") {
				incidents++
			}
		}
		if incidents != maxFeedIncidents {
			t.Fatalf("feed carries %d incidents, want the newest %d", incidents, maxFeedIncidents)
		}
	})

	t.Run("badges only for rendered monitors", func(t *testing.T) {
		if rec := serve("/public/status/addons/monitors/" + onPage.String() + "/badge.svg"); rec.Code != http.StatusOK {
			t.Fatalf("on-page badge = %d", rec.Code)
		}
		if rec := serve("/public/status/addons/monitors/" + onPage.String() + "/uptime.svg?range=24h"); rec.Code != http.StatusOK {
			t.Fatalf("on-page uptime badge = %d", rec.Code)
		}
		if rec := serve("/public/status/addons/monitors/" + offPage.String() + "/badge.svg"); rec.Code != http.StatusNotFound {
			t.Fatalf("off-page badge = %d, want 404", rec.Code)
		}
		summary := serve("/public/status/addons/summary.json")
		if summary.Code != http.StatusOK || strings.Contains(summary.Body.String(), offPage.String()) || strings.Contains(summary.Body.String(), "Withdrawn") {
			t.Fatalf("summary = %d %s", summary.Code, summary.Body)
		}
	})

	t.Run("unknown page serves no add-on", func(t *testing.T) {
		for _, route := range pageAddonRoutes {
			target := "/public/status/" + strings.NewReplacer("{slug}", "no-such-page", "{id}", onPage.String()).Replace(route)
			rec := serve(target)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s = %d, want 404", target, rec.Code)
			}
			if rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("Cache-Control") != "" {
				t.Errorf("%s: a 404 must not be marked shareable or cacheable: %v", target, rec.Header())
			}
		}
	})
}

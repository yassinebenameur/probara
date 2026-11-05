//go:build !unit

package statuspage

import (
	"context"
	"encoding/xml"
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

// The 1h and 24h response-time series come from the page's existing
// short-range queries: successful timed checks only, and a group pools its
// members.
func TestGetStatusPageBySlug_ShortRangeLatencySeries(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-page-latency")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	member := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "member")
	group := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "Group")
	testutil.AddMonitorToGroup(ctx, t, dbClient, member, group)
	pageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "latency", "Latency")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, pageID, monitorA, 0)
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, pageID, group, 1)

	now := time.Now().UTC()
	recent := now.Add(-5 * time.Minute)
	ok, slow, memberLatency := 100, 900, 300
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, recent, "success", "monitor", &ok)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, recent, "failure", "monitor", &slow)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, member, now.Add(-10*time.Minute), "success", "monitor", &memberLatency)
	rolledHour := now.Truncate(time.Hour).Add(-3 * time.Hour)
	testutil.InsertHourlyRollup(ctx, t, dbClient, tenantID, monitorA, rolledHour, 4, 4, 1000, 4, "success", rolledHour.Add(30*time.Minute))

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	page, err := svc.GetStatusPageBySlug(ctx, "latency")
	if err != nil {
		t.Fatalf("GetStatusPageBySlug() error = %v", err)
	}

	a := findMonitorStatus(t, page.Monitors, monitorA)
	if len(a.LatencyHistory1h) != 1 || a.LatencyHistory1h[0].LatencyMS != 100 {
		t.Fatalf("API 1h latency = %+v, want one 100ms point (the failure excluded)", a.LatencyHistory1h)
	}
	byHour := map[time.Time]int{}
	for _, p := range a.LatencyHistory {
		byHour[p.Timestamp] = p.LatencyMS
	}
	if byHour[rolledHour] != 250 || byHour[recent.Truncate(time.Hour)] != 100 {
		t.Fatalf("API 24h latency = %+v, want 250ms from the rollup and 100ms from the raw tail", a.LatencyHistory)
	}

	g := findMonitorStatus(t, page.Monitors, group)
	if len(g.LatencyHistory1h) != 1 || g.LatencyHistory1h[0].LatencyMS != 300 {
		t.Fatalf("group 1h latency = %+v, want its member's 300ms", g.LatencyHistory1h)
	}
	if len(g.LatencyHistory) != 1 || g.LatencyHistory[0].LatencyMS != 300 {
		t.Fatalf("group 24h latency = %+v, want its member's 300ms", g.LatencyHistory)
	}
}

// The feed is bounded (50 incidents, 5 updates each), page-scoped, excludes
// unpublished incidents, includes maintenance, and is served as Atom through
// the page dispatcher. The page itself is bounded the same way, open
// incidents first.
func TestStatusPageFeed_BoundedAndPageScoped(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-page-feed")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	pageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "feed", "Feed")
	otherPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "other", "Other")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, pageID, monitorID, 0)

	base := time.Now().UTC().Add(-30 * 24 * time.Hour).Truncate(time.Second)
	resolvedAt := base
	for i := 0; i < 55; i++ {
		fixture := incidentFixture{
			tenantID:     tenantID,
			statusPageID: pageID,
			title:        fmt.Sprintf("resolved-%02d", i),
			state:        "resolved",
			resolvedAt:   &resolvedAt,
			updatedAt:    base.Add(time.Duration(i) * time.Hour),
			publishedAt:  base,
			monitorIDs:   []uuid.UUID{monitorID},
		}
		if i == 54 {
			for u := 0; u < 7; u++ {
				fixture.updates = append(fixture.updates, incidentUpdateFixture{
					message: fmt.Sprintf("update-%d", u), createdAt: base.Add(time.Duration(60+u) * time.Hour),
				})
			}
		}
		insertIncidentFixture(ctx, t, dbClient, fixture)
	}
	// An old open incident: the page keeps it (open first), the feed orders
	// by recency and drops it past the bound.
	insertIncidentFixture(ctx, t, dbClient, incidentFixture{
		tenantID: tenantID, statusPageID: pageID, title: "old-open", state: "investigating",
		updatedAt: base.Add(-time.Hour), publishedAt: base.Add(-time.Hour),
	})
	insertIncidentFixture(ctx, t, dbClient, incidentFixture{
		tenantID: tenantID, statusPageID: pageID, title: "unpublished", state: "investigating",
		updatedAt: base.Add(100 * time.Hour), publishedAt: base,
	})
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE incident_status_page_publications isp SET unpublished_at = NOW()
		FROM incidents i WHERE i.id = isp.incident_id AND i.title = 'unpublished'
	`); err != nil {
		t.Fatal(err)
	}
	insertIncidentFixture(ctx, t, dbClient, incidentFixture{
		tenantID: tenantID, statusPageID: otherPageID, title: "other-page", state: "investigating",
		updatedAt: base.Add(200 * time.Hour), publishedAt: base,
	})
	windowID := insertTestMaintenanceWindow(ctx, t, dbClient, tenantID, "DB upgrade", time.Now().Add(time.Hour), time.Now().Add(2*time.Hour), monitorID)

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	feed, err := svc.GetStatusPageFeed(ctx, "feed")
	if err != nil {
		t.Fatalf("GetStatusPageFeed() error = %v", err)
	}
	if len(feed.Incidents) != maxFeedIncidents {
		t.Fatalf("feed incidents = %d, want the %d bound", len(feed.Incidents), maxFeedIncidents)
	}
	newest := feed.Incidents[0]
	if newest.Title != "resolved-54" || len(newest.Updates) != maxFeedUpdatesPerIncident || newest.Updates[0].Message != "update-6" {
		t.Fatalf("newest feed entry = %s with %d updates (first %+v)", newest.Title, len(newest.Updates), newest.Updates)
	}
	if !newest.UpdatedAt.Equal(base.Add(66 * time.Hour)) {
		t.Fatalf("newest UpdatedAt = %v, want its latest public update", newest.UpdatedAt)
	}
	for _, inc := range feed.Incidents {
		if inc.Title == "unpublished" || inc.Title == "other-page" || inc.Title == "old-open" {
			t.Fatalf("feed includes %q", inc.Title)
		}
	}
	if len(feed.Maintenance) != 1 || feed.Maintenance[0].ID != windowID.String() || feed.Maintenance[0].UpdatedAt.IsZero() {
		t.Fatalf("feed maintenance = %+v", feed.Maintenance)
	}

	page, err := svc.GetStatusPageBySlug(ctx, "feed")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Incidents) != maxPageIncidents || page.Incidents[0].Title != "old-open" {
		t.Fatalf("page incidents = %d (first %q), want %d with the open one first", len(page.Incidents), page.Incidents[0].Title, maxPageIncidents)
	}

	h := NewHandlers(svc, &config.StatusPageConfig{StatusPageBaseURL: "https://status.example.com"}, logger.New("status-page", "error"), NewHub(), newRenderCache(time.Minute), nil)
	rec := httptest.NewRecorder()
	h.HandleStatusPage(rec, httptest.NewRequest(http.MethodGet, "/public/status/feed/feed.atom", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/atom+xml") {
		t.Fatalf("feed.atom = %d %q: %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	var parsed struct {
		Entries []struct {
			ID string `xml:"id"`
		} `xml:"http://www.w3.org/2005/Atom entry"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Entries) != maxFeedIncidents+1 {
		t.Fatalf("feed entries = %d, want %d incidents + 1 maintenance window", len(parsed.Entries), maxFeedIncidents)
	}
	if !strings.Contains(rec.Body.String(), `href="https://status.example.com/public/status/feed"`) {
		t.Fatal("feed links do not use the canonical page URL")
	}

	missing := httptest.NewRecorder()
	h.HandleStatusPage(missing, httptest.NewRequest(http.MethodGet, "/public/status/nope/feed.atom", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown slug feed = %d, want 404", missing.Code)
	}

	// summary.json goes through the same cached build as the page.
	summary := httptest.NewRecorder()
	h.HandleStatusPage(summary, httptest.NewRequest(http.MethodGet, "/public/status/feed/summary.json", nil))
	if summary.Code != http.StatusOK || !strings.Contains(summary.Body.String(), `"url":"https://status.example.com/public/status/feed"`) {
		t.Fatalf("summary.json = %d: %s", summary.Code, summary.Body.String())
	}
}

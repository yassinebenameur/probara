package statuspage

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/logger"
)

func summaryTestPage() *StatusPageData {
	resolved := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	return &StatusPageData{
		ID:    "page-1",
		Slug:  "acme",
		Title: "Acme",
		Sections: []StatusPageSectionData{
			{ID: "s1", Title: "Core", Monitors: []MonitorStatus{
				{ID: "m-up", Name: "API", MonitorType: "http", Status: "up",
					UptimeHistory24h: []HourlyUptime{{Hour: "10:00", Uptime: 100}, {Hour: "11:00", Uptime: 98.5}},
					UptimeHistory30d: []DailyUptime{{Date: "2026-03-01", Uptime: 99.999}, {Date: "2026-03-02", Uptime: -1}}},
				{ID: "m-down", Name: "Billing <EU>", MonitorType: "http", Status: "down"},
			}},
			{ID: "s2", Title: "Edge", Monitors: []MonitorStatus{
				{ID: "m-maint", Name: "CDN", MonitorType: "http", Status: "maintenance"},
			}},
		},
		Incidents: []StatusPageIncident{
			{ID: "i-open", Title: "Billing errors", State: "identified", PublishedAt: resolved,
				AffectedComponents: []string{"Billing <EU>"},
				Updates:            []StatusPageIncidentUpdate{{Message: "Fix rolling out", CreatedAt: resolved.Add(time.Hour)}}},
			{ID: "i-old", Title: "Old", State: "resolved", PublishedAt: resolved, ResolvedAt: &resolved},
		},
		MaintenanceWindows: []StatusPageMaintenanceWindow{
			{Title: "CDN swap", StartsAt: resolved, EndsAt: resolved.Add(time.Hour), IsActive: true, AffectedMonitors: []string{"CDN"}},
		},
		UptimeHistory1: []HourlyUptime{{Hour: "10:00", Uptime: 97}},
	}
}

func TestBuildPageSummary_Contract(t *testing.T) {
	now := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	s := buildPageSummary(summaryTestPage(), now)

	if s.Version != summaryVersion || s.Page.Slug != "acme" || !s.UpdatedAt.Equal(now) {
		t.Fatalf("header = %+v", s)
	}
	if s.Status.Indicator != "issues" || s.Status.Description != "Issues detected" {
		t.Fatalf("status = %+v, want the page's issues header", s.Status)
	}
	c := s.Status.Counts
	if c.Total != 3 || c.Operational != 1 || c.Down != 1 || c.Maintenance != 1 {
		t.Fatalf("counts = %+v", c)
	}
	if len(s.Components) != 3 || s.Components[1].Status != "down" || s.Components[2].Section != "Edge" || s.Components[2].Status != "maintenance" {
		t.Fatalf("components = %+v", s.Components)
	}
	if len(s.Incidents) != 1 || s.Incidents[0].ID != "i-open" || s.Incidents[0].LatestUpdate == nil {
		t.Fatalf("incidents = %+v, want only the open one with its latest update", s.Incidents)
	}
	if len(s.Maintenance) != 1 || !s.Maintenance[0].InProgress {
		t.Fatalf("maintenance = %+v", s.Maintenance)
	}
	if s.Uptime.H24 == nil || *s.Uptime.H24 != 97 || s.Uptime.D30 != nil {
		t.Fatalf("page uptime = %+v", s.Uptime)
	}

	// The documented keys: a rename here is a breaking change to the contract.
	raw, _ := json.Marshal(s)
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"version", "page", "status", "uptime", "components", "incidents", "maintenance", "updated_at"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("summary.json lacks %q", k)
		}
	}
	if !strings.Contains(string(keys["uptime"]), `"1h":null`) {
		t.Errorf("uptime without data must be null, got %s", keys["uptime"])
	}
}

// TestSummaryUptimeMatchesRenderedValues pins monitorSummaryUptime to the
// page's own per-range figures, so a badge never disagrees with the page.
func TestSummaryUptimeMatchesRenderedValues(t *testing.T) {
	m := summaryTestPage().Sections[0].Monitors[0]
	m.UptimeHistory1h = []MinuteUptime{{Time: "10:00", Uptime: 50}, {Time: "10:01", Uptime: 100}}
	m.UptimeHistory7d = []DailyUptime{{Date: "a", Uptime: 99.123}}
	m.UptimeHistory90d = []DailyUptime{{Date: "a", Uptime: 91}, {Date: "b", Uptime: 92}}
	view := buildStatusPageMonitorView(m)
	u := monitorSummaryUptime(m)
	for _, tc := range []struct {
		got  *float64
		want string
	}{{u.H1, view.Uptime1hValue}, {u.H24, view.Uptime24Value}, {u.D7, view.Uptime7Value}, {u.D30, view.Uptime30Value}, {u.D90, view.Uptime90Value}} {
		value, _ := uptimeBadge(tc.got)
		if tc.got == nil {
			value = "—"
		}
		if value != tc.want {
			t.Errorf("summary %v renders %q, page shows %q", tc.got, value, tc.want)
		}
	}
}

func TestRenderBadge_EscapesAndSizes(t *testing.T) {
	svg := string(renderBadge(`<script>"x"`, "down", badgeRed))
	if strings.Contains(svg, "<script>") {
		t.Fatal("badge label is not escaped")
	}
	if err := xml.Unmarshal([]byte(svg), new(struct{})); err != nil {
		t.Fatalf("badge is not well-formed XML: %v", err)
	}
	long := string(renderBadge(strings.Repeat("a", 200), "up", badgeGreen))
	if strings.Contains(long, strings.Repeat("a", maxBadgeLabelRunes)) {
		t.Fatal("long label not truncated")
	}
}

func TestPageStatusBadge_WorstStateWins(t *testing.T) {
	cases := []struct {
		counts summaryCounts
		want   string
	}{
		{summaryCounts{Total: 3, Operational: 1, Down: 1, Degraded: 1}, "outage"},
		{summaryCounts{Total: 2, Operational: 1, Degraded: 1}, "degraded"},
		{summaryCounts{Total: 2, Operational: 1, Unknown: 1}, "unknown"},
		{summaryCounts{Total: 2, Operational: 1, Maintenance: 1}, "maintenance"},
		{summaryCounts{Total: 1, Operational: 1}, "operational"},
		{summaryCounts{}, "no data"},
	}
	for _, tc := range cases {
		if got, _ := pageStatusBadge(tc.counts); got != tc.want {
			t.Errorf("pageStatusBadge(%+v) = %q, want %q", tc.counts, got, tc.want)
		}
	}
}

// addonTestHandlers returns handlers whose render cache already holds the
// test page, so add-on routes run without a database.
func addonTestHandlers(t *testing.T) *Handlers {
	t.Helper()
	cache := newRenderCache(time.Hour)
	data := summaryTestPage()
	if _, _, err := cache.GetPage("acme", func() (renderedPage, error) {
		return renderedPage{html: "<html></html>", summary: buildPageSummary(data, time.Now())}, nil
	}); err != nil {
		t.Fatal(err)
	}
	h := NewHandlers(nil, nil, logger.New("status-page-test", "error"), nil, cache, nil)
	// The visibility gate denies by default; these pages are public.
	h.access.store = allPublicAccess{}
	return h
}

func serveAddon(h *Handlers, target string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.HandleStatusPage(rec, req)
	return rec
}

func TestPageAddons_SummaryJSON(t *testing.T) {
	h := addonTestHandlers(t)
	rec := serveAddon(h, "/public/status/acme/summary.json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" || rec.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("headers = %v", rec.Header())
	}
	var got pageSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Page.Slug != "acme" {
		t.Fatalf("body = %s (%v)", rec.Body.String(), err)
	}

	again := serveAddon(h, "/public/status/acme/summary.json", http.Header{"If-None-Match": {rec.Header().Get("ETag")}})
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Fatalf("conditional request = %d, want 304", again.Code)
	}
}

// A gate in front of the dispatcher marks a non-public page private, no-store
// before the add-on runs; the add-on must not loosen it.
func TestWriteAddon_KeepsCacheControlSetByAGate(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Cache-Control", "private, no-store")
	writeAddon(rec, httptest.NewRequest(http.MethodGet, "/x", nil), addonResponse{contentType: "text/plain", body: []byte("x"), maxAge: 60})
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want the gate's private, no-store", got)
	}
}

func TestPageAddons_Badges(t *testing.T) {
	h := addonTestHandlers(t)
	cases := []struct {
		target string
		code   int
		want   string
	}{
		{"/public/status/acme/badge.svg", 200, ">outage<"},
		{"/public/status/acme/badge.svg?metric=uptime&range=24h", 200, ">97.00%<"},
		{"/public/status/acme/badge.svg?metric=uptime", 200, ">no data<"},
		{"/public/status/acme/monitors/m-up/badge.svg", 200, ">operational<"},
		{"/public/status/acme/monitors/m-down/badge.svg", 200, "Billing &lt;EU&gt;"},
		{"/public/status/acme/monitors/m-up/badge.svg?metric=uptime&range=24h", 200, ">99.25%<"},
		{"/public/status/acme/monitors/not-on-page/badge.svg", 404, ""},
		{"/public/status/acme/badge.svg?metric=latency", 400, ""},
		{"/public/status/acme/badge.svg?metric=uptime&range=1y", 400, ""},
	}
	for _, tc := range cases {
		rec := serveAddon(h, tc.target, nil)
		if rec.Code != tc.code {
			t.Errorf("%s: status = %d, want %d", tc.target, rec.Code, tc.code)
			continue
		}
		if tc.code != 200 {
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
			t.Errorf("%s: Content-Type = %q", tc.target, ct)
		}
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: body lacks %q: %s", tc.target, tc.want, rec.Body.String())
		}
		if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
			t.Errorf("%s: SVG served without a script-free CSP", tc.target)
		}
	}
}

func TestPageAddons_EmbedScriptAndFeedRedirect(t *testing.T) {
	h := addonTestHandlers(t)
	rec := serveAddon(h, "/public/status/acme/embed.js", nil)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("embed.js = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" || rec.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Fatalf("embed.js must be loadable cross-origin: %v", rec.Header())
	}
	if !strings.Contains(rec.Body.String(), "attachShadow") || !strings.Contains(rec.Body.String(), "/summary.json") {
		t.Fatal("embed.js body is not the widget")
	}

	redirect := serveAddon(h, "/public/status/acme/feed.rss", nil)
	if redirect.Code != http.StatusMovedPermanently || redirect.Header().Get("Location") != "/public/status/acme/feed.atom" {
		t.Fatalf("feed.rss = %d -> %q", redirect.Code, redirect.Header().Get("Location"))
	}
}

// Every add-on suffix must be reached through HandleStatusPage's dispatcher
// (so a gate there covers it) and refuse non-GET methods like the page.
func TestPageAddons_DispatchedThroughHandleStatusPage(t *testing.T) {
	h := addonTestHandlers(t)
	for _, suffix := range pageAddonSuffixes {
		req := httptest.NewRequest(http.MethodPost, "/public/status/acme"+suffix, nil)
		rec := httptest.NewRecorder()
		h.HandleStatusPage(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405", suffix, rec.Code)
		}
		path := "acme" + suffix
		if suffix == "/feed.atom" {
			continue // needs the database; covered by the integration test
		}
		if !h.servePageAddon(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/public/status/"+path, nil), path) {
			t.Errorf("%s is not dispatched", suffix)
		}
	}
}

func TestRenderAtomFeed_WellFormed(t *testing.T) {
	published := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	resolved := published.Add(2 * time.Hour)
	feed := &statusPageFeed{
		PageID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Slug:   "acme",
		Title:  "Acme & Co",
		Incidents: []feedIncident{{
			StatusPageIncident: StatusPageIncident{
				ID: "22222222-2222-2222-2222-222222222222", Title: "API <down>", State: "resolved",
				PublishedAt: published, ResolvedAt: &resolved,
				Updates: []StatusPageIncidentUpdate{{Message: "Fixed", CreatedAt: resolved}},
			},
			UpdatedAt: resolved,
		}},
		Maintenance: []StatusPageMaintenanceWindow{{
			ID: "33333333-3333-3333-3333-333333333333", Title: "DB upgrade",
			StartsAt: published.Add(48 * time.Hour), EndsAt: published.Add(49 * time.Hour),
			UpdatedAt: published.Add(time.Hour),
		}},
	}
	body, err := renderAtomFeed(feed, "https://status.example.com/public/status/acme", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		XMLName xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
		Updated string   `xml:"updated"`
		Entries []struct {
			ID    string `xml:"id"`
			Title string `xml:"title"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("feed is not valid Atom XML: %v\n%s", err, body)
	}
	if len(parsed.Entries) != 2 || parsed.Entries[0].Title != "Resolved: API <down>" || parsed.Entries[1].ID != "urn:uuid:33333333-3333-3333-3333-333333333333" {
		t.Fatalf("entries = %+v", parsed.Entries)
	}
	if parsed.Updated != "2026-03-01T14:00:00Z" {
		t.Fatalf("feed updated = %q, want the newest entry", parsed.Updated)
	}
	if !strings.Contains(string(body), `href="https://status.example.com/public/status/acme/feed.atom"`) {
		t.Fatal("feed lacks its self link")
	}
}

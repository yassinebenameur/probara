package statuspage

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
)

var updateGolden = flag.Bool("update", false, "rewrite status-page/internal/statuspage/testdata golden files")

const (
	addonTestPageID  = "5f0c6a3e-9a4f-4d55-9c39-2c3bbd1f0a01"
	addonTestAPI     = "0b6f0a7e-1111-4c2d-8e9f-000000000001"
	addonTestDB      = "0b6f0a7e-1111-4c2d-8e9f-000000000002"
	addonTestOffPage = "0b6f0a7e-1111-4c2d-8e9f-000000000099"
)

// addonTestData is a page with two sections, one degraded monitor, an open
// and a resolved incident and a repeating maintenance window.
func addonTestData() *StatusPageData {
	published := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	resolved := published.Add(2 * time.Hour)
	api := MonitorStatus{
		ID: addonTestAPI, Name: "Public API", MonitorType: "http", Status: "up",
		UptimeHistory1h:  []MinuteUptime{{Time: "10:00", Uptime: 100}},
		UptimeHistory24h: []HourlyUptime{{Hour: "09:00", Uptime: 100}, {Hour: "10:00", Uptime: 99}},
		UptimeHistory7d:  []DailyUptime{{Date: "2026-11-09", Uptime: 99.5}, {Date: "2026-11-10", Uptime: 100}},
		UptimeHistory30d: []DailyUptime{{Date: "2026-11-09", Uptime: 98.25}, {Date: "2026-11-10", Uptime: 100}},
		UptimeHistory90d: []DailyUptime{{Date: "2026-11-09", Uptime: 97}},
	}
	db := MonitorStatus{
		ID: addonTestDB, Name: "Database", MonitorType: "postgres", Status: "degraded",
		UptimeHistory24h: []HourlyUptime{{Hour: "10:00", Uptime: 90}},
	}
	return &StatusPageData{
		ID:                  addonTestPageID,
		Slug:                "acme",
		Title:               "Acme",
		ShowGlobalUptime:    true,
		ShowMonitorUptime:   true,
		AllowSearchIndexing: true,
		CanonicalURL:        "https://status.example.com/public/status/acme",
		Sections: []StatusPageSectionData{
			{ID: "s1", Title: "Edge", Monitors: []MonitorStatus{api}},
			{ID: "s2", Title: "Storage", Monitors: []MonitorStatus{db}},
		},
		Monitors: []MonitorStatus{api, db},
		Incidents: []StatusPageIncident{
			{
				ID: "11111111-0000-0000-0000-000000000001", Title: "Slow queries", Summary: "Reads are slow.",
				State: "identified", PublishedAt: published, AffectedComponents: []string{"Database"},
				Updates: []StatusPageIncidentUpdate{{Message: "Index rebuilding", CreatedAt: published.Add(30 * time.Minute)}},
			},
			{
				ID: "11111111-0000-0000-0000-000000000002", Title: "Old outage", State: "resolved",
				PublishedAt: published.Add(-48 * time.Hour), ResolvedAt: &resolved,
			},
		},
		MaintenanceWindows: []StatusPageMaintenanceWindow{{
			ID:               uuid.MustParse("22222222-0000-0000-0000-000000000001"),
			Title:            "Kernel patching",
			Description:      "Rolling reboot",
			StartsAt:         time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC),
			EndsAt:           time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC),
			Repeating:        true,
			NextStartsAt:     []time.Time{time.Date(2026, 11, 25, 2, 0, 0, 0, time.UTC)},
			AffectedMonitors: []string{"Public API"},
			UpdatedAt:        published,
		}},
		UptimeHistory1h: []MinuteUptime{{Time: "10:00", Uptime: 100}},
		UptimeHistory1:  []HourlyUptime{{Hour: "10:00", Uptime: 95}},
		UptimeHistory7:  []DailyUptime{{Date: "2026-11-10", Uptime: 99.9}},
		UptimeHistory30: []DailyUptime{{Date: "2026-11-10", Uptime: 99.8}},
		UptimeHistory90: []DailyUptime{{Date: "2026-11-10", Uptime: 99.7}},
	}
}

var addonTestNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func renderHead(t *testing.T, data *StatusPageData) string {
	t.Helper()
	html, _, err := renderStatusPageHTML(data, false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	end := strings.Index(html, "</head>")
	if end < 0 {
		t.Fatal("no </head>")
	}
	return html[:end]
}

func TestHeadTags_Defaults(t *testing.T) {
	data := addonTestData()
	logo := "https://cdn.example.com/logo.png"
	data.LogoURL = &logo
	head := renderHead(t, data)

	for _, want := range []string{
		`<title>Acme · Status</title>`,
		`<meta name="description" content="Live status, incident history and scheduled maintenance for Acme." />`,
		`<meta name="robots" content="index, follow" />`,
		`<link rel="canonical" href="https://status.example.com/public/status/acme" />`,
		`<meta property="og:url" content="https://status.example.com/public/status/acme" />`,
		`<meta property="og:site_name" content="Acme" />`,
		`<meta property="og:title" content="Acme · Status" />`,
		`<meta property="og:image" content="https://cdn.example.com/logo.png" />`,
		`<meta name="twitter:card" content="summary" />`,
		`<link rel="alternate" type="application/atom+xml" title="Acme · Status" href="https://status.example.com/public/status/acme/feed.atom" />`,
		`href="https://status.example.com/public/status/acme/summary.json"`,
	} {
		if !strings.Contains(head, want) {
			t.Errorf("head is missing %s", want)
		}
	}
}

// The derived description is a fixed sentence: a crawler keeps it for days,
// so it must not freeze the page's current state into a search result.
func TestHeadTags_DefaultDescriptionCarriesNoLiveState(t *testing.T) {
	data := addonTestData()
	data.Sections[0].Monitors[0].Status = "down"
	data.Monitors[0].Status = "down"
	seo := resolvePageSEO(data)
	for _, word := range []string{"operational", "down", "issue", "degraded", "outage"} {
		if strings.Contains(strings.ToLower(seo.Description), word) {
			t.Fatalf("default description %q carries live state (%q)", seo.Description, word)
		}
	}
	description := "Status of every Acme service."
	data.Description = &description
	if got := resolvePageSEO(data).Description; got != description {
		t.Fatalf("page description should be the fallback, got %q", got)
	}
}

func TestHeadTags_OverridesAndNoIndex(t *testing.T) {
	data := addonTestData()
	data.SEOTitle = "Acme Cloud status"
	data.SEODescription = "Uptime of Acme Cloud."
	data.AllowSearchIndexing = false
	data.CanonicalURL = ""
	relative := "/logo.png"
	data.LogoURL = &relative
	head := renderHead(t, data)

	for _, want := range []string{
		`<title>Acme Cloud status</title>`,
		`<meta name="description" content="Uptime of Acme Cloud." />`,
		`<meta name="robots" content="noindex, nofollow" />`,
		`href="/public/status/acme/feed.atom"`,
	} {
		if !strings.Contains(head, want) {
			t.Errorf("head is missing %s", want)
		}
	}
	for _, unwanted := range []string{`rel="canonical"`, `og:url`, `og:image`} {
		if strings.Contains(head, unwanted) {
			t.Errorf("head carries %s without a public origin or absolute logo", unwanted)
		}
	}
}

func TestHeadTags_EscapeHostileText(t *testing.T) {
	data := addonTestData()
	data.Title = `Acme"><script>alert(1)</script>`
	data.SEODescription = `desc" onload="x()<b>`
	hostileLogo := `javascript:alert(1)`
	data.LogoURL = &hostileLogo
	head := renderHead(t, data)

	if strings.Contains(head, "<script>alert(1)</script>") || strings.Contains(head, `onload="x()`) {
		t.Fatalf("hostile page text reached the head unescaped:\n%s", head)
	}
	if strings.Contains(head, "og:image") {
		t.Fatal("a non-http logo must not become og:image")
	}
	if !strings.Contains(head, `content="Acme&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;"`) {
		t.Fatalf("og:site_name not escaped as expected:\n%s", head)
	}
}

func TestCustomTemplateReceivesSEOFields(t *testing.T) {
	data := addonTestData()
	data.AllowSearchIndexing = false
	html, err := renderStatusPageWithSource(data, false,
		`<head><title>{{.SEOTitle}}</title><meta name="robots" content="{{.Robots}}">`+
			`<link href="{{.FeedURL}}"><link href="{{.SummaryURL}}"><link href="{{.CanonicalURL}}">`+
			`<meta content="{{.MetaDescription}}"><meta content="{{.SiteName}}"><meta content="{{.OGImageURL}}"></head>`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<title>Acme · Status</title>", `content="noindex, nofollow"`, "/acme/feed.atom", "/acme/summary.json"} {
		if !strings.Contains(html, want) {
			t.Errorf("custom template output missing %s: %s", want, html)
		}
	}
}

func TestRenderedPageETagCoversNoIndex(t *testing.T) {
	indexed := renderedPage{html: "<p>same</p>"}
	hidden := renderedPage{html: "<p>same</p>", noindex: true}
	if indexed.etag() == hidden.etag() {
		t.Fatal("an indexing change with identical markup must change the ETag")
	}
}

func TestSummaryGolden(t *testing.T) {
	got, err := json.MarshalIndent(buildPageSummary(addonTestData(), addonTestNow), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "summary_v1.golden.json")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("summary.json drifted from its documented v1 contract; add fields only and rerun with -update.\ngot:\n%s", got)
	}
}

// The summary's uptime and the page's rendered figures come from the same
// averaging; a drift would publish a number the page does not show.
func TestSummaryUptimeMatchesRenderedValues(t *testing.T) {
	data := addonTestData()
	summary := buildPageSummary(data, addonTestNow)
	view := buildStatusPageRenderView(data, false)
	for _, m := range view.Monitors {
		c, ok := summary.component(m.ID)
		if !ok {
			t.Fatalf("component %s missing", m.ID)
		}
		for rangeName, rendered := range map[string]string{
			"1h": m.Uptime1hValue, "24h": m.Uptime24Value, "7d": m.Uptime7Value, "30d": m.Uptime30Value, "90d": m.Uptime90Value,
		} {
			got := c.Uptime.forRange(rangeName)
			want := "—"
			if got != nil {
				want = uptimeValue(*got)
			}
			if want != rendered {
				t.Errorf("%s %s: summary %s, page %s", m.Name, rangeName, want, rendered)
			}
		}
	}
	if got := summary.Uptime.forRange("24h"); got == nil || uptimeValue(*got) != view.GlobalUptime24Value {
		t.Errorf("page 24h uptime %v, rendered %s", got, view.GlobalUptime24Value)
	}
}

func TestSummaryKeepsHiddenUptimeOut(t *testing.T) {
	data := addonTestData()
	data.ShowGlobalUptime = false
	data.ShowMonitorUptime = false
	body, err := json.Marshal(buildPageSummary(data, addonTestNow))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Uptime   json.RawMessage `json:"uptime"`
		Sections []struct {
			Components []struct {
				Uptime json.RawMessage `json:"uptime"`
			} `json:"components"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if string(doc.Uptime) != "null" {
		t.Fatalf("hidden page uptime published: %s", doc.Uptime)
	}
	for _, s := range doc.Sections {
		for _, c := range s.Components {
			if string(c.Uptime) != "null" {
				t.Fatalf("hidden component uptime published: %s", c.Uptime)
			}
		}
	}
	if strings.Contains(string(body), "99.") || strings.Contains(string(body), "98.") {
		t.Fatalf("an uptime figure leaked: %s", body)
	}
}

func parseSVG(t *testing.T, svg []byte) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(svg))
	dec.Strict = true
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("badge is not well-formed XML: %v\n%s", err, svg)
		}
	}
}

func TestBadgeEscapesTenantText(t *testing.T) {
	svg := renderBadge("API <script>alert(1)</script> & \"co\" \x00\x1b", "down", badgeRed, badgeStyleFlat)
	parseSVG(t, svg)
	s := string(svg)
	if strings.Contains(s, "<script") {
		t.Fatalf("label markup reached the SVG: %s", s)
	}
	if !strings.Contains(s, "&lt;script&gt;") || !strings.Contains(s, "&amp;") {
		t.Fatalf("label not escaped: %s", s)
	}
	if strings.ContainsAny(s, "\x00\x1b") {
		t.Fatal("characters XML cannot carry were kept")
	}
}

func TestBadgeGeometryAndStyles(t *testing.T) {
	short := string(renderBadge("API", "operational", badgeGreen, badgeStyleFlat))
	long := string(renderBadge(strings.Repeat("W", 80), "operational", badgeGreen, badgeStyleSquare))
	width := regexp.MustCompile(`^<svg [^>]*width="(\d+)"`)
	ws, wl := width.FindStringSubmatch(short), width.FindStringSubmatch(long)
	if ws == nil || wl == nil || len(ws[1]) > len(wl[1]) || ws[1] == wl[1] {
		t.Fatalf("width does not follow the text: %v vs %v", ws, wl)
	}
	if !strings.Contains(short, `rx="3"`) || !strings.Contains(long, `rx="0"`) {
		t.Fatal("style does not set the corner radius")
	}
	if strings.Contains(long, "linearGradient") {
		t.Fatal("square style carries the gradient")
	}
	if !strings.Contains(short, `textLength="`) {
		t.Fatal("text runs must carry textLength so the layout survives font substitution")
	}
	title := regexp.MustCompile(`<title>(.*?): `).FindStringSubmatch(long)
	if title == nil || utf8.RuneCountInString(title[1]) > maxBadgeLabelRunes {
		t.Fatalf("label not truncated to %d runes: %v", maxBadgeLabelRunes, title)
	}
	for _, forbidden := range []string{"<script", "<style", "href", "<foreignObject", "@import"} {
		if strings.Contains(short, forbidden) {
			t.Fatalf("badge contains %s", forbidden)
		}
	}
}

func TestPageStatusBadgeNamesWorstState(t *testing.T) {
	cases := []struct {
		status summaryStatus
		want   string
	}{
		{summaryStatus{Indicator: "operational"}, "operational"},
		{summaryStatus{Indicator: "maintenance"}, "maintenance"},
		{summaryStatus{Indicator: "issues", Counts: summaryCounts{Down: 1, Degraded: 1}}, "outage"},
		{summaryStatus{Indicator: "issues", Counts: summaryCounts{Degraded: 1}}, "degraded"},
		{summaryStatus{Indicator: "issues", Counts: summaryCounts{Unknown: 1}}, "unknown"},
	}
	for _, tc := range cases {
		if got, _ := pageStatusBadge(tc.status); got != tc.want {
			t.Errorf("%+v: got %s, want %s", tc.status, got, tc.want)
		}
	}
}

func TestParsePageAddon(t *testing.T) {
	id := uuid.MustParse(addonTestAPI)
	cases := map[string]pageAddon{
		"acme/summary.json": {slug: "acme", route: routeSummary},
		"acme/feed.atom":    {slug: "acme", route: routeFeed},
		"acme/badge.svg":    {slug: "acme", route: routeBadge},
		"acme/monitors/" + strings.ToUpper(id.String()) + "/badge.svg": {slug: "acme", route: routeBadge, monitorID: id.String()},
		"acme/monitors/" + id.String() + "/uptime.svg":                 {slug: "acme", route: routeUptimeBadge, monitorID: id.String()},
	}
	for path, want := range cases {
		got, ok := parsePageAddon(path)
		if !ok || got != want {
			t.Errorf("%s: got %+v %v, want %+v", path, got, ok, want)
		}
	}
	for _, path := range []string{
		"acme", "acme/data", "/summary.json", "acme/uptime.svg", "acme/monitors/not-a-uuid/badge.svg",
		"acme/monitors/" + id.String() + "/summary.json", "a/b/summary.json", "acme/monitors//badge.svg",
	} {
		if got, ok := parsePageAddon(path); ok {
			t.Errorf("%s parsed as an add-on: %+v", path, got)
		}
	}
	if len(pageAddonRoutes) != 5 {
		t.Fatalf("route table lists %d routes; keep it in step with parsePageAddon", len(pageAddonRoutes))
	}
}

// primedHandlers serves data for its slug from the render cache, so handler
// behaviour is tested without a database. The service is never reached.
func primedHandlers(t *testing.T, data *StatusPageData) *Handlers {
	t.Helper()
	cache := newRenderCache(time.Hour)
	_, _, err := cache.GetPage(data.Slug, func() (renderedPage, error) {
		view := buildStatusPageRenderView(data, false)
		html, _, err := renderStatusPageView(data, view)
		if err != nil {
			return renderedPage{}, err
		}
		summary := buildPageSummaryFromView(data, view, addonTestNow)
		summaryJSON, summaryETag, err := encodeSummary(summary)
		return renderedPage{html: html, noindex: !pageIndexable(data), summary: summary, summaryJSON: summaryJSON, summaryETag: summaryETag}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewHandlers(NewService(nil, nil), &config.StatusPageConfig{}, logger.New("status-page", "error"), NewHub(), cache, nil)
}

func serveAddonTest(h *Handlers, method, target string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.HandleStatusPage(rec, req)
	return rec
}

func assertAddonHeaders(t *testing.T, rec *httptest.ResponseRecorder, contentType string) {
	t.Helper()
	h := rec.Header()
	checks := map[string]string{
		"Content-Type":                contentType,
		"X-Content-Type-Options":      "nosniff",
		"Access-Control-Allow-Origin": "*",
		"Cache-Control":               "public, max-age=60",
		"X-Robots-Tag":                "noindex",
	}
	for k, want := range checks {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if h.Get("Access-Control-Allow-Credentials") != "" {
		t.Error("public data must not allow credentials")
	}
	if !strings.HasPrefix(h.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("CSP = %q", h.Get("Content-Security-Policy"))
	}
	if h.Get("ETag") == "" {
		t.Error("no ETag")
	}
}

func TestBadgeHandler_RenderedMonitorsOnly(t *testing.T) {
	h := primedHandlers(t, addonTestData())

	rec := serveAddonTest(h, http.MethodGet, "/public/status/acme/monitors/"+addonTestAPI+"/badge.svg", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	assertAddonHeaders(t, rec, "image/svg+xml; charset=utf-8")
	parseSVG(t, rec.Body.Bytes())
	if !strings.Contains(rec.Body.String(), "Public API: operational") {
		t.Fatalf("badge text: %s", rec.Body)
	}

	notModified := serveAddonTest(h, http.MethodGet, "/public/status/acme/monitors/"+addonTestAPI+"/badge.svg",
		map[string]string{"If-None-Match": rec.Header().Get("ETag")})
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("revalidation = %d", notModified.Code)
	}

	// A real monitor ID that the page does not render has no badge.
	if rec := serveAddonTest(h, http.MethodGet, "/public/status/acme/monitors/"+addonTestOffPage+"/badge.svg", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("off-page monitor badge = %d, want 404", rec.Code)
	}
	if rec := serveAddonTest(h, http.MethodGet, "/public/status/acme/monitors/"+addonTestOffPage+"/uptime.svg", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("off-page monitor uptime badge = %d, want 404", rec.Code)
	}

	page := serveAddonTest(h, http.MethodGet, "/public/status/acme/badge.svg?style=square", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "status: degraded") {
		t.Fatalf("page badge = %d %s", page.Code, page.Body)
	}

	uptime := serveAddonTest(h, http.MethodGet, "/public/status/acme/monitors/"+addonTestAPI+"/uptime.svg?range=7d", nil)
	if uptime.Code != http.StatusOK || !strings.Contains(uptime.Body.String(), "Public API 7d: 99.75%") {
		t.Fatalf("uptime badge = %d %s", uptime.Code, uptime.Body)
	}

	for _, bad := range []string{"/public/status/acme/badge.svg?style=plastic", "/public/status/acme/monitors/" + addonTestAPI + "/uptime.svg?range=1y"} {
		if rec := serveAddonTest(h, http.MethodGet, bad, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, rec.Code)
		}
	}
	if rec := serveAddonTest(h, http.MethodPost, "/public/status/acme/badge.svg", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST badge = %d, want 405", rec.Code)
	}
}

func TestBadgeHandler_HiddenUptime(t *testing.T) {
	data := addonTestData()
	data.ShowMonitorUptime = false
	h := primedHandlers(t, data)
	if rec := serveAddonTest(h, http.MethodGet, "/public/status/acme/monitors/"+addonTestAPI+"/uptime.svg", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("uptime badge on a page hiding uptime = %d, want 404", rec.Code)
	}
	// The status badge stays: status is shown either way.
	if rec := serveAddonTest(h, http.MethodGet, "/public/status/acme/monitors/"+addonTestAPI+"/badge.svg", nil); rec.Code != http.StatusOK {
		t.Fatalf("status badge = %d", rec.Code)
	}
}

func TestSummaryHandler(t *testing.T) {
	h := primedHandlers(t, addonTestData())
	rec := serveAddonTest(h, http.MethodGet, "/public/status/acme/summary.json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	assertAddonHeaders(t, rec, "application/json; charset=utf-8")
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["schema_version"] != float64(1) {
		t.Fatalf("schema_version = %v", doc["schema_version"])
	}
}

func TestPageHandler_NoIndexHeader(t *testing.T) {
	data := addonTestData()
	data.AllowSearchIndexing = false
	h := primedHandlers(t, data)
	rec := serveAddonTest(h, http.MethodGet, "/public/status/acme", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
		t.Fatalf("status %d, X-Robots-Tag %q", rec.Code, rec.Header().Get("X-Robots-Tag"))
	}
	revalidated := serveAddonTest(h, http.MethodGet, "/public/status/acme", map[string]string{"If-None-Match": rec.Header().Get("ETag")})
	if revalidated.Code != http.StatusNotModified || revalidated.Header().Get("X-Robots-Tag") == "" {
		t.Fatalf("304 dropped the robots header: %d %q", revalidated.Code, revalidated.Header().Get("X-Robots-Tag"))
	}

	indexed := primedHandlers(t, addonTestData())
	if got := serveAddonTest(indexed, http.MethodGet, "/public/status/acme", nil).Header().Get("X-Robots-Tag"); got != "" {
		t.Fatalf("indexable page sent X-Robots-Tag %q", got)
	}
}

// An access check that runs before dispatch marks a non-public response
// private; the add-on writer must keep that rather than invite shared caches.
func TestWriteAddonKeepsPresetCacheControl(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Cache-Control", "private, no-store")
	writeAddon(rec, httptest.NewRequest(http.MethodGet, "/", nil), addonResponse{contentType: "application/json", body: []byte("{}"), maxAge: 60, csp: addonDocumentCSP})
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

type parsedAtom struct {
	XMLName xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
	ID      string   `xml:"id"`
	Title   string   `xml:"title"`
	Updated string   `xml:"updated"`
	Author  struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Links []struct {
		Rel  string `xml:"rel,attr"`
		Href string `xml:"href,attr"`
	} `xml:"link"`
	Entries []struct {
		ID      string `xml:"id"`
		Title   string `xml:"title"`
		Updated string `xml:"updated"`
		Content struct {
			Type string `xml:"type,attr"`
			Body string `xml:",chardata"`
		} `xml:"content"`
		Links []struct {
			Href string `xml:"href,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

func feedFixture() *statusPageFeed {
	data := addonTestData()
	feed := &statusPageFeed{
		PageID:      uuid.MustParse(addonTestPageID),
		Slug:        "acme",
		SEO:         resolvePageSEO(data),
		PageUpdated: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Maintenance: data.MaintenanceWindows,
	}
	for _, incident := range data.Incidents {
		feed.Incidents = append(feed.Incidents, feedIncident{StatusPageIncident: incident, UpdatedAt: incident.PublishedAt.Add(time.Hour)})
	}
	return feed
}

func decodeAtom(t *testing.T, body []byte) parsedAtom {
	t.Helper()
	var doc parsedAtom
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = true
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("feed is not well-formed Atom: %v\n%s", err, body)
	}
	return doc
}

func TestAtomFeed_ValidAndEscaped(t *testing.T) {
	feed := feedFixture()
	hostile := "Outage <script>alert(1)</script> ]]> & \"quotes\" \x00\x0b"
	feed.Incidents[0].Title = hostile
	feed.Incidents[0].Summary = "<img src=x onerror=alert(1)>"
	feed.Incidents[0].Updates = []StatusPageIncidentUpdate{{Message: "</content><entry>", CreatedAt: feed.Incidents[0].PublishedAt}}

	body, err := renderAtomFeed(feed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(body, []byte(xml.Header)) {
		t.Fatal("missing XML declaration")
	}
	if bytes.Contains(body, []byte("<script>")) || bytes.Contains(body, []byte("<img")) || bytes.Contains(body, []byte("</content><entry>")) {
		t.Fatalf("incident text reached the feed as markup:\n%s", body)
	}
	doc := decodeAtom(t, body)

	if doc.ID != "urn:uuid:"+addonTestPageID || doc.Title != "Acme · Status" || doc.Author.Name != "Acme" {
		t.Fatalf("feed head: %+v", doc)
	}
	if _, err := time.Parse(time.RFC3339, doc.Updated); err != nil {
		t.Fatalf("feed updated %q: %v", doc.Updated, err)
	}
	links := map[string]string{}
	for _, l := range doc.Links {
		links[l.Rel] = l.Href
	}
	if links["self"] != "https://status.example.com/public/status/acme/feed.atom" || links["alternate"] != "https://status.example.com/public/status/acme" {
		t.Fatalf("feed links: %v", links)
	}
	if len(doc.Entries) != 3 {
		t.Fatalf("entries = %d, want 2 incidents and 1 maintenance", len(doc.Entries))
	}
	var sawHostile bool
	for _, e := range doc.Entries {
		if e.ID == "" || e.Title == "" || e.Content.Type != "text" || len(e.Links) == 0 {
			t.Fatalf("entry lacks a required element: %+v", e)
		}
		if _, err := time.Parse(time.RFC3339, e.Updated); err != nil {
			t.Fatalf("entry updated %q: %v", e.Updated, err)
		}
		if strings.Contains(e.Title, "<script>alert(1)</script> ]]> & \"quotes\"") {
			sawHostile = true
			if strings.ContainsAny(e.Title, "\x00\x0b") {
				t.Fatal("characters XML cannot carry were kept")
			}
			if !strings.Contains(e.Content.Body, "</content><entry>") {
				t.Fatal("update text did not round-trip as text")
			}
		}
	}
	if !sawHostile {
		t.Fatal("hostile title did not round-trip as text")
	}
}

// Entry ids must survive polls and edits: a reader that sees a new id shows
// the entry again as unread.
func TestAtomFeed_StableEntryIDs(t *testing.T) {
	ids := func(feed *statusPageFeed) []string {
		body, err := renderAtomFeed(feed)
		if err != nil {
			t.Fatal(err)
		}
		doc := decodeAtom(t, body)
		out := make([]string, 0, len(doc.Entries))
		for _, e := range doc.Entries {
			out = append(out, e.ID)
		}
		return out
	}
	first := ids(feedFixture())
	edited := feedFixture()
	edited.Incidents[0].Title = "Renamed"
	edited.Incidents[0].State = "resolved"
	edited.Maintenance[0].Title = "Renamed window"
	edited.Maintenance[0].IsActive = true
	second := ids(edited)
	if !sameSet(first, second) {
		t.Fatalf("entry ids changed with an edit: %v -> %v", first, second)
	}

	next := feedFixture()
	next.Maintenance[0].StartsAt = next.Maintenance[0].StartsAt.Add(7 * 24 * time.Hour)
	if sameSet(first, ids(next)) {
		t.Fatal("the next occurrence of a repeating window must be a new entry")
	}
	want := maintenanceEntryID(uuid.MustParse(addonTestPageID), uuid.MustParse("22222222-0000-0000-0000-000000000001"), time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC))
	if !strings.HasPrefix(want, "urn:uuid:") || !contains(first, want) {
		t.Fatalf("maintenance id %s not in %v", want, first)
	}
}

func TestAtomFeed_EmptyFeedIsStable(t *testing.T) {
	feed := &statusPageFeed{PageID: uuid.MustParse(addonTestPageID), Slug: "acme", SEO: resolvePageSEO(addonTestData()), PageUpdated: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	a, err := renderAtomFeed(feed)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := renderAtomFeed(feed)
	if !bytes.Equal(a, b) {
		t.Fatal("an empty feed changed between renders")
	}
	if doc := decodeAtom(t, a); doc.Updated != "2026-01-01T00:00:00Z" {
		t.Fatalf("empty feed updated = %s, want the page's last change", doc.Updated)
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, v := range a {
		if !contains(b, v) {
			return false
		}
	}
	return true
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// The same incident and occurrence shown on two pages are different
// entries: their content (public names, links) is page-specific.
func TestAtomFeed_EntryIDsArePerPage(t *testing.T) {
	ids := func(feed *statusPageFeed) []string {
		body, err := renderAtomFeed(feed)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range decodeAtom(t, body).Entries {
			out = append(out, e.ID)
		}
		return out
	}
	first := feedFixture()
	other := feedFixture()
	other.PageID = uuid.MustParse("5f0c6a3e-9a4f-4d55-9c39-2c3bbd1f0a02")
	a, b := ids(first), ids(other)
	for _, id := range a {
		if contains(b, id) {
			t.Fatalf("entry id %s is shared by two pages", id)
		}
	}
	if !sameSet(a, ids(feedFixture())) {
		t.Fatal("entry ids are not stable for one page")
	}
}

// The summary is rebuilt every render-cache TTL; with unchanged data its
// validator must not move, or a polling client never gets a 304.
func TestSummaryETagIgnoresGeneratedAt(t *testing.T) {
	data := addonTestData()
	body1, etag1, err := encodeSummary(buildPageSummary(data, addonTestNow))
	if err != nil {
		t.Fatal(err)
	}
	body2, etag2, err := encodeSummary(buildPageSummary(data, addonTestNow.Add(10*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(body1, body2) {
		t.Fatal("fixture should differ in generated_at")
	}
	if etag1 != etag2 {
		t.Fatal("two builds of unchanged data have different ETags")
	}
	data.Sections[0].Monitors[0].Status = "down"
	if _, etag3, _ := encodeSummary(buildPageSummary(data, addonTestNow)); etag3 == etag1 {
		t.Fatal("a status change must change the ETag")
	}
}

func TestUptimeBadgeLabelKeepsRange(t *testing.T) {
	name := strings.Repeat("Customer-facing API gateway ", 3)
	l7, l90 := uptimeBadgeLabel(name, "7d"), uptimeBadgeLabel(name, "90d")
	if !strings.HasSuffix(l7, " 7d") || !strings.HasSuffix(l90, " 90d") || l7 == l90 {
		t.Fatalf("labels lost the range: %q %q", l7, l90)
	}
	for _, l := range []string{l7, l90} {
		if utf8.RuneCountInString(l) > maxBadgeLabelRunes {
			t.Fatalf("label %q exceeds %d runes", l, maxBadgeLabelRunes)
		}
		svg := string(renderBadge(l, "99.00%", badgeGreen, badgeStyleFlat))
		if !strings.Contains(svg, "<title>"+escapeBadgeText(l)+": ") {
			t.Fatalf("renderBadge cut the label again: %s", svg)
		}
	}
	if got := uptimeBadgeLabel("API", "30d"); got != "API 30d" {
		t.Fatalf("short label = %q", got)
	}
}

// One call marks a gated page's add-on: private, not shareable, not
// readable cross-origin. Without it, values set before dispatch are kept.
func TestWriteAddon_GatedAndPresetHeaders(t *testing.T) {
	resp := addonResponse{contentType: "image/svg+xml", body: []byte("<svg/>"), maxAge: 60, csp: addonImageCSP}

	rec := httptest.NewRecorder()
	rec.Header().Set("Access-Control-Allow-Origin", "*")
	writeAddon(rec, gatedPageRequest(httptest.NewRequest(http.MethodGet, "/", nil)), resp)
	h := rec.Header()
	if h.Get("Cache-Control") != "private, no-store" || h.Get("Access-Control-Allow-Origin") != "" || h.Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Fatalf("gated headers: %v", h)
	}

	rec = httptest.NewRecorder()
	rec.Header().Set("Access-Control-Allow-Origin", "https://ops.example.com")
	rec.Header().Set("Cross-Origin-Resource-Policy", "same-site")
	writeAddon(rec, httptest.NewRequest(http.MethodGet, "/", nil), resp)
	h = rec.Header()
	if h.Get("Access-Control-Allow-Origin") != "https://ops.example.com" || h.Get("Cross-Origin-Resource-Policy") != "same-site" || h.Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("preset headers not kept: %v", h)
	}
}

// The feed is served from the render cache: a query string cannot force a
// rebuild, and invalidating the page drops its feed too.
func TestFeedIsCachedWithThePage(t *testing.T) {
	cache := newRenderCache(time.Hour)
	builds := 0
	build := func() (renderedPage, error) {
		builds++
		return renderedPage{html: `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"/>`}, nil
	}
	if _, _, err := cache.GetPage(feedCacheKey("acme"), build); err != nil {
		t.Fatal(err)
	}
	// The service has no database: a rebuild would panic.
	h := NewHandlers(NewService(nil, nil), &config.StatusPageConfig{}, logger.New("status-page", "error"), NewHub(), cache, nil)
	for _, target := range []string{"/public/status/acme/feed.atom", "/public/status/acme/feed.atom?nocache=123"} {
		rec := serveAddonTest(h, http.MethodGet, target, nil)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/atom+xml; charset=utf-8" || rec.Header().Get("Cache-Control") != "public, max-age=300" {
			t.Fatalf("%s = %d %v", target, rec.Code, rec.Header())
		}
	}
	cache.Invalidate("acme")
	if _, _, err := cache.GetPage(feedCacheKey("acme"), build); err != nil || builds != 2 {
		t.Fatalf("invalidating the page kept its feed (builds = %d)", builds)
	}
}

func TestPublicPageData_HidesWhatSettingsHide(t *testing.T) {
	uptime, latency, days := 99.5, 120.0, 30
	monitor := MonitorStatus{
		ID: addonTestAPI, Name: "API", URL: "https://internal.example.com/health", Status: "up",
		Tags: []string{"core"}, Uptime1h: &uptime, Uptime24h: &uptime, AvgLatency1h: &latency, AvgLatency24h: &latency,
		TLSDaysUntilExpiry: &days, TLSNotAfter: "2027-01-01",
	}
	data := &StatusPageData{
		Slug: "acme", Title: "Acme",
		Sections: []StatusPageSectionData{{Title: "Edge", Monitors: []MonitorStatus{monitor}}},
		Monitors: []MonitorStatus{monitor},
	}
	body, err := json.Marshal(publicPageData(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"uptime_1h", "uptime_24h", "internal.example.com", "avg_latency", "core", "tls_days_until_expiry", "2027-01-01"} {
		if strings.Contains(string(body), leaked) {
			t.Errorf("/data published hidden %s: %s", leaked, body)
		}
	}
	if data.Monitors[0].Uptime24h == nil || data.Sections[0].Monitors[0].URL == "" {
		t.Fatal("redaction modified the cached page data")
	}

	data.ShowMonitorUptime, data.ShowMonitorURL, data.ShowLatencyCharts, data.ShowMonitorTags, data.ShowMonitorTLS = true, true, true, true, true
	body, _ = json.Marshal(publicPageData(data))
	for _, shown := range []string{"uptime_1h", "uptime_24h", "internal.example.com", "avg_latency_24h", "core", "tls_days_until_expiry"} {
		if !strings.Contains(string(body), shown) {
			t.Errorf("/data dropped %s although the page shows it", shown)
		}
	}
}

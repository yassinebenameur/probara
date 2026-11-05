package statuspage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func ptrStr(s string) *string { return &s }

func TestLatencyCells_ResamplesOntoFixedGrid(t *testing.T) {
	now := time.Date(2026, 3, 10, 14, 37, 0, 0, time.UTC)
	grid := latencyGrids["24h"]
	// Window: [Mar 9 15:00, Mar 10 15:00), 24 hourly cells.
	points := []LatencyPoint{
		{Timestamp: time.Date(2026, 3, 9, 14, 0, 0, 0, time.UTC), LatencyMS: 999},  // before the window
		{Timestamp: time.Date(2026, 3, 9, 15, 0, 0, 0, time.UTC), LatencyMS: 100},  // first cell
		{Timestamp: time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC), LatencyMS: 200}, // last cell
		{Timestamp: time.Date(2026, 3, 10, 14, 30, 0, 0, time.UTC), LatencyMS: 301},
	}
	cells := latencyCells(points, grid, now)
	if len(cells) != 24 {
		t.Fatalf("cells = %d, want 24", len(cells))
	}
	if cells[0] == nil || *cells[0] != 100 {
		t.Fatalf("first cell = %v, want 100", cells[0])
	}
	if cells[23] == nil || *cells[23] != 251 {
		t.Fatalf("last cell = %v, want mean 251", cells[23])
	}
	for i := 1; i < 23; i++ {
		if cells[i] != nil {
			t.Fatalf("cell %d = %d, want a gap (nil), never zero", i, *cells[i])
		}
	}
}

func TestApplyMonitorLatency_SevenDaysIsTheLastWeekOfThirtyDays(t *testing.T) {
	now := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	var daily []LatencyPoint
	for d := 0; d < 30; d++ {
		day := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -d)
		daily = append(daily, LatencyPoint{Timestamp: day, LatencyMS: 100 + d})
	}
	var view statusPageMonitorView
	applyMonitorLatency(&view, MonitorStatus{LatencyHistory30d: daily}, now)
	if !view.HasLatency {
		t.Fatal("HasLatency = false with 30 days of data")
	}
	var week []*int
	if err := json.Unmarshal([]byte(view.Latency7dJSON), &week); err != nil {
		t.Fatal(err)
	}
	if len(week) != 7 || week[6] == nil || *week[6] != 100 || week[0] == nil || *week[0] != 106 {
		t.Fatalf("7d series = %s, want the newest 7 days ending with today's 100ms", view.Latency7dJSON)
	}
	if view.Latency1hJSON == "" || strings.Contains(view.Latency1hJSON, "1") {
		t.Fatalf("1h series = %s, want all null", view.Latency1hJSON)
	}

	var empty statusPageMonitorView
	applyMonitorLatency(&empty, MonitorStatus{}, now)
	if empty.HasLatency {
		t.Fatal("HasLatency = true without data")
	}
}

func TestApplyPageMeta_DescriptionFallbackAndURLs(t *testing.T) {
	cases := []struct {
		name string
		data StatusPageData
		want string
	}{
		{"seo wins", StatusPageData{Title: "Acme", SEODescription: "Search text", Description: ptrStr("Desc")}, "Search text"},
		{"description next", StatusPageData{Title: "Acme", Description: ptrStr("  Desc  ")}, "Desc"},
		{"generic last", StatusPageData{Title: "Acme"}, "Live status, uptime and incident history for Acme."},
	}
	for _, tc := range cases {
		var view statusPageRenderView
		applyPageMeta(&view, &tc.data)
		if view.MetaDescription != tc.want {
			t.Errorf("%s: MetaDescription = %q, want %q", tc.name, view.MetaDescription, tc.want)
		}
	}

	long := strings.Repeat("é", maxMetaDescriptionRunes+20)
	var view statusPageRenderView
	applyPageMeta(&view, &StatusPageData{Title: "Acme", Description: &long})
	if n := len([]rune(view.MetaDescription)); n != maxMetaDescriptionRunes {
		t.Fatalf("long description = %d runes, want %d", n, maxMetaDescriptionRunes)
	}

	applyPageMeta(&view, &StatusPageData{Slug: "acme", LogoURL: ptrStr("/logo.png")})
	if view.CanonicalURL != "" || view.FeedURL != "/public/status/acme/feed.atom" || view.OGImageURL != "" {
		t.Fatalf("no base URL: canonical=%q feed=%q og=%q", view.CanonicalURL, view.FeedURL, view.OGImageURL)
	}
	applyPageMeta(&view, &StatusPageData{Slug: "acme", CanonicalURL: "https://status.example.com/public/status/acme", LogoURL: ptrStr("https://cdn.example.com/l.png")})
	if view.FeedURL != "https://status.example.com/public/status/acme/feed.atom" || view.OGImageURL != "https://cdn.example.com/l.png" {
		t.Fatalf("with base URL: feed=%q og=%q", view.FeedURL, view.OGImageURL)
	}
}

func TestPublicPageURL(t *testing.T) {
	if got := publicPageURL(" https://status.example.com/ ", "acme"); got != "https://status.example.com/public/status/acme" {
		t.Fatalf("publicPageURL = %q", got)
	}
	if got := publicPageURL("", "acme"); got != "" {
		t.Fatalf("unset base = %q, want empty (never a guessed host)", got)
	}
}

func polishTestPage() *StatusPageData {
	return &StatusPageData{
		ID:                "page-1",
		Slug:              "acme",
		Title:             `Acme "Cloud"`,
		SEODescription:    `Status for <Acme> & friends`,
		CanonicalURL:      "https://status.example.com/public/status/acme",
		ShowFooter:        true,
		ShowMonitorUptime: true,
		ShowLatencyCharts: true,
		Monitors: []MonitorStatus{{
			ID:          "m-1",
			Name:        "API",
			MonitorType: "http",
			Status:      "up",
			LatencyHistory1h: []LatencyPoint{
				{Timestamp: time.Now().UTC().Add(-3 * time.Minute), LatencyMS: 120},
			},
		}},
	}
}

func TestRenderPublicStatusPage_SEOHeadTags(t *testing.T) {
	html, err := renderPublicStatusPage(polishTestPage(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<meta name="description" content="Status for &lt;Acme&gt; &amp; friends" />`,
		`<meta property="og:title" content="Acme &#34;Cloud&#34; · Status" />`,
		`<meta property="og:url" content="https://status.example.com/public/status/acme" />`,
		`<link rel="canonical" href="https://status.example.com/public/status/acme" />`,
		`<meta name="twitter:card" content="summary" />`,
		`<link rel="alternate" type="application/atom+xml"`,
		`href="https://status.example.com/public/status/acme/feed.atom"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page is missing %s", want)
		}
	}

	page := polishTestPage()
	page.CanonicalURL = ""
	html, err = renderPublicStatusPage(page, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, `rel="canonical"`) || strings.Contains(html, `og:url`) {
		t.Fatal("canonical link rendered without a configured public origin")
	}
	if !strings.Contains(html, `href="/public/status/acme/feed.atom"`) {
		t.Fatal("feed discovery link should fall back to the page path")
	}
}

func TestRenderPublicStatusPage_LatencyChartFollowsSetting(t *testing.T) {
	html, err := renderPublicStatusPage(polishTestPage(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `class="latency-chart js-latency"`) || !strings.Contains(html, `data-latency-1h="[`) {
		t.Fatal("show_latency_charts with data renders no chart")
	}

	off := polishTestPage()
	off.ShowLatencyCharts = false
	html, err = renderPublicStatusPage(off, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, `class="latency-chart js-latency"`) {
		t.Fatal("show_latency_charts=false still renders the chart")
	}

	noData := polishTestPage()
	noData.Monitors[0].LatencyHistory1h = nil
	html, err = renderPublicStatusPage(noData, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, `class="latency-chart js-latency"`) {
		t.Fatal("a component with no latency data renders an empty chart")
	}
}

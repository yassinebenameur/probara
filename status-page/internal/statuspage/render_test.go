package statuspage

import (
	"encoding/json"
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestRenderPublicStatusPage_UsesSharedRangeControlForSingleMonitorAndDarkThemeByDefault(t *testing.T) {
	html, err := renderPublicStatusPage(&StatusPageData{
		ID:                "page-1",
		Slug:              "status",
		Title:             "Example Status",
		DefaultTheme:      "dark",
		AllowThemeToggle:  true,
		ShowFooter:        true,
		ShowGlobalUptime:  true,
		ShowMonitorUptime: true,
		Monitors: []MonitorStatus{
			{
				ID:          "monitor-1",
				Name:        "API",
				MonitorType: "http",
				Status:      "up",
				URL:         "https://example.com/health",
				Tags:        []string{"core"},
				UptimeHistory24h: []HourlyUptime{
					{Hour: "2026-03-01T00:00", Uptime: 100},
					{Hour: "2026-03-01T01:00", Uptime: 99},
				},
				UptimeHistory7d: []DailyUptime{
					{Date: "2026-02-23", Uptime: 98},
					{Date: "2026-02-24", Uptime: 97},
				},
				UptimeHistory30d: []DailyUptime{
					{Date: "2026-02-01", Uptime: 96},
					{Date: "2026-02-02", Uptime: 94},
				},
				UptimeHistory90d: []DailyUptime{
					{Date: "2026-01-01", Uptime: 92},
					{Date: "2026-01-02", Uptime: 90},
				},
			},
		},
		UptimeHistory1: []HourlyUptime{
			{Hour: "2026-03-01T00:00", Uptime: 99},
			{Hour: "2026-03-01T01:00", Uptime: 97},
		},
		UptimeHistory7: []DailyUptime{
			{Date: "2026-02-23", Uptime: 98},
			{Date: "2026-02-24", Uptime: 96},
		},
		UptimeHistory30: []DailyUptime{
			{Date: "2026-02-01", Uptime: 95},
			{Date: "2026-02-02", Uptime: 93},
		},
		UptimeHistory90: []DailyUptime{
			{Date: "2026-01-01", Uptime: 91},
			{Date: "2026-01-02", Uptime: 89},
		},
	}, true)
	if err != nil {
		t.Fatalf("renderPublicStatusPage() error = %v", err)
	}

	for _, want := range []string{
		`data-default-theme="dark"`,
		`data-default-range="24h"`,
		`id="kioskStats"`,
		`id="statusHero"`,
		`id="incidentsSection"`,
		`<div class="header-inner">`,
		`id="themeToggleBtn"`,
		`data-mode="default"`,
		`data-mode="compact"`,
		`data-mode="kiosk"`,
		`data-range-pill="24h"`,
		`data-range-pill="7d"`,
		`data-range-pill="30d"`,
		`data-range-pill="90d"`,
		`class="range-btn active" data-range-pill="24h" aria-pressed="true"`,
		`data-range-7d=`,
		`data-monitor-id="monitor-1"`,
		`localStorage.setItem('status-page-mode'`,
		`status-page-live-refresh`,
		`new DOMParser().parseFromString`,
		`replaceLiveRegion('servicesList', nextDoc)`,
		`Status Updates`,
		`No active incidents.`,
		`class="group-cards"`,
		`<div class="footer-inner">`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected rendered HTML to contain %q", want)
		}
	}

	for _, unwanted := range []string{
		`id="statusPageSearch"`,
		`id="statusFilter"`,
		`id="rangeFilter"`,
		`id="typeFilter"`,
		`id="sortControl"`,
		`id="layoutControl"`,
		`window.location.reload()`,
		`fonts.googleapis.com`,
		`unpkg.com/lucide`,
		`Incident timeline coming later`,
		`System Logs`,
	} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("expected rendered HTML to omit %q for single-monitor pages", unwanted)
		}
	}
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`(?s)class="uptime-bars js-strip".*?data-cells="48".*?data-cells-7d="70".*?data-cells-30d="90".*?data-cells-90d="90"`),
		regexp.MustCompile(`(?s)class="monitor-bar js-strip".*?data-cells="36".*?data-cells-30d="60".*?data-cells-90d="60"`),
		regexp.MustCompile(`(?s)class="expanded-bars js-strip".*?data-cells="36".*?data-cells-30d="90".*?data-cells-90d="90"`),
	} {
		if !re.MatchString(html) {
			t.Fatalf("expected rendered HTML to match %q", re.String())
		}
	}
	assertStripPointCount(t, html, `class="uptime-bars js-strip"[\s\S]*?data-range-30d="([^"]+)"`, 90)
	assertStripPointCount(t, html, `class="uptime-bars js-strip"[\s\S]*?data-range-90d="([^"]+)"`, 90)
	assertStripPointCount(t, html, `class="monitor-bar js-strip"[\s\S]*?data-range-30d="([^"]+)"`, 60)
	assertStripPointCount(t, html, `class="monitor-bar js-strip"[\s\S]*?data-range-90d="([^"]+)"`, 60)
	assertStripPointCount(t, html, `class="expanded-bars js-strip"[\s\S]*?data-range-30d="([^"]+)"`, 90)
	assertStripPointCount(t, html, `class="expanded-bars js-strip"[\s\S]*?data-range-90d="([^"]+)"`, 90)
	assertStripContainsUptime(t, html, `class="uptime-bars js-strip"[\s\S]*?data-range-30d="([^"]+)"`, 95, 93)
	assertStripContainsUptime(t, html, `class="uptime-bars js-strip"[\s\S]*?data-range-90d="([^"]+)"`, 91, 89)
	assertStripContainsUptime(t, html, `class="monitor-bar js-strip"[\s\S]*?data-range-30d="([^"]+)"`, 96, 94)
	assertStripContainsUptime(t, html, `class="monitor-bar js-strip"[\s\S]*?data-range-90d="([^"]+)"`, 92, 90)
	for _, want := range []string{
		`data-monitor-uptime-value`,
		`data-range-value-24h="99.50%"`,
		`data-range-value-7d="97.50%"`,
		`data-range-value-30d="95.00%"`,
		`data-range-value-90d="91.00%"`,
		`data-monitor-uptime-label`,
		`>24h uptime<`,
		`>99.50%<`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected rendered HTML to contain %q", want)
		}
	}
}

func TestRenderPublicStatusPage_IncludesToolbarForMultipleMonitors(t *testing.T) {
	html, err := renderPublicStatusPage(&StatusPageData{
		ID:                "page-1",
		Slug:              "status",
		Title:             "Example Status",
		DefaultTheme:      "dark",
		ShowMonitorUptime: true,
		Sections: []StatusPageSectionData{
			{
				ID:    "section-core",
				Title: "Core",
				Monitors: []MonitorStatus{
					{Name: "API", MonitorType: "http", Status: "up"},
				},
			},
			{
				ID:    "section-edge",
				Title: "Edge",
				Monitors: []MonitorStatus{
					{Name: "Worker", MonitorType: "ping", Status: "degraded"},
				},
			},
		},
	}, false)
	if err != nil {
		t.Fatalf("renderPublicStatusPage() error = %v", err)
	}

	for _, want := range []string{
		`id="statusPageSearch"`,
		`id="statusFilter"`,
		`id="kioskStats"`,
		`id="statusHero"`,
		`id="incidentsSection"`,
		`class="group-cards"`,
		`data-range-pill="24h"`,
		`data-range-pill="7d"`,
		`data-range-pill="30d"`,
		`data-range-pill="90d"`,
		`data-range-7d=`,
		`data-monitor-id="`,
		`<div class="header-inner">`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected rendered HTML to contain %q", want)
		}
	}
	for _, unwanted := range []string{
		`id="typeFilter"`,
		`id="sortControl"`,
		`id="layoutControl"`,
		`id="rangeFilter"`,
		`id="resultsCounter"`,
		`customizeOpenBtn`,
	} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("expected rendered HTML to omit obsolete control %q", unwanted)
		}
	}
	if !regexp.MustCompile(`(?s)class="monitor-bar js-strip".*?data-cells="36".*?data-cells-30d="60".*?data-cells-90d="60"`).MatchString(html) {
		t.Fatalf("expected monitor strips to render with increased cells for longer ranges")
	}
	assertStripPointCount(t, html, `class="monitor-bar js-strip"[\s\S]*?data-range-30d="([^"]+)"`, 60)
	assertStripPointCount(t, html, `class="monitor-bar js-strip"[\s\S]*?data-range-90d="([^"]+)"`, 60)
	for _, want := range []string{`Core`, `Edge`, `data-section-id="section-core"`, `data-section-id="section-edge"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected rendered HTML to contain section marker %q", want)
		}
	}
	if strings.Contains(html, `<h2 class="section-title">Services</h2>`) {
		t.Fatalf("expected grouped render to omit redundant outer Services heading")
	}
}

func TestRenderPublicStatusPage_HidesThemeToggleWhenDisabled(t *testing.T) {
	html, err := renderPublicStatusPage(&StatusPageData{
		ID:               "page-1",
		Slug:             "status",
		Title:            "Example Status",
		DefaultTheme:     "light",
		AllowThemeToggle: false,
		Monitors: []MonitorStatus{
			{Name: "API", MonitorType: "http", Status: "up"},
		},
	}, false)
	if err != nil {
		t.Fatalf("renderPublicStatusPage() error = %v", err)
	}

	if !strings.Contains(html, `data-default-theme="light"`) {
		t.Fatalf("expected rendered HTML to include light default theme")
	}
	if strings.Contains(html, `id="themeToggleBtn"`) {
		t.Fatalf("expected theme toggle button to be omitted")
	}
}

func TestRenderPublicStatusPage_RendersPublishedIncidentCards(t *testing.T) {
	html, err := renderPublicStatusPage(&StatusPageData{
		ID:    "page-1",
		Slug:  "status",
		Title: "Example Status",
		Incidents: []StatusPageIncident{
			{
				Title:              "API outage",
				Summary:            "Requests are failing.",
				State:              "investigating",
				AffectedComponents: []string{"API"},
				Updates: []StatusPageIncidentUpdate{
					{Message: "We are investigating elevated API errors."},
				},
			},
		},
	}, true)
	if err != nil {
		t.Fatalf("renderPublicStatusPage() error = %v", err)
	}
	if !strings.Contains(html, "API outage") {
		t.Fatalf("expected incident title in HTML")
	}
	if !strings.Contains(html, "Latest update: We are investigating elevated API errors.") {
		t.Fatalf("expected public incident update in HTML")
	}
	for _, want := range []string{
		`Status Updates`,
		`Affected services: API`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected clearer incident copy %q in HTML", want)
		}
	}
	if strings.Contains(html, "System Logs") {
		t.Fatalf("expected incidents section heading to avoid vague System Logs wording")
	}
	if strings.Contains(html, "Incident timeline coming later") {
		t.Fatalf("expected placeholder copy to be removed")
	}
}

func TestPublicStatusPageDarkThemeDimTextMeetsContrastOnDarkSurfaces(t *testing.T) {
	darkTheme := strings.Split(publicStatusPageTemplate, `body[data-theme="light"]`)[0]
	textDim := extractCSSHexVariable(t, darkTheme, "--text-dim")
	surfaceCard := extractCSSHexVariable(t, darkTheme, "--surface-card")
	bg := extractCSSHexVariable(t, darkTheme, "--bg")

	for name, background := range map[string]string{
		"card": surfaceCard,
		"page": bg,
	} {
		if got := contrastRatio(textDim, background); got < 4.5 {
			t.Fatalf("dark --text-dim contrast on %s background = %.2f, want at least 4.50", name, got)
		}
	}
}

func assertStripPointCount(t *testing.T, renderedHTML string, pattern string, want int) {
	t.Helper()
	match := regexp.MustCompile(pattern).FindStringSubmatch(renderedHTML)
	if len(match) < 2 {
		t.Fatalf("expected rendered HTML to match %q", pattern)
	}
	var points []map[string]any
	if err := json.Unmarshal([]byte(html.UnescapeString(match[1])), &points); err != nil {
		t.Fatalf("unmarshal strip data for %q: %v", pattern, err)
	}
	if got := len(points); got != want {
		t.Fatalf("expected %d strip points for %q, got %d", want, pattern, got)
	}
}

func extractCSSHexVariable(t *testing.T, css string, name string) string {
	t.Helper()
	match := regexp.MustCompile(regexp.QuoteMeta(name) + `:\s*(#[0-9a-fA-F]{6});`).FindStringSubmatch(css)
	if len(match) < 2 {
		t.Fatalf("expected CSS variable %s to be a hex color", name)
	}
	return match[1]
}

func contrastRatio(foreground, background string) float64 {
	foregroundLuminance := relativeLuminance(foreground)
	backgroundLuminance := relativeLuminance(background)
	lighter := math.Max(foregroundLuminance, backgroundLuminance)
	darker := math.Min(foregroundLuminance, backgroundLuminance)
	return (lighter + 0.05) / (darker + 0.05)
}

func relativeLuminance(hexColor string) float64 {
	red := linearRGB(hexColor[1:3])
	green := linearRGB(hexColor[3:5])
	blue := linearRGB(hexColor[5:7])
	return 0.2126*red + 0.7152*green + 0.0722*blue
}

func linearRGB(hexPair string) float64 {
	value, err := strconv.ParseInt(hexPair, 16, 64)
	if err != nil {
		return 0
	}
	channel := float64(value) / 255
	if channel <= 0.03928 {
		return channel / 12.92
	}
	return math.Pow((channel+0.055)/1.055, 2.4)
}

func assertStripContainsUptime(t *testing.T, renderedHTML string, pattern string, want ...float64) {
	t.Helper()
	match := regexp.MustCompile(pattern).FindStringSubmatch(renderedHTML)
	if len(match) < 2 {
		t.Fatalf("expected rendered HTML to match %q", pattern)
	}
	var points []map[string]any
	if err := json.Unmarshal([]byte(html.UnescapeString(match[1])), &points); err != nil {
		t.Fatalf("unmarshal strip data for %q: %v", pattern, err)
	}
	for _, expected := range want {
		found := false
		for _, point := range points {
			uptime, ok := point["uptime"].(float64)
			if ok && uptime == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected strip %q to contain uptime %.2f", pattern, expected)
		}
	}
}

func benchmarkStatusPageData(monitorCount int) *StatusPageData {
	monitors := make([]MonitorStatus, 0, monitorCount)
	for i := 0; i < monitorCount; i++ {
		monitors = append(monitors, MonitorStatus{
			ID:          "monitor-" + strconv.Itoa(i),
			Name:        "Service " + strconv.Itoa(i),
			MonitorType: "http",
			Status:      "up",
			URL:         "https://example.com/health/" + strconv.Itoa(i),
			Tags:        []string{"core"},
			UptimeHistory24h: []HourlyUptime{
				{Hour: "2026-03-01T00:00", Uptime: 100},
				{Hour: "2026-03-01T01:00", Uptime: 99},
			},
			UptimeHistory7d: []DailyUptime{
				{Date: "2026-02-23", Uptime: 98},
				{Date: "2026-02-24", Uptime: 97},
			},
			UptimeHistory30d: []DailyUptime{
				{Date: "2026-02-01", Uptime: 96},
				{Date: "2026-02-02", Uptime: 94},
			},
			UptimeHistory90d: []DailyUptime{
				{Date: "2026-01-01", Uptime: 92},
				{Date: "2026-01-02", Uptime: 90},
			},
		})
	}
	return &StatusPageData{
		ID:                "page-bench",
		Slug:              "status",
		Title:             "Benchmark Status",
		DefaultTheme:      "dark",
		AllowThemeToggle:  true,
		ShowFooter:        true,
		ShowGlobalUptime:  true,
		ShowMonitorUptime: true,
		Monitors:          monitors,
		UptimeHistory1: []HourlyUptime{
			{Hour: "2026-03-01T00:00", Uptime: 99},
			{Hour: "2026-03-01T01:00", Uptime: 97},
		},
		UptimeHistory7: []DailyUptime{
			{Date: "2026-02-23", Uptime: 98},
			{Date: "2026-02-24", Uptime: 96},
		},
	}
}

func BenchmarkRenderPublicStatusPage(b *testing.B) {
	data := benchmarkStatusPageData(180)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := renderPublicStatusPage(data, true); err != nil {
			b.Fatalf("renderPublicStatusPage() error = %v", err)
		}
	}
}

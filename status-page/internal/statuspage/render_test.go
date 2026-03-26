package statuspage

import (
	"regexp"
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
				UptimeHistory90d: []DailyUptime{
					{Date: "2026-03-01", Uptime: 100},
				},
			},
		},
		UptimeHistory90: []DailyUptime{{Date: "2026-03-01", Uptime: 100}},
	}, true)
	if err != nil {
		t.Fatalf("renderPublicStatusPage() error = %v", err)
	}

	for _, want := range []string{
		`data-default-theme="dark"`,
		`data-default-range="30d"`,
		`id="kioskStats"`,
		`id="statusHero"`,
		`id="incidentsSection"`,
		`id="themeToggleBtn"`,
		`data-mode="default"`,
		`data-mode="compact"`,
		`data-mode="kiosk"`,
		`data-range-pill="24h"`,
		`data-range-pill="7d"`,
		`data-range-pill="30d"`,
		`data-range-pill="90d"`,
		`class="range-btn active" data-range-pill="30d" aria-pressed="true"`,
		`data-range-7d=`,
		`data-monitor-id="monitor-1"`,
		`localStorage.setItem('status-page-mode'`,
		`status-page-live-refresh`,
		`new DOMParser().parseFromString`,
		`replaceLiveRegion('servicesList', nextDoc)`,
		`No active incidents.`,
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
	} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("expected rendered HTML to omit %q for single-monitor pages", unwanted)
		}
	}
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`(?s)class="uptime-bars js-strip".*?data-cells="48"`),
		regexp.MustCompile(`(?s)class="monitor-bar js-strip".*?data-cells="36"`),
	} {
		if !re.MatchString(html) {
			t.Fatalf("expected rendered HTML to match %q", re.String())
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
		`data-range-pill="24h"`,
		`data-range-pill="7d"`,
		`data-range-pill="30d"`,
		`data-range-pill="90d"`,
		`data-range-7d=`,
		`data-monitor-id="`,
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
	if !regexp.MustCompile(`(?s)class="monitor-bar js-strip".*?data-cells="36"`).MatchString(html) {
		t.Fatalf("expected monitor strips to render with 36 cells")
	}
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

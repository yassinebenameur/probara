package statuspage

import (
	"strings"
	"testing"
)

func TestRenderPublicStatusPage_IncludesToolbarAndDarkThemeByDefault(t *testing.T) {
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
		`id="statusPageSearch"`,
		`id="statusFilter"`,
		`id="typeFilter"`,
		`id="sortControl"`,
		`data-default-theme="dark"`,
		`id="themeToggleBtn"`,
		`detail-footer`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected rendered HTML to contain %q", want)
		}
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

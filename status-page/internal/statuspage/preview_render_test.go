package statuspage

import (
	"os"
	"strconv"
	"testing"
	"time"
)

// Temporary helper: renders a rich sample page to /tmp for visual review.
// Run with: go test ./status-page/internal/statuspage/ -run TestWritePreviewHTML -count=1
func TestWritePreviewHTML(t *testing.T) {
	if os.Getenv("WRITE_PREVIEW") == "" {
		t.Skip("preview generation disabled")
	}

	now := time.Now().UTC()
	recent := now.Add(-90 * time.Second)
	older := now.Add(-6 * time.Minute)

	hourly := func(base float64) []HourlyUptime {
		points := make([]HourlyUptime, 0, 24)
		for i := 0; i < 24; i++ {
			up := base
			if i%9 == 0 {
				up = base - 3.5
			}
			if i == 13 {
				up = base - 12
			}
			if up > 100 {
				up = 100
			}
			points = append(points, HourlyUptime{Hour: "2026-06-09T" + strconv.Itoa(i) + ":00", Uptime: up})
		}
		return points
	}
	daily := func(base float64, days int) []DailyUptime {
		points := make([]DailyUptime, 0, days)
		for i := 0; i < days; i++ {
			up := base
			if i%11 == 0 {
				up = base - 4
			}
			if up > 100 {
				up = 100
			}
			points = append(points, DailyUptime{Date: "2026-05-" + strconv.Itoa(1+i%28), Uptime: up})
		}
		return points
	}

	latency := func(ms int) *int { return &ms }
	uptime := func(v float64) *float64 { return &v }

	mon := func(id, name, kind, status, url string, lat int) MonitorStatus {
		checked := recent
		if lat%2 == 0 {
			checked = older
		}
		return MonitorStatus{
			ID:                id,
			Name:              name,
			MonitorType:       kind,
			Status:            status,
			URL:               url,
			LastCheckTime:     &checked,
			LastLatency:       latency(lat),
			Uptime24h:         uptime(99.98),
			Tags:              []string{"core"},
			UptimeHistory24h:  hourly(99.9),
			UptimeHistory7d:   daily(99.7, 7),
			UptimeHistory30d:  daily(99.4, 30),
			UptimeHistory90d:  daily(99.1, 90),
			UptimeHistory365d: daily(98.9, 180),
		}
	}

	sections := []StatusPageSectionData{
		{
			ID:    "sec-core",
			Title: "Core Platform",
			Monitors: []MonitorStatus{
				mon("m1", "Public API", "http", "up", "https://api.example.com/health", 42),
				mon("m2", "Dashboard", "http", "up", "https://app.example.com", 88),
				mon("m3", "Auth Service", "grpc", "up", "auth.internal:443", 12),
				mon("m4", "GraphQL Gateway", "http", "degraded", "https://gql.example.com", 412),
				mon("m5", "Billing Worker", "push", "up", "", 0),
				mon("m6", "Webhooks", "http", "up", "https://hooks.example.com", 61),
			},
		},
		{
			ID:    "sec-edge",
			Title: "Edge & Network",
			Monitors: []MonitorStatus{
				mon("m7", "CDN eu-west", "ping", "up", "cdn-eu.example.com", 9),
				mon("m8", "CDN us-east", "ping", "up", "cdn-us.example.com", 24),
				mon("m9", "DNS Resolution", "dns", "up", "example.com", 5),
				mon("m10", "Load Balancer", "http", "down", "https://lb.example.com/ping", 0),
				mon("m11", "SIP Trunk", "sip", "up", "sip.example.com", 33),
			},
		},
		{
			ID:    "sec-data",
			Title: "Data & Storage",
			Monitors: []MonitorStatus{
				mon("m12", "Primary Postgres", "agent", "up", "", 0),
				mon("m13", "Read Replicas", "agent", "up", "", 0),
				mon("m14", "Object Storage", "http", "up", "https://s3.example.com", 102),
				mon("m15", "Search Cluster", "http", "maintenance", "https://search.example.com", 76),
				mon("m16", "Cache Layer", "agent", "up", "", 0),
				mon("m17", "Event Stream", "synthetic_api", "up", "https://nats.example.com", 18),
				mon("m18", "Checkout Flow", "synthetic_browser", "up", "https://shop.example.com", 1843),
			},
		},
	}

	all := make([]MonitorStatus, 0)
	for _, s := range sections {
		all = append(all, s.Monitors...)
	}

	desc := "Live availability for the Example platform, refreshed in real time."
	html, err := renderPublicStatusPage(&StatusPageData{
		ID:                "page-preview",
		Slug:              "example",
		Title:             "Example Cloud",
		Description:       &desc,
		DefaultTheme:      "dark",
		AllowThemeToggle:  true,
		ShowFooter:        true,
		ShowGlobalUptime:  true,
		ShowMonitorUptime: true,
		Sections:          sections,
		Monitors:          all,
		UptimeHistory1:    hourly(99.8),
		UptimeHistory7:    daily(99.6, 7),
		UptimeHistory30:   daily(99.3, 30),
		UptimeHistory90:   daily(99.0, 90),
		Incidents: []StatusPageIncident{
			{
				Title:              "Elevated latency on GraphQL Gateway",
				Summary:            "We are seeing elevated p95 latency on the GraphQL gateway and have identified a slow downstream dependency.",
				State:              "monitoring",
				AffectedComponents: []string{"GraphQL Gateway"},
				Updates:            []StatusPageIncidentUpdate{{Message: "A fix has been deployed; latency is recovering."}},
			},
		},
	}, true)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := os.WriteFile("/tmp/status-preview.html", []byte(html), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

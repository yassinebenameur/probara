package statuspage

import (
	"time"

	"github.com/yassinebenameur/probara/shared/statustemplate"
)

// classicScenario names a sample page state for the classic template tests
// and previews.
type classicScenario string

const (
	scenarioOperational classicScenario = "operational"
	scenarioDegraded    classicScenario = "degraded"
	scenarioOutage      classicScenario = "outage"
	scenarioMaintenance classicScenario = "maintenance"
	scenarioSingle      classicScenario = "single"
	scenarioNoData      classicScenario = "nodata"
	scenarioEmpty       classicScenario = "empty"
)

// classicSampleData builds a page like the approved mock: three sections of
// three services with 90 days of history, one service only 59 days old.
func classicSampleData(scenario classicScenario, now time.Time) *StatusPageData {
	now = now.UTC()
	seed := int64(5)
	rnd := func(s int64) func() float64 {
		return func() float64 { s = (s * 16807) % 2147483647; return float64(s) / 2147483647 }
	}
	daily := func(r func() float64, days, fresh int) []DailyUptime {
		out := make([]DailyUptime, 0, days)
		for i := 0; i < days; i++ {
			day := utcDay(now).AddDate(0, 0, -(days - 1 - i))
			up := 100.0
			switch x := r(); {
			case i < fresh:
				up = -1
			case x > .996:
				up = 91.2
			case x > .982:
				up = 98.1
			}
			out = append(out, DailyUptime{Date: day.Format("2006-01-02"), Uptime: up})
		}
		return out
	}
	hourly := func(r func() float64, fault string) []HourlyUptime {
		out := make([]HourlyUptime, 0, 24)
		for i := 0; i < 24; i++ {
			hour := now.Truncate(time.Hour).Add(-time.Duration(23-i) * time.Hour)
			up := 100.0
			if x := r(); x > .97 {
				up = 98.4
			}
			if i == 23 && fault == "down" {
				up = 62
			}
			if i == 23 && fault == "degraded" {
				up = 97
			}
			out = append(out, HourlyUptime{Hour: hour.Format("15:04"), Uptime: up})
		}
		return out
	}
	checked := now.Add(-40 * time.Second)
	latency := func(ms int) *int { return &ms }

	faults := map[string]string{}
	switch scenario {
	case scenarioDegraded:
		faults["GraphQL gateway"] = "degraded"
	case scenarioOutage:
		faults["Load balancer"] = "down"
		faults["GraphQL gateway"] = "degraded"
	case scenarioMaintenance:
		faults["Primary Postgres"] = "maintenance"
	}

	mon := func(id, name, kind, url string, ms int) MonitorStatus {
		seed += 17
		r := rnd(seed * 31)
		fresh := 0
		if name == "Billing worker" {
			fresh = 31
		}
		status := "up"
		if f, ok := faults[name]; ok {
			status = f
		}
		m := MonitorStatus{
			ID: id, Name: name, MonitorType: kind, URL: url, Status: status,
			LastCheckTime: &checked, LastLatency: latency(ms),
			UptimeHistory24h: hourly(r, status),
			UptimeHistory7d:  daily(rnd(seed*7), 7, 0),
			UptimeHistory30d: daily(rnd(seed*7), 30, 0),
			UptimeHistory90d: daily(rnd(seed*7), 90, fresh),
		}
		if kind == "push" {
			m.LastLatency = nil
		}
		if scenario == scenarioNoData {
			m.LastCheckTime = nil
			m.LastLatency = nil
			m.Status = "unknown"
			m.UptimeHistory24h, m.UptimeHistory7d, m.UptimeHistory30d, m.UptimeHistory90d = nil, nil, nil, nil
		}
		return m
	}

	sections := []StatusPageSectionData{
		{ID: "s1", Title: "Core platform", Monitors: []MonitorStatus{
			mon("m1", "Public API", "http", "https://api.example.com/health", 42),
			mon("m2", "GraphQL gateway", "http", "https://gql.example.com", 118),
			mon("m3", "Auth service", "grpc", "auth.example.com:443", 12),
		}},
		{ID: "s2", Title: "Edge and network", Monitors: []MonitorStatus{
			mon("m4", "Load balancer", "http", "https://lb.example.com/ping", 31),
			mon("m5", "CDN eu-west", "ping", "cdn-eu.example.com", 9),
			mon("m6", "SIP trunk", "sip", "sip.example.com", 33),
		}},
		{ID: "s3", Title: "Data and storage", Monitors: []MonitorStatus{
			mon("m7", "Primary Postgres", "postgres", "", 4),
			mon("m8", "Billing worker", "push", "", 0),
			mon("m9", "Checkout flow", "synthetic_browser", "https://shop.example.com", 1843),
		}},
	}
	switch scenario {
	case scenarioSingle:
		sections = []StatusPageSectionData{{Title: "Services", Monitors: []MonitorStatus{sections[0].Monitors[0]}}}
	case scenarioEmpty:
		sections = nil
	}
	monitors := make([]MonitorStatus, 0)
	for _, s := range sections {
		monitors = append(monitors, s.Monitors...)
	}

	at := func(day, hour, minute int) time.Time {
		return utcDay(now).AddDate(0, 0, -day).Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
	}
	resolvedAt := at(1, 9, 31)
	incidents := []StatusPageIncident{{
		ID: "i-past", Title: "Delayed webhook deliveries", State: "resolved",
		PublishedAt: at(1, 9, 12), ResolvedAt: &resolvedAt,
		Updates: []StatusPageIncidentUpdate{
			{Message: "A stuck queue consumer was restarted. Deliveries are back to normal.", CreatedAt: at(1, 9, 31)},
		},
		Summary: "Webhook deliveries are delayed by up to 20 minutes.",
	}}
	// "Now" in the fixture is a fixed hour of today so update times sit on
	// today's date; at(0, …) values must not be in the future.
	recent := func(minutesAgo int) time.Time { return now.Add(-time.Duration(minutesAgo) * time.Minute) }
	switch scenario {
	case scenarioDegraded:
		incidents = append(incidents, StatusPageIncident{
			ID: "i-active", Title: "Slow GraphQL responses", State: "investigating",
			PublishedAt: recent(17), AffectedComponents: []string{"GraphQL gateway"},
			Summary: "Queries through the GraphQL gateway take several seconds. Requests still succeed.",
			Updates: []StatusPageIncidentUpdate{{Message: "We traced the slowdown to a cache node and are replacing it.", CreatedAt: recent(3)}},
		})
	case scenarioOutage:
		incidents = append(incidents, StatusPageIncident{
			ID: "i-active", Title: "Load balancer outage in eu-west", State: "identified",
			PublishedAt: recent(36), AffectedComponents: []string{"Load balancer", "GraphQL gateway"},
			Summary: "Requests through the eu-west load balancer are failing.",
			Updates: []StatusPageIncidentUpdate{
				{Message: "The rollback is 60% complete. Error rates are falling.", CreatedAt: recent(2)},
				{Message: "A faulty configuration push is the cause. We are rolling it back.", CreatedAt: recent(24)},
			},
		})
	}

	windows := []StatusPageMaintenanceWindow{{
		Title: "Weekly database patching", Description: "Primary Postgres fails over to its replica. Writes pause for under a minute.",
		StartsAt: at(-3, 16, 0), EndsAt: at(-3, 17, 0), AffectedMonitors: []string{"Primary Postgres"},
		Repeating: true, NextStartsAt: []time.Time{at(-10, 16, 0)},
	}}
	if scenario == scenarioMaintenance {
		windows = []StatusPageMaintenanceWindow{{
			Title: "Primary Postgres upgrade", Description: "Writes may pause briefly while the replica is promoted.",
			StartsAt: now.Add(-20 * time.Minute), EndsAt: now.Add(70 * time.Minute), IsActive: true,
			AffectedMonitors: []string{"Primary Postgres"},
		}}
	}
	if scenario == scenarioEmpty || scenario == scenarioSingle {
		windows = nil
	}

	primary := "#ff5a24"
	desc := "Live availability for Example Cloud."
	return &StatusPageData{
		ID: "page-classic", Slug: "example", Title: "Example Cloud status", Description: &desc,
		PrimaryColor: &primary, DefaultTheme: "dark", AllowThemeToggle: true, ShowFooter: true,
		ShowGlobalUptime: true, ShowMonitorUptime: true, ShowMonitorURL: true,
		Sections: sections, Monitors: monitors, Incidents: incidents, MaintenanceWindows: windows,
		Template: statustemplate.BuiltinClassic, SubscribeFormEnabled: true,
		CanonicalURL: "https://status.example.com",
	}
}

package statuspage

import (
	"math"
	"strings"
	"time"
)

// summaryVersion is the /summary.json contract version. Fields are only ever
// added; a removal or a changed meaning bumps it.
const summaryVersion = 1

// pageSummary is the documented /public/status/{slug}/summary.json contract
// (website/lib/docs/pages/status-pages.ts, "Summary JSON"), which embed.js and
// the badges read too. It is built from the same render view as the page, at
// the same time, and cached with it, so the summary can never disagree with
// what the page shows.
type pageSummary struct {
	Version     int                  `json:"version"`
	Page        summaryPage          `json:"page"`
	Status      summaryStatus        `json:"status"`
	Uptime      summaryUptime        `json:"uptime"`
	Components  []summaryComponent   `json:"components"`
	Incidents   []summaryIncident    `json:"incidents"`
	Maintenance []summaryMaintenance `json:"maintenance"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

type summaryPage struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	// URL is the page's canonical URL; omitted when the deployment does not
	// configure its public origin.
	URL string `json:"url,omitempty"`
}

// summaryStatus is the page header: indicator is operational, issues or
// maintenance (the same rule as the page's overall badge), description its
// label, and counts the components per status.
type summaryStatus struct {
	Indicator   string        `json:"indicator"`
	Description string        `json:"description"`
	Counts      summaryCounts `json:"counts"`
}

type summaryCounts struct {
	Total       int `json:"total"`
	Operational int `json:"operational"`
	Degraded    int `json:"degraded"`
	Down        int `json:"down"`
	Maintenance int `json:"maintenance"`
	Unknown     int `json:"unknown"`
}

// summaryUptime is availability in percent per visitor range, rounded to two
// decimals; null when the range has no data.
type summaryUptime struct {
	H1  *float64 `json:"1h"`
	H24 *float64 `json:"24h"`
	D7  *float64 `json:"7d"`
	D30 *float64 `json:"30d"`
	D90 *float64 `json:"90d"`
}

func (u summaryUptime) forRange(rangeName string) (*float64, bool) {
	switch rangeName {
	case "1h":
		return u.H1, true
	case "24h":
		return u.H24, true
	case "7d":
		return u.D7, true
	case "30d":
		return u.D30, true
	case "90d":
		return u.D90, true
	default:
		return nil, false
	}
}

// summaryComponent is one rendered monitor. Status is operational, degraded,
// down, maintenance or unknown.
type summaryComponent struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Section string        `json:"section"`
	Status  string        `json:"status"`
	Uptime  summaryUptime `json:"uptime"`
}

// summaryIncident is a published incident that is not resolved yet.
type summaryIncident struct {
	ID                 string                 `json:"id"`
	Title              string                 `json:"title"`
	State              string                 `json:"state"`
	PublishedAt        time.Time              `json:"published_at"`
	AffectedComponents []string               `json:"affected_components"`
	LatestUpdate       *summaryIncidentUpdate `json:"latest_update,omitempty"`
}

type summaryIncidentUpdate struct {
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// summaryMaintenance is a window in progress or starting within 14 days.
type summaryMaintenance struct {
	Title              string    `json:"title"`
	StartsAt           time.Time `json:"starts_at"`
	EndsAt             time.Time `json:"ends_at"`
	InProgress         bool      `json:"in_progress"`
	AffectedComponents []string  `json:"affected_components"`
}

// componentStatusForTone maps a render tone to the summary's component status.
func componentStatusForTone(tone string) string {
	switch tone {
	case "ok":
		return "operational"
	case "warn":
		return "degraded"
	case "down":
		return "down"
	case "maint":
		return "maintenance"
	default:
		return "unknown"
	}
}

// uptimePercent rounds an averageBarPoints result for the contract; a
// negative value (no data) is null.
func uptimePercent(value float64) *float64 {
	if value < 0 {
		return nil
	}
	rounded := math.Round(value*100) / 100
	return &rounded
}

// monitorSummaryUptime computes a monitor's per-range availability exactly as
// buildStatusPageMonitorView does for its Uptime*Value strings
// (TestSummaryUptimeMatchesRenderedValues pins the pair).
func monitorSummaryUptime(m MonitorStatus) summaryUptime {
	return summaryUptime{
		H1:  uptimePercent(averageBarPoints(sliceToBarPoints(m.UptimeHistory1h))),
		H24: uptimePercent(averageBarPoints(sliceToBarPoints(m.UptimeHistory24h))),
		D7:  uptimePercent(averageBarPoints(sliceToBarPoints(trimDailyHistory(m.UptimeHistory7d, m.UptimeHistory30d, 7)))),
		D30: uptimePercent(averageBarPoints(sliceToBarPoints(trimDailyHistory(m.UptimeHistory30d, m.UptimeHistory90d, 30)))),
		D90: uptimePercent(averageBarPoints(sliceToBarPoints(trimDailyHistory(m.UptimeHistory90d, m.UptimeHistory365d, 90)))),
	}
}

// pageSummaryUptime is the page-level counterpart (the global uptime strip).
func pageSummaryUptime(data *StatusPageData) summaryUptime {
	return summaryUptime{
		H1:  uptimePercent(averageBarPoints(sliceToBarPoints(data.UptimeHistory1h))),
		H24: uptimePercent(averageBarPoints(sliceToBarPoints(data.UptimeHistory1))),
		D7:  uptimePercent(averageBarPoints(sliceToBarPoints(trimDailyHistory(data.UptimeHistory7, data.UptimeHistory30, 7)))),
		D30: uptimePercent(averageBarPoints(sliceToBarPoints(trimDailyHistory(data.UptimeHistory30, data.UptimeHistory90, 30)))),
		D90: uptimePercent(averageBarPoints(sliceToBarPoints(trimDailyHistory(data.UptimeHistory90, data.UptimeHistory365, 90)))),
	}
}

// buildPageSummary derives the summary from the page data through the same
// render view the HTML uses.
func buildPageSummary(data *StatusPageData, now time.Time) *pageSummary {
	view := buildStatusPageRenderView(data, false)

	byID := make(map[string]MonitorStatus)
	for _, section := range data.Sections {
		for _, m := range section.Monitors {
			byID[m.ID] = m
		}
	}
	for _, m := range data.Monitors {
		if _, ok := byID[m.ID]; !ok {
			byID[m.ID] = m
		}
	}

	summary := &pageSummary{
		Version: summaryVersion,
		Page: summaryPage{
			Slug:  data.Slug,
			Title: data.Title,
			URL:   view.CanonicalURL,
		},
		Status: summaryStatus{
			Indicator:   view.OverallStatus,
			Description: view.OverallStatusLabel,
			Counts: summaryCounts{
				Total:       view.MonitorCount,
				Operational: view.OperationalCount,
				Degraded:    view.DegradedCount,
				Down:        view.DownCount,
				Maintenance: view.MaintenanceCount,
				Unknown:     view.UnknownCount,
			},
		},
		Uptime:      pageSummaryUptime(data),
		Components:  make([]summaryComponent, 0, view.MonitorCount),
		Incidents:   make([]summaryIncident, 0),
		Maintenance: make([]summaryMaintenance, 0, len(data.MaintenanceWindows)),
		UpdatedAt:   now.UTC().Truncate(time.Second),
	}

	for _, section := range view.Sections {
		for _, m := range section.Monitors {
			summary.Components = append(summary.Components, summaryComponent{
				ID:      m.ID,
				Name:    m.Name,
				Section: section.Title,
				Status:  componentStatusForTone(m.ToneClass),
				Uptime:  monitorSummaryUptime(byID[m.ID]),
			})
		}
	}

	for _, incident := range data.Incidents {
		if strings.TrimSpace(incident.State) == "resolved" {
			continue
		}
		item := summaryIncident{
			ID:                 incident.ID,
			Title:              strings.TrimSpace(incident.Title),
			State:              strings.TrimSpace(incident.State),
			PublishedAt:        incident.PublishedAt.UTC(),
			AffectedComponents: nonNilStrings(incident.AffectedComponents),
		}
		if len(incident.Updates) > 0 {
			item.LatestUpdate = &summaryIncidentUpdate{
				Message:   incident.Updates[0].Message,
				CreatedAt: incident.Updates[0].CreatedAt.UTC(),
			}
		}
		summary.Incidents = append(summary.Incidents, item)
	}

	for _, window := range data.MaintenanceWindows {
		summary.Maintenance = append(summary.Maintenance, summaryMaintenance{
			Title:              strings.TrimSpace(window.Title),
			StartsAt:           window.StartsAt.UTC(),
			EndsAt:             window.EndsAt.UTC(),
			InProgress:         window.IsActive,
			AffectedComponents: nonNilStrings(window.AffectedMonitors),
		})
	}

	return summary
}

// component returns the rendered component with the given monitor ID.
func (s *pageSummary) component(id string) (summaryComponent, bool) {
	for _, c := range s.Components {
		if c.ID == id {
			return c, true
		}
	}
	return summaryComponent{}, false
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

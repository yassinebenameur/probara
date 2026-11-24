package statuspage

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// summarySchemaVersion is the summary.json contract version. Fields are only
// ever added; removing one or changing its meaning bumps the version.
const summarySchemaVersion = 1

// pageSummary is the documented GET /public/status/{slug}/summary.json
// contract (website/lib/docs/pages/status-pages.ts, "Summary JSON"), pinned
// by testdata/summary_v1.golden.json. It is built from the same data and
// render view as the HTML, in the same build, and cached with it, so it can
// never disagree with what the page shows; the badges read it too.
type pageSummary struct {
	SchemaVersion int                  `json:"schema_version"`
	Page          summaryPage          `json:"page"`
	Status        summaryStatus        `json:"status"`
	Uptime        *summaryUptime       `json:"uptime"`
	Sections      []summarySection     `json:"sections"`
	Incidents     []summaryIncident    `json:"incidents"`
	Maintenance   []summaryMaintenance `json:"maintenance"`
	GeneratedAt   time.Time            `json:"generated_at"`

	// componentUptimeHidden records show_monitor_uptime being off: every
	// component's uptime is then null, and the uptime badges refuse rather
	// than read that null as "no data". A public, cross-origin document
	// must not publish availability the page owner chose to hide.
	componentUptimeHidden bool
}

type summaryPage struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	// URL is the page's canonical URL; omitted when the deployment does not
	// configure its public origin.
	URL string `json:"url,omitempty"`
}

// summaryStatus is the page header. Indicator is operational, issues or
// maintenance (the page's own overall rule), Description its headline, and
// Counts the components per status.
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
// decimals, null for a range without data. The object itself is null where
// the page does not show uptime.
type summaryUptime struct {
	H1  *float64 `json:"1h"`
	H24 *float64 `json:"24h"`
	D7  *float64 `json:"7d"`
	D30 *float64 `json:"30d"`
	D90 *float64 `json:"90d"`
}

func (u *summaryUptime) forRange(rangeName string) *float64 {
	if u == nil {
		return nil
	}
	switch rangeName {
	case "1h":
		return u.H1
	case "24h":
		return u.H24
	case "7d":
		return u.D7
	case "30d":
		return u.D30
	case "90d":
		return u.D90
	default:
		return nil
	}
}

// summarySection is one rendered section with its worst component status.
type summarySection struct {
	Title      string             `json:"title"`
	Status     string             `json:"status"`
	Components []summaryComponent `json:"components"`
}

// summaryComponent is one monitor the page renders, under its public name.
// Status is operational, degraded, down, maintenance or unknown.
type summaryComponent struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Status string         `json:"status"`
	Uptime *summaryUptime `json:"uptime"`
}

// summaryIncident is a published incident that is not resolved.
type summaryIncident struct {
	ID                 string                 `json:"id"`
	Title              string                 `json:"title"`
	State              string                 `json:"state"`
	PublishedAt        time.Time              `json:"published_at"`
	AffectedComponents []string               `json:"affected_components"`
	LatestUpdate       *summaryIncidentUpdate `json:"latest_update"`
}

type summaryIncidentUpdate struct {
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// summaryMaintenance is a window the page lists: in progress, or starting
// within the page's 14-day look-ahead, at that occurrence.
type summaryMaintenance struct {
	Title              string      `json:"title"`
	StartsAt           time.Time   `json:"starts_at"`
	EndsAt             time.Time   `json:"ends_at"`
	InProgress         bool        `json:"in_progress"`
	Repeating          bool        `json:"repeating"`
	NextStartsAt       []time.Time `json:"next_starts_at"`
	AffectedComponents []string    `json:"affected_components"`
}

// summaryStatusForTone maps a render tone to the contract's status words.
func summaryStatusForTone(tone string) string {
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

// uptimePercent rounds an averageBarPoints result for the contract exactly
// as uptimeValue formats it for the page (%.2f), so 99.125 is 99.12 in both
// places; a negative value (no data) is null.
func uptimePercent(value float64) *float64 {
	if value < 0 {
		return nil
	}
	rounded, err := strconv.ParseFloat(strconv.FormatFloat(value, 'f', 2, 64), 64)
	if err != nil {
		return nil
	}
	return &rounded
}

// monitorSummaryUptime computes a monitor's per-range availability exactly
// as buildStatusPageMonitorView computes its Uptime*Value strings
// (TestSummaryUptimeMatchesRenderedValues pins the pair).
func monitorSummaryUptime(m MonitorStatus) *summaryUptime {
	return &summaryUptime{
		H1:  uptimePercent(averageBarPoints(sliceToBarPoints(m.UptimeHistory1h))),
		H24: uptimePercent(averageBarPoints(sliceToBarPoints(m.UptimeHistory24h))),
		D7:  uptimePercent(averageBarPoints(sliceToBarPoints(trimDailyHistory(m.UptimeHistory7d, m.UptimeHistory30d, 7)))),
		D30: uptimePercent(averageBarPoints(sliceToBarPoints(trimDailyHistory(m.UptimeHistory30d, m.UptimeHistory90d, 30)))),
		D90: uptimePercent(averageBarPoints(sliceToBarPoints(trimDailyHistory(m.UptimeHistory90d, m.UptimeHistory365d, 90)))),
	}
}

// pageSummaryUptime is the page-level counterpart (the global uptime strip).
func pageSummaryUptime(data *StatusPageData) *summaryUptime {
	return &summaryUptime{
		H1:  uptimePercent(averageBarPoints(sliceToBarPoints(data.UptimeHistory1h))),
		H24: uptimePercent(averageBarPoints(sliceToBarPoints(data.UptimeHistory1))),
		D7:  uptimePercent(averageBarPoints(sliceToBarPoints(trimDailyHistory(data.UptimeHistory7, data.UptimeHistory30, 7)))),
		D30: uptimePercent(averageBarPoints(sliceToBarPoints(trimDailyHistory(data.UptimeHistory30, data.UptimeHistory90, 30)))),
		D90: uptimePercent(averageBarPoints(sliceToBarPoints(trimDailyHistory(data.UptimeHistory90, data.UptimeHistory365, 90)))),
	}
}

// buildPageSummary derives the summary from the page data through the same
// render view the HTML uses. Uptime follows the page's settings like the
// built-in template's {{if .ShowGlobalUptime}} / {{if $.ShowMonitorUptime}}
// blocks: a hidden figure is null here, never computed.
func buildPageSummary(data *StatusPageData, now time.Time) *pageSummary {
	return buildPageSummaryFromView(data, buildStatusPageRenderView(data), now)
}

// buildPageSummaryFromView is buildPageSummary for the view the page was
// rendered from.
func buildPageSummaryFromView(data *StatusPageData, view statusPageRenderView, now time.Time) *pageSummary {
	byID := make(map[string]MonitorStatus, len(data.Monitors))
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
		SchemaVersion: summarySchemaVersion,
		Page: summaryPage{
			Slug:  data.Slug,
			Title: strings.TrimSpace(data.Title),
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
		Sections:    make([]summarySection, 0, len(view.Sections)),
		Incidents:   make([]summaryIncident, 0),
		Maintenance: make([]summaryMaintenance, 0, len(data.MaintenanceWindows)),
		GeneratedAt: now.UTC().Truncate(time.Second),

		componentUptimeHidden: !data.ShowMonitorUptime,
	}
	if data.ShowGlobalUptime {
		summary.Uptime = pageSummaryUptime(data)
	}

	for _, section := range view.Sections {
		item := summarySection{
			Title:      section.Title,
			Status:     summaryStatusForTone(section.ToneClass),
			Components: make([]summaryComponent, 0, len(section.Monitors)),
		}
		for _, m := range section.Monitors {
			component := summaryComponent{
				ID:     m.ID,
				Name:   m.Name,
				Status: summaryStatusForTone(m.ToneClass),
			}
			if data.ShowMonitorUptime {
				component.Uptime = monitorSummaryUptime(byID[m.ID])
			}
			item.Components = append(item.Components, component)
		}
		summary.Sections = append(summary.Sections, item)
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
		next := make([]time.Time, 0, len(window.NextStartsAt))
		for _, t := range window.NextStartsAt {
			next = append(next, t.UTC())
		}
		summary.Maintenance = append(summary.Maintenance, summaryMaintenance{
			Title:              strings.TrimSpace(window.Title),
			StartsAt:           window.StartsAt.UTC(),
			EndsAt:             window.EndsAt.UTC(),
			InProgress:         window.IsActive,
			Repeating:          window.Repeating,
			NextStartsAt:       next,
			AffectedComponents: nonNilStrings(window.AffectedMonitors),
		})
	}

	return summary
}

// encodeSummary returns the summary.json body and its ETag. The validator
// leaves generated_at out: the render cache rebuilds every TTL, and a
// polling client must get a 304 while the page data is unchanged.
func encodeSummary(summary *pageSummary) ([]byte, string, error) {
	body, err := json.Marshal(summary)
	if err != nil {
		return nil, "", err
	}
	stable := *summary
	stable.GeneratedAt = time.Time{}
	validator, err := json.Marshal(&stable)
	if err != nil {
		return nil, "", err
	}
	return append(body, '\n'), computeETag(string(validator)), nil
}

// component returns the rendered component with the given monitor ID. Only
// monitors the page renders are found; nothing else is ever looked up.
func (s *pageSummary) component(id string) (summaryComponent, bool) {
	for _, section := range s.Sections {
		for _, c := range section.Components {
			if c.ID == id {
				return c, true
			}
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

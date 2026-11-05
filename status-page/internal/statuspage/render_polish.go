package statuspage

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// latencyGrid is one visitor range's response-time chart: cells of a fixed
// step ending at the current (partial) align unit, so a gap in the data is a
// gap in the line rather than a squeezed x-axis.
type latencyGrid struct {
	step  time.Duration
	cells int
	align time.Duration
}

// latencyGrids holds one entry per visitor range (the template's RANGES).
// Cell counts stay small on purpose: a page with hundreds of components
// carries every series inline.
var latencyGrids = map[string]latencyGrid{
	"1h":  {step: 2 * time.Minute, cells: 30, align: time.Minute},
	"24h": {step: time.Hour, cells: 24, align: time.Hour},
	"7d":  {step: 24 * time.Hour, cells: 7, align: 24 * time.Hour},
	"30d": {step: 24 * time.Hour, cells: 30, align: 24 * time.Hour},
	"90d": {step: 48 * time.Hour, cells: 45, align: 24 * time.Hour},
}

// latencyCells resamples points onto grid: each cell holds the mean of the
// points inside it, nil when none fall there. Points outside the window are
// dropped, which is also how the 7d chart takes the last week of the 30d
// daily series.
func latencyCells(points []LatencyPoint, grid latencyGrid, now time.Time) []*int {
	end := now.UTC().Truncate(grid.align).Add(grid.align)
	start := end.Add(-time.Duration(grid.cells) * grid.step)
	sums := make([]float64, grid.cells)
	counts := make([]int, grid.cells)
	for _, p := range points {
		at := p.Timestamp.UTC()
		if at.Before(start) || !at.Before(end) {
			continue
		}
		i := int(at.Sub(start) / grid.step)
		sums[i] += float64(p.LatencyMS)
		counts[i]++
	}
	cells := make([]*int, grid.cells)
	for i := range cells {
		if counts[i] > 0 {
			v := int(sums[i]/float64(counts[i]) + 0.5)
			cells[i] = &v
		}
	}
	return cells
}

// firstNonEmpty returns the first series with data, mirroring
// trimDailyHistory's fallback from a short range to the next longer one.
func firstNonEmpty(series ...[]LatencyPoint) []LatencyPoint {
	for _, s := range series {
		if len(s) > 0 {
			return s
		}
	}
	return nil
}

// applyMonitorLatency fills the monitor's response-time series. 1h and 24h
// come from the short-range queries, 7d and longer from the daily rollups
// (the analytics 30d/90d series).
func applyMonitorLatency(view *statusPageMonitorView, monitor MonitorStatus, now time.Time) {
	series := map[string][]LatencyPoint{
		"1h":  monitor.LatencyHistory1h,
		"24h": monitor.LatencyHistory,
		"7d":  firstNonEmpty(monitor.LatencyHistory30d, monitor.LatencyHistory90d),
		"30d": firstNonEmpty(monitor.LatencyHistory30d, monitor.LatencyHistory90d),
		"90d": firstNonEmpty(monitor.LatencyHistory90d, monitor.LatencyHistory365d),
	}
	out := map[string]string{}
	for rangeName, grid := range latencyGrids {
		cells := latencyCells(series[rangeName], grid, now)
		for _, c := range cells {
			if c != nil {
				view.HasLatency = true
				break
			}
		}
		out[rangeName] = mustJSON(cells)
	}
	view.Latency1hJSON = out["1h"]
	view.Latency24hJSON = out["24h"]
	view.Latency7dJSON = out["7d"]
	view.Latency30dJSON = out["30d"]
	view.Latency90dJSON = out["90d"]
}

// maxMetaDescriptionRunes caps the meta description when it falls back to the
// page description, which has no length limit of its own.
const maxMetaDescriptionRunes = 300

// applyPageMeta sets the page-level fields the head tags (description, Open
// Graph, Twitter card, canonical link, feed discovery) and the response-time
// chart read.
func applyPageMeta(view *statusPageRenderView, data *StatusPageData) {
	view.ShowLatencyCharts = data.ShowLatencyCharts

	description := strings.TrimSpace(data.SEODescription)
	if description == "" {
		description = strings.TrimSpace(derefString(data.Description))
	}
	if description == "" {
		description = fmt.Sprintf("Live status, uptime and incident history for %s.", strings.TrimSpace(data.Title))
	}
	view.MetaDescription = truncateRunes(description, maxMetaDescriptionRunes)

	view.CanonicalURL = strings.TrimSpace(data.CanonicalURL)
	if view.CanonicalURL != "" {
		view.FeedURL = view.CanonicalURL + "/feed.atom"
	} else {
		view.FeedURL = "/public/status/" + data.Slug + "/feed.atom"
	}

	// og:image must be absolute; a relative or data: logo is left out.
	if logo := strings.TrimSpace(derefString(data.LogoURL)); strings.HasPrefix(logo, "https://") || strings.HasPrefix(logo, "http://") {
		view.OGImageURL = logo
	}
}

// publicPageURL is a page's absolute URL under the deployment's public origin
// (STATUS_PAGE_BASE_URL), or "" when that origin is not configured. It is the
// one place that spells the public URL shape: the canonical link, og:url, the
// feed's links and push deep links all go through it.
func publicPageURL(baseURL, slug string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" || slug == "" {
		return ""
	}
	return base + "/public/status/" + slug
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}

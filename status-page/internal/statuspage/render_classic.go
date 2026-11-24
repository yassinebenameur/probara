// View fields added with the classic built-in template (classic.gohtml).
// They are derived from data the render already loads: no queries. Every
// template receives them; the default template does not use them, so its
// output is unchanged.

package statuspage

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// incidentHistoryDays is how many UTC days, today included, the incident
// history covers.
const incidentHistoryDays = 7

// defaultAccentColor is Probara's orange, the classic template's accent when
// the page sets no primary colour.
const defaultAccentColor = "#ff5a24"

// Accent inks: the classic template's dark text on a light accent, white on
// a dark one, whichever reads better.
const (
	accentInkDark  = "#1a0b04"
	accentInkLight = "#ffffff"
)

// statusPageBarView is one bar of a server-rendered uptime strip. Tone is
// ok, warn, down or none (no data); Tip is the slot and its uptime.
type statusPageBarView struct {
	Tone string
	Tip  string
}

// applyClassicView fills the classic fields of view from data at now.
func applyClassicView(view *statusPageRenderView, data *StatusPageData, now time.Time) {
	now = now.UTC()
	view.RenderedAtISO = now.Format(time.RFC3339)
	view.BannerTitle, view.BannerTone, view.BannerSummary = classicBanner(*view)
	view.AccentColor, view.AccentInk = accentColors(data.PrimaryColor)

	today := now.Format("2006-01-02")
	historyStart := utcDay(now).AddDate(0, 0, -(incidentHistoryDays - 1))
	byDay := map[string][]statusPageIncidentView{}

	active := make([]StatusPageIncident, 0)
	past := make([]StatusPageIncident, 0)
	for _, incident := range data.Incidents {
		if strings.TrimSpace(incident.State) == "resolved" {
			past = append(past, incident)
		} else {
			active = append(active, incident)
		}
	}
	// Newest first: active by publication, past by resolution.
	sort.SliceStable(active, func(i, j int) bool { return active[i].PublishedAt.After(active[j].PublishedAt) })
	sort.SliceStable(past, func(i, j int) bool { return incidentEnd(past[i]).After(incidentEnd(past[j])) })

	for _, incident := range active {
		view.ActiveIncidents = append(view.ActiveIncidents, classicIncidentView(incident, today))
	}
	for _, incident := range past {
		day, ok := historyDay(incident, historyStart, now)
		ref := today
		if ok {
			ref = day
		}
		iv := classicIncidentView(incident, ref)
		view.PastIncidents = append(view.PastIncidents, iv)
		if ok {
			byDay[day] = append(byDay[day], iv)
		}
	}
	for i := 0; i < incidentHistoryDays; i++ {
		day := utcDay(now).AddDate(0, 0, -i)
		key := day.Format("2006-01-02")
		view.IncidentHistory = append(view.IncidentHistory, statusPageIncidentDayView{
			DateISO:   key,
			DateText:  day.Format("Jan 2, 2006"),
			Incidents: byDay[key],
		})
	}
}

// classicIncidentView is buildStatusPageIncidentView with its update times
// written relative to refDay (YYYY-MM-DD, the UTC day it is shown under).
func classicIncidentView(incident StatusPageIncident, refDay string) statusPageIncidentView {
	iv := buildStatusPageIncidentView(incident)
	iv.Updates = incidentUpdateViews(incident, iv.StateLabel, refDay)
	return iv
}

// historyDay is the UTC day a resolved incident is listed under: the day it
// was published, or, when that is before the window, the day it was
// resolved. ok is false when neither falls in [start, now].
func historyDay(incident StatusPageIncident, start, now time.Time) (string, bool) {
	inWindow := func(t time.Time) bool { return !t.IsZero() && !t.Before(start) && !t.After(now) }
	if published := incident.PublishedAt.UTC(); inWindow(published) {
		return published.Format("2006-01-02"), true
	}
	if incident.ResolvedAt != nil {
		if resolved := incident.ResolvedAt.UTC(); inWindow(resolved) {
			return resolved.Format("2006-01-02"), true
		}
	}
	return "", false
}

// incidentEnd orders past incidents: resolution time, else publication.
func incidentEnd(incident StatusPageIncident) time.Time {
	if incident.ResolvedAt != nil {
		return *incident.ResolvedAt
	}
	return incident.PublishedAt
}

func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// incidentUpdateViews lists an incident's public updates newest first, then
// its summary as the original post. Only the newest entry carries the
// current state: the updates themselves do not record one.
func incidentUpdateViews(incident StatusPageIncident, stateLabel, refDay string) []statusPageIncidentUpdateView {
	out := make([]statusPageIncidentUpdateView, 0, len(incident.Updates)+1)
	add := func(at time.Time, message string) {
		label := "Update"
		if len(out) == 0 {
			label = stateLabel
		}
		out = append(out, incidentUpdateView(at, refDay, label, message))
	}
	for _, update := range incident.Updates {
		if message := strings.TrimSpace(update.Message); message != "" {
			add(update.CreatedAt, message)
		}
	}
	if summary := strings.TrimSpace(incident.Summary); summary != "" {
		add(incident.PublishedAt, summary)
	}
	if len(out) == 0 {
		add(incident.PublishedAt, "No public summary is available yet.")
	}
	return out
}

func incidentUpdateView(at time.Time, refDay, label, message string) statusPageIncidentUpdateView {
	v := statusPageIncidentUpdateView{DayISO: refDay, StateLabel: label, Message: message}
	if at.IsZero() {
		return v
	}
	at = at.UTC()
	v.AtISO = at.Format(time.RFC3339)
	v.TimeText = at.Format("15:04") + " UTC"
	if at.Format("2006-01-02") != refDay {
		v.TimeText = at.Format("Jan 2, 15:04") + " UTC"
	}
	return v
}

// affectsText words an affected list for the classic template.
func affectsText(names []string) string {
	clean := make([]string, 0, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			clean = append(clean, name)
		}
	}
	if len(clean) == 0 {
		return "Affects all services"
	}
	return "Affects " + strings.Join(clean, ", ")
}

// classicBanner derives the banner from the counts the default template's
// header uses (it keeps its own wording). More than half of the services
// down is a major outage; fewer a partial one.
func classicBanner(view statusPageRenderView) (title, tone, summary string) {
	affected := view.DownCount + view.DegradedCount + view.UnknownCount
	switch {
	case view.DownCount > 0:
		title, tone = "Partial outage", "down"
		if view.DownCount*2 > view.MonitorCount {
			title = "Major outage"
		}
	case view.DegradedCount > 0:
		title, tone = "Degraded performance", "warn"
	case view.UnknownCount > 0:
		return "Some services are not reporting", "unknown", servicesSentence(view.UnknownCount, view.MonitorCount, "without recent data")
	case view.MaintenanceCount > 0:
		return "Maintenance in progress", "maint", servicesSentence(view.MaintenanceCount, view.MonitorCount, "under planned maintenance")
	default:
		return "All systems operational", "ok", ""
	}
	return title, tone, servicesSentence(affected, view.MonitorCount, "affected")
}

// servicesSentence is "1 of 9 services is affected." / "2 of 9 services are
// affected.".
func servicesSentence(n, total int, what string) string {
	verb := "are"
	if n == 1 {
		verb = "is"
	}
	noun := "services"
	if total == 1 {
		noun = "service"
	}
	return fmt.Sprintf("%d of %d %s %s %s.", n, total, noun, verb, what)
}

// accentColors is the page's primary colour (#rrggbb) or the default
// accent, and whichever ink contrasts with it more.
func accentColors(primary *string) (color, ink string) {
	color = defaultAccentColor
	if primary != nil {
		if c := strings.ToLower(strings.TrimSpace(*primary)); parseHexColor(c) != nil {
			color = c
		}
	}
	rgb := parseHexColor(color)
	bg := srgbLuminance(rgb)
	if luminanceContrast(bg, srgbLuminance(parseHexColor(accentInkDark))) >= luminanceContrast(bg, 1) {
		return color, accentInkDark
	}
	return color, accentInkLight
}

func parseHexColor(c string) []float64 {
	if len(c) != 7 || c[0] != '#' {
		return nil
	}
	out := make([]float64, 3)
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseUint(c[1+2*i:3+2*i], 16, 8)
		if err != nil {
			return nil
		}
		out[i] = float64(v) / 255
	}
	return out
}

// srgbLuminance is the WCAG 2 relative luminance of an sRGB colour.
func srgbLuminance(rgb []float64) float64 {
	lin := func(v float64) float64 {
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(rgb[0]) + 0.7152*lin(rgb[1]) + 0.0722*lin(rgb[2])
}

func luminanceContrast(a, b float64) float64 {
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

// monitorStatusWord is the classic template's status wording.
func monitorStatusWord(status string) string {
	switch status {
	case "up":
		return "Operational"
	case "degraded":
		return "Degraded"
	case "down", "error":
		return "Outage"
	case "maintenance":
		return "Maintenance"
	default:
		return "Unknown"
	}
}

// Native strip resolutions: one bar per bucket the data has (hourly for
// 24h, daily beyond), so every bar's tooltip names exactly one slot.
const (
	bars24hCells = 24
	bars7dCells  = 7
	bars30dCells = 30
	bars90dCells = 90
)

// nativeBars keeps the newest cells points, pads the oldest end with no-data
// bars, and rewrites each label for a tooltip.
func nativeBars(points []barPoint, cells int, label func(string) string) []barPoint {
	if len(points) > cells {
		points = points[len(points)-cells:]
	}
	out := make([]barPoint, 0, cells)
	for i := len(points); i < cells; i++ {
		out = append(out, barPoint{Uptime: -1})
	}
	for _, p := range points {
		out = append(out, barPoint{Label: label(p.Label), Uptime: p.Uptime})
	}
	return out
}

// hourSlotLabel turns an hourly bucket label ("15:00", UTC) into its slot.
func hourSlotLabel(raw string) string {
	t, err := time.Parse("15:04", strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return t.Format("15:04") + "–" + t.Add(time.Hour).Format("15:04") + " UTC"
}

// daySlotLabel turns a daily bucket label ("2006-01-02") into "Jan 2, 2006".
func daySlotLabel(raw string) string {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return t.Format("Jan 2, 2006")
}

// barTone matches the default template's strip thresholds.
func barTone(uptime float64) string {
	switch {
	case uptime < 0:
		return "none"
	case uptime >= 99:
		return "ok"
	case uptime >= 95:
		return "warn"
	default:
		return "down"
	}
}

// barTip is a bar's tooltip, the same text the classic script builds.
func barTip(p barPoint) string {
	value := "No data"
	if p.Uptime >= 0 {
		value = fmt.Sprintf("%.2f%%", p.Uptime)
	}
	if p.Label == "" {
		return value
	}
	return p.Label + " · " + value
}

// stripCells is the classic strip density: every range draws about this
// many cells, so the bars keep one rhythm whatever the range.
const stripCells = 90

// barViews draws native points at the classic density: each slot repeated
// stripCells/len(points) times (the classic script does the same), so a
// slot's cells share its tooltip and the strip never pretends to a finer
// resolution than its data.
func barViews(points []barPoint) []statusPageBarView {
	if len(points) == 0 {
		return nil
	}
	repeat := stripCells / len(points)
	if repeat < 1 {
		repeat = 1
	}
	out := make([]statusPageBarView, 0, len(points)*repeat)
	for _, p := range points {
		bar := statusPageBarView{Tone: barTone(p.Uptime), Tip: barTip(p)}
		for i := 0; i < repeat; i++ {
			out = append(out, bar)
		}
	}
	return out
}

// applyClassicBars fills a monitor view's native-resolution strips from the
// same raw series the default strips resample.
func applyClassicBars(view *statusPageMonitorView, raw24, raw7, raw30, raw90 []barPoint) {
	bars24 := nativeBars(raw24, bars24hCells, hourSlotLabel)
	view.Bars = barViews(bars24)
	view.Bars24hJSON = mustJSON(bars24)
	view.Bars7dJSON = mustJSON(nativeBars(raw7, bars7dCells, daySlotLabel))
	view.Bars30dJSON = mustJSON(nativeBars(raw30, bars30dCells, daySlotLabel))
	view.Bars90dJSON = mustJSON(nativeBars(raw90, bars90dCells, daySlotLabel))
}

package statuspage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"sort"
	"strings"
	"time"
)

type statusPageRenderView struct {
	ID                  string
	Slug                string
	Title               string
	Description         string
	HasDescription      bool
	LogoURL             *string
	HasLogo             bool
	PrimaryColor        string
	SecondaryColor      string
	DefaultTheme        string
	AllowThemeToggle    bool
	ShowFooter          bool
	FooterText          string
	ShowGlobalUptime    bool
	ShowMonitorUptime   bool
	ShowToolbar         bool
	ShowSearchControls  bool
	ShowRangeSelector   bool
	ShowLayoutControl   bool
	DefaultRange        string
	GlobalStripCells    int
	MonitorStripCells   int
	OverallStatus       string
	OverallTone         string
	OverallStatusLabel  string
	OverallSummary      string
	OperationalCount    int
	DegradedCount       int
	DownCount           int
	UnknownCount        int
	IssueCount          int
	MonitorCount        int
	MaintenanceCount    int
	GlobalUptimePercent string
	GlobalUptime24JSON  string
	GlobalUptime7JSON   string
	GlobalUptime30JSON  string
	GlobalUptime90JSON  string
	GlobalUptime24Value string
	GlobalUptime7Value  string
	GlobalUptime30Value string
	GlobalUptime90Value string
	Sections            []statusPageSectionView
	Monitors            []statusPageMonitorView
	Incidents           []statusPageIncidentView
}

const (
	defaultStatusPageRange      = "24h"
	globalUptimeStripCells      = 48
	globalUptime7dStripCells    = 70
	monitorUptimeStripCells     = 36
	longRangeMonitorStripCells  = 60
	longRangeExpandedStripCells = 90
)

type statusPageIncidentView struct {
	Title                  string
	State                  string
	StateLabel             string
	ToneClass              string
	Summary                string
	AffectedComponentsText string
	LatestUpdate           string
	ResolvedAtLabel        string
}

type statusPageSectionView struct {
	ID               string
	Title            string
	MonitorCount     int
	OperationalCount int
	DegradedCount    int
	DownCount        int
	MaintenanceCount int
	UnknownCount     int
	ToneClass        string
	StatusSummary    string
	Monitors         []statusPageMonitorView
}

type statusPageMonitorView struct {
	ID                     string
	Name                   string
	URL                    string
	Type                   string
	TypeLabel              string
	Status                 string
	ToneClass              string
	StatusLabel            string
	StatusRank             int
	SearchText             string
	SummaryMetric          string
	SummaryCaption         string
	LastCheckAgo           string
	LastCheckUnix          int64
	LatencyText            string
	LatencySort            int
	UptimeText             string
	Uptime24Value          string
	Uptime7Value           string
	Uptime30Value          string
	Uptime90Value          string
	UptimeSort             float64
	TagsText               string
	Tags                   []string
	MonitorDetailLine      string
	TLSDetail              string
	History1hJSON          string
	History24hJSON         string
	History7dJSON          string
	History30dJSON         string
	History90dJSON         string
	ExpandedHistory30dJSON string
	ExpandedHistory90dJSON string
	History1yJSON          string
}

func typeIcon(kind string) string {
	switch kind {
	case "http":
		return "globe"
	case "ping":
		return "wifi"
	case "dns":
		return "search"
	case "grpc":
		return "cpu"
	case "group":
		return "layers"
	case "agent":
		return "monitor"
	case "push":
		return "upload-cloud"
	case "sip":
		return "phone"
	case "synthetic_api":
		return "code"
	case "synthetic_browser":
		return "monitor-check"
	case "redis", "postgres", "mongodb", "rabbitmq":
		return "database"
	default:
		return "activity"
	}
}

func globalStripCellCount(rangeName string) int {
	switch rangeName {
	case "7d":
		return globalUptime7dStripCells
	case "30d", "90d":
		return longRangeExpandedStripCells
	default:
		return globalUptimeStripCells
	}
}

func monitorStripCellCount(rangeName string) int {
	switch rangeName {
	case "30d", "90d":
		return longRangeMonitorStripCells
	default:
		return monitorUptimeStripCells
	}
}

func expandedStripCellCount(rangeName string) int {
	switch rangeName {
	case "30d", "90d":
		return longRangeExpandedStripCells
	default:
		return monitorUptimeStripCells
	}
}

// publicStatusPageTmpl is parsed once at package init; the template source is
// large (~200KB), so re-parsing per request is wasteful. Execute is safe for
// concurrent use.
var publicStatusPageTmpl = template.Must(template.New("public_status_page").Funcs(template.FuncMap{
	"expandedStripCells": expandedStripCellCount,
	"globalStripCells":   globalStripCellCount,
	"join":               strings.Join,
	"monitorStripCells":  monitorStripCellCount,
	"typeIcon":           typeIcon,
}).Parse(publicStatusPageTemplate))

func renderPublicStatusPage(data *StatusPageData, apiEnabled bool) (string, error) {
	view := buildStatusPageRenderView(data, apiEnabled)

	var buf bytes.Buffer
	if err := publicStatusPageTmpl.Execute(&buf, view); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func buildStatusPageRenderView(data *StatusPageData, apiEnabled bool) statusPageRenderView {
	primary := "#06b6d4"
	if data.PrimaryColor != nil && strings.TrimSpace(*data.PrimaryColor) != "" {
		primary = strings.TrimSpace(*data.PrimaryColor)
	}
	secondary := "#22c55e"
	if data.SecondaryColor != nil && strings.TrimSpace(*data.SecondaryColor) != "" {
		secondary = strings.TrimSpace(*data.SecondaryColor)
	}

	view := statusPageRenderView{
		ID:                data.ID,
		Slug:              data.Slug,
		Title:             data.Title,
		Description:       strings.TrimSpace(derefString(data.Description)),
		HasDescription:    strings.TrimSpace(derefString(data.Description)) != "",
		LogoURL:           data.LogoURL,
		HasLogo:           data.HasLogo,
		PrimaryColor:      primary,
		SecondaryColor:    secondary,
		DefaultTheme:      normalizeTheme(data.DefaultTheme),
		AllowThemeToggle:  data.AllowThemeToggle,
		ShowFooter:        data.ShowFooter,
		FooterText:        strings.TrimSpace(derefString(data.CustomFooterText)),
		ShowGlobalUptime:  data.ShowGlobalUptime,
		ShowMonitorUptime: data.ShowMonitorUptime,
		DefaultRange:      defaultStatusPageRange,
		GlobalStripCells:  globalUptimeStripCells,
		MonitorStripCells: monitorUptimeStripCells,
		MonitorCount:      len(data.Monitors),
	}

	if view.FooterText == "" {
		view.FooterText = fmt.Sprintf("© %d %s", time.Now().UTC().Year(), data.Title)
	}

	sections := data.Sections
	if len(sections) == 0 && len(data.Monitors) > 0 {
		sections = []StatusPageSectionData{{
			Title:    "Services",
			Position: 0,
			Monitors: data.Monitors,
		}}
	}

	for _, section := range sections {
		renderSection := statusPageSectionView{
			ID:    section.ID,
			Title: strings.TrimSpace(section.Title),
		}
		if renderSection.Title == "" {
			renderSection.Title = "Services"
		}

		for _, monitor := range section.Monitors {
			renderMonitor := buildStatusPageMonitorView(monitor)
			renderSection.Monitors = append(renderSection.Monitors, renderMonitor)
			view.Monitors = append(view.Monitors, renderMonitor)
			renderSection.MonitorCount++
			switch renderMonitor.ToneClass {
			case "ok":
				view.OperationalCount++
				renderSection.OperationalCount++
			case "warn":
				view.DegradedCount++
				view.IssueCount++
				renderSection.DegradedCount++
			case "down":
				view.DownCount++
				view.IssueCount++
				renderSection.DownCount++
			case "maint":
				view.MaintenanceCount++
				view.IssueCount++
				renderSection.MaintenanceCount++
			default:
				view.UnknownCount++
				view.IssueCount++
				renderSection.UnknownCount++
			}
		}

		renderSection.ToneClass, renderSection.StatusSummary = sectionStatus(renderSection)
		view.Sections = append(view.Sections, renderSection)
	}

	for _, incident := range data.Incidents {
		view.Incidents = append(view.Incidents, buildStatusPageIncidentView(incident))
	}

	view.MonitorCount = len(view.Monitors)

	if view.IssueCount > 0 {
		view.OverallStatus = "issues"
		view.OverallTone = "warn"
		view.OverallStatusLabel = "Issues detected"
		view.OverallSummary = fmt.Sprintf("%d of %d services need attention", view.IssueCount, view.MonitorCount)
	} else {
		view.OverallStatus = "operational"
		view.OverallTone = "ok"
		view.OverallStatusLabel = "All systems operational"
		view.OverallSummary = fmt.Sprintf("%d monitored services are healthy", view.OperationalCount)
	}

	global24Raw := sliceToBarPoints(data.UptimeHistory1)
	global7Raw := sliceToBarPoints(trimDailyHistory(data.UptimeHistory7, data.UptimeHistory30, 7))
	global30Raw := sliceToBarPoints(trimDailyHistory(data.UptimeHistory30, data.UptimeHistory90, 30))
	global90Raw := sliceToBarPoints(trimDailyHistory(data.UptimeHistory90, data.UptimeHistory365, 90))
	global24 := fixedStripPoints(global24Raw, globalStripCellCount("24h"))
	global7 := fixedStripPoints(global7Raw, globalStripCellCount("7d"))
	global30 := fixedStripPoints(global30Raw, globalStripCellCount("30d"))
	global90 := fixedStripPoints(global90Raw, globalStripCellCount("90d"))
	view.GlobalUptime24JSON = mustJSON(global24)
	view.GlobalUptime7JSON = mustJSON(global7)
	view.GlobalUptime30JSON = mustJSON(global30)
	view.GlobalUptime90JSON = mustJSON(global90)
	view.GlobalUptime24Value = uptimeValue(averageBarPoints(global24Raw))
	view.GlobalUptime7Value = uptimeValue(averageBarPoints(global7Raw))
	view.GlobalUptime30Value = uptimeValue(averageBarPoints(global30Raw))
	view.GlobalUptime90Value = uptimeValue(averageBarPoints(global90Raw))
	view.GlobalUptimePercent = view.GlobalUptime24Value
	view.ShowSearchControls = view.MonitorCount > 1
	view.ShowRangeSelector = view.ShowGlobalUptime || view.ShowMonitorUptime
	view.ShowToolbar = view.ShowSearchControls
	view.ShowLayoutControl = true
	sort.Slice(view.Incidents, func(i, j int) bool {
		return incidentStateRank(view.Incidents[i].State) < incidentStateRank(view.Incidents[j].State)
	})

	return view
}

// sectionStatus rolls a section's monitor tones up to a single worst-status
// tone plus a short human summary shown next to the section title.
func sectionStatus(section statusPageSectionView) (string, string) {
	switch {
	case section.DownCount > 0:
		return "down", fmt.Sprintf("%d down", section.DownCount)
	case section.DegradedCount > 0:
		return "warn", fmt.Sprintf("%d degraded", section.DegradedCount)
	case section.UnknownCount > 0:
		return "unknown", fmt.Sprintf("%d unknown", section.UnknownCount)
	case section.MaintenanceCount > 0:
		return "maint", fmt.Sprintf("%d in maintenance", section.MaintenanceCount)
	default:
		return "ok", "Operational"
	}
}

func buildStatusPageIncidentView(incident StatusPageIncident) statusPageIncidentView {
	summary := strings.TrimSpace(incident.Summary)
	if summary == "" {
		summary = "No public summary is available yet."
	}

	state := strings.TrimSpace(incident.State)
	stateLabel := "Investigating"
	toneClass := "down"
	switch state {
	case "identified":
		stateLabel = "Identified"
	case "monitoring":
		stateLabel = "Monitoring"
		toneClass = "warn"
	case "resolved":
		stateLabel = "Resolved"
		toneClass = "ok"
	case "investigating":
		stateLabel = "Investigating"
	default:
		stateLabel = "Incident"
		toneClass = "unknown"
	}

	components := make([]string, 0, len(incident.AffectedComponents))
	for _, component := range incident.AffectedComponents {
		component = strings.TrimSpace(component)
		if component != "" {
			components = append(components, component)
		}
	}
	affectedComponentsText := "Affected services: platform-wide"
	if len(components) > 0 {
		affectedComponentsText = fmt.Sprintf("Affected services: %s", strings.Join(components, ", "))
	}

	latestUpdate := ""
	if len(incident.Updates) > 0 {
		latestUpdate = strings.TrimSpace(incident.Updates[0].Message)
	}

	resolvedAtLabel := ""
	if incident.ResolvedAt != nil {
		resolvedAtLabel = fmt.Sprintf("Resolved %s", relativeTime(*incident.ResolvedAt))
	}

	return statusPageIncidentView{
		Title:                  strings.TrimSpace(incident.Title),
		State:                  state,
		StateLabel:             stateLabel,
		ToneClass:              toneClass,
		Summary:                summary,
		AffectedComponentsText: affectedComponentsText,
		LatestUpdate:           latestUpdate,
		ResolvedAtLabel:        resolvedAtLabel,
	}
}

func incidentStateRank(state string) int {
	switch strings.TrimSpace(state) {
	case "investigating", "identified":
		return 0
	case "monitoring":
		return 1
	case "resolved":
		return 2
	default:
		return 3
	}
}

func buildStatusPageMonitorView(monitor MonitorStatus) statusPageMonitorView {
	lastCheckUnix := int64(0)
	lastCheckAgo := "No recent check"
	if monitor.LastCheckTime != nil {
		lastCheckUnix = monitor.LastCheckTime.Unix()
		lastCheckAgo = relativeTime(*monitor.LastCheckTime)
	}

	latencyText := "—"
	latencySort := -1
	if monitor.LastLatency != nil {
		latencySort = *monitor.LastLatency
		latencyText = fmt.Sprintf("%dms", *monitor.LastLatency)
	}

	uptimeText := "No uptime data"
	uptimeSort := -1.0
	if monitor.Uptime24h != nil {
		uptimeSort = *monitor.Uptime24h
		uptimeText = fmt.Sprintf("%.2f%% uptime", *monitor.Uptime24h)
	}

	detailLine := strings.TrimSpace(monitor.URL)
	if monitor.MonitorType != "agent" && monitor.MonitorType != "push" && monitor.LastLatency != nil {
		detailLine = latencyText
	}
	if detailLine == "" && monitor.LastCheckTime != nil {
		detailLine = lastCheckAgo
	}
	if detailLine == "" {
		detailLine = "No detail available"
	}

	tlsDetail := ""
	if monitor.TLSDaysUntilExpiry != nil {
		tlsDetail = fmt.Sprintf("TLS expires in %d days", *monitor.TLSDaysUntilExpiry)
	} else if strings.TrimSpace(monitor.TLSNotAfter) != "" {
		tlsDetail = fmt.Sprintf("TLS expiry %s", strings.TrimSpace(monitor.TLSNotAfter))
	}

	searchParts := []string{monitor.Name, monitor.URL, monitor.MonitorType, strings.Join(monitor.Tags, " ")}
	for _, metric := range []string{latencyText, uptimeText} {
		if metric != "" {
			searchParts = append(searchParts, metric)
		}
	}

	history24Raw := sliceToBarPoints(monitor.UptimeHistory24h)
	history7Raw := sliceToBarPoints(trimDailyHistory(monitor.UptimeHistory7d, monitor.UptimeHistory30d, 7))
	history30Raw := sliceToBarPoints(trimDailyHistory(monitor.UptimeHistory30d, monitor.UptimeHistory90d, 30))
	history90Raw := sliceToBarPoints(trimDailyHistory(monitor.UptimeHistory90d, monitor.UptimeHistory365d, 90))
	history24 := fixedStripPoints(history24Raw, monitorStripCellCount("24h"))
	history7 := fixedStripPoints(history7Raw, monitorStripCellCount("7d"))
	history30Monitor := fixedStripPoints(history30Raw, monitorStripCellCount("30d"))
	history90Monitor := fixedStripPoints(history90Raw, monitorStripCellCount("90d"))
	history30Expanded := fixedStripPoints(history30Raw, expandedStripCellCount("30d"))
	history90Expanded := fixedStripPoints(history90Raw, expandedStripCellCount("90d"))
	uptime24Value := uptimeValue(averageBarPoints(history24Raw))
	uptime7Value := uptimeValue(averageBarPoints(history7Raw))
	uptime30Value := uptimeValue(averageBarPoints(history30Raw))
	uptime90Value := uptimeValue(averageBarPoints(history90Raw))

	return statusPageMonitorView{
		ID:                     monitor.ID,
		Name:                   monitor.Name,
		URL:                    monitor.URL,
		Type:                   monitor.MonitorType,
		TypeLabel:              typeLabel(monitor.MonitorType),
		Status:                 monitor.Status,
		ToneClass:              statusTone(monitor.Status),
		StatusLabel:            monitorStatusLabel(monitor.Status),
		StatusRank:             monitorStatusRank(monitor.Status),
		SearchText:             strings.ToLower(strings.Join(searchParts, " ")),
		SummaryMetric:          uptime24Value,
		SummaryCaption:         monitorUptimeLabel(defaultStatusPageRange),
		LastCheckAgo:           lastCheckAgo,
		LastCheckUnix:          lastCheckUnix,
		LatencyText:            latencyText,
		LatencySort:            latencySort,
		UptimeText:             uptime24Value,
		Uptime24Value:          uptime24Value,
		Uptime7Value:           uptime7Value,
		Uptime30Value:          uptime30Value,
		Uptime90Value:          uptime90Value,
		UptimeSort:             uptimeSort,
		TagsText:               strings.Join(monitor.Tags, ", "),
		Tags:                   monitor.Tags,
		MonitorDetailLine:      detailLine,
		TLSDetail:              tlsDetail,
		History1hJSON:          mustJSON(sliceToBarPoints(monitor.UptimeHistory1h)),
		History24hJSON:         mustJSON(history24),
		History7dJSON:          mustJSON(history7),
		History30dJSON:         mustJSON(history30Monitor),
		History90dJSON:         mustJSON(history90Monitor),
		ExpandedHistory30dJSON: mustJSON(history30Expanded),
		ExpandedHistory90dJSON: mustJSON(history90Expanded),
		History1yJSON:          mustJSON(sliceToBarPoints(monitor.UptimeHistory365d)),
	}
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "No recent check"
	}
	diff := time.Since(t.UTC())
	if diff < 0 {
		diff = 0
	}

	switch {
	case diff < time.Minute:
		return "Just now"
	case diff < time.Hour:
		minutes := int(diff / time.Minute)
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	case diff < 24*time.Hour:
		hours := int(diff / time.Hour)
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	default:
		days := int(diff / (24 * time.Hour))
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

func summaryMetric(monitor MonitorStatus) string {
	switch {
	case monitor.Uptime24h != nil:
		return fmt.Sprintf("%.2f%%", *monitor.Uptime24h)
	case monitor.LastLatency != nil:
		return fmt.Sprintf("%dms", *monitor.LastLatency)
	case monitor.LastCheckTime != nil:
		return relativeTime(*monitor.LastCheckTime)
	default:
		return "—"
	}
}

func summaryCaption(monitor MonitorStatus) string {
	switch {
	case monitor.Uptime24h != nil:
		return "24h uptime"
	case monitor.LastLatency != nil:
		return "Latest response"
	case monitor.LastCheckTime != nil:
		return "Last check"
	default:
		return "No data"
	}
}

func monitorUptimeLabel(rangeName string) string {
	switch rangeName {
	case "24h":
		return "24h uptime"
	case "7d":
		return "7d uptime"
	case "90d":
		return "90d uptime"
	default:
		return "30d uptime"
	}
}

func monitorStatusLabel(status string) string {
	switch status {
	case "up":
		return "Healthy"
	case "degraded":
		return "Degraded"
	case "down":
		return "Down"
	case "error":
		return "Error"
	case "maintenance":
		return "Maintenance"
	default:
		return "Unknown"
	}
}

func monitorStatusRank(status string) int {
	switch status {
	case "down":
		return 0
	case "error":
		return 1
	case "degraded":
		return 2
	case "maintenance":
		return 3
	case "unknown":
		return 4
	case "up":
		return 5
	default:
		return 6
	}
}

func statusTone(status string) string {
	switch status {
	case "up":
		return "ok"
	case "degraded":
		return "warn"
	case "down", "error":
		return "down"
	case "maintenance":
		return "maint"
	default:
		return "unknown"
	}
}

func typeLabel(kind string) string {
	switch kind {
	case "http":
		return "HTTP"
	case "ping":
		return "Ping"
	case "dns":
		return "DNS"
	case "grpc":
		return "gRPC"
	case "group":
		return "Group"
	case "agent":
		return "Agent"
	case "push":
		return "Push"
	case "sip":
		return "SIP"
	case "synthetic_api":
		return "Synthetic API"
	case "synthetic_browser":
		return "Synthetic Browser"
	case "redis":
		return "Redis"
	case "postgres":
		return "PostgreSQL"
	case "mongodb":
		return "MongoDB"
	case "rabbitmq":
		return "RabbitMQ"
	default:
		return strings.ToUpper(kind)
	}
}

func normalizeTheme(theme string) string {
	switch strings.ToLower(strings.TrimSpace(theme)) {
	case "light":
		return "light"
	default:
		return "dark"
	}
}

func averageBarPoints(history []barPoint) float64 {
	if len(history) == 0 {
		return -1
	}
	total := 0.0
	count := 0
	for _, point := range history {
		if point.Uptime < 0 {
			continue
		}
		total += point.Uptime
		count++
	}
	if count == 0 {
		return -1
	}
	return total / float64(count)
}

type barPoint struct {
	Label  string  `json:"label"`
	Uptime float64 `json:"uptime"`
}

func sliceToBarPoints[T interface {
	getLabel() string
	getUptime() float64
}](history []T) []barPoint {
	points := make([]barPoint, 0, len(history))
	for _, point := range history {
		points = append(points, barPoint{Label: point.getLabel(), Uptime: point.getUptime()})
	}
	return points
}

func (m MinuteUptime) getLabel() string   { return m.Time }
func (m MinuteUptime) getUptime() float64 { return m.Uptime }
func (h HourlyUptime) getLabel() string   { return h.Hour }
func (h HourlyUptime) getUptime() float64 { return h.Uptime }
func (d DailyUptime) getLabel() string    { return d.Date }
func (d DailyUptime) getUptime() float64  { return d.Uptime }

func trimDailyHistory(primary []DailyUptime, fallback []DailyUptime, limit int) []DailyUptime {
	history := primary
	if len(history) == 0 {
		history = fallback
	}
	if limit > 0 && len(history) > limit {
		return history[len(history)-limit:]
	}
	return history
}

func fixedStripPoints(points []barPoint, cells int) []barPoint {
	if cells <= 0 {
		return nil
	}
	if len(points) == 0 {
		filled := make([]barPoint, cells)
		for i := range filled {
			filled[i] = barPoint{Uptime: -1}
		}
		return filled
	}

	filled := make([]barPoint, 0, cells)
	for i := 0; i < cells; i++ {
		start := i * len(points) / cells
		end := (i + 1) * len(points) / cells
		if end <= start {
			end = start + 1
		}
		if start >= len(points) {
			start = len(points) - 1
		}
		if end > len(points) {
			end = len(points)
		}
		window := points[start:end]
		if len(window) == 0 {
			window = points[len(points)-1:]
		}

		label := strings.TrimSpace(window[0].Label)
		if len(window) > 1 {
			lastLabel := strings.TrimSpace(window[len(window)-1].Label)
			if lastLabel != "" && lastLabel != label {
				label = label + " - " + lastLabel
			}
		}
		filled = append(filled, barPoint{
			Label:  label,
			Uptime: averageBarPoints(window),
		})
	}
	return filled
}

func uptimeValue(value float64) string {
	if value < 0 {
		return "—"
	}
	return fmt.Sprintf("%.2f%%", value)
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

const publicStatusPageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{.Title}} · Status</title>
  <style>
    :root {
      --bg: #050505;
      --bg-glow: radial-gradient(ellipse at 50% 0%, rgba(0, 229, 255, .04), transparent 60%);
      --surface: rgba(255, 255, 255, .025);
      --surface-hover: rgba(255, 255, 255, .05);
      --surface-card: #0d0d0d;
      --text: #f0f0f0;
      --text-muted: #a1a1aa;
      --text-dim: #81818b;
      --border: rgba(255, 255, 255, .08);
      --border-hover: rgba(255, 255, 255, .15);
      --green: #00ff9d;
      --yellow: #ffb800;
      --red: #ff2e93;
      --blue: #00e5ff;
      --gray: #aab4c8;
      --green-a: rgba(0, 255, 157, .10);
      --yellow-a: rgba(255, 184, 0, .10);
      --red-a: rgba(255, 46, 147, .10);
      --blue-a: rgba(0, 229, 255, .10);
      --gray-a: rgba(170, 180, 200, .10);
      --green-glow: rgba(0, 255, 157, .28);
      --yellow-glow: rgba(255, 184, 0, .28);
      --red-glow: rgba(255, 46, 147, .28);
      --blue-glow: rgba(0, 229, 255, .24);
      --gray-glow: rgba(170, 180, 200, .18);
      --radius-sm: 8px;
      --radius-md: 14px;
      --radius-lg: 22px;
      --radius-pill: 9999px;
      --font: "Segoe UI", "Inter", ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, sans-serif;
      --mono: "JetBrains Mono", "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
      --ease: cubic-bezier(.25, .8, .25, 1);
      --brand-primary: {{.PrimaryColor}};
      --brand-secondary: {{.SecondaryColor}};
    }

    body[data-theme="light"] {
      --bg: #fafafa;
      --bg-glow: radial-gradient(ellipse at 50% 0%, rgba(0, 0, 0, .03), transparent 60%);
      --surface: rgba(0, 0, 0, .03);
      --surface-hover: rgba(0, 0, 0, .06);
      --surface-card: #ffffff;
      --text: #111827;
      --text-muted: #4b5563;
      --text-dim: #71717a;
      --border: rgba(0, 0, 0, .09);
      --border-hover: rgba(0, 0, 0, .18);
      --green: #00c875;
      --yellow: #d38d00;
      --red: #da1b69;
      --blue: #00a8cc;
      --gray: #64748b;
      --green-a: rgba(0, 200, 117, .08);
      --yellow-a: rgba(211, 141, 0, .08);
      --red-a: rgba(218, 27, 105, .08);
      --blue-a: rgba(0, 168, 204, .08);
      --gray-a: rgba(100, 116, 139, .08);
      --green-glow: rgba(0, 200, 117, .14);
      --yellow-glow: rgba(211, 141, 0, .16);
      --red-glow: rgba(218, 27, 105, .14);
      --blue-glow: rgba(0, 168, 204, .14);
      --gray-glow: rgba(100, 116, 139, .12);
    }

    * { box-sizing: border-box; margin: 0; padding: 0; }
    html { scroll-behavior: smooth; }
    body {
      font-family: var(--font);
      background: var(--bg);
      background-image: var(--bg-glow);
      background-repeat: no-repeat;
      color: var(--text);
      line-height: 1.6;
      -webkit-font-smoothing: antialiased;
      min-height: 100vh;
      transition: background-color .35s, color .35s;
    }
    button, input, select { font: inherit; }
    .hidden { display: none !important; }
    .container { max-width: 900px; margin: 0 auto; padding: 0 24px; }
    .header {
      position: sticky;
      top: 16px;
      z-index: 100;
      padding: 0 24px;
      pointer-events: none;
    }
    .header-inner {
      display: flex;
      justify-content: space-between;
      align-items: center;
      min-height: 58px;
      max-width: 900px;
      margin: 0 auto;
      padding: 0 20px;
      gap: 12px;
      background: rgba(10, 10, 10, .75);
      backdrop-filter: blur(24px);
      -webkit-backdrop-filter: blur(24px);
      border: 1px solid var(--border);
      border-radius: var(--radius-pill);
      box-shadow: 0 8px 32px rgba(0, 0, 0, .25);
      pointer-events: all;
    }
    body[data-theme="light"] .header-inner {
      background: rgba(255, 255, 255, .8);
      box-shadow: 0 4px 24px rgba(0, 0, 0, .08);
    }
    .brand {
      display: flex;
      align-items: center;
      gap: 12px;
      min-width: 0;
      font-weight: 500;
      letter-spacing: -.01em;
    }
    .brand-logo,
    .brand-mark {
      width: 32px;
      height: 32px;
      border-radius: var(--radius-sm);
      flex-shrink: 0;
    }
    .brand-logo { object-fit: cover; }
    .brand-mark {
      display: grid;
      place-items: center;
      color: #fff;
      font: 600 .8rem var(--mono);
      background: linear-gradient(135deg, color-mix(in srgb, var(--brand-primary) 70%, transparent), color-mix(in srgb, var(--brand-secondary) 78%, transparent));
      box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--brand-primary) 26%, transparent);
    }
    .brand-copy {
      min-width: 0;
      display: grid;
    }
    .brand-name {
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .brand-slug {
      font: .64rem var(--mono);
      color: var(--text-dim);
      text-transform: uppercase;
      letter-spacing: .08em;
    }
    .header-right {
      display: flex;
      align-items: center;
      gap: 8px;
      flex-shrink: 0;
    }
    .mode-toggle {
      display: flex;
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: var(--radius-pill);
      overflow: hidden;
    }
    .mode-btn,
    .btn-s,
    .range-btn,
    .filter-sel,
    .search-input {
      transition: all .22s var(--ease);
    }
    .mode-btn {
      appearance: none;
      background: none;
      border: none;
      color: var(--text-muted);
      font: 500 .72rem var(--mono);
      text-transform: uppercase;
      letter-spacing: .05em;
      padding: 8px 12px;
      cursor: pointer;
    }
    .mode-btn:hover { color: var(--text); }
    .mode-btn.active {
      color: var(--text);
      background: var(--surface-hover);
    }
    .btn-s {
      appearance: none;
      background: transparent;
      border: 1px solid var(--border);
      color: var(--text);
      min-height: 34px;
      border-radius: var(--radius-md);
      padding: 0 12px;
      cursor: pointer;
      font-size: .78rem;
      font-weight: 500;
    }
    .btn-s:hover {
      background: var(--surface-hover);
      border-color: color-mix(in srgb, var(--brand-primary) 24%, var(--border));
    }
    .hero {
      padding: 96px 0 52px;
      text-align: center;
      display: flex;
      flex-direction: column;
      align-items: center;
    }
    .overall-badge {
      display: inline-flex;
      align-items: center;
      gap: 10px;
      padding: 6px 14px 6px 8px;
      border-radius: 999px;
      background: var(--green-a);
      border: 1px solid rgba(0, 255, 157, .2);
      font: .75rem var(--mono);
      text-transform: uppercase;
      letter-spacing: .05em;
      margin-bottom: 28px;
      color: var(--green);
    }
    .pulse-dot {
      width: 8px;
      height: 8px;
      border-radius: 50%;
      animation: pulse 2s infinite var(--ease);
    }
    @keyframes pulse {
      0%, 100% { opacity: 1; transform: scale(1); }
      50% { opacity: .42; transform: scale(.72); }
    }
    .overall-badge.ok .pulse-dot {
      background: var(--green);
      box-shadow: 0 0 12px var(--green-glow);
    }
    .overall-badge.warn {
      background: var(--yellow-a);
      border-color: rgba(255, 184, 0, .2);
      color: var(--yellow);
    }
    .overall-badge.warn .pulse-dot {
      background: var(--yellow);
      box-shadow: 0 0 12px var(--yellow-glow);
    }
    .overall-badge.down {
      background: var(--red-a);
      border-color: rgba(255, 46, 147, .2);
      color: var(--red);
    }
    .overall-badge.down .pulse-dot {
      background: var(--red);
      box-shadow: 0 0 12px var(--red-glow);
    }
    .hero h1 {
      font-size: clamp(2.4rem, 5vw, 3.3rem);
      font-weight: 600;
      letter-spacing: -.045em;
      line-height: 1.05;
      margin-bottom: 14px;
      color: var(--text);
    }
    .tagline {
      color: var(--text-muted);
      font-size: 1.05rem;
      font-weight: 400;
      max-width: 520px;
      margin-bottom: 12px;
    }
    .hero-summary {
      color: var(--text-muted);
      max-width: 600px;
      margin-bottom: 14px;
      font-size: .95rem;
    }
    .meta-line {
      font: .75rem var(--mono);
      color: var(--text-muted);
      text-transform: uppercase;
      letter-spacing: .08em;
      display: flex;
      gap: 20px;
      flex-wrap: wrap;
      justify-content: center;
      margin-bottom: 56px;
    }
    .meta-line b {
      color: var(--text);
      font-weight: 600;
    }
    .meta-line .count { color: var(--text-muted); }
    .meta-line .count-ok,
    .meta-line .count-warn,
    .meta-line .count-down,
    .meta-line .count-maint {
      color: var(--text-muted);
      display: inline-flex;
      align-items: center;
      gap: 8px;
    }
    .meta-line .count-ok::before,
    .meta-line .count-warn::before,
    .meta-line .count-down::before,
    .meta-line .count-maint::before {
      content: "";
      width: 7px;
      height: 7px;
      border-radius: 50%;
      flex-shrink: 0;
    }
    .meta-line .count-ok::before { background: var(--green); box-shadow: 0 0 6px var(--green-glow); }
    .meta-line .count-warn::before { background: var(--yellow); box-shadow: 0 0 6px var(--yellow-glow); }
    .meta-line .count-down::before { background: var(--red); box-shadow: 0 0 6px var(--red-glow); }
    .meta-line .count-maint::before { background: var(--blue); box-shadow: 0 0 6px var(--blue-glow); }
    .uptime-section,
    .range-section {
      width: 100%;
      margin-bottom: 48px;
      background: var(--surface-card);
      border: 1px solid var(--border);
      border-radius: var(--radius-lg);
      padding: 24px 28px;
    }
    /* When there are no global uptime bars, the range-only card collapses
       to a compact pill so it doesn't render as a near-empty box. */
    .range-section {
      padding: 12px 18px;
      margin-bottom: 32px;
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 14px;
      flex-wrap: wrap;
      border-radius: var(--radius-pill);
    }
    .range-section .range-group { margin-top: 0; }
    .uptime-head {
      display: flex;
      justify-content: space-between;
      align-items: flex-end;
      gap: 12px;
      margin-bottom: 14px;
    }
    .uptime-label {
      font: .8rem var(--mono);
      color: var(--text-muted);
      text-transform: uppercase;
      letter-spacing: .06em;
    }
    .uptime-pct {
      font: 300 1.4rem var(--mono);
      color: var(--green);
      text-shadow: 0 0 18px var(--green-glow);
    }
    .range-group {
      display: flex;
      gap: 4px;
      margin-top: 8px;
      flex-wrap: wrap;
    }
    .range-btn {
      appearance: none;
      background: none;
      border: 1px solid var(--border);
      color: var(--text-dim);
      font: 500 .65rem var(--mono);
      text-transform: uppercase;
      letter-spacing: .05em;
      padding: 3px 10px;
      cursor: pointer;
      border-radius: var(--radius-sm);
    }
    .range-btn:hover { color: var(--text-muted); }
    .range-btn.active,
    .range-btn[aria-pressed="true"] {
      color: var(--green);
      border-color: color-mix(in srgb, var(--green) 22%, var(--border));
      background: var(--green-a);
    }
    .uptime-bars {
      display: flex;
      gap: 2px;
      height: 44px;
      align-items: flex-end;
    }
    /* Skeleton placeholder when bars haven't been hydrated yet */
    .uptime-bars:empty {
      background: repeating-linear-gradient(
        to right,
        var(--surface-hover) 0,
        var(--surface-hover) 4px,
        transparent 4px,
        transparent 6px
      );
      border-radius: 4px;
      opacity: .55;
    }
    .uptime-foot {
      display: flex;
      justify-content: space-between;
      margin-top: 10px;
      font: .65rem var(--mono);
      color: var(--text-dim);
      text-transform: uppercase;
    }
    .toolbar {
      display: flex;
      justify-content: space-between;
      align-items: center;
      width: 100%;
      margin-bottom: 24px;
      padding: 10px 16px;
      background: var(--surface-card);
      border: 1px solid var(--border);
      border-radius: var(--radius-pill);
      flex-wrap: wrap;
      gap: 12px;
    }
    .search-wrap {
      display: flex;
      align-items: center;
      gap: 10px;
      color: var(--text-muted);
      flex: 1;
      min-width: 140px;
      border-right: 1px solid var(--border);
      padding-right: 16px;
      margin-right: 4px;
    }
    .search-icon {
      font: .85rem var(--mono);
      color: var(--text-dim);
    }
    .search-input {
      width: 100%;
      background: none;
      border: none;
      color: var(--text);
      font-size: .95rem;
      outline: none;
    }
    .search-input::placeholder { color: var(--text-dim); }
    .filters { display: flex; gap: 12px; }
    .filter-sel {
      appearance: none;
      background: transparent;
      border: none;
      color: var(--text-muted);
      font: .75rem var(--mono);
      text-transform: uppercase;
      cursor: pointer;
      outline: none;
      padding-right: 16px;
    }
    .filter-sel:hover { color: var(--text); }
    .svc-group { margin-bottom: 32px; }
    .svc-group.hidden { display: none; }
    .group-label {
      display: flex;
      align-items: center;
      gap: 10px;
      font-size: .92rem;
      font-weight: 600;
      letter-spacing: -.01em;
      color: var(--text);
      margin-bottom: 10px;
      padding: 2px 4px;
    }
    summary.group-label {
      list-style: none;
      cursor: pointer;
      user-select: none;
      border-radius: var(--radius-sm);
    }
    summary.group-label::-webkit-details-marker { display: none; }
    summary.group-label:hover .group-chevron { color: var(--text); }
    .group-chevron {
      color: var(--text-dim);
      font: 500 .85rem var(--mono);
      transition: transform .3s var(--ease);
      flex-shrink: 0;
    }
    .svc-group:not([open]) > summary .group-chevron { transform: rotate(-90deg); }
    .group-title {
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .group-label .group-count {
      font: .65rem var(--mono);
      color: var(--text-dim);
      background: var(--surface);
      border: 1px solid var(--border);
      padding: 1px 7px;
      border-radius: var(--radius-pill);
      letter-spacing: .02em;
      font-weight: 400;
      flex-shrink: 0;
    }
    .group-rule {
      flex: 1;
      height: 1px;
      background: var(--border);
      min-width: 16px;
    }
    .group-status {
      display: inline-flex;
      align-items: center;
      gap: 7px;
      font: 500 .66rem var(--mono);
      text-transform: uppercase;
      letter-spacing: .05em;
      flex-shrink: 0;
    }
    .gs-ok { color: var(--green); }
    .gs-ok .status-dot { background: var(--green); box-shadow: 0 0 8px var(--green-glow); }
    .gs-warn { color: var(--yellow); }
    .gs-warn .status-dot { background: var(--yellow); box-shadow: 0 0 8px var(--yellow-glow); }
    .gs-down { color: var(--red); }
    .gs-down .status-dot { background: var(--red); box-shadow: 0 0 8px var(--red-glow); }
    .gs-maint { color: var(--blue); }
    .gs-maint .status-dot { background: var(--blue); box-shadow: 0 0 8px var(--blue-glow); }
    .gs-unknown { color: var(--gray); }
    .gs-unknown .status-dot { background: var(--gray); box-shadow: 0 0 8px var(--gray-glow); }
    .group-cards { display: flex; flex-direction: column; gap: 8px; }
    .svc-row {
      background: var(--surface-card);
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      overflow: hidden;
      transition: border-color .2s, background .2s;
    }
    .svc-row:hover { border-color: var(--border-hover); }
    .svc-row[open] { border-color: var(--border-hover); }
    .svc-row > summary {
      list-style: none;
      cursor: pointer;
    }
    .svc-row > summary::-webkit-details-marker { display: none; }
    .svc-header {
      padding: 16px 20px 8px;
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 12px;
      user-select: none;
    }
    .svc-left {
      display: flex;
      align-items: center;
      gap: 12px;
      min-width: 0;
      flex: 1;
    }
    .svc-main {
      min-width: 0;
    }
    .svc-name {
      font-size: .95rem;
      font-weight: 500;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .svc-meta {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
      color: var(--text-dim);
      font: .67rem var(--mono);
      text-transform: uppercase;
      letter-spacing: .05em;
      margin-top: 3px;
    }
    .svc-meta span + span::before {
      content: "•";
      margin-right: 8px;
      color: var(--text-dim);
    }
    .svc-type {
      font: 500 .62rem var(--mono);
      color: var(--text-muted);
      background: var(--surface);
      border: 1px solid var(--border);
      padding: 2px 7px;
      border-radius: var(--radius-sm);
      text-transform: uppercase;
      letter-spacing: .04em;
      flex-shrink: 0;
    }
    .svc-right {
      display: flex;
      align-items: center;
      gap: 22px;
      flex-shrink: 0;
    }
    .svc-metric {
      min-width: 58px;
      text-align: right;
    }
    .svc-metric-value {
      font: .8rem var(--mono);
      color: var(--text-muted);
    }
    .svc-metric-label {
      font: .58rem var(--mono);
      color: var(--text-dim);
      text-transform: uppercase;
      letter-spacing: .05em;
    }
    .status-badge {
      display: flex;
      align-items: center;
      gap: 8px;
      font: .7rem var(--mono);
      text-transform: uppercase;
      letter-spacing: .03em;
      min-width: 96px;
    }
    .status-dot {
      width: 6px;
      height: 6px;
      border-radius: 50%;
      flex-shrink: 0;
    }
    .status-badge.s-ok { color: var(--green); }
    .status-badge.s-ok .status-dot {
      background: var(--green);
      box-shadow: 0 0 8px var(--green-glow);
    }
    .status-badge.s-warn { color: var(--yellow); }
    .status-badge.s-warn .status-dot {
      background: var(--yellow);
      box-shadow: 0 0 8px var(--yellow-glow);
    }
    .status-badge.s-down { color: var(--red); }
    .status-badge.s-down .status-dot {
      background: var(--red);
      box-shadow: 0 0 8px var(--red-glow);
    }
    .status-badge.s-unknown { color: var(--gray); }
    .status-badge.s-unknown .status-dot {
      background: var(--gray);
      box-shadow: 0 0 8px var(--gray-glow);
    }
    .status-badge.s-maint { color: var(--blue); }
    .status-badge.s-maint .status-dot {
      background: var(--blue);
      box-shadow: 0 0 8px var(--blue-glow);
    }
    .chevron {
      color: var(--text-dim);
      transition: transform .35s var(--ease);
      font: 500 .9rem var(--mono);
    }
    .svc-row[open] .chevron { transform: rotate(180deg); }
    .monitor-bar {
      display: flex;
      gap: 1px;
      height: 3px;
      margin: 0 20px 10px;
      border-radius: 2px;
      overflow: hidden;
    }
    .svc-details {
      display: grid;
      grid-template-rows: 0fr;
      transition: grid-template-rows .35s var(--ease);
    }
    .svc-row[open] .svc-details { grid-template-rows: 1fr; }
    .details-inner { overflow: hidden; }
    .details-content {
      padding: 0 20px 22px;
      border-top: 1px solid var(--border);
      margin-top: 4px;
      padding-top: 18px;
    }
    .expanded-bar-section { margin-bottom: 20px; }
    .expanded-bar-head {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 8px;
    }
    .expanded-bar-label {
      font: .65rem var(--mono);
      color: var(--text-dim);
      text-transform: uppercase;
      letter-spacing: .05em;
    }
    .expanded-bars {
      display: flex;
      gap: 2px;
      height: 28px;
      align-items: flex-end;
    }
    .expanded-bar-foot {
      display: flex;
      justify-content: space-between;
      margin-top: 6px;
      font: .6rem var(--mono);
      color: var(--text-dim);
      text-transform: uppercase;
    }
    .details-grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
      gap: 18px;
    }
    .dp {
      display: flex;
      flex-direction: column;
      gap: 6px;
    }
    .dp-label {
      font: .6rem var(--mono);
      color: var(--text-dim);
      text-transform: uppercase;
      letter-spacing: .05em;
    }
    .dp-value {
      font: .82rem var(--mono);
      color: var(--text-muted);
    }
    .bar {
      flex: 1;
      min-width: 1px;
      border-radius: 2px;
      transition: opacity .15s, filter .15s, transform .15s;
      cursor: pointer;
      transform-origin: center bottom;
    }
    .bar.good { background: var(--green); }
    .bar.warn { background: var(--yellow); }
    .bar.bad { background: var(--red); }
    .bar.nodata { background: var(--border); }
    .uptime-bars .bar {
      height: 100%;
      opacity: .78;
      border-radius: 3px;
    }
    .uptime-bars .bar.warn,
    .uptime-bars .bar.bad { opacity: 1; }
    .uptime-bars .bar.warn { box-shadow: 0 0 10px var(--yellow-glow); }
    .uptime-bars .bar.bad { box-shadow: 0 0 10px var(--red-glow); }
    .uptime-bars .bar:hover {
      opacity: 1;
      filter: brightness(1.15);
      transform: scaleY(1.05);
    }
    .monitor-bar .bar {
      opacity: .55;
      border-radius: 1px;
    }
    .monitor-bar .bar.warn,
    .monitor-bar .bar.bad { opacity: .95; }
    .monitor-bar .bar:hover {
      filter: brightness(1.3);
      transform: scaleY(1.8);
    }
    .expanded-bars .bar {
      height: 100%;
      opacity: .72;
    }
    .expanded-bars .bar.warn,
    .expanded-bars .bar.bad { opacity: 1; }
    .expanded-bars .bar:hover {
      filter: brightness(1.15);
      transform: scaleY(1.06);
    }
    .bar-tooltip {
      position: fixed;
      z-index: 300;
      pointer-events: none;
      display: grid;
      gap: 1px;
      background: var(--surface-card);
      border: 1px solid var(--border-hover);
      border-radius: var(--radius-sm);
      padding: 7px 11px;
      font: .66rem var(--mono);
      box-shadow: 0 10px 30px rgba(0, 0, 0, .35);
      opacity: 0;
      transform: translate(-50%, -100%);
      transition: opacity .12s var(--ease);
      white-space: nowrap;
    }
    .bar-tooltip.visible { opacity: 1; }
    .bar-tooltip .tip-value {
      font-size: .8rem;
      font-weight: 600;
      color: var(--text);
    }
    .bar-tooltip .tip-value.good { color: var(--green); }
    .bar-tooltip .tip-value.warn { color: var(--yellow); }
    .bar-tooltip .tip-value.bad { color: var(--red); }
    .bar-tooltip .tip-value.nodata { color: var(--text-dim); }
    .bar-tooltip .tip-label {
      color: var(--text-dim);
      text-transform: uppercase;
      letter-spacing: .04em;
    }
    .incidents-section { margin-bottom: 48px; }
    .inc-card {
      padding: 24px;
      background: var(--surface-card);
      border: 1px solid var(--border);
      border-radius: var(--radius-md);
      position: relative;
      overflow: hidden;
      margin-bottom: 10px;
    }
    .inc-card.i-warn { border-left: 3px solid var(--yellow); }
    .inc-card.i-ok { border-left: 3px solid var(--green); }
    .inc-card.i-down { border-left: 3px solid var(--red); }
    .inc-card.i-unknown,
    .inc-card.i-empty { border-left: 3px solid var(--blue); }
    .inc-card::before {
      content: "";
      position: absolute;
      inset: 0;
      opacity: .04;
      pointer-events: none;
    }
    .inc-card.i-warn::before { background: var(--yellow); }
    .inc-card.i-ok::before { background: var(--green); }
    .inc-card.i-down::before { background: var(--red); }
    .inc-card.i-unknown::before,
    .inc-card.i-empty::before { background: var(--blue); }
    .inc-head {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 16px;
      margin-bottom: 10px;
    }
    .inc-title {
      font-size: 1rem;
      font-weight: 500;
    }
    .inc-meta {
      font: 500 .7rem var(--mono);
      background: var(--surface);
      padding: 3px 10px;
      border-radius: var(--radius-pill);
      border: 1px solid var(--border);
      text-transform: uppercase;
      letter-spacing: .05em;
      white-space: nowrap;
      color: var(--text-muted);
    }
    .inc-desc {
      color: var(--text-muted);
      font-size: .92rem;
      line-height: 1.65;
      max-width: 100%;
    }
    .inc-update {
      margin-top: 12px;
      padding: 10px 14px;
      font: .75rem var(--mono);
      color: var(--text-dim);
      background: var(--surface);
      border-radius: var(--radius-sm);
      border-left: 2px solid var(--yellow);
    }
    .empty-state {
      display: grid;
      place-items: center;
      padding: 28px 18px;
      border: 1px dashed var(--border);
      border-radius: var(--radius-md);
      background: var(--surface);
      text-align: center;
      color: var(--text-muted);
    }
    .footer { border-top: 1px solid var(--border); padding: 40px 0; margin-top: 20px; }
    .footer-inner {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 16px;
      flex-wrap: wrap;
      font: .8rem var(--mono);
      color: var(--text-dim);
      text-transform: uppercase;
      letter-spacing: .04em;
    }
    .footer-text {
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .footer-shortcuts {
      font-size: .65rem;
      opacity: .68;
    }
    .footer-shortcuts kbd {
      display: inline-block;
      padding: 1px 6px;
      border: 1px solid var(--border);
      border-radius: 3px;
      font-family: var(--mono);
      font-size: .6rem;
      margin: 0 2px;
    }
    body.compact .hero { padding: 36px 0 18px; }
    body.compact .hero h1 { font-size: 1.6rem; margin-bottom: 6px; }
    body.compact .overall-badge { margin-bottom: 16px; }
    body.compact .tagline,
    body.compact .hero-summary { display: none; }
    body.compact .meta-line { margin-bottom: 22px; gap: 14px; }
    body.compact .uptime-section { padding: 14px 18px; margin-bottom: 20px; }
    body.compact .uptime-head { margin-bottom: 8px; }
    body.compact .uptime-bars { height: 20px; }
    body.compact .uptime-foot { margin-top: 6px; }
    body.compact .uptime-pct { font-size: 1.1rem; }
    body.compact .range-section { margin-bottom: 18px; padding: 8px 14px; }
    body.compact .toolbar { margin-bottom: 14px; padding: 6px 14px; }
    body.compact .svc-group { margin-bottom: 18px; }
    body.compact .group-label { margin-bottom: 6px; font-size: .84rem; }
    body.compact .group-cards { gap: 4px; }
    body.compact .svc-header { padding: 7px 14px 4px; }
    body.compact .svc-left { gap: 10px; }
    body.compact .svc-name { font-size: .82rem; }
    body.compact .svc-meta { display: none; }
    body.compact .svc-type { font-size: .56rem; padding: 1px 5px; }
    body.compact .svc-right { gap: 14px; }
    body.compact .svc-metric-label { display: none; }
    body.compact .status-badge { min-width: 84px; font-size: .64rem; }
    body.compact .monitor-bar { height: 2px; margin: 0 14px 7px; }
    body.compact .details-content { padding-bottom: 16px; }
    body.compact .incidents-section { margin-bottom: 28px; }
    body.compact .inc-card { padding: 16px 18px; }
    body.kiosk { overflow: hidden; }
    body.kiosk .header,
    body.kiosk main,
    body.kiosk .footer { display: none !important; }
    body.kiosk .kiosk-view { display: flex !important; }
    .kiosk-view {
      display: none;
      flex-direction: column;
      position: fixed;
      inset: 0;
      z-index: 200;
      background: var(--bg);
      background-image: var(--bg-glow);
      background-repeat: no-repeat;
    }
    .kiosk-bar {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 0 20px;
      height: 52px;
      border-bottom: 1px solid var(--border);
      gap: 16px;
      flex-shrink: 0;
    }
    .kiosk-brand {
      display: flex;
      align-items: center;
      gap: 10px;
      min-width: 0;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
      font: 500 .84rem var(--font);
    }
    .kiosk-brand-mark {
      width: 18px;
      height: 18px;
      border-radius: 5px;
      background: linear-gradient(135deg, color-mix(in srgb, var(--brand-primary) 70%, transparent), color-mix(in srgb, var(--brand-secondary) 78%, transparent));
      box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--brand-primary) 24%, transparent);
    }
    .kiosk-stats {
      display: flex;
      gap: 18px;
      font: 500 .68rem var(--mono);
      text-transform: uppercase;
      letter-spacing: .04em;
      flex-wrap: wrap;
      justify-content: center;
    }
    .kiosk-stat {
      display: flex;
      align-items: center;
      gap: 6px;
    }
    .kiosk-stat .ks-dot {
      width: 6px;
      height: 6px;
      border-radius: 50%;
    }
    .kiosk-stat.k-ok { color: var(--green); }
    .kiosk-stat.k-ok .ks-dot {
      background: var(--green);
      box-shadow: 0 0 6px var(--green-glow);
    }
    .kiosk-stat.k-warn { color: var(--yellow); }
    .kiosk-stat.k-warn .ks-dot {
      background: var(--yellow);
      box-shadow: 0 0 6px var(--yellow-glow);
    }
    .kiosk-stat.k-down { color: var(--red); }
    .kiosk-stat.k-down .ks-dot {
      background: var(--red);
      box-shadow: 0 0 6px var(--red-glow);
    }
    .kiosk-stat.k-unknown { color: var(--gray); }
    .kiosk-stat.k-unknown .ks-dot {
      background: var(--gray);
      box-shadow: 0 0 6px var(--gray-glow);
    }
    .kiosk-stat.k-maint { color: var(--blue); }
    .kiosk-stat.k-maint .ks-dot {
      background: var(--blue);
      box-shadow: 0 0 6px var(--blue-glow);
    }
    .kiosk-clock {
      display: flex;
      align-items: center;
      gap: 10px;
      font: 500 .74rem var(--mono);
      color: var(--text-muted);
    }
    .kiosk-exit {
      background: none;
      border: 1px solid var(--border);
      color: var(--text-muted);
      font: 500 .68rem var(--mono);
      padding: 4px 12px;
      cursor: pointer;
      border-radius: 3px;
      text-transform: uppercase;
    }
    .kiosk-exit:hover {
      color: var(--text);
      border-color: color-mix(in srgb, var(--brand-primary) 24%, var(--border));
    }
    .kiosk-grid-wrap {
      flex: 1;
      overflow-y: auto;
      padding: 12px 14px 16px;
    }
    .kiosk-section.hidden { display: none; }
    .kiosk-section + .kiosk-section { margin-top: 16px; }
    .kiosk-section-head {
      display: flex;
      align-items: center;
      gap: 10px;
      margin-bottom: 8px;
      padding: 0 2px;
    }
    .kiosk-section-title {
      font: 600 .78rem var(--mono);
      text-transform: uppercase;
      letter-spacing: .08em;
      color: var(--text);
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .kiosk-section-count {
      font: .62rem var(--mono);
      color: var(--text-dim);
      background: var(--surface);
      border: 1px solid var(--border);
      padding: 0 6px;
      border-radius: var(--radius-pill);
      flex-shrink: 0;
    }
    .kiosk-section-rule {
      flex: 1;
      height: 1px;
      background: var(--border);
      min-width: 16px;
    }
    .kiosk-section-status {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      font: 500 .62rem var(--mono);
      text-transform: uppercase;
      letter-spacing: .05em;
      flex-shrink: 0;
    }
    .kiosk-grid {
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(168px, 1fr));
      gap: 5px;
    }
    .k-tile {
      background: var(--surface-card);
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      padding: 9px 11px 7px;
      position: relative;
      border-left: 3px solid transparent;
      display: flex;
      flex-direction: column;
      gap: 4px;
      transition: all .25s;
    }
    .k-tile:hover { background: var(--surface-hover); }
    .k-tile.t-ok { border-left-color: var(--green); }
    .k-tile.t-warn {
      border-left-color: var(--yellow);
      background: var(--yellow-a);
    }
    .k-tile.t-down {
      border-left-color: var(--red);
      background: var(--red-a);
    }
    .k-tile.t-maint {
      border-left-color: var(--blue);
      background: var(--blue-a);
    }
    .k-tile.t-unknown {
      border-left-color: var(--gray);
      background: var(--gray-a);
    }
    .k-tile-head {
      display: flex;
      justify-content: space-between;
      align-items: flex-start;
      gap: 6px;
    }
    .k-tile-name {
      font-size: .8rem;
      font-weight: 500;
      line-height: 1.3;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .k-tile-dot {
      width: 6px;
      height: 6px;
      border-radius: 50%;
      flex-shrink: 0;
      margin-top: 4px;
    }
    .t-ok .k-tile-dot {
      background: var(--green);
      box-shadow: 0 0 6px var(--green-glow);
    }
    .t-warn .k-tile-dot {
      background: var(--yellow);
      box-shadow: 0 0 6px var(--yellow-glow);
    }
    .t-down .k-tile-dot {
      background: var(--red);
      box-shadow: 0 0 8px var(--red-glow);
    }
    .t-maint .k-tile-dot {
      background: var(--blue);
      box-shadow: 0 0 6px var(--blue-glow);
    }
    .t-unknown .k-tile-dot {
      background: var(--gray);
      box-shadow: 0 0 6px var(--gray-glow);
    }
    .k-tile-sub {
      font: .6rem var(--mono);
      color: var(--text-dim);
      text-transform: uppercase;
      letter-spacing: .03em;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .k-tile-metrics {
      display: flex;
      gap: 12px;
      font: .7rem var(--mono);
      color: var(--text-muted);
    }
    .k-tile-bar {
      display: flex;
      gap: 1px;
      height: 3px;
      border-radius: 1px;
      overflow: hidden;
      margin-top: auto;
    }
    .k-tile-bar .bar { opacity: .46; }
    .k-tile-bar .bar.warn { opacity: .74; }
    .k-tile-bar .bar.bad { opacity: .9; }
    @media (max-width: 768px) {
      .hero h1 { font-size: 2.2rem; }
      .svc-right { gap: 10px; }
      /* Drop the second metric (uptime %), keep latency value visible */
      .svc-metric + .svc-metric { display: none; }
      /* Drop the small caps sub-label so only the value remains */
      .svc-metric-label { display: none; }
      .svc-metric { min-width: 0; }
      /* Keep status pill text on phones — it's the primary signal */
      .status-badge { min-width: auto; font-size: .62rem; letter-spacing: 0; }
      .mode-btn { padding: 6px 8px; font-size: .6rem; }
      .toolbar { border-radius: var(--radius-md); padding: 8px 14px; flex-direction: column; align-items: stretch; }
      .search-wrap { border-right: none; padding-right: 0; margin-right: 0; }
      .filters { justify-content: space-between; }
      .kiosk-grid { grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); }
      .kiosk-stats { gap: 10px; }
      .inc-desc { max-width: 100%; }
    }
    @media (max-width: 640px) {
      .container { padding: 0 14px; }
      .header-inner,
      .hero,
      .uptime-head,
      .kiosk-bar,
      .inc-head { align-items: flex-start; }
      .header-inner,
      .svc-header,
      .details-grid { display: grid; }
      .header-right { justify-content: flex-start; flex-wrap: wrap; }
      .svc-right { justify-content: space-between; width: 100%; }
      .kiosk-bar { height: auto; padding: 12px 16px; }
      .kiosk-clock { justify-content: space-between; width: 100%; }
    }
  </style>
</head>
<body
  data-status-page-slug="{{.Slug}}"
  data-default-theme="{{.DefaultTheme}}"
  data-allow-theme-toggle="{{if .AllowThemeToggle}}1{{else}}0{{end}}"
  data-default-range="{{.DefaultRange}}"
  data-default-mode="default"
>
  <div class="kiosk-view">
    <div class="kiosk-bar">
      <div class="kiosk-brand">
        <span class="kiosk-brand-mark"></span>
        <span>{{.Title}}</span>
      </div>
      <div class="kiosk-stats" id="kioskStats">
        <div class="kiosk-stat k-ok"><span class="ks-dot"></span>{{.OperationalCount}} OK</div>
        <div class="kiosk-stat k-warn"><span class="ks-dot"></span>{{.DegradedCount}} WARN</div>
        <div class="kiosk-stat k-down"><span class="ks-dot"></span>{{.DownCount}} DOWN</div>
        <div class="kiosk-stat k-maint"><span class="ks-dot"></span>{{.MaintenanceCount}} MAINT</div>
        {{if gt .UnknownCount 0}}
          <div class="kiosk-stat k-unknown"><span class="ks-dot"></span>{{.UnknownCount}} UNKNOWN</div>
        {{end}}
      </div>
      <div class="kiosk-clock">
        <span id="kioskTime"></span>
        <button type="button" class="kiosk-exit" id="kioskExitBtn">Esc Exit</button>
      </div>
    </div>
    <div class="kiosk-grid-wrap" id="kioskGrid">
      {{range .Sections}}
        <section class="kiosk-section kiosk-section-group" data-section-id="{{.ID}}">
          <div class="kiosk-section-head">
            <span class="kiosk-section-title">{{.Title}}</span>
            <span class="kiosk-section-count">{{.MonitorCount}}</span>
            <span class="kiosk-section-rule"></span>
            <span class="kiosk-section-status gs-{{.ToneClass}}">
              <span class="status-dot"></span>
              <span>{{.StatusSummary}}</span>
            </span>
          </div>
          <div class="kiosk-grid">
            {{range .Monitors}}
              <article class="k-tile t-{{.ToneClass}} kiosk-tile" data-search="{{.SearchText}}" data-status="{{.Status}}">
                <div class="k-tile-head">
                  <div class="k-tile-name">{{.Name}}</div>
                  <span class="k-tile-dot"></span>
                </div>
                <div class="k-tile-sub">{{.TypeLabel}} · {{.LastCheckAgo}}</div>
                <div class="k-tile-metrics">
                  <span>{{.LatencyText}}</span>
                  <span>{{.SummaryMetric}}</span>
                </div>
                {{if $.ShowMonitorUptime}}
                  <div
                    class="k-tile-bar js-strip"
                    data-strip-kind="kiosk"
                    data-active-range="{{$.DefaultRange}}"
                    data-cells="{{$.MonitorStripCells}}"
                    data-range-24h="{{.History24hJSON}}"
                    data-range-7d="{{.History7dJSON}}"
                    data-range-30d="{{.History30dJSON}}"
                    data-range-90d="{{.History90dJSON}}"
                  ></div>
                {{end}}
              </article>
            {{end}}
          </div>
        </section>
      {{end}}
    </div>
  </div>

  <header class="header">
    <div class="header-inner">
      <div class="brand">
        {{if .HasLogo}}
          <img class="brand-logo" src="{{.LogoURL}}" alt="{{.Title}}" />
        {{else}}
          <div class="brand-mark">{{printf "%.1s" .Title}}</div>
        {{end}}
        <div class="brand-copy">
          <span class="brand-name">{{.Title}}</span>
          <span class="brand-slug">status // /{{.Slug}}</span>
        </div>
      </div>
      <div class="header-right">
        {{if .ShowLayoutControl}}
          <div class="mode-toggle" role="group" aria-label="Display mode">
            <button type="button" class="mode-btn active" data-mode="default"><span>List</span></button>
            <button type="button" class="mode-btn" data-mode="compact"><span>Compact</span></button>
            <button type="button" class="mode-btn" data-mode="kiosk"><span>Kiosk</span></button>
          </div>
        {{end}}
        {{if .AllowThemeToggle}}
          <button type="button" class="btn-s" id="themeToggleBtn">Theme</button>
        {{end}}
      </div>
    </div>
  </header>

  <main class="container">
    <section class="hero" id="statusHero">
      <div class="overall-badge {{.OverallTone}}" id="overallBadge">
        <div class="pulse-dot"></div>
        <span>{{.OverallStatusLabel}}</span>
      </div>
      <h1>{{.Title}}</h1>
      {{if .HasDescription}}
        <p class="tagline">{{.Description}}</p>
      {{end}}
      <p class="hero-summary">{{.OverallSummary}}</p>
      <div class="meta-line">
        <span class="count"><b>{{.MonitorCount}}</b> monitors</span>
        <span class="count-ok"><b>{{.OperationalCount}}</b> operational</span>
        <span class="count-warn"><b>{{.DegradedCount}}</b> degraded</span>
        <span class="count-down"><b>{{.DownCount}}</b> outage</span>
        <span class="count-maint"><b>{{.MaintenanceCount}}</b> maintenance</span>
      </div>

      {{if .ShowGlobalUptime}}
        <div class="uptime-section">
          <div class="uptime-head">
            <div>
              <div class="uptime-label">Network uptime</div>
              {{if .ShowRangeSelector}}
                <div class="range-group" role="group" aria-label="Select uptime range">
                  <button type="button" class="range-btn active" data-range-pill="24h" aria-pressed="true">24h</button>
                  <button type="button" class="range-btn" data-range-pill="7d" aria-pressed="false">7d</button>
                  <button type="button" class="range-btn" data-range-pill="30d" aria-pressed="false">30d</button>
                  <button type="button" class="range-btn" data-range-pill="90d" aria-pressed="false">90d</button>
                </div>
              {{end}}
            </div>
            <div
              class="uptime-pct"
              id="globalUptimeValue"
              data-range-value-24h="{{.GlobalUptime24Value}}"
              data-range-value-7d="{{.GlobalUptime7Value}}"
              data-range-value-30d="{{.GlobalUptime30Value}}"
              data-range-value-90d="{{.GlobalUptime90Value}}"
            >{{.GlobalUptime24Value}}</div>
          </div>
          <div
            class="uptime-bars js-strip"
            data-strip-kind="global"
            data-active-range="{{.DefaultRange}}"
            data-cells="{{.GlobalStripCells}}"
            data-cells-7d="{{globalStripCells "7d"}}"
            data-cells-30d="{{globalStripCells "30d"}}"
            data-cells-90d="{{globalStripCells "90d"}}"
            data-range-24h="{{.GlobalUptime24JSON}}"
            data-range-7d="{{.GlobalUptime7JSON}}"
            data-range-30d="{{.GlobalUptime30JSON}}"
            data-range-90d="{{.GlobalUptime90JSON}}"
          ></div>
          <div class="uptime-foot">
            <span id="globalRangeStart">Last 24 hours</span>
            <span>Today</span>
          </div>
        </div>
      {{else if .ShowRangeSelector}}
        <div class="range-section">
          <div class="uptime-label">Uptime range</div>
          <div class="range-group" role="group" aria-label="Select uptime range">
            <button type="button" class="range-btn active" data-range-pill="24h" aria-pressed="true">24h</button>
            <button type="button" class="range-btn" data-range-pill="7d" aria-pressed="false">7d</button>
            <button type="button" class="range-btn" data-range-pill="30d" aria-pressed="false">30d</button>
            <button type="button" class="range-btn" data-range-pill="90d" aria-pressed="false">90d</button>
          </div>
        </div>
      {{end}}
    </section>

    {{if .ShowToolbar}}
      <div class="toolbar">
        {{if .ShowSearchControls}}
          <label class="search-wrap" for="statusPageSearch">
            <span class="search-icon">⌕</span>
            <input type="search" id="statusPageSearch" class="search-input" placeholder="Search infrastructure..." />
          </label>
          <div class="filters">
            <select class="filter-sel" id="statusFilter">
              <option value="all">Status: All</option>
              <option value="up">Operational</option>
              <option value="degraded">Degraded</option>
              <option value="down">Outage</option>
              <option value="error">Error</option>
              <option value="maintenance">Maintenance</option>
              <option value="unknown">Unknown</option>
            </select>
          </div>
        {{end}}
      </div>
    {{end}}

    <section id="servicesList">
      {{range .Sections}}
        <details class="svc-group monitor-section-group" data-section-id="{{.ID}}" open>
          <summary class="group-label">
            <span class="group-chevron">⌄</span>
            <span class="group-title">{{.Title}}</span>
            <span class="group-count" data-monitor-count>{{.MonitorCount}}</span>
            <span class="group-rule"></span>
            <span class="group-status gs-{{.ToneClass}}">
              <span class="status-dot"></span>
              <span>{{.StatusSummary}}</span>
            </span>
          </summary>
          <div class="group-cards">
            {{range .Monitors}}
              <details class="svc-row monitor-card" data-monitor-id="{{.ID}}" data-search="{{.SearchText}}" data-status="{{.Status}}">
                <summary class="svc-header">
                  <div class="svc-left">
                    <span class="svc-type">{{.TypeLabel}}</span>
                    <div class="svc-main">
                      <div class="svc-name">{{.Name}}</div>
                      <div class="svc-meta">
                        <span data-monitor-uptime-label>{{.SummaryCaption}}</span>
                        <span>{{.MonitorDetailLine}}</span>
                        <span>{{.LastCheckAgo}}</span>
                      </div>
                    </div>
                  </div>
                  <div class="svc-right">
                    <div class="svc-metric">
                      <div class="svc-metric-value">{{.LatencyText}}</div>
                      <div class="svc-metric-label">Latency</div>
                    </div>
                    <div class="svc-metric">
                      <div
                        class="svc-metric-value"
                        data-monitor-uptime-value
                        data-range-value-24h="{{.Uptime24Value}}"
                        data-range-value-7d="{{.Uptime7Value}}"
                        data-range-value-30d="{{.Uptime30Value}}"
                        data-range-value-90d="{{.Uptime90Value}}"
                      >{{.SummaryMetric}}</div>
                      <div class="svc-metric-label" data-monitor-uptime-label>{{.SummaryCaption}}</div>
                    </div>
                    <div class="status-badge s-{{.ToneClass}}">
                      <span class="status-dot"></span>
                      <span>{{.StatusLabel}}</span>
                    </div>
                    <span class="chevron">⌄</span>
                  </div>
                </summary>
                {{if $.ShowMonitorUptime}}
                  <div
                    class="monitor-bar js-strip"
                    data-strip-kind="monitor"
                    data-active-range="{{$.DefaultRange}}"
                    data-cells="{{$.MonitorStripCells}}"
                    data-cells-30d="{{monitorStripCells "30d"}}"
                    data-cells-90d="{{monitorStripCells "90d"}}"
                    data-range-24h="{{.History24hJSON}}"
                    data-range-7d="{{.History7dJSON}}"
                    data-range-30d="{{.History30dJSON}}"
                    data-range-90d="{{.History90dJSON}}"
                  ></div>
                {{end}}
                <div class="svc-details">
                  <div class="details-inner">
                    <div class="details-content">
                      {{if $.ShowMonitorUptime}}
                        <div class="expanded-bar-section">
                          <div class="expanded-bar-head">
                            <span class="expanded-bar-label" data-shared-range-label>Last 24 hours</span>
                            <span
                              class="expanded-bar-label"
                              data-monitor-uptime-value
                              data-range-value-24h="{{.Uptime24Value}}"
                              data-range-value-7d="{{.Uptime7Value}}"
                              data-range-value-30d="{{.Uptime30Value}}"
                              data-range-value-90d="{{.Uptime90Value}}"
                            >{{.UptimeText}}</span>
                          </div>
                          <div
                            class="expanded-bars js-strip"
                            data-strip-kind="expanded"
                            data-active-range="{{$.DefaultRange}}"
                            data-cells="{{$.MonitorStripCells}}"
                            data-cells-30d="{{expandedStripCells "30d"}}"
                            data-cells-90d="{{expandedStripCells "90d"}}"
                            data-range-24h="{{.History24hJSON}}"
                            data-range-7d="{{.History7dJSON}}"
                            data-range-30d="{{.ExpandedHistory30dJSON}}"
                            data-range-90d="{{.ExpandedHistory90dJSON}}"
                          ></div>
                          <div class="expanded-bar-foot">
                            <span>{{.TypeLabel}}</span>
                            <span>{{.LastCheckAgo}}</span>
                          </div>
                        </div>
                      {{end}}
                      <div class="details-grid">
                        <div class="dp">
                          <span class="dp-label">Last check</span>
                          <span class="dp-value">{{.LastCheckAgo}}</span>
                        </div>
                        <div class="dp">
                          <span class="dp-label">Latest latency</span>
                          <span class="dp-value">{{.LatencyText}}</span>
                        </div>
                        <div class="dp">
                          <span class="dp-label" data-monitor-uptime-label>{{.SummaryCaption}}</span>
                          <span
                            class="dp-value"
                            data-monitor-uptime-value
                            data-range-value-24h="{{.Uptime24Value}}"
                            data-range-value-7d="{{.Uptime7Value}}"
                            data-range-value-30d="{{.Uptime30Value}}"
                            data-range-value-90d="{{.Uptime90Value}}"
                          >{{.UptimeText}}</span>
                        </div>
                        <div class="dp">
                          <span class="dp-label">Monitor type</span>
                          <span class="dp-value">{{.TypeLabel}}</span>
                        </div>
                      </div>
                    </div>
                  </div>
                </div>
              </details>
            {{end}}
          </div>
        </details>
      {{end}}
      <div class="empty-state hidden" id="emptyState">
        <div>
          <strong>No services match the current view.</strong>
          <div>Adjust the search or status filter.</div>
        </div>
      </div>
    </section>

    <section class="incidents-section" id="incidentsSection">
      <div class="group-label">Status Updates</div>
      {{if .Incidents}}
        {{range .Incidents}}
          <article class="inc-card {{if eq .ToneClass "warn"}}i-warn{{else if eq .ToneClass "ok"}}i-ok{{else if eq .ToneClass "down"}}i-down{{else}}i-unknown{{end}}">
            <div class="inc-head">
              <h3 class="inc-title">{{.Title}}</h3>
              <span class="inc-meta">{{.StateLabel}}</span>
            </div>
            <p class="inc-desc">{{.Summary}}</p>
            <div class="inc-update">{{.AffectedComponentsText}}</div>
            {{if .LatestUpdate}}
              <div class="inc-update">Latest update: {{.LatestUpdate}}</div>
            {{else if .ResolvedAtLabel}}
              <div class="inc-update">{{.ResolvedAtLabel}}</div>
            {{end}}
          </article>
        {{end}}
      {{else}}
        <article class="inc-card i-empty">
          <div class="inc-head">
            <h3 class="inc-title">No active incidents</h3>
            <span class="inc-meta">Stable</span>
          </div>
          <p class="inc-desc">We are not currently tracking any active public incidents.</p>
          <div class="inc-update">No active incidents.</div>
        </article>
      {{end}}
    </section>
  </main>

  {{if .ShowFooter}}
    <footer class="footer">
      <div class="container">
        <div class="footer-inner">
          <div class="footer-text">{{.FooterText}}</div>
          <div class="footer-shortcuts">
            <kbd>1</kbd> List
            <kbd>2</kbd> Compact
            <kbd>K</kbd> Kiosk
            <kbd>T</kbd> Theme
            <kbd>/</kbd> Search
            <kbd>Esc</kbd> Exit
          </div>
        </div>
      </div>
    </footer>
  {{end}}

  <script>
    (function () {
      const body = document.body;
      const defaultTheme = body.dataset.defaultTheme === 'light' ? 'light' : 'dark';
      const themeAllowed = body.dataset.allowThemeToggle === '1';
      const defaultRange = ['24h', '7d', '30d', '90d'].includes(body.dataset.defaultRange || '') ? body.dataset.defaultRange : '24h';
      const defaultMode = body.dataset.defaultMode || 'default';
      let clockTimer = null;
      let refreshTimer = null;
      let refreshInFlight = false;
      let refreshQueued = false;

      function getDom() {
        return {
          sectionGroups: Array.from(document.querySelectorAll('.monitor-section-group')),
          rows: Array.from(document.querySelectorAll('.monitor-card')),
          kioskTiles: Array.from(document.querySelectorAll('.kiosk-tile')),
          kioskSections: Array.from(document.querySelectorAll('.kiosk-section-group')),
          emptyState: document.getElementById('emptyState'),
          searchInput: document.getElementById('statusPageSearch'),
          statusFilter: document.getElementById('statusFilter'),
          rangeButtons: Array.from(document.querySelectorAll('[data-range-pill]')),
          modeButtons: Array.from(document.querySelectorAll('[data-mode]')),
          themeToggleBtn: document.getElementById('themeToggleBtn'),
          kioskExitBtn: document.getElementById('kioskExitBtn'),
          strips: Array.from(document.querySelectorAll('.js-strip[data-active-range]')),
          monitorUptimeValues: Array.from(document.querySelectorAll('[data-monitor-uptime-value]')),
          monitorUptimeLabels: Array.from(document.querySelectorAll('[data-monitor-uptime-label]')),
          sharedRangeLabels: Array.from(document.querySelectorAll('[data-shared-range-label]')),
          globalUptimeValue: document.getElementById('globalUptimeValue'),
          globalRangeStart: document.getElementById('globalRangeStart'),
          kioskTime: document.getElementById('kioskTime')
        };
      }

      function isValidRange(range) {
        return ['24h', '7d', '30d', '90d'].includes(range || '');
      }

      function getParams() {
        return new URLSearchParams(window.location.search);
      }

      function setParam(name, value) {
        const params = getParams();
        if (name === 'range') {
          params.set(name, value || defaultRange);
        } else if (name === 'mode') {
          if (!value || value === defaultMode) {
            params.delete(name);
          } else {
            params.set(name, value);
          }
        } else if (!value || value === 'all' || (name === 'theme' && value === defaultTheme)) {
          params.delete(name);
        } else {
          params.set(name, value);
        }
        const next = window.location.pathname + (params.toString() ? '?' + params.toString() : '');
        window.history.replaceState({}, '', next);
      }

      function parseStripData(raw) {
        if (!raw) return [];
        try {
          const parsed = JSON.parse(raw);
          return Array.isArray(parsed) ? parsed : [];
        } catch (err) {
          return [];
        }
      }

      function toneForUptime(value) {
        if (typeof value !== 'number' || value < 0) return 'nodata';
        if (value >= 99) return 'good';
        if (value >= 95) return 'warn';
        return 'bad';
      }

      function rangeLabel(range) {
        switch (range) {
          case '24h': return 'Last 24 hours';
          case '7d': return 'Last 7 days';
          case '90d': return 'Last 90 days';
          default: return 'Last 30 days';
        }
      }

      function monitorUptimeLabel(range) {
        switch (range) {
          case '24h': return '24h uptime';
          case '7d': return '7d uptime';
          case '90d': return '90d uptime';
          default: return '30d uptime';
        }
      }

      function stripCells(el, range) {
        const rangeCells = parseInt(el.getAttribute('data-cells-' + range) || '', 10);
        if (rangeCells > 0) {
          return rangeCells;
        }
        return Math.max(1, parseInt(el.dataset.cells || '24', 10) || 24);
      }

      function escapeAttr(value) {
        return String(value)
          .replace(/&/g, '&amp;')
          .replace(/</g, '&lt;')
          .replace(/>/g, '&gt;')
          .replace(/"/g, '&quot;');
      }

      function renderStrip(el, raw, range) {
        const points = parseStripData(raw);
        const cells = stripCells(el, range);
        const filled = points.slice(0, cells);
        while (filled.length < cells) {
          filled.push({ uptime: -1 });
        }
        el.innerHTML = filled.map(function (point) {
          const tone = toneForUptime(point.uptime);
          const label = escapeAttr(String(point.label || '').trim());
          const value = point.uptime >= 0 ? point.uptime.toFixed(2) + '%' : '';
          return '<span class="bar ' + tone + '" data-label="' + label + '" data-value="' + value + '"></span>';
        }).join('');
      }

      function setupBarTooltip() {
        const tip = document.createElement('div');
        tip.className = 'bar-tooltip';
        tip.setAttribute('aria-hidden', 'true');
        const tipValue = document.createElement('span');
        tipValue.className = 'tip-value';
        const tipLabel = document.createElement('span');
        tipLabel.className = 'tip-label';
        tip.appendChild(tipValue);
        tip.appendChild(tipLabel);
        document.body.appendChild(tip);

        function hideTip() {
          tip.classList.remove('visible');
        }

        document.addEventListener('mouseover', function (event) {
          const bar = event.target.closest('.js-strip .bar');
          if (!bar) {
            hideTip();
            return;
          }
          const value = bar.dataset.value || '';
          const label = bar.dataset.label || '';
          let tone = 'nodata';
          if (value) {
            tone = bar.classList.contains('bad') ? 'bad' : bar.classList.contains('warn') ? 'warn' : 'good';
          }
          tipValue.textContent = value || 'No data';
          tipValue.className = 'tip-value ' + tone;
          tipLabel.textContent = label;
          tipLabel.style.display = label ? '' : 'none';
          tip.classList.add('visible');

          const rect = bar.getBoundingClientRect();
          const half = tip.offsetWidth / 2;
          const left = Math.max(half + 8, Math.min(rect.left + rect.width / 2, window.innerWidth - half - 8));
          tip.style.left = left + 'px';
          tip.style.top = Math.max(rect.top - 8, 8) + 'px';
        });
        document.addEventListener('mouseout', function (event) {
          if (!event.relatedTarget) {
            hideTip();
          }
        });
        document.addEventListener('scroll', hideTip, true);
      }

      function applyRange(range, syncUrl) {
        const dom = getDom();
        const nextRange = isValidRange(range) ? range : defaultRange;
        dom.rangeButtons.forEach(function (btn) {
          const active = btn.dataset.rangePill === nextRange;
          btn.classList.toggle('active', active);
          btn.setAttribute('aria-pressed', active ? 'true' : 'false');
        });
        dom.sharedRangeLabels.forEach(function (el) {
          el.textContent = rangeLabel(nextRange);
        });
        if (dom.globalRangeStart) {
          dom.globalRangeStart.textContent = rangeLabel(nextRange);
        }
        if (dom.globalUptimeValue) {
          dom.globalUptimeValue.textContent = dom.globalUptimeValue.getAttribute('data-range-value-' + nextRange) || '—';
        }
        dom.monitorUptimeValues.forEach(function (el) {
          el.textContent = el.getAttribute('data-range-value-' + nextRange) || '—';
        });
        dom.monitorUptimeLabels.forEach(function (el) {
          el.textContent = monitorUptimeLabel(nextRange);
        });
        dom.strips.forEach(function (el) {
          el.dataset.activeRange = nextRange;
          renderStrip(el, el.getAttribute('data-range-' + nextRange), nextRange);
        });
        if (syncUrl) {
          setParam('range', nextRange);
        }
      }

      function applyTheme(theme, syncUrl) {
        const dom = getDom();
        const nextTheme = theme === 'light' ? 'light' : 'dark';
        body.dataset.theme = nextTheme;
        if (dom.themeToggleBtn) {
          dom.themeToggleBtn.textContent = nextTheme === 'dark' ? 'Light' : 'Dark';
        }
        if (syncUrl) {
          setParam('theme', nextTheme);
        }
        try {
          localStorage.setItem('status-page-theme', nextTheme);
        } catch (err) {}
      }

      function setMode(mode, syncUrl) {
        const dom = getDom();
        const nextMode = ['default', 'compact', 'kiosk'].includes(mode || '') ? mode : defaultMode;
        body.classList.remove('compact', 'kiosk');
        if (nextMode === 'compact') {
          body.classList.add('compact');
        } else if (nextMode === 'kiosk') {
          body.classList.add('kiosk');
        }
        dom.modeButtons.forEach(function (btn) {
          btn.classList.toggle('active', btn.dataset.mode === nextMode);
        });
        if (syncUrl) {
          setParam('mode', nextMode);
        }
        try {
          localStorage.setItem('status-page-mode', nextMode);
        } catch (err) {}
      }

      function applyViewState() {
        const dom = getDom();
        const query = dom.searchInput ? (dom.searchInput.value || '').trim().toLowerCase() : '';
        const activeStatus = dom.statusFilter ? (dom.statusFilter.value || 'all') : 'all';

        setParam('q', query);
        setParam('status', activeStatus);

        const visibleRows = dom.rows.filter(function (row) {
          const matchesQuery = !query || (row.dataset.search || '').includes(query);
          const matchesStatus = activeStatus === 'all' || row.dataset.status === activeStatus;
          const visible = matchesQuery && matchesStatus;
          row.classList.toggle('hidden', !visible);
          return visible;
        });

        dom.kioskTiles.forEach(function (tile) {
          const matchesQuery = !query || (tile.dataset.search || '').includes(query);
          const matchesStatus = activeStatus === 'all' || tile.dataset.status === activeStatus;
          tile.classList.toggle('hidden', !(matchesQuery && matchesStatus));
        });

        // While a search/status filter is active every matching section is
        // forced open so results stay visible; once cleared, the visitor's
        // saved collapse preferences are restored.
        const filtering = Boolean(query) || activeStatus !== 'all';
        const collapsedSections = filtering ? null : new Set(readCollapsedSectionIds());
        dom.sectionGroups.forEach(function (section) {
          const visibleCount = dom.rows.filter(function (row) {
            return row.closest('.monitor-section-group') === section && !row.classList.contains('hidden');
          }).length;
          section.classList.toggle('hidden', visibleCount === 0);
          if (filtering) {
            section.open = true;
          } else if (collapsedSections) {
            section.open = !collapsedSections.has(section.dataset.sectionId || '');
          }
        });

        dom.kioskSections.forEach(function (section) {
          const visibleTiles = dom.kioskTiles.filter(function (tile) {
            return tile.closest('.kiosk-section-group') === section && !tile.classList.contains('hidden');
          }).length;
          section.classList.toggle('hidden', visibleTiles === 0);
        });

        if (dom.emptyState) {
          dom.emptyState.classList.toggle('hidden', visibleRows.length > 0);
        }
      }

      function hydrateControlsFromUrl() {
        const dom = getDom();
        const params = getParams();
        if (dom.searchInput) dom.searchInput.value = params.get('q') || '';
        if (dom.statusFilter) dom.statusFilter.value = params.get('status') || 'all';

        const requestedRange = params.get('range');
        const initialRange = isValidRange(requestedRange) ? requestedRange : defaultRange;
        applyRange(initialRange, false);
        if (requestedRange && requestedRange !== initialRange) {
          setParam('range', initialRange);
        }

        let theme = params.get('theme');
        if (!themeAllowed) {
          theme = defaultTheme;
        }
        if (!theme) {
          try {
            theme = localStorage.getItem('status-page-theme') || '';
          } catch (err) {}
        }
        applyTheme(theme || defaultTheme, false);

        let mode = params.get('mode');
        if (!mode) {
          try {
            mode = localStorage.getItem('status-page-mode') || '';
          } catch (err) {}
        }
        setMode(mode || defaultMode, false);
      }

      function currentRange() {
        const requestedRange = getParams().get('range');
        if (isValidRange(requestedRange)) {
          return requestedRange;
        }
        const activeButton = getDom().rangeButtons.find(function (btn) {
          return btn.classList.contains('active');
        });
        if (activeButton && isValidRange(activeButton.dataset.rangePill)) {
          return activeButton.dataset.rangePill;
        }
        return defaultRange;
      }

      function currentMode() {
        if (body.classList.contains('kiosk')) {
          return 'kiosk';
        }
        if (body.classList.contains('compact')) {
          return 'compact';
        }
        return 'default';
      }

      function openMonitorIds() {
        return getDom().rows.filter(function (row) {
          return row.open && row.dataset.monitorId;
        }).map(function (row) {
          return row.dataset.monitorId;
        });
      }

      function restoreOpenMonitorIds(ids) {
        const open = new Set(ids || []);
        getDom().rows.forEach(function (row) {
          row.open = open.has(row.dataset.monitorId || '');
        });
      }

      function collapsedSectionsKey() {
        return 'status-page-collapsed-' + (body.dataset.statusPageSlug || '');
      }

      function readCollapsedSectionIds() {
        try {
          const parsed = JSON.parse(localStorage.getItem(collapsedSectionsKey()) || '[]');
          return Array.isArray(parsed) ? parsed : [];
        } catch (err) {
          return [];
        }
      }

      function saveCollapsedSectionIds() {
        const collapsed = getDom().sectionGroups.filter(function (section) {
          return !section.open && section.dataset.sectionId;
        }).map(function (section) {
          return section.dataset.sectionId;
        });
        try {
          localStorage.setItem(collapsedSectionsKey(), JSON.stringify(collapsed));
        } catch (err) {}
      }

      function replaceLiveRegion(id, nextDoc) {
        const current = document.getElementById(id);
        const incoming = nextDoc.getElementById(id);
        if (!current || !incoming || !current.parentNode) {
          return;
        }
        current.parentNode.replaceChild(incoming, current);
      }

      function setupControls() {
        document.addEventListener('input', function (event) {
          if (event.target && event.target.id === 'statusPageSearch') {
            applyViewState();
          }
        });
        document.addEventListener('change', function (event) {
          if (event.target && event.target.id === 'statusFilter') {
            applyViewState();
          }
        });
        document.addEventListener('click', function (event) {
          const sectionSummary = event.target.closest('.monitor-section-group > summary');
          if (sectionSummary) {
            // The details element toggles after this handler runs, so persist
            // the collapse state on the next tick.
            window.setTimeout(saveCollapsedSectionIds, 0);
            return;
          }

          const rangeBtn = event.target.closest('[data-range-pill]');
          if (rangeBtn) {
            applyRange(rangeBtn.dataset.rangePill || defaultRange, true);
            return;
          }

          const modeBtn = event.target.closest('[data-mode]');
          if (modeBtn) {
            setMode(modeBtn.dataset.mode || defaultMode, true);
            return;
          }

          if (themeAllowed && event.target.closest('#themeToggleBtn')) {
            applyTheme(body.dataset.theme === 'dark' ? 'light' : 'dark', true);
            return;
          }

          if (event.target.closest('#kioskExitBtn')) {
            setMode('default', true);
          }
        });
      }

      function setupShortcuts() {
        document.addEventListener('keydown', function (event) {
          if (event.ctrlKey || event.metaKey || event.altKey) {
            return;
          }
          const targetTag = event.target && event.target.tagName ? event.target.tagName.toLowerCase() : '';
          if (event.key !== 'Escape' && (targetTag === 'input' || targetTag === 'select' || targetTag === 'textarea')) {
            return;
          }
          const dom = getDom();
          if (event.key === '/') {
            if (dom.searchInput) {
              event.preventDefault();
              dom.searchInput.focus();
            }
            return;
          }
          if (event.key === '1') {
            setMode('default', true);
            return;
          }
          if (event.key === '2') {
            setMode('compact', true);
            return;
          }
          if (event.key === 'k' || event.key === 'K') {
            setMode('kiosk', true);
            return;
          }
          if ((event.key === 't' || event.key === 'T') && dom.themeToggleBtn && themeAllowed) {
            dom.themeToggleBtn.click();
            return;
          }
          if (event.key === 'Escape' && body.classList.contains('kiosk')) {
            setMode('default', true);
          }
        });
      }

      function updateClock() {
        const dom = getDom();
        if (!dom.kioskTime) return;
        dom.kioskTime.textContent = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      }

      function setupClock() {
        updateClock();
        if (clockTimer) {
          return;
        }
        clockTimer = window.setInterval(updateClock, 30000);
      }

      async function refreshLiveContent() {
        if (refreshInFlight) {
          refreshQueued = true;
          return;
        }

        refreshInFlight = true;
        refreshQueued = false;

        try {
          const expandedMonitorIds = openMonitorIds();
          const response = await fetch(window.location.pathname + window.location.search, {
            cache: 'no-store',
            headers: {
              'X-Requested-With': 'status-page-live-refresh'
            }
          });
          if (!response.ok) {
            return;
          }

          const html = await response.text();
          const nextDoc = new DOMParser().parseFromString(html, 'text/html');

          document.title = nextDoc.title || document.title;
          replaceLiveRegion('kioskStats', nextDoc);
          replaceLiveRegion('kioskGrid', nextDoc);
          replaceLiveRegion('statusHero', nextDoc);
          replaceLiveRegion('servicesList', nextDoc);
          replaceLiveRegion('incidentsSection', nextDoc);

          restoreOpenMonitorIds(expandedMonitorIds);
          applyTheme(body.dataset.theme || defaultTheme, false);
          setMode(currentMode(), false);
          applyRange(currentRange(), false);
          applyViewState();
          updateClock();
        } catch (err) {
        } finally {
          refreshInFlight = false;
          if (refreshQueued) {
            refreshQueued = false;
            void refreshLiveContent();
          }
        }
      }

      function setupLiveRefresh() {
        const slug = body.dataset.statusPageSlug;
        if (!slug || typeof EventSource === 'undefined') return;
        try {
          const source = new EventSource('/public/status/' + slug + '/stream');
          function scheduleRefresh() {
            if (refreshTimer) {
              window.clearTimeout(refreshTimer);
            }
            refreshTimer = window.setTimeout(function () {
              refreshTimer = null;
              void refreshLiveContent();
            }, 900);
          }
          source.addEventListener('update', scheduleRefresh);
          source.addEventListener('connected', function () {});
          source.addEventListener('heartbeat', function () {});
        } catch (err) {}
      }

      hydrateControlsFromUrl();
      setupControls();
      setupShortcuts();
      setupClock();
      setupBarTooltip();
      applyViewState();
      setupLiveRefresh();
    })();
  </script>
</body>
</html>`

package statuspage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yassinebenameur/probara/shared/statustemplate"
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
	// Tenant-authored branding injections. Typed CSS/HTML on purpose: the
	// tenant admin writes them for their own public page, so they bypass
	// autoescaping and are emitted verbatim into the default template.
	CustomCSS          template.CSS
	CustomHeadHTML     template.HTML
	CustomFooterHTML   template.HTML
	Sections           []statusPageSectionView
	Monitors           []statusPageMonitorView
	Incidents          []statusPageIncidentView
	MaintenanceWindows []statusPageMaintenanceView
}

const (
	defaultStatusPageRange  = "24h"
	globalUptimeStripCells  = statustemplate.GlobalUptimeStripCells
	monitorUptimeStripCells = statustemplate.MonitorUptimeStripCells
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

type statusPageMaintenanceView struct {
	Title                  string
	Description            string
	StateLabel             string
	IsActive               bool
	TimeRangeText          string
	AffectedComponentsText string
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
	CertExpiresSoon        bool
	History1hJSON          string
	History24hJSON         string
	History7dJSON          string
	History30dJSON         string
	History90dJSON         string
	ExpandedHistory30dJSON string
	ExpandedHistory90dJSON string
	History1yJSON          string
}

// The template function contract (typeIcon, strip cell counts, join) lives in
// shared/statustemplate so the admin API validates customer templates against
// the exact function map they render with.
var (
	globalStripCellCount   = statustemplate.GlobalStripCells
	monitorStripCellCount  = statustemplate.MonitorStripCells
	expandedStripCellCount = statustemplate.ExpandedStripCells
)

// publicStatusPageTmpl is parsed once at package init; the template source is
// large (~200KB), so re-parsing per request is wasteful. Execute is safe for
// concurrent use.
var publicStatusPageTmpl = template.Must(statustemplate.New("public_status_page", statustemplate.DefaultSource))

// customTemplateCache caches parsed customer templates keyed by
// pageID:version so steady-state renders skip re-parsing ~100KB of source per
// request. A publish creates a new version (new key); the map is reset
// wholesale when over cap (same eviction strategy as the slug cache).
const customTemplateCacheCap = 128

var customTemplateCache = struct {
	mu      sync.Mutex
	entries map[string]*template.Template
}{entries: make(map[string]*template.Template)}

func customTemplateFor(pageID string, version int, source string) (*template.Template, error) {
	key := fmt.Sprintf("%s:%d", pageID, version)
	customTemplateCache.mu.Lock()
	if tmpl, ok := customTemplateCache.entries[key]; ok {
		customTemplateCache.mu.Unlock()
		return tmpl, nil
	}
	customTemplateCache.mu.Unlock()

	tmpl, err := statustemplate.New("custom_status_page", source)
	if err != nil {
		return nil, err
	}

	customTemplateCache.mu.Lock()
	if len(customTemplateCache.entries) >= customTemplateCacheCap {
		customTemplateCache.entries = make(map[string]*template.Template)
	}
	customTemplateCache.entries[key] = tmpl
	customTemplateCache.mu.Unlock()
	return tmpl, nil
}

func renderPublicStatusPage(data *StatusPageData, apiEnabled bool) (string, error) {
	html, _, err := renderStatusPageHTML(data, apiEnabled)
	return html, err
}

// renderStatusPageHTML renders data with the page's published custom template
// when one exists, falling back to the built-in template if the custom one
// fails to parse or execute. customErr reports that failure so callers can
// log it; a page must keep serving on a broken template, never 500.
func renderStatusPageHTML(data *StatusPageData, apiEnabled bool) (html string, customErr error, err error) {
	view := buildStatusPageRenderView(data, apiEnabled)

	if strings.TrimSpace(data.CustomTemplateSource) != "" {
		tmpl, cerr := customTemplateFor(data.ID, data.CustomTemplateVersion, data.CustomTemplateSource)
		if cerr == nil {
			var buf bytes.Buffer
			if cerr = tmpl.Execute(&buf, view); cerr == nil {
				return buf.String(), nil, nil
			}
		}
		customErr = cerr
	}

	var buf bytes.Buffer
	if err := publicStatusPageTmpl.Execute(&buf, view); err != nil {
		return "", customErr, err
	}
	return buf.String(), customErr, nil
}

// renderStatusPageWithSource renders data with an explicit template source
// (draft previews). No fallback: a broken draft must surface its error to the
// author instead of silently rendering the default page.
func renderStatusPageWithSource(data *StatusPageData, apiEnabled bool, source string) (string, error) {
	tmpl, err := statustemplate.New("draft_status_page", source)
	if err != nil {
		return "", err
	}
	view := buildStatusPageRenderView(data, apiEnabled)
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, view); err != nil {
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
		CustomCSS:         template.CSS(strings.TrimSpace(data.CustomCSS)),
		CustomHeadHTML:    template.HTML(strings.TrimSpace(data.CustomHeadHTML)),
		CustomFooterHTML:  template.HTML(strings.TrimSpace(data.CustomFooterHTML)),
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
				// Planned maintenance is not an issue: it must not flip the
				// header to "Issues detected".
				view.MaintenanceCount++
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

	for _, window := range data.MaintenanceWindows {
		view.MaintenanceWindows = append(view.MaintenanceWindows, buildStatusPageMaintenanceView(window))
	}

	view.MonitorCount = len(view.Monitors)

	switch {
	case view.IssueCount > 0:
		view.OverallStatus = "issues"
		view.OverallTone = "warn"
		view.OverallStatusLabel = "Issues detected"
		view.OverallSummary = fmt.Sprintf("%d of %d services need attention", view.IssueCount, view.MonitorCount)
	case view.MaintenanceCount > 0:
		view.OverallStatus = "maintenance"
		view.OverallTone = "maint"
		view.OverallStatusLabel = "Maintenance in progress"
		view.OverallSummary = fmt.Sprintf("%d of %d services under planned maintenance", view.MaintenanceCount, view.MonitorCount)
	default:
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

// buildStatusPageMaintenanceView formats a maintenance window for the public
// template. Times are rendered in UTC so the label is unambiguous for viewers
// in any timezone.
func buildStatusPageMaintenanceView(window StatusPageMaintenanceWindow) statusPageMaintenanceView {
	stateLabel := "Scheduled"
	if window.IsActive {
		stateLabel = "In progress"
	}

	start := window.StartsAt.UTC()
	end := window.EndsAt.UTC()
	timeRangeText := fmt.Sprintf("%s – %s UTC", start.Format("Jan 2, 15:04"), end.Format("Jan 2, 15:04"))
	if start.Format("2006-01-02") == end.Format("2006-01-02") {
		timeRangeText = fmt.Sprintf("%s – %s UTC", start.Format("Jan 2, 15:04"), end.Format("15:04"))
	}

	affectedComponentsText := "Affected services: platform-wide"
	if len(window.AffectedMonitors) > 0 {
		affectedComponentsText = fmt.Sprintf("Affected services: %s", strings.Join(window.AffectedMonitors, ", "))
	}

	return statusPageMaintenanceView{
		Title:                  strings.TrimSpace(window.Title),
		Description:            strings.TrimSpace(window.Description),
		StateLabel:             stateLabel,
		IsActive:               window.IsActive,
		TimeRangeText:          timeRangeText,
		AffectedComponentsText: affectedComponentsText,
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
		CertExpiresSoon:        monitor.CertExpiresSoon,
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

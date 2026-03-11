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
	APIEnabled          bool
	ShowFooter          bool
	FooterText          string
	ShowGlobalUptime    bool
	ShowMonitorUptime   bool
	ShowMonitorURL      bool
	ShowMonitorTags     bool
	ShowMonitorTLS      bool
	ShowAgentMetrics    bool
	ShowToolbar         bool
	ShowTypeFilter      bool
	ShowLayoutControl   bool
	OverallStatus       string
	OverallStatusLabel  string
	OverallSummary      string
	OperationalCount    int
	IssueCount          int
	MonitorCount        int
	GlobalUptimePercent string
	GlobalUptime90JSON  string
	TypeOptions         []statusPageTypeOption
	Sections            []statusPageSectionView
	Monitors            []statusPageMonitorView
	Incidents           []statusPageIncidentView
}

type statusPageTypeOption struct {
	Value string
	Label string
}

type statusPageIncidentView struct {
	Name        string
	Status      string
	StatusLabel string
	Summary     string
}

type statusPageSectionView struct {
	ID           string
	Title        string
	MonitorCount int
	Monitors     []statusPageMonitorView
}

type statusPageMonitorView struct {
	ID                    string
	Name                  string
	URL                   string
	Type                  string
	TypeLabel             string
	Status                string
	StatusLabel           string
	StatusRank            int
	SearchText            string
	SummaryMetric         string
	SummaryCaption        string
	LastCheckAgo          string
	LastCheckUnix         int64
	LatencyText           string
	LatencySort           int
	UptimeText            string
	UptimeSort            float64
	TagsText              string
	Tags                  []string
	MonitorDetailLine     string
	TLSDetail             string
	History1hJSON         string
	History24hJSON        string
	History30dJSON        string
	History90dJSON        string
	History1yJSON         string
	AgentMetrics          *AgentMetricsData
	AgentMetricsTimestamp string
	PushMetrics           map[string]interface{}
}

func renderPublicStatusPage(data *StatusPageData, apiEnabled bool) (string, error) {
	view := buildStatusPageRenderView(data, apiEnabled)
	tmpl, err := template.New("public_status_page").Funcs(template.FuncMap{
		"join": strings.Join,
	}).Parse(publicStatusPageTemplate)
	if err != nil {
		return "", err
	}

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
		APIEnabled:        apiEnabled,
		ShowFooter:        data.ShowFooter,
		FooterText:        strings.TrimSpace(derefString(data.CustomFooterText)),
		ShowGlobalUptime:  data.ShowGlobalUptime,
		ShowMonitorUptime: data.ShowMonitorUptime,
		ShowMonitorURL:    data.ShowMonitorURL,
		ShowMonitorTags:   data.ShowMonitorTags,
		ShowMonitorTLS:    data.ShowMonitorTLS,
		ShowAgentMetrics:  data.ShowAgentMetrics,
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

	typeSet := make(map[string]struct{})
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
			if monitor.Status == "up" {
				view.OperationalCount++
			} else {
				view.IssueCount++
				view.Incidents = append(view.Incidents, statusPageIncidentView{
					Name:        monitor.Name,
					Status:      monitor.Status,
					StatusLabel: monitorStatusLabel(monitor.Status),
					Summary:     incidentSummary(monitor),
				})
			}

			typeSet[monitor.MonitorType] = struct{}{}
		}

		view.Sections = append(view.Sections, renderSection)
	}

	view.MonitorCount = len(view.Monitors)

	if view.IssueCount > 0 {
		view.OverallStatus = "issues"
		view.OverallStatusLabel = "Issues detected"
		view.OverallSummary = fmt.Sprintf("%d of %d services need attention", view.IssueCount, view.MonitorCount)
	} else {
		view.OverallStatus = "operational"
		view.OverallStatusLabel = "All systems operational"
		view.OverallSummary = fmt.Sprintf("%d monitored services are healthy", view.OperationalCount)
	}

	if uptime := dailyHistoryAverage(data.UptimeHistory90); uptime >= 0 {
		view.GlobalUptimePercent = fmt.Sprintf("%.2f%%", uptime)
	}
	view.GlobalUptime90JSON = mustJSON(sliceToBarPoints(data.UptimeHistory90))
	view.TypeOptions = sortedTypeOptions(typeSet)
	view.ShowToolbar = view.MonitorCount > 1
	view.ShowTypeFilter = len(view.TypeOptions) > 1
	view.ShowLayoutControl = view.MonitorCount > 4
	sort.Slice(view.Incidents, func(i, j int) bool {
		return monitorStatusRank(view.Incidents[i].Status) < monitorStatusRank(view.Incidents[j].Status)
	})

	return view
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

	return statusPageMonitorView{
		ID:                    monitor.ID,
		Name:                  monitor.Name,
		URL:                   monitor.URL,
		Type:                  monitor.MonitorType,
		TypeLabel:             typeLabel(monitor.MonitorType),
		Status:                monitor.Status,
		StatusLabel:           monitorStatusLabel(monitor.Status),
		StatusRank:            monitorStatusRank(monitor.Status),
		SearchText:            strings.ToLower(strings.Join(searchParts, " ")),
		SummaryMetric:         summaryMetric(monitor),
		SummaryCaption:        summaryCaption(monitor),
		LastCheckAgo:          lastCheckAgo,
		LastCheckUnix:         lastCheckUnix,
		LatencyText:           latencyText,
		LatencySort:           latencySort,
		UptimeText:            uptimeText,
		UptimeSort:            uptimeSort,
		TagsText:              strings.Join(monitor.Tags, ", "),
		Tags:                  monitor.Tags,
		MonitorDetailLine:     detailLine,
		TLSDetail:             tlsDetail,
		History1hJSON:         mustJSON(sliceToBarPoints(monitor.UptimeHistory1h)),
		History24hJSON:        mustJSON(sliceToBarPoints(monitor.UptimeHistory24h)),
		History30dJSON:        mustJSON(sliceToBarPoints(monitor.UptimeHistory30d)),
		History90dJSON:        mustJSON(sliceToBarPoints(monitor.UptimeHistory90d)),
		History1yJSON:         mustJSON(sliceToBarPoints(monitor.UptimeHistory365d)),
		AgentMetrics:          monitor.AgentMetrics,
		AgentMetricsTimestamp: relativeTime(monitor.AgentMetricsTimestamp()),
		PushMetrics:           monitor.PushMetrics,
	}
}

func (m MonitorStatus) AgentMetricsTimestamp() time.Time {
	if m.AgentMetrics == nil {
		return time.Time{}
	}
	return m.AgentMetrics.Timestamp
}

func sortedTypeOptions(typeSet map[string]struct{}) []statusPageTypeOption {
	values := make([]string, 0, len(typeSet))
	for value := range typeSet {
		values = append(values, value)
	}
	sort.Strings(values)

	options := make([]statusPageTypeOption, 0, len(values))
	for _, value := range values {
		options = append(options, statusPageTypeOption{
			Value: value,
			Label: typeLabel(value),
		})
	}
	return options
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

func incidentSummary(monitor MonitorStatus) string {
	if monitor.LastCheckTime != nil {
		return fmt.Sprintf("%s • Last check %s", monitorStatusLabel(monitor.Status), relativeTime(*monitor.LastCheckTime))
	}
	return monitorStatusLabel(monitor.Status)
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
	case "unknown":
		return 3
	case "up":
		return 4
	default:
		return 5
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

func dailyHistoryAverage(history []DailyUptime) float64 {
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
      --brand-primary: {{.PrimaryColor}};
      --brand-secondary: {{.SecondaryColor}};
      --radius-sm: 8px;
      --radius-md: 12px;
      --radius-lg: 14px;
      --shadow-soft: 0 2px 12px rgba(2, 6, 23, 0.18);
      --transition: 160ms ease;
      --font-sans: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      --font-mono: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    }

    body[data-theme="dark"] {
      --bg-base: #030712;
      --bg-elevated: rgba(15, 23, 42, 0.50);
      --bg-muted: rgba(15, 23, 42, 0.45);
      --surface: rgba(15, 23, 42, 0.60);
      --surface-soft: rgba(15, 23, 42, 0.40);
      --surface-strong: rgba(30, 41, 59, 0.50);
      --border-subtle: rgba(255, 255, 255, 0.06);
      --border-strong: rgba(255, 255, 255, 0.10);
      --text-primary: #f8fafc;
      --text-secondary: #94a3b8;
      --text-muted: #64748b;
      --success-bg: rgba(34, 197, 94, 0.12);
      --success-text: #86efac;
      --danger-bg: rgba(239, 68, 68, 0.12);
      --danger-text: #fca5a5;
      --warning-bg: rgba(234, 179, 8, 0.12);
      --warning-text: #fde047;
      --unknown-bg: rgba(100, 116, 139, 0.12);
      --unknown-text: #cbd5e1;
      --toolbar-bg: rgba(15, 23, 42, 0.55);
      --input-bg: rgba(2, 6, 23, 0.35);
    }

    body[data-theme="light"] {
      --bg-base: #f8fafc;
      --bg-elevated: rgba(255, 255, 255, 0.80);
      --bg-muted: rgba(255, 255, 255, 0.72);
      --surface: rgba(255, 255, 255, 0.85);
      --surface-soft: rgba(255, 255, 255, 0.70);
      --surface-strong: rgba(241, 245, 249, 0.80);
      --border-subtle: rgba(15, 23, 42, 0.06);
      --border-strong: rgba(15, 23, 42, 0.10);
      --text-primary: #0f172a;
      --text-secondary: #475569;
      --text-muted: #64748b;
      --success-bg: rgba(34, 197, 94, 0.10);
      --success-text: #15803d;
      --danger-bg: rgba(239, 68, 68, 0.10);
      --danger-text: #b91c1c;
      --warning-bg: rgba(234, 179, 8, 0.10);
      --warning-text: #a16207;
      --unknown-bg: rgba(148, 163, 184, 0.12);
      --unknown-text: #475569;
      --toolbar-bg: rgba(255, 255, 255, 0.72);
      --input-bg: rgba(248, 250, 252, 0.80);
    }

    * { box-sizing: border-box; margin: 0; }
    html { scroll-behavior: smooth; }
    body {
      margin: 0;
      min-height: 100vh;
      font-family: var(--font-sans);
      color: var(--text-primary);
      background: var(--bg-base);
      line-height: 1.5;
      -webkit-font-smoothing: antialiased;
    }
    body::before {
      content: "";
      position: fixed;
      inset: 0;
      pointer-events: none;
      background:
        radial-gradient(ellipse 80% 50% at 50% -20%, color-mix(in srgb, var(--brand-primary) 12%, transparent) 0%, transparent),
        radial-gradient(ellipse 60% 40% at 100% 0%, color-mix(in srgb, var(--brand-secondary) 8%, transparent) 0%, transparent);
      z-index: 0;
    }
    a { color: inherit; text-decoration: none; }
    button, select, input { font: inherit; }
    .page {
      position: relative;
      z-index: 1;
      max-width: 1040px;
      margin: 0 auto;
      padding: 24px 20px 40px;
    }
    .shell {
      display: grid;
      gap: 20px;
    }
    .glass {
      border: 1px solid var(--border-subtle);
      background: var(--bg-elevated);
      backdrop-filter: blur(12px);
    }
    .topbar {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      padding: 2px 0;
    }
    .brand {
      display: flex;
      align-items: center;
      gap: 10px;
      min-width: 0;
    }
    .brand-mark,
    .brand-logo {
      width: 28px;
      height: 28px;
      border-radius: 8px;
      flex-shrink: 0;
    }
    .brand-logo { object-fit: cover; }
    .brand-mark {
      display: grid;
      place-items: center;
      background: linear-gradient(135deg, color-mix(in srgb, var(--brand-primary) 70%, transparent), color-mix(in srgb, var(--brand-secondary) 76%, transparent));
      border: 1px solid color-mix(in srgb, var(--brand-primary) 24%, var(--border-subtle));
      color: white;
      font-size: 0.72rem;
      font-weight: 600;
    }
    .topbar-actions {
      display: flex;
      align-items: center;
      gap: 6px;
      flex-shrink: 0;
    }
    .ghost-btn,
    .pill-btn,
    .toolbar-control {
      border: 1px solid var(--border-subtle);
      background: var(--input-bg);
      color: var(--text-primary);
      border-radius: 999px;
      font-size: 0.75rem;
      transition: border-color var(--transition), background var(--transition);
    }
    .ghost-btn:hover,
    .pill-btn:hover,
    .toolbar-control:hover {
      border-color: var(--border-strong);
    }
    .ghost-btn {
      padding: 5px 11px;
      color: var(--text-muted);
      cursor: pointer;
      font-size: 0.72rem;
    }
    .hero {
      display: grid;
      gap: 12px;
      padding: 0;
    }
    .hero-head {
      display: grid;
      gap: 8px;
    }
    .hero-title {
      font-size: clamp(1.8rem, 4vw, 2.6rem);
      line-height: 1.1;
      letter-spacing: -0.03em;
      font-weight: 650;
      margin: 0;
    }
    .hero-description {
      margin: 0;
      color: var(--text-secondary);
      font-size: 0.95rem;
      line-height: 1.5;
      max-width: 640px;
    }
    .hero-summary {
      color: var(--text-secondary);
      font-size: 0.92rem;
      max-width: 640px;
    }
    .status-pill {
      display: inline-flex;
      align-items: center;
      gap: 5px;
      padding: 5px 10px;
      border-radius: 999px;
      width: fit-content;
      font-size: 0.75rem;
      font-weight: 600;
      flex-shrink: 0;
    }
    .status-pill.up { background: var(--success-bg); color: var(--success-text); }
    .status-pill.down, .status-pill.error { background: var(--danger-bg); color: var(--danger-text); }
    .status-pill.degraded { background: var(--warning-bg); color: var(--warning-text); }
    .status-pill.unknown { background: var(--unknown-bg); color: var(--unknown-text); }
    .status-pill-dot {
      width: 5px;
      height: 5px;
      border-radius: 999px;
      background: currentColor;
    }
    .hero-meta {
      display: flex;
      align-items: center;
      gap: 10px;
      flex-wrap: wrap;
    }
    .hero-meta-item {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      font-size: 0.82rem;
      color: var(--text-secondary);
    }
    .hero-meta-item::before {
      content: "";
      width: 4px;
      height: 4px;
      border-radius: 999px;
      background: var(--border-strong);
      flex-shrink: 0;
    }
    .hero-meta-item strong {
      font-weight: 600;
      color: var(--text-primary);
      font-size: 0.82rem;
    }
    .global-strip {
      display: grid;
      gap: 8px;
      padding-top: 2px;
    }
    .mini-strip-header {
      display: flex;
      justify-content: space-between;
      align-items: baseline;
      gap: 8px;
    }
    .mini-strip-title {
      font-size: 0.72rem;
      color: var(--text-muted);
    }
    .mini-strip-value {
      font-size: 0.82rem;
      font-weight: 700;
      letter-spacing: -0.03em;
    }
    .toolbar {
      display: flex;
      gap: 8px;
      align-items: center;
      border-radius: var(--radius-lg);
      padding: 10px 12px;
      background: var(--surface-soft);
      border: 1px solid var(--border-subtle);
      position: sticky;
      top: 12px;
      z-index: 3;
      backdrop-filter: blur(12px);
    }
    .search-wrap {
      flex: 1;
      min-width: 0;
    }
    .search-field {
      position: relative;
    }
    .search-icon {
      position: absolute;
      left: 12px;
      top: 50%;
      transform: translateY(-50%);
      color: var(--text-muted);
      font-size: 0.85rem;
    }
    .toolbar-control {
      width: 100%;
      min-height: 36px;
      padding: 0 10px;
      outline: none;
      font-size: 0.78rem;
    }
    .toolbar-label {
      display: block;
      margin-bottom: 6px;
      color: var(--text-muted);
      font-size: 0.68rem;
      text-transform: uppercase;
      letter-spacing: 0.08em;
    }
    .toolbar > label:not(.search-wrap) {
      width: 150px;
      flex-shrink: 0;
    }
    .search-input {
      padding-left: 34px;
    }
    .monitor-section-header,
    .incident-header {
      display: flex;
      justify-content: space-between;
      align-items: baseline;
      gap: 8px;
      margin-bottom: 10px;
    }
    .section-title {
      font-size: 0.85rem;
      font-weight: 600;
      letter-spacing: -0.02em;
      margin: 0;
    }
    .section-subtitle {
      margin: 0;
      font-size: 0.75rem;
      color: var(--text-muted);
    }
    .section-meta {
      color: var(--text-muted);
      font-size: 0.75rem;
    }
    .monitor-section-group {
      display: grid;
      gap: 12px;
      margin-top: 18px;
    }
    .monitor-section-group.hidden {
      display: none;
    }
    .monitor-section-group:first-of-type {
      margin-top: 0;
    }
    .monitor-subsection-header {
      display: flex;
      justify-content: space-between;
      align-items: baseline;
      gap: 8px;
    }
    .monitor-subsection-title {
      margin: 0;
      font-size: 0.95rem;
      font-weight: 600;
      color: var(--text-primary);
    }
    .monitor-grid {
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
      gap: 12px;
    }
    .monitor-grid[data-layout="condensed"] {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
    }
    .monitor-card {
      border-radius: var(--radius-lg);
      border: 1px solid var(--border-subtle);
      background: var(--surface);
      overflow: hidden;
      transition: border-color var(--transition);
    }
    .monitor-grid[data-layout="condensed"] .monitor-card {
      border-radius: 999px;
      margin: 0;
    }
    .monitor-card:hover {
      border-color: var(--border-strong);
    }
    .monitor-card[open] {
      border-color: color-mix(in srgb, var(--brand-primary) 20%, var(--border-strong));
    }
    .monitor-card summary {
      list-style: none;
      cursor: pointer;
      padding: 16px;
    }
    .monitor-grid[data-layout="condensed"] summary {
      padding: 6px 14px;
      pointer-events: none;
    }
    .monitor-card summary::-webkit-details-marker { display: none; }
    .monitor-head {
      display: flex;
      justify-content: space-between;
      gap: 10px;
      align-items: center;
    }
    .monitor-grid[data-layout="condensed"] .monitor-head {
      flex-direction: row-reverse;
      justify-content: flex-start;
      gap: 8px;
    }
    .monitor-title {
      margin: 0;
      font-size: 0.98rem;
      font-weight: 600;
      letter-spacing: -0.02em;
      line-height: 1.2;
      word-break: break-word;
    }
    .monitor-grid[data-layout="condensed"] .monitor-title {
      font-size: 0.8rem;
      font-weight: 500;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
      max-width: 220px;
    }
    .monitor-meta {
      margin-top: 6px;
      font-size: 0.78rem;
      color: var(--text-muted);
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
    }
    .monitor-meta span + span::before {
      content: "•";
      margin-right: 8px;
      color: var(--border-strong);
    }
    .monitor-grid[data-layout="condensed"] .monitor-meta {
      display: none !important;
    }
    .monitor-summary {
      text-align: right;
      flex-shrink: 0;
    }
    .monitor-summary-value {
      font-size: 1rem;
      font-weight: 700;
      letter-spacing: -0.02em;
    }
    .monitor-grid[data-layout="condensed"] .monitor-summary-value,
    .monitor-grid[data-layout="condensed"] .monitor-summary-label {
      display: none !important;
    }
    .monitor-summary-label {
      margin-top: 2px;
      font-size: 0.7rem;
      color: var(--text-muted);
    }
    .monitor-grid[data-layout="condensed"] .status-pill {
      font-size: 0;
      padding: 0;
      background: transparent !important;
      gap: 0;
    }
    .monitor-grid[data-layout="condensed"] .monitor-card[data-status="up"] {
      background: color-mix(in srgb, var(--success-bg) 60%, transparent);
      border-color: color-mix(in srgb, var(--success-bg) 80%, transparent);
    }
    .monitor-grid[data-layout="condensed"] .monitor-card[data-status="down"],
    .monitor-grid[data-layout="condensed"] .monitor-card[data-status="error"] {
      background: color-mix(in srgb, var(--danger-bg) 60%, transparent);
      border-color: color-mix(in srgb, var(--danger-bg) 80%, transparent);
    }
    .monitor-grid[data-layout="condensed"] .monitor-card[data-status="degraded"] {
      background: color-mix(in srgb, var(--warning-bg) 60%, transparent);
      border-color: color-mix(in srgb, var(--warning-bg) 80%, transparent);
    }
    .monitor-grid[data-layout="condensed"] .monitor-card[data-status="unknown"] {
      background: color-mix(in srgb, var(--unknown-bg) 60%, transparent);
      border-color: color-mix(in srgb, var(--unknown-bg) 80%, transparent);
    }
    .monitor-grid[data-layout="condensed"] .status-pill-dot {
      width: 8px;
      height: 8px;
    }
    .strip-wrap {
      margin-top: 14px;
    }
    .monitor-grid[data-layout="condensed"] .strip-wrap,
    .monitor-grid[data-layout="condensed"] .monitor-detail {
      display: none !important;
    }
    .strip-meta {
      display: flex;
      justify-content: space-between;
      gap: 8px;
      font-size: 0.72rem;
      color: var(--text-muted);
      margin-bottom: 6px;
    }
    .strip-meta span:last-child {
      font-family: var(--font-mono);
      font-size: 0.68rem;
    }
    .uptime-strip {
      display: grid;
      grid-auto-flow: column;
      grid-auto-columns: 1fr;
      gap: 2px;
      min-height: 22px;
      align-items: end;
    }
    .uptime-bar {
      width: 100%;
      min-width: 0;
      border-radius: 2px;
      background: color-mix(in srgb, var(--text-muted) 18%, transparent);
      height: 10px;
      opacity: 0.9;
      transition: opacity var(--transition);
    }
    .uptime-bar.good { background: #22c55e; }
    .uptime-bar.warn { background: #eab308; }
    .uptime-bar.bad { background: #ef4444; }
    .uptime-bar.nodata { background: color-mix(in srgb, var(--text-muted) 24%, transparent); }
    .monitor-card:hover .uptime-bar { opacity: 1; }
    .monitor-detail {
      display: grid;
      gap: 12px;
      padding: 0 16px 16px;
      border-top: 1px solid var(--border-subtle);
      background: color-mix(in srgb, var(--surface-strong) 50%, transparent);
    }
    .detail-grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 8px;
      margin-top: 12px;
    }
    .detail-item {
      padding: 10px 12px;
      border-radius: var(--radius-sm);
      border: 1px solid var(--border-subtle);
      background: var(--surface-soft);
    }
    .detail-label {
      font-size: 0.68rem;
      color: var(--text-muted);
      text-transform: uppercase;
      letter-spacing: 0.06em;
    }
    .detail-value {
      margin-top: 3px;
      color: var(--text-primary);
      font-size: 0.82rem;
      word-break: break-word;
    }
    .range-row {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 8px;
      margin-top: 12px;
    }
    .range-row h4 {
      margin: 0;
      font-size: 0.78rem;
      color: var(--text-secondary);
      font-weight: 500;
    }
    .range-controls {
      display: flex;
      gap: 4px;
    }
    .pill-btn {
      padding: 4px 9px;
      color: var(--text-muted);
      cursor: pointer;
      font-size: 0.72rem;
    }
    .pill-btn.active {
      background: color-mix(in srgb, var(--brand-primary) 14%, transparent);
      color: var(--text-primary);
      border-color: color-mix(in srgb, var(--brand-primary) 24%, var(--border-strong));
    }
    .detail-strip {
      min-height: 36px;
    }
    .detail-footer {
      display: flex;
      justify-content: flex-end;
      gap: 8px;
      align-items: center;
      color: var(--text-muted);
      font-size: 0.72rem;
      font-family: var(--font-mono);
    }
    .empty-state {
      display: grid;
      place-items: center;
      padding: 24px 16px;
      border-radius: var(--radius-lg);
      border: 1px dashed var(--border-strong);
      background: var(--surface-soft);
      color: var(--text-muted);
      text-align: center;
      font-size: 0.85rem;
    }
    .empty-state strong { font-weight: 600; color: var(--text-secondary); }
    .empty-state div { margin-top: 4px; font-size: 0.78rem; }
    .incident-list {
      display: grid;
      gap: 8px;
    }
    .incident-empty {
      color: var(--text-muted);
      font-size: 0.82rem;
      padding-top: 2px;
    }
    .incident-item {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      align-items: center;
      padding: 12px 14px;
      border-radius: var(--radius-lg);
      border: 1px solid var(--border-subtle);
      background: var(--surface);
    }
    .incident-item strong {
      display: block;
      font-size: 0.85rem;
      font-weight: 600;
      letter-spacing: -0.01em;
    }
    .incident-item span {
      display: block;
      margin-top: 2px;
      color: var(--text-muted);
      font-size: 0.78rem;
    }
    .footer {
      padding-top: 2px;
      display: flex;
      justify-content: space-between;
      gap: 8px;
      color: var(--text-muted);
      font-size: 0.72rem;
    }
    .hidden { display: none !important; }
    .customize-modal {
      position: fixed;
      inset: 0;
      background: rgba(2, 6, 23, 0.6);
      display: none;
      align-items: center;
      justify-content: center;
      padding: 20px;
      z-index: 20;
    }
    .customize-modal.open { display: flex; }
    .customize-panel {
      width: min(640px, 100%);
      max-height: min(90vh, 860px);
      overflow: auto;
      border-radius: var(--radius-lg);
      border: 1px solid var(--border-subtle);
      background: var(--surface);
      padding: 20px;
      box-shadow: var(--shadow-soft);
    }
    .customize-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 10px;
      margin-bottom: 16px;
    }
    .customize-title {
      font-size: 0.95rem;
      font-weight: 600;
      letter-spacing: -0.02em;
    }
    .customize-grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 10px;
    }
    .customize-field {
      display: grid;
      gap: 6px;
    }
    .customize-field.full {
      grid-column: 1 / -1;
    }
    .customize-field label {
      color: var(--text-muted);
      font-size: 0.75rem;
      font-weight: 500;
    }
    .customize-input,
    .customize-select,
    .customize-textarea {
      width: 100%;
      min-height: 38px;
      border-radius: var(--radius-sm);
      border: 1px solid var(--border-subtle);
      background: var(--input-bg);
      color: var(--text-primary);
      padding: 0 12px;
      font-size: 0.82rem;
    }
    .customize-textarea {
      min-height: 80px;
      padding: 10px 12px;
      resize: vertical;
    }
    .customize-toggle-grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 8px;
      margin-top: 6px;
    }
    .customize-toggle {
      display: flex;
      gap: 8px;
      align-items: center;
      color: var(--text-secondary);
      font-size: 0.78rem;
      padding: 10px 12px;
      border-radius: var(--radius-sm);
      border: 1px solid var(--border-subtle);
      background: var(--surface-soft);
    }
    .customize-footer {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 10px;
      margin-top: 14px;
    }
    .customize-status {
      color: var(--text-muted);
      font-size: 0.78rem;
    }
    .sr-only {
      position: absolute;
      width: 1px;
      height: 1px;
      padding: 0;
      margin: -1px;
      overflow: hidden;
      clip: rect(0, 0, 0, 0);
      white-space: nowrap;
      border: 0;
    }
    @media (max-width: 900px) {
      .toolbar { flex-wrap: wrap; }
      .toolbar > label:not(.search-wrap) { width: auto; flex: 1; min-width: 100px; }
    }
    @media (max-width: 640px) {
      .page { padding: 14px 12px 28px; }
      .topbar { padding: 6px 0; }
      .toolbar { padding: 8px; }
      .topbar,
      .monitor-head,
      .incident-item,
      .footer,
      .customize-footer { display: grid; grid-template-columns: 1fr; }
      .hero-head { gap: 6px; }
      .topbar-actions { justify-content: flex-start; }
      .detail-grid,
      .customize-grid,
      .customize-toggle-grid { grid-template-columns: 1fr; }
      .monitor-summary { text-align: left; }
      .range-row { align-items: start; flex-direction: column; }
      .toolbar > label:not(.search-wrap) { width: 100%; }
      .hero-title { font-size: 1.9rem; }
    }
  </style>
</head>
<body
  data-status-page-id="{{.ID}}"
  data-status-page-slug="{{.Slug}}"
  data-default-theme="{{.DefaultTheme}}"
  data-allow-theme-toggle="{{if .AllowThemeToggle}}1{{else}}0{{end}}"
  data-api-enabled="{{if .APIEnabled}}1{{else}}0{{end}}"
  data-show-monitor-tags="{{if .ShowMonitorTags}}1{{else}}0{{end}}"
  data-show-monitor-url="{{if .ShowMonitorURL}}1{{else}}0{{end}}"
  data-show-monitor-tls="{{if .ShowMonitorTLS}}1{{else}}0{{end}}"
  data-show-monitor-uptime="{{if .ShowMonitorUptime}}1{{else}}0{{end}}"
  data-show-agent-metrics="{{if .ShowAgentMetrics}}1{{else}}0{{end}}"
  data-show-global-uptime="{{if .ShowGlobalUptime}}1{{else}}0{{end}}"
>
  <main class="page">
    <div class="shell">
      <header class="topbar">
        <div class="brand">
          {{if .HasLogo}}
            <img class="brand-logo" src="{{.LogoURL}}" alt="{{.Title}}" />
          {{else}}
            <div class="brand-mark">{{printf "%.1s" .Title}}</div>
          {{end}}
        </div>
        <div class="topbar-actions">
          {{if .AllowThemeToggle}}
            <button type="button" class="ghost-btn" id="themeToggleBtn" aria-label="Toggle theme">Theme</button>
          {{end}}
          {{if .APIEnabled}}
            <button type="button" class="ghost-btn" id="customizeOpenBtn">Customize</button>
          {{end}}
        </div>
      </header>

      <section class="hero">
        <div class="hero-head">
          <div class="status-pill {{if eq .OverallStatus "operational"}}up{{else}}down{{end}}">
            <span class="status-pill-dot"></span>
            {{.OverallStatusLabel}}
          </div>
          <h1 class="hero-title">{{.Title}}</h1>
        </div>
        {{if .HasDescription}}
          <p class="hero-description">{{.Description}}</p>
        {{end}}
        <p class="hero-summary">{{.OverallSummary}}</p>
        <div class="hero-meta">
          <div class="hero-meta-item"><strong>{{.OperationalCount}}</strong> healthy</div>
          <div class="hero-meta-item"><strong>{{.MonitorCount}}</strong> monitored</div>
          {{if gt .IssueCount 0}}
            <div class="hero-meta-item"><strong>{{.IssueCount}}</strong> active issues</div>
          {{end}}
          {{if .GlobalUptimePercent}}
            <div class="hero-meta-item"><strong>{{.GlobalUptimePercent}}</strong> over 90 days</div>
          {{end}}
        </div>
        {{if .ShowGlobalUptime}}
          <div class="global-strip">
            <div class="mini-strip-header">
              <span class="mini-strip-title">90-day uptime</span>
              <span class="mini-strip-value">{{if .GlobalUptimePercent}}{{.GlobalUptimePercent}}{{else}}—{{end}}</span>
            </div>
            <div class="uptime-strip" data-uptime-source="{{.GlobalUptime90JSON}}" data-strip-height="10"></div>
          </div>
        {{end}}
      </section>

      {{if .ShowToolbar}}
        <section class="toolbar">
          <label class="search-wrap" for="statusPageSearch">
            <span class="toolbar-label">Search</span>
            <div class="search-field">
              <span class="search-icon">⌕</span>
              <span class="sr-only">Search services</span>
              <input id="statusPageSearch" class="toolbar-control search-input" type="search" placeholder="Search services…" />
            </div>
          </label>
          <label>
            <span class="toolbar-label">Status</span>
            <select id="statusFilter" class="toolbar-control">
              <option value="all">All statuses</option>
              <option value="up">Healthy</option>
              <option value="degraded">Degraded</option>
              <option value="down">Down</option>
              <option value="error">Error</option>
              <option value="unknown">Unknown</option>
            </select>
          </label>
          <label>
            <span class="toolbar-label">Sort</span>
            <select id="sortControl" class="toolbar-control">
              <option value="status">Status</option>
              <option value="name">Name</option>
              <option value="uptime">Uptime</option>
              <option value="latency">Latency</option>
              <option value="latest">Latest check</option>
            </select>
          </label>
          {{if .ShowTypeFilter}}
            <label>
              <span class="toolbar-label">Type</span>
              <select id="typeFilter" class="toolbar-control">
                <option value="all">All types</option>
                {{range .TypeOptions}}
                  <option value="{{.Value}}">{{.Label}}</option>
                {{end}}
              </select>
            </label>
          {{end}}
          {{if .ShowLayoutControl}}
            <label>
              <span class="toolbar-label">Layout</span>
              <select id="layoutControl" class="toolbar-control">
                <option value="detailed">Detailed</option>
                <option value="condensed">Condensed</option>
              </select>
            </label>
          {{end}}
        </section>
      {{end}}

      <section>
        <div class="monitor-section-header">
          <div class="section-meta" id="resultsCounter">{{.MonitorCount}} services</div>
        </div>
        {{range .Sections}}
          <div class="monitor-section-group" data-section-id="{{.ID}}">
            <div class="monitor-subsection-header">
              <h3 class="monitor-subsection-title">{{.Title}}</h3>
              <div class="section-meta">{{.MonitorCount}} services</div>
            </div>
            <div class="monitor-grid section-monitor-grid" data-layout="detailed">
              {{range .Monitors}}
            <details
              class="monitor-card"
              data-search="{{.SearchText}}"
              data-status="{{.Status}}"
              data-type="{{.Type}}"
              data-uptime="{{printf "%.2f" .UptimeSort}}"
              data-latency="{{.LatencySort}}"
              data-latest="{{.LastCheckUnix}}"
              data-status-rank="{{.StatusRank}}"
            >
              <summary>
                <div class="monitor-head">
                  <div>
                    <h3 class="monitor-title">{{.Name}}</h3>
                    <div class="monitor-meta">
                      <span>{{.TypeLabel}}</span>
                      <span>{{.MonitorDetailLine}}</span>
                    </div>
                  </div>
                  <div class="monitor-summary">
                    <div class="status-pill {{.Status}}">
                      <span class="status-pill-dot"></span>
                      {{.StatusLabel}}
                    </div>
                    <div class="monitor-summary-value">{{.SummaryMetric}}</div>
                    <div class="monitor-summary-label">{{.SummaryCaption}}</div>
                  </div>
                </div>
                {{if $.ShowMonitorUptime}}
                  <div class="strip-wrap">
                    <div class="strip-meta">
                      <span>90-day uptime</span>
                      <span>{{.LastCheckAgo}}</span>
                    </div>
                    <div class="uptime-strip" data-uptime-source="{{.History90dJSON}}" data-strip-height="12"></div>
                  </div>
                {{end}}
              </summary>
              <div class="monitor-detail">
                <div class="detail-grid">
                  <div class="detail-item">
                    <div class="detail-label">Last check</div>
                    <div class="detail-value">{{.LastCheckAgo}}</div>
                  </div>
                  <div class="detail-item">
                    <div class="detail-label">Latest latency</div>
                    <div class="detail-value">{{.LatencyText}}</div>
                  </div>
                  <div class="detail-item">
                    <div class="detail-label">24h uptime</div>
                    <div class="detail-value">{{.UptimeText}}</div>
                  </div>
                  <div class="detail-item">
                    <div class="detail-label">Monitor type</div>
                    <div class="detail-value">{{.TypeLabel}}</div>
                  </div>
                  {{if $.ShowMonitorURL}}
                    <div class="detail-item">
                      <div class="detail-label">Target</div>
                      <div class="detail-value">{{if .URL}}{{.URL}}{{else}}—{{end}}</div>
                    </div>
                  {{end}}
                  {{if and $.ShowMonitorTags .Tags}}
                    <div class="detail-item">
                      <div class="detail-label">Tags</div>
                      <div class="detail-value">{{join .Tags ", "}}</div>
                    </div>
                  {{end}}
                  {{if and $.ShowMonitorTLS .TLSDetail}}
                    <div class="detail-item">
                      <div class="detail-label">TLS</div>
                      <div class="detail-value">{{.TLSDetail}}</div>
                    </div>
                  {{end}}
                  {{if and $.ShowAgentMetrics .AgentMetrics}}
                    <div class="detail-item">
                      <div class="detail-label">CPU</div>
                      <div class="detail-value">{{printf "%.1f%%" .AgentMetrics.CPUPercent}}</div>
                    </div>
                    <div class="detail-item">
                      <div class="detail-label">Memory</div>
                      <div class="detail-value">{{printf "%.1f%%" .AgentMetrics.MemoryPercent}}</div>
                    </div>
                  {{end}}
                </div>
                {{if $.ShowMonitorUptime}}
                  <div class="range-row">
                    <h4>Uptime history</h4>
                    <div class="range-controls">
                      <button type="button" class="pill-btn" data-range="1h">1h</button>
                      <button type="button" class="pill-btn" data-range="24h">24h</button>
                      <button type="button" class="pill-btn" data-range="30d">30d</button>
                      <button type="button" class="pill-btn active" data-range="90d">90d</button>
                      <button type="button" class="pill-btn" data-range="1y">1y</button>
                    </div>
                  </div>
                  <div
                    class="uptime-strip detail-strip"
                    data-active-range="90d"
                    data-range-1h="{{.History1hJSON}}"
                    data-range-24h="{{.History24hJSON}}"
                    data-range-30d="{{.History30dJSON}}"
                    data-range-90d="{{.History90dJSON}}"
                    data-range-1y="{{.History1yJSON}}"
                    data-strip-height="16"
                  ></div>
                  <div class="detail-footer">
                    <span>{{.LastCheckAgo}}</span>
                  </div>
                {{end}}
              </div>
            </details>
              {{end}}
            </div>
          </div>
        {{end}}
        <div class="empty-state hidden" id="emptyState">
          <div>
            <strong>No services match the current view.</strong>
            <div>Adjust the search, filter, or sort controls.</div>
          </div>
        </div>
      </section>

      <section>
        <div class="incident-header">
          <div>
            <h2 class="section-title">Incidents</h2>
          </div>
        </div>
        {{if .Incidents}}
          <div class="incident-list">
            {{range .Incidents}}
              <article class="incident-item">
                <div>
                  <strong>{{.Name}}</strong>
                  <span>{{.Summary}}</span>
                </div>
                <div class="status-pill {{.Status}}">
                  <span class="status-pill-dot"></span>
                  {{.StatusLabel}}
                </div>
              </article>
            {{end}}
          </div>
        {{else}}
          <p class="incident-empty">No active incidents.</p>
        {{end}}
      </section>

      {{if .ShowFooter}}
        <footer class="footer">
          <span>{{.FooterText}}</span>
          <span>Live status page · /{{.Slug}}</span>
        </footer>
      {{end}}
    </div>
  </main>

  {{if .APIEnabled}}
    <div class="customize-modal" id="customizeModal" aria-hidden="true">
      <div class="customize-panel">
        <div class="customize-header">
          <div class="customize-title">Customize status page</div>
          <button type="button" class="ghost-btn" id="customizeCloseBtn">Close</button>
        </div>
        <div class="customize-grid">
          <div class="customize-field full">
            <label for="customizeTitle">Title</label>
            <input id="customizeTitle" class="customize-input" type="text" value="{{.Title}}" />
          </div>
          <div class="customize-field full">
            <label for="customizeDescription">Description</label>
            <textarea id="customizeDescription" class="customize-textarea">{{.Description}}</textarea>
          </div>
          <div class="customize-field">
            <label for="customizePrimaryColor">Primary color</label>
            <input id="customizePrimaryColor" class="customize-input" type="text" value="{{.PrimaryColor}}" />
          </div>
          <div class="customize-field">
            <label for="customizeSecondaryColor">Secondary color</label>
            <input id="customizeSecondaryColor" class="customize-input" type="text" value="{{.SecondaryColor}}" />
          </div>
          <div class="customize-field">
            <label for="customizeDefaultTheme">Default theme</label>
            <select id="customizeDefaultTheme" class="customize-select">
              <option value="dark" {{if eq .DefaultTheme "dark"}}selected{{end}}>Dark</option>
              <option value="light" {{if eq .DefaultTheme "light"}}selected{{end}}>Light</option>
            </select>
          </div>
          <div class="customize-field">
            <label for="customizeFooterText">Footer text</label>
            <input id="customizeFooterText" class="customize-input" type="text" value="{{.FooterText}}" />
          </div>
          <div class="customize-field full">
            <label>Visibility</label>
            <div class="customize-toggle-grid">
              <label class="customize-toggle"><input id="toggleThemeSwitch" type="checkbox" {{if .AllowThemeToggle}}checked{{end}} /> Allow theme toggle</label>
              <label class="customize-toggle"><input id="toggleGlobalUptime" type="checkbox" {{if .ShowGlobalUptime}}checked{{end}} /> Show global uptime</label>
              <label class="customize-toggle"><input id="toggleMonitorUptime" type="checkbox" {{if .ShowMonitorUptime}}checked{{end}} /> Show monitor uptime</label>
              <label class="customize-toggle"><input id="toggleMonitorURL" type="checkbox" {{if .ShowMonitorURL}}checked{{end}} /> Show monitor targets</label>
              <label class="customize-toggle"><input id="toggleMonitorTags" type="checkbox" {{if .ShowMonitorTags}}checked{{end}} /> Show monitor tags</label>
              <label class="customize-toggle"><input id="toggleMonitorTLS" type="checkbox" {{if .ShowMonitorTLS}}checked{{end}} /> Show TLS details</label>
              <label class="customize-toggle"><input id="toggleAgentMetrics" type="checkbox" {{if .ShowAgentMetrics}}checked{{end}} /> Show agent metrics</label>
              <label class="customize-toggle"><input id="toggleFooter" type="checkbox" {{if .ShowFooter}}checked{{end}} /> Show footer</label>
            </div>
          </div>
        </div>
        <div class="customize-footer">
          <div class="customize-status" id="customizeStatus"></div>
          <button type="button" class="ghost-btn" id="customizeSaveBtn">Save changes</button>
        </div>
      </div>
    </div>
  {{end}}

  <script>
    (function () {
      const body = document.body;
      const sectionGroups = Array.from(document.querySelectorAll(".monitor-section-group"));
      const monitorGrids = Array.from(document.querySelectorAll(".section-monitor-grid"));
      const emptyState = document.getElementById("emptyState");
      const resultsCounter = document.getElementById("resultsCounter");
      const searchInput = document.getElementById("statusPageSearch");
      const statusFilter = document.getElementById("statusFilter");
      const typeFilter = document.getElementById("typeFilter");
      const sortControl = document.getElementById("sortControl");
      const layoutControl = document.getElementById("layoutControl");
      const themeToggleBtn = document.getElementById("themeToggleBtn");
      const cards = Array.from(document.querySelectorAll(".monitor-card"));
      const defaultTheme = body.dataset.defaultTheme === "light" ? "light" : "dark";
      const themeAllowed = body.dataset.allowThemeToggle === "1";

      function getParams() {
        return new URLSearchParams(window.location.search);
      }

      function setParam(name, value) {
        const params = getParams();
        if (!value || value === "all" || (name === "sort" && value === "status") || (name === "theme" && value === defaultTheme)) {
          params.delete(name);
        } else {
          params.set(name, value);
        }
        const next = window.location.pathname + (params.toString() ? "?" + params.toString() : "");
        window.history.replaceState({}, "", next);
      }

      function applyTheme(theme, syncUrl) {
        const nextTheme = theme === "light" ? "light" : "dark";
        body.dataset.theme = nextTheme;
        if (syncUrl) {
          setParam("theme", nextTheme);
        }
        try {
          localStorage.setItem("status-page-theme", nextTheme);
        } catch (err) {}
      }

      function statusSortRank(status) {
        switch (status) {
          case "down": return 0;
          case "error": return 1;
          case "degraded": return 2;
          case "unknown": return 3;
          case "up": return 4;
          default: return 5;
        }
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
        if (typeof value !== "number" || value < 0) return "nodata";
        if (value >= 99) return "good";
        if (value >= 95) return "warn";
        return "bad";
      }

      function renderStrip(el, raw, defaultHeight) {
        const points = parseStripData(raw);
        const height = parseInt(el.dataset.stripHeight || defaultHeight || "12", 10);
        if (!points.length) {
          el.innerHTML = '<span class="uptime-bar nodata" style="height:' + Math.max(10, height) + 'px"></span>';
          return;
        }
        el.innerHTML = points.map((point) => {
          const tone = toneForUptime(point.uptime);
          const barHeight = Math.max(8, Math.round((height * (tone === "bad" ? 1 : tone === "warn" ? 0.92 : 0.88))));
          return '<span class="uptime-bar ' + tone + '" style="height:' + barHeight + 'px" title="' + String(point.label || "") + ': ' + (point.uptime >= 0 ? point.uptime.toFixed(2) + '%' : 'No data') + '"></span>';
        }).join("");
      }

      function renderAllStrips() {
        document.querySelectorAll(".uptime-strip[data-uptime-source]").forEach((el) => {
          renderStrip(el, el.getAttribute("data-uptime-source"), 12);
        });
        document.querySelectorAll(".detail-strip").forEach((el) => {
          const range = el.dataset.activeRange || "90d";
          renderStrip(el, el.getAttribute("data-range-" + range), 16);
        });
      }

      function hydrateControlsFromUrl() {
        const params = getParams();
        if (searchInput) searchInput.value = params.get("q") || "";
        if (statusFilter) statusFilter.value = params.get("status") || "all";
        if (typeFilter) typeFilter.value = params.get("type") || "all";
        if (sortControl) sortControl.value = params.get("sort") || "status";
        if (layoutControl) {
          layoutControl.value = params.get("layout") || "detailed";
          monitorGrids.forEach((grid) => grid.setAttribute("data-layout", layoutControl.value));
        }

        const urlTheme = params.get("theme");
        let theme = urlTheme;
        if (!themeAllowed) {
          theme = defaultTheme;
        }
        if (!theme) {
          try {
            theme = localStorage.getItem("status-page-theme") || "";
          } catch (err) {}
        }
        applyTheme(theme || defaultTheme, false);
      }

      function applyViewState() {
        const query = searchInput ? (searchInput.value || "").trim().toLowerCase() : "";
        const activeStatus = statusFilter ? (statusFilter.value || "all") : "all";
        const activeType = typeFilter ? (typeFilter.value || "all") : "all";
        const sortKey = sortControl ? (sortControl.value || "status") : "status";
        const activeLayout = layoutControl ? (layoutControl.value || "detailed") : "detailed";

        setParam("q", query);
        setParam("status", activeStatus);
        setParam("type", activeType);
        setParam("sort", sortKey);
        setParam("layout", activeLayout);

        if (layoutControl) {
          monitorGrids.forEach((grid) => grid.setAttribute("data-layout", activeLayout));
        }

        const filtered = cards.filter((card) => {
          const matchesQuery = !query || (card.dataset.search || "").includes(query);
          const matchesStatus = activeStatus === "all" || card.dataset.status === activeStatus;
          const matchesType = activeType === "all" || card.dataset.type === activeType;
          const visible = matchesQuery && matchesStatus && matchesType;
          card.classList.toggle("hidden", !visible);
          return visible;
        });

        filtered.sort((a, b) => {
          const nameA = (a.querySelector(".monitor-title")?.textContent || "").toLowerCase();
          const nameB = (b.querySelector(".monitor-title")?.textContent || "").toLowerCase();

          if (sortKey === "name") {
            return nameA.localeCompare(nameB);
          }
          if (sortKey === "uptime") {
            const uptimeA = parseFloat(a.dataset.uptime || "-1");
            const uptimeB = parseFloat(b.dataset.uptime || "-1");
            if (uptimeA !== uptimeB) return uptimeB - uptimeA;
            return nameA.localeCompare(nameB);
          }
          if (sortKey === "latency") {
            const latencyA = parseInt(a.dataset.latency || "-1", 10);
            const latencyB = parseInt(b.dataset.latency || "-1", 10);
            const safeA = latencyA < 0 ? Number.MAX_SAFE_INTEGER : latencyA;
            const safeB = latencyB < 0 ? Number.MAX_SAFE_INTEGER : latencyB;
            if (safeA !== safeB) return safeA - safeB;
            return nameA.localeCompare(nameB);
          }
          if (sortKey === "latest") {
            const latestA = parseInt(a.dataset.latest || "0", 10);
            const latestB = parseInt(b.dataset.latest || "0", 10);
            if (latestA !== latestB) return latestB - latestA;
            return nameA.localeCompare(nameB);
          }

          const rankA = statusSortRank(a.dataset.status || "");
          const rankB = statusSortRank(b.dataset.status || "");
          if (rankA !== rankB) return rankA - rankB;
          return nameA.localeCompare(nameB);
        });

        sectionGroups.forEach((section) => {
          const grid = section.querySelector(".section-monitor-grid");
          if (!grid) return;
          const sectionCards = filtered.filter((card) => card.closest(".monitor-section-group") === section);
          sectionCards.forEach((card) => grid.appendChild(card));
          section.classList.toggle("hidden", sectionCards.length === 0);
        });
        if (emptyState) {
          emptyState.classList.toggle("hidden", filtered.length > 0);
        }
        if (resultsCounter) {
          resultsCounter.textContent = filtered.length + (filtered.length === 1 ? " service" : " services");
        }
      }

      function setupRangeButtons() {
        document.querySelectorAll(".monitor-card").forEach((card) => {
          const detailStrip = card.querySelector(".detail-strip");
          if (!detailStrip) return;
          card.querySelectorAll(".pill-btn[data-range]").forEach((btn) => {
            btn.addEventListener("click", function (event) {
              event.preventDefault();
              event.stopPropagation();
              const nextRange = btn.dataset.range || "90d";
              detailStrip.dataset.activeRange = nextRange;
              card.querySelectorAll(".pill-btn[data-range]").forEach((other) => {
                other.classList.toggle("active", other === btn);
              });
              renderStrip(detailStrip, detailStrip.getAttribute("data-range-" + nextRange), 16);
            });
          });
        });
      }

      function setupThemeToggle() {
        if (!themeToggleBtn || !themeAllowed) return;
        themeToggleBtn.addEventListener("click", function () {
          applyTheme(body.dataset.theme === "dark" ? "light" : "dark", true);
        });
      }

      function setupControls() {
        [searchInput, statusFilter, typeFilter, sortControl, layoutControl].forEach((el) => {
          if (!el) return;
          el.addEventListener("input", applyViewState);
          el.addEventListener("change", applyViewState);
        });
      }

      function setupCustomizer() {
        const openBtn = document.getElementById("customizeOpenBtn");
        const closeBtn = document.getElementById("customizeCloseBtn");
        const saveBtn = document.getElementById("customizeSaveBtn");
        const modal = document.getElementById("customizeModal");
        const statusEl = document.getElementById("customizeStatus");
        if (!openBtn || !closeBtn || !saveBtn || !modal) return;

        function openModal() {
          modal.classList.add("open");
          modal.setAttribute("aria-hidden", "false");
        }

        function closeModal() {
          modal.classList.remove("open");
          modal.setAttribute("aria-hidden", "true");
        }

        openBtn.addEventListener("click", openModal);
        closeBtn.addEventListener("click", closeModal);
        modal.addEventListener("click", function (event) {
          if (event.target === modal) closeModal();
        });

        saveBtn.addEventListener("click", async function () {
          const pageID = body.dataset.statusPageId;
          if (!pageID) return;

          const payload = {
            title: String(document.getElementById("customizeTitle").value || "").trim(),
            description: String(document.getElementById("customizeDescription").value || "").trim(),
            primary_color: String(document.getElementById("customizePrimaryColor").value || "").trim(),
            secondary_color: String(document.getElementById("customizeSecondaryColor").value || "").trim(),
            settings: {
              default_theme: String(document.getElementById("customizeDefaultTheme").value || "dark"),
              allow_theme_toggle: !!document.getElementById("toggleThemeSwitch").checked,
              show_global_uptime: !!document.getElementById("toggleGlobalUptime").checked,
              show_monitor_uptime: !!document.getElementById("toggleMonitorUptime").checked,
              show_monitor_url: !!document.getElementById("toggleMonitorURL").checked,
              show_monitor_tags: !!document.getElementById("toggleMonitorTags").checked,
              show_monitor_tls: !!document.getElementById("toggleMonitorTLS").checked,
              show_agent_metrics: !!document.getElementById("toggleAgentMetrics").checked,
              show_footer: !!document.getElementById("toggleFooter").checked,
              footer_text: String(document.getElementById("customizeFooterText").value || "").trim()
            }
          };

          if (!payload.title) {
            statusEl.textContent = "Title is required.";
            return;
          }

          statusEl.textContent = "Saving changes...";
          saveBtn.disabled = true;
          try {
            const response = await fetch("/_sp_api/api/v1/status-pages/" + pageID, {
              method: "PATCH",
              credentials: "include",
              headers: {
                "Content-Type": "application/json",
                "X-Status-Page-ID": pageID
              },
              body: JSON.stringify(payload)
            });

            if (!response.ok) {
              throw new Error("Request failed with status " + response.status);
            }

            statusEl.textContent = "Saved. Reloading…";
            window.location.reload();
          } catch (error) {
            statusEl.textContent = "Failed to save changes.";
          } finally {
            saveBtn.disabled = false;
          }
        });
      }

      function setupLiveRefresh() {
        const slug = body.dataset.statusPageSlug;
        if (!slug || typeof EventSource === "undefined") return;
        try {
          const source = new EventSource("/public/status/" + slug + "/stream");
          let refreshTimer = null;
          function scheduleRefresh() {
            if (refreshTimer) {
              window.clearTimeout(refreshTimer);
            }
            refreshTimer = window.setTimeout(function () {
              window.location.reload();
            }, 900);
          }
          source.addEventListener("update", scheduleRefresh);
          source.addEventListener("connected", function () {});
          source.addEventListener("heartbeat", function () {});
        } catch (err) {}
      }

      hydrateControlsFromUrl();
      renderAllStrips();
      setupRangeButtons();
      setupThemeToggle();
      setupControls();
      applyViewState();
      setupCustomizer();
      setupLiveRefresh();
    })();
  </script>
</body>
</html>`

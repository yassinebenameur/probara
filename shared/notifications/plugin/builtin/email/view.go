package email

import (
	"fmt"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/metricstore"
	"github.com/yassinebenameur/probara/shared/notifications"
)

// Brand tokens, shared with the operator UI (web/components/ui/BrandMark.tsx)
// and status pages (shared/statustemplate/default.gohtml). Email clients cannot
// use CSS variables reliably, so the values are duplicated here as constants and
// interpolated inline.
const (
	brandOrange = "#ff5a24"
	brandInk    = "#140a05"
)

// tone is the status colour triple used by one alert email: a saturated accent
// for bars and numbers, a pale tint for callout backgrounds, and a darker text
// shade that stays legible on that tint. Values are the light-theme status
// colours from the status-page palette, darkened where needed for contrast on
// white.
type tone struct {
	Accent string
	Tint   string
	OnTint string
	Label  string // short status word, e.g. "DOWN"
}

var (
	toneDown = tone{Accent: "#da1b69", Tint: "#fdeff4", OnTint: "#a11350", Label: "DOWN"}
	toneUp   = tone{Accent: "#00a35f", Tint: "#ecfaf3", OnTint: "#007845", Label: "RECOVERED"}
	toneWarn = tone{Accent: "#d38d00", Tint: "#fdf6e8", OnTint: "#946200", Label: "WARNING"}
)

// detailRow is one label/value pair in the metadata table.
type detailRow struct {
	Label string
	Value string
}

// locationLine is one failing vantage point.
type locationLine struct {
	Name      string
	DownSince string
}

// metricPanel is the large-number readout used by the kinds that carry a
// measurement (latency anomaly, host metric, certificate expiry).
type metricPanel struct {
	Label string
	Value string
	Note  string
}

// alertView is the fully-resolved presentation model for one alert email. Both
// the HTML template and the plain-text body render from it, so the two parts of
// a multipart message can never drift apart.
type alertView struct {
	Tone tone

	Label       string // headline label, e.g. "Alert Triggered"
	MonitorName string
	Summary     string // one human sentence describing what happened
	Preheader   string // inbox preview text

	Metric    *metricPanel
	LastError string
	RootCause string
	Locations []locationLine
	Rows      []detailRow

	ActionURL   string
	ActionLabel string

	AlertID  string
	TenantID string
	SentAt   string
}

// newAlertView resolves an event into the presentation model. appBaseURL is the
// public operator-UI origin; empty disables the call-to-action button.
func newAlertView(event notifications.AlertEvent, appBaseURL string) alertView {
	alert := event.Alert
	eventType := event.Type
	resolved := eventType == "resolved" || alert.Status == "resolved"
	// The event's own timestamp is the clock reference, not time.Now(): a queued
	// or retried dispatch must not inflate the reported outage length, and it
	// keeps rendering deterministic.
	elapsed := outageDuration(alert, eventType, eventTimestamp(event))

	v := alertView{
		Tone:        toneFor(alert, eventType, resolved),
		Label:       DefaultLabel(event),
		MonitorName: strings.TrimSpace(alert.MonitorName),
		AlertID:     alert.ID,
		TenantID:    event.TenantID,
		SentAt:      humanTime(eventTimestamp(event)),
	}
	if v.MonitorName == "" {
		v.MonitorName = "Unnamed monitor"
	}
	v.Summary = summaryFor(alert, eventType, resolved, elapsed)
	v.Preheader = v.Summary
	v.Metric = metricFor(alert, resolved)

	if alert.LastError != nil {
		v.LastError = strings.TrimSpace(*alert.LastError)
	}
	if alert.RootCauseMonitorName != nil && strings.TrimSpace(*alert.RootCauseMonitorName) != "" {
		v.RootCause = strings.TrimSpace(*alert.RootCauseMonitorName)
		if alert.RootCauseDownSince != nil {
			v.RootCause = fmt.Sprintf("%s — down since %s", v.RootCause, humanTime(*alert.RootCauseDownSince))
		}
	}
	for _, loc := range alert.FailingLocations {
		line := locationLine{Name: loc.Name}
		if line.Name == "" {
			line.Name = loc.ID
		}
		if loc.DownSince != nil {
			line.DownSince = "down since " + humanTime(*loc.DownSince)
		}
		v.Locations = append(v.Locations, line)
	}

	v.Rows = detailRows(alert, elapsed)
	v.ActionURL, v.ActionLabel = actionFor(alert, appBaseURL)
	return v
}

func toneFor(alert notifications.AlertDetails, eventType string, resolved bool) tone {
	if resolved {
		t := toneUp
		switch {
		case alert.IsLatencyAnomaly():
			t.Label = "LATENCY NORMAL"
		case alert.IsHostMetric():
			t.Label = "BACK TO NORMAL"
		case alert.IsTLSExpiry():
			t.Label = "CERTIFICATE RENEWED"
		}
		return t
	}
	// Degradations that are not hard outages read as warnings, not failures.
	if alert.IsLatencyAnomaly() {
		t := toneWarn
		t.Label = "DEGRADED"
		return t
	}
	if alert.IsHostMetric() {
		t := toneWarn
		t.Label = "THRESHOLD BREACHED"
		return t
	}
	if alert.IsTLSExpiry() {
		t := toneWarn
		t.Label = "EXPIRING SOON"
		return t
	}
	t := toneDown
	if eventType == "reminder" {
		t.Label = "STILL DOWN"
	}
	if alert.IsMeshEdge() {
		t.Label = "PATH DOWN"
		if eventType == "reminder" {
			t.Label = "PATH STILL DOWN"
		}
	}
	return t
}

// DefaultLabel is the kind- and event-aware headline label, e.g.
// "Alert Triggered" or "CPU Usage High". Shared by the subject line and the
// email header so they always agree.
func DefaultLabel(event notifications.AlertEvent) string {
	alert := event.Alert
	switch {
	case alert.IsLatencyAnomaly():
		if event.Type == "resolved" {
			return "Latency Recovered"
		}
		if event.Type == "reminder" {
			return "Latency Still Degraded"
		}
		return "Latency Degraded"
	case alert.IsHostMetric():
		return alert.HostMetricLabel(event.Type)
	case alert.IsTLSExpiry():
		return alert.TLSExpiryLabel(event.Type)
	case alert.IsMeshEdge():
		switch event.Type {
		case "resolved":
			return "Mesh Path Recovered"
		case "reminder":
			return "Mesh Path Still Down"
		default:
			return "Mesh Path Down"
		}
	}
	switch event.Type {
	case "resolved":
		return "Alert Resolved"
	case "reminder":
		return "Alert Still Active"
	default:
		return "Alert Triggered"
	}
}

// summaryFor writes the one-sentence explanation that replaces the old
// key/value dump as the first thing a reader sees. It deliberately omits the
// monitor name: both the HTML headline and the plain-text heading print it
// immediately above, and both the subject line carries it.
func summaryFor(alert notifications.AlertDetails, eventType string, resolved bool, outage string) string {
	switch {
	case alert.IsLatencyAnomaly():
		if resolved {
			return "Response time is back within its normal range."
		}
		if alert.ObservedLatencyMs != nil && alert.BaselineLatencyMs != nil {
			s := fmt.Sprintf("Response time climbed to %s, against a usual baseline of %s",
				msValue(*alert.ObservedLatencyMs), msValue(*alert.BaselineLatencyMs))
			if alert.AnomalyScore != nil {
				s += fmt.Sprintf(" — %.1f standard deviations out", *alert.AnomalyScore)
			}
			return s + "."
		}
		return "Response time is significantly slower than the usual baseline."

	case alert.IsHostMetric():
		metric := alert.MetricLabel()
		if metric == "" {
			metric = "a host metric"
		}
		// Values render in the metric's display unit (percent for
		// utilization ratios and legacy kinds, bytes for usage, raw
		// otherwise); wording stays direction-neutral because a rule may be
		// a <= threshold.
		if resolved {
			if alert.ThresholdValue != nil {
				return fmt.Sprintf("%s is back within the %s threshold.", capitalize(metric), hostMetricValue(alert, *alert.ThresholdValue))
			}
			return fmt.Sprintf("%s is back within its configured threshold.", capitalize(metric))
		}
		if alert.MetricValue != nil && alert.ThresholdValue != nil {
			return fmt.Sprintf("%s reached %s, breaching the %s threshold.",
				capitalize(metric), hostMetricValue(alert, *alert.MetricValue), hostMetricValue(alert, *alert.ThresholdValue))
		}
		return fmt.Sprintf("%s breached its configured threshold.", capitalize(metric))

	case alert.IsTLSExpiry():
		if resolved {
			return "The TLS certificate has been renewed."
		}
		if alert.MetricValue != nil {
			s := fmt.Sprintf("The TLS certificate expires in %s", dayValue(*alert.MetricValue))
			if alert.ThresholdValue != nil {
				s += fmt.Sprintf(", inside the %s warning window", dayAdjective(*alert.ThresholdValue))
			}
			return s + "."
		}
		return "The TLS certificate is approaching expiry."

	case alert.IsMeshEdge():
		path := meshPath(alert)
		if resolved {
			if outage != "" {
				return fmt.Sprintf("Connectivity %s has been restored after %s.", path, outage)
			}
			return fmt.Sprintf("Connectivity %s has been restored.", path)
		}
		if eventType == "reminder" && outage != "" {
			return fmt.Sprintf("Probes %s are still failing, %s after the first failure.", path, outage)
		}
		return fmt.Sprintf("Probes %s are failing.", path)
	}

	// Availability.
	if resolved {
		if outage != "" {
			return fmt.Sprintf("Responding again after %s of downtime.", outage)
		}
		return "Responding again."
	}
	checks := checkPhrase(alert.FailureCount)
	if eventType == "reminder" {
		if outage != "" {
			return fmt.Sprintf("Still down — failing for %s%s.", outage, andChecks(checks))
		}
		return fmt.Sprintf("Still down%s.", andChecks(checks))
	}
	if checks != "" {
		return fmt.Sprintf("Stopped responding — %s failed in a row.", checks)
	}
	return "Stopped responding."
}

func metricFor(alert notifications.AlertDetails, resolved bool) *metricPanel {
	switch {
	case alert.IsLatencyAnomaly():
		if alert.ObservedLatencyMs == nil {
			return nil
		}
		p := &metricPanel{Label: "Response time", Value: msValue(*alert.ObservedLatencyMs)}
		if alert.BaselineLatencyMs != nil {
			p.Note = "baseline " + msValue(*alert.BaselineLatencyMs)
			if alert.AnomalyScore != nil {
				p.Note = fmt.Sprintf("%s · %.1fσ", p.Note, *alert.AnomalyScore)
			}
		}
		return p
	case alert.IsHostMetric():
		if alert.MetricValue == nil {
			return nil
		}
		label := alert.MetricLabel()
		if label == "" {
			label = "Host metric"
		}
		p := &metricPanel{Label: capitalize(label), Value: hostMetricValue(alert, *alert.MetricValue)}
		if alert.ThresholdValue != nil {
			p.Note = "threshold " + hostMetricValue(alert, *alert.ThresholdValue)
			if resolved {
				p.Note = "back within " + hostMetricValue(alert, *alert.ThresholdValue)
			}
		}
		return p
	case alert.IsTLSExpiry():
		if alert.MetricValue == nil {
			return nil
		}
		p := &metricPanel{Label: "Certificate lifetime left", Value: dayValue(*alert.MetricValue)}
		if alert.ThresholdValue != nil {
			p.Note = "warns under " + dayValue(*alert.ThresholdValue)
		}
		return p
	}
	return nil
}

func detailRows(alert notifications.AlertDetails, elapsed string) []detailRow {
	var rows []detailRow

	if alert.IsMeshEdge() {
		if path := meshPath(alert); path != "" {
			rows = append(rows, detailRow{Label: "Path", Value: strings.TrimPrefix(path, "from ")})
		}
	}
	if name := strings.TrimSpace(alert.PolicyName); name != "" {
		rows = append(rows, detailRow{Label: "Alert rule", Value: name})
	}
	if !alert.TriggeredAt.IsZero() {
		rows = append(rows, detailRow{Label: "Triggered", Value: humanTime(alert.TriggeredAt)})
	}
	if alert.ResolvedAt != nil {
		rows = append(rows, detailRow{Label: "Resolved", Value: humanTime(*alert.ResolvedAt)})
	}
	if d := elapsed; d != "" {
		label := "Open for"
		if alert.ResolvedAt != nil {
			label = "Total duration"
		}
		rows = append(rows, detailRow{Label: label, Value: d})
	}
	if alert.FailureCount > 0 {
		rows = append(rows, detailRow{Label: "Failed checks", Value: fmt.Sprintf("%d", alert.FailureCount)})
	}
	return rows
}

// actionFor builds the deep link into the operator UI. Returns empty strings
// when no app origin is configured, in which case the button is not rendered.
func actionFor(alert notifications.AlertDetails, appBaseURL string) (string, string) {
	base := strings.TrimRight(strings.TrimSpace(appBaseURL), "/")
	if base == "" {
		return "", ""
	}
	if alert.IsMeshEdge() {
		return base + "/mesh", "Open the mesh matrix"
	}
	if id := strings.TrimSpace(alert.MonitorID); id != "" && id != emptyUUID {
		return base + "/monitors/" + id, "Open the monitor"
	}
	return base + "/alerts", "Open Probara"
}

const emptyUUID = "00000000-0000-0000-0000-000000000000"

// --- formatting helpers -----------------------------------------------------

func eventTimestamp(event notifications.AlertEvent) time.Time {
	if !event.Timestamp.IsZero() {
		return event.Timestamp
	}
	if !event.Alert.TriggeredAt.IsZero() {
		return event.Alert.TriggeredAt
	}
	return time.Now()
}

// humanTime renders a timestamp for humans instead of RFC3339. Always UTC with
// an explicit zone label — the recipient's timezone is unknowable from an email.
func humanTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2 Jan 2006, 15:04 MST")
}

// outageDuration is how long the alert has been (or was) open at the time the
// event was emitted, as a compact human string. Empty when it cannot be
// computed or is not yet meaningful.
func outageDuration(alert notifications.AlertDetails, eventType string, now time.Time) string {
	if alert.TriggeredAt.IsZero() {
		return ""
	}
	end := now
	if alert.ResolvedAt != nil {
		end = *alert.ResolvedAt
	} else if eventType != "reminder" && eventType != "resolved" {
		// A freshly-created alert has no meaningful elapsed time yet.
		return ""
	}
	d := end.Sub(alert.TriggeredAt)
	if d < time.Second {
		return ""
	}
	return humanDuration(d)
}

// humanDuration renders a duration with at most two units, e.g. "1h 4m".
func humanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	switch {
	case days > 0:
		if hours > 0 {
			return fmt.Sprintf("%dd %dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	case hours > 0:
		if minutes > 0 {
			return fmt.Sprintf("%dh %dm", hours, minutes)
		}
		return fmt.Sprintf("%dh", hours)
	case minutes > 0:
		if seconds > 0 {
			return fmt.Sprintf("%dm %ds", minutes, seconds)
		}
		return fmt.Sprintf("%dm", minutes)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

func msValue(ms float64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.2f s", ms/1000)
	}
	return fmt.Sprintf("%.0f ms", ms)
}

func pctValue(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d%%", int64(v))
	}
	return fmt.Sprintf("%.1f%%", v)
}

// hostMetricValue renders a host-metric value/threshold in the metric's
// display unit, keyed by the alert's metric name (canonical series key or
// legacy percent kind). Falls back to percent when the name is missing.
func hostMetricValue(alert notifications.AlertDetails, v float64) string {
	if alert.MetricName != nil {
		return metricstore.FormatAlertValue(*alert.MetricName, v)
	}
	return pctValue(v)
}

// dayAdjective renders a day count for attributive use, e.g. "14-day window".
func dayAdjective(days float64) string {
	return fmt.Sprintf("%d-day", int(days))
}

func dayValue(days float64) string {
	n := int(days)
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}

func checkPhrase(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "1 check"
	default:
		return fmt.Sprintf("%d checks", n)
	}
}

func andChecks(checks string) string {
	if checks == "" {
		return ""
	}
	return " across " + checks
}

func meshPath(alert notifications.AlertDetails) string {
	src, dst := "", ""
	if alert.SourceLocationName != nil {
		src = strings.TrimSpace(*alert.SourceLocationName)
	}
	if alert.TargetLocationName != nil {
		dst = strings.TrimSpace(*alert.TargetLocationName)
	}
	if src != "" && dst != "" {
		return fmt.Sprintf("from %s to %s", src, dst)
	}
	if name := strings.TrimSpace(alert.MonitorName); name != "" {
		return "on " + name
	}
	return "between locations"
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

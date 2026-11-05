// Package present turns an alert event into the channel-agnostic wording every
// notification plugin renders: the headline label, the one-sentence summary,
// the measurement readout, the detail rows and the deep link.
//
// It is the single place alert wording lives. Add a new alert kind's phrasing
// here (labelFor/summaryFor/toneFor/metricFor) and every channel — email,
// chat, paging, SMS — picks it up; plugins decide only layout. Plugins reach
// it through plugin.DispatchRequest.View, which supplies the configured
// operator-UI origin.
//
// Event types: created, reminder, resolved, acknowledged (paging channels
// only) and suspected_cause, the opt-in follow-up carrying a local AI
// triage's named cause (channels advertising plugin.CapabilityFollowUp).
package present

import (
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/metricstore"
	"github.com/yassinebenameur/probara/shared/notifications"
)

// Event types a Message can describe.
const (
	EventCreated      = "created"
	EventReminder     = "reminder"
	EventResolved     = "resolved"
	EventAcknowledged = "acknowledged"
	// EventSuspectedCause is the advisory follow-up sent once per paged
	// channel when an alert's automatic triage names a cause. It never
	// changes the alert; its wording lives here like every other event's.
	EventSuspectedCause = "suspected_cause"
)

// Tone is the semantic colour of one notification. Plugins map it onto their
// own palette (email hex triples, Discord integers, Slack emoji).
type Tone string

const (
	ToneDown Tone = "down" // hard outage
	ToneWarn Tone = "warn" // degradation, threshold breach, expiring cert
	ToneUp   Tone = "up"   // recovery
	ToneInfo Tone = "info" // acknowledgement and other non-state updates
)

// Metric is the large-number readout used by the kinds that carry a
// measurement (latency anomaly, host metric, certificate expiry).
type Metric struct {
	Label string
	Value string
	Note  string
}

// Row is one label/value pair in the details block.
type Row struct {
	Label string
	Value string
}

// Location is one failing vantage point.
type Location struct {
	Name      string
	DownSince string
}

// Message is the fully-resolved presentation of one alert event.
type Message struct {
	EventType string
	Tone      Tone
	// StatusWord is the short upper-case state, e.g. "DOWN", "STILL DOWN",
	// "DEGRADED", "RECOVERED", "ACKNOWLEDGED".
	StatusWord string
	// Label is the kind- and event-aware headline, e.g. "Alert Triggered" or
	// "CPU Usage High".
	Label       string
	MonitorName string
	// Title is "Label: MonitorName", the one-line form chat and paging
	// channels lead with.
	Title string
	// Summary is one human sentence describing what happened. It omits the
	// monitor name, which every layout prints right next to it.
	Summary string
	Metric  *Metric

	LastError string
	RootCause string
	Locations []Location
	// Impacted lists the downstream monitors this alert's notification stands
	// in for (their own pages are suppressed by dependency); ImpactedMore is
	// how many further monitors the list omits.
	Impacted     []string
	ImpactedMore int
	Rows         []Row

	ActionURL   string
	ActionLabel string
	// AckURL is the signed one-click acknowledge link (the operator UI's
	// /alert-action/<token> confirm page) and AckLabel its caption. Set only
	// on created and reminder events for an alert still active, when a link
	// signer, the app origin and the channel are all known; see ackFor.
	AckURL   string
	AckLabel string

	AlertID   string
	TenantID  string
	Timestamp time.Time
	SentAt    string // Timestamp, human formatted
	Resolved  bool
}

// LinkSigner mints the signed token behind a one-click action link (for
// example "acknowledge this alert") for one alert as delivered on one channel.
// Options.Links may be nil, and a nil signer means no action links.
// shared/actionlink implements it; BuildWith renders its token as AckURL.
type LinkSigner interface {
	// ActionToken returns an opaque, URL-safe token authorizing action on
	// alertID in tenantID, as delivered on channelID, or an error when no
	// token can be minted (no key configured).
	ActionToken(action, tenantID, alertID, channelID string) (string, error)
}

// Options is the delivery context a Message is built for, beyond the event
// itself.
type Options struct {
	// AppBaseURL is the public operator-UI origin; empty omits every link.
	AppBaseURL string
	// Links signs action links; nil omits them.
	Links LinkSigner
	// ChannelID is the channel this Message is delivered on, which action
	// links are scoped to. Empty for channel-less renders (previews).
	ChannelID string
}

// Build resolves an event into a Message. eventType overrides event.Type when
// set (dispatch requests carry both). appBaseURL is the public operator-UI
// origin; empty omits the deep link. It is BuildWith with only AppBaseURL set.
func Build(event notifications.AlertEvent, eventType, appBaseURL string) Message {
	return BuildWith(event, eventType, Options{AppBaseURL: appBaseURL})
}

// BuildWith resolves an event into a Message for the delivery context opts.
// eventType overrides event.Type when set (dispatch requests carry both).
func BuildWith(event notifications.AlertEvent, eventType string, opts Options) Message {
	appBaseURL := opts.AppBaseURL
	if eventType == "" {
		eventType = event.Type
	}
	if eventType == "" {
		eventType = EventCreated
	}
	alert := event.Alert
	resolved := eventType == EventResolved || alert.Status == "resolved"
	// The event's own timestamp is the clock reference, not time.Now(): a
	// queued or retried dispatch must not inflate the reported outage length,
	// and it keeps rendering deterministic.
	ts := eventTimestamp(event)
	elapsed := outageDuration(alert, eventType, ts)

	m := Message{
		EventType:   eventType,
		Label:       labelFor(alert, eventType),
		MonitorName: strings.TrimSpace(alert.MonitorName),
		AlertID:     alert.ID,
		TenantID:    event.TenantID,
		Timestamp:   ts,
		SentAt:      HumanTime(ts),
		Resolved:    resolved,
	}
	m.Tone, m.StatusWord = toneFor(alert, eventType, resolved)
	if m.MonitorName == "" {
		m.MonitorName = "Unnamed monitor"
	}
	m.Title = m.Label + ": " + m.MonitorName
	m.Summary = summaryFor(alert, eventType, resolved, elapsed)
	m.Metric = metricFor(alert, resolved)
	if eventType == EventSuspectedCause && !resolved {
		// A follow-up to a page already delivered: it keeps what tells the
		// recipient which page it follows (the kind-aware metric, the root
		// cause annotation, the timing) and drops what the page already
		// carried at length (last error, locations, blast radius).
		m.RootCause = rootCauseLine(alert)
		m.Rows = suspectedCauseRows(alert, elapsed)
		m.ActionURL, m.ActionLabel = suspectedCauseAction(event, appBaseURL)
		return m
	}

	if alert.LastError != nil {
		m.LastError = strings.TrimSpace(*alert.LastError)
	}
	m.RootCause = rootCauseLine(alert)
	for _, loc := range alert.FailingLocations {
		line := Location{Name: loc.Name}
		if line.Name == "" {
			line.Name = loc.ID
		}
		if loc.DownSince != nil {
			line.DownSince = "down since " + HumanTime(*loc.DownSince)
		}
		m.Locations = append(m.Locations, line)
	}
	// A recovered root cause no longer stands in for anything, so the blast
	// radius is only worth printing while the outage is live.
	if !resolved {
		m.Impacted = alert.ImpactedMonitorNames()
		if alert.ImpactedCount > len(m.Impacted) {
			m.ImpactedMore = alert.ImpactedCount - len(m.Impacted)
		}
	}

	m.Rows = detailRows(alert, elapsed)
	m.ActionURL, m.ActionLabel = actionFor(alert, appBaseURL)
	m.AckURL, m.AckLabel = ackFor(event, eventType, resolved, opts)
	return m
}

// rootCauseLine renders the dependency root-cause annotation, or "".
func rootCauseLine(alert notifications.AlertDetails) string {
	if alert.RootCauseMonitorName == nil || strings.TrimSpace(*alert.RootCauseMonitorName) == "" {
		return ""
	}
	line := strings.TrimSpace(*alert.RootCauseMonitorName)
	if alert.RootCauseDownSince != nil {
		line = fmt.Sprintf("%s — down since %s", line, HumanTime(*alert.RootCauseDownSince))
	}
	return line
}

// suspectedCauseRows are the detail rows of a suspected_cause follow-up.
// "Follows up on" names the page (kind-aware, so two open host-metric alerts
// on one host stay distinguishable), then the cause, then the timing rows.
func suspectedCauseRows(alert notifications.AlertDetails, elapsed string) []Row {
	rows := []Row{{Label: "Follows up on", Value: followedUpPage(alert)}}
	if sc := alert.SuspectedCause; sc != nil {
		if sc.Title != "" {
			rows = append(rows, Row{Label: "Suspected cause", Value: sc.Title})
		}
		if sc.Confidence != "" {
			rows = append(rows, Row{Label: "Confidence", Value: capitalize(sc.Confidence)})
		}
		if len(sc.Actions) > 0 && strings.TrimSpace(sc.Actions[0]) != "" {
			rows = append(rows, Row{Label: "Suggested first check", Value: strings.TrimSpace(sc.Actions[0])})
		}
	}
	if !alert.TriggeredAt.IsZero() {
		rows = append(rows, Row{Label: "Triggered", Value: HumanTime(alert.TriggeredAt)})
	}
	if elapsed != "" {
		rows = append(rows, Row{Label: "Open for", Value: elapsed})
	}
	return rows
}

// followedUpPage names the page a follow-up refers to: the kind-aware
// headline the page itself carried, e.g. "CPU Usage High".
func followedUpPage(alert notifications.AlertDetails) string {
	if alert.IsMeshEdge() || alert.IsLatencyAnomaly() || alert.IsHostMetric() || alert.IsTLSExpiry() {
		return labelFor(alert, EventCreated)
	}
	return "Down alert"
}

// suspectedCauseAction links the follow-up to the one alert it is about on
// the Alerts page (which shows the triage pill and its label controls), in
// the alert's own workspace.
func suspectedCauseAction(event notifications.AlertEvent, appBaseURL string) (string, string) {
	base := strings.TrimRight(strings.TrimSpace(appBaseURL), "/")
	id := strings.TrimSpace(event.Alert.ID)
	if base == "" || id == "" {
		return "", ""
	}
	q := url.Values{"alert": {id}}
	if tenant := strings.TrimSpace(event.TenantID); tenant != "" {
		q.Set("tenant", tenant)
	}
	return base + "/alerts?" + q.Encode(), "Review the triage"
}

// Severity maps the tone onto the three-level scale paging providers use.
func (m Message) Severity() string {
	switch m.Tone {
	case ToneDown:
		return "critical"
	case ToneWarn:
		return "warning"
	default:
		return "info"
	}
}

// LocationNames renders the failing locations as "eu-west, us-east".
func (m Message) LocationNames() string {
	names := make([]string, len(m.Locations))
	for i, l := range m.Locations {
		names[i] = l.Name
	}
	return strings.Join(names, ", ")
}

// ImpactedSummary renders the blast radius as "api, web and 3 more". Empty
// when nothing is impacted.
func (m Message) ImpactedSummary() string {
	if len(m.Impacted) == 0 {
		return ""
	}
	s := strings.Join(m.Impacted, ", ")
	if m.ImpactedMore > 0 {
		s += fmt.Sprintf(" and %d more", m.ImpactedMore)
	}
	return s
}

// MetricLine renders the metric readout as one line, e.g.
// "Response time: 2.40 s (baseline 180 ms · 6.1σ)". Empty without a metric.
func (m Message) MetricLine() string {
	if m.Metric == nil {
		return ""
	}
	s := m.Metric.Label + ": " + m.Metric.Value
	if m.Metric.Note != "" {
		s += " (" + m.Metric.Note + ")"
	}
	return s
}

// Short renders a compact plain-text form for length-limited channels (SMS,
// push): title, summary, the deep link and the acknowledge link when
// configured. ShortWithin bounds it.
func (m Message) Short() string { return m.ShortWithin(0) }

// Facts returns the label/value pairs chat layouts show as fields: the
// metric, the detail rows, root cause, failing locations and blast radius, in
// that order. Last error is left out because every layout gives it its own
// block (it is long and often multi-line).
func (m Message) Facts() []Row {
	var out []Row
	if m.Metric != nil {
		v := m.Metric.Value
		if m.Metric.Note != "" {
			v += " (" + m.Metric.Note + ")"
		}
		out = append(out, Row{Label: m.Metric.Label, Value: v})
	}
	out = append(out, m.Rows...)
	if m.RootCause != "" {
		out = append(out, Row{Label: "Likely caused by", Value: m.RootCause})
	}
	if names := m.LocationNames(); names != "" {
		out = append(out, Row{Label: "Failing locations", Value: names})
	}
	if s := m.ImpactedSummary(); s != "" {
		out = append(out, Row{Label: "Also affecting", Value: s})
	}
	return out
}

func toneFor(alert notifications.AlertDetails, eventType string, resolved bool) (Tone, string) {
	if resolved {
		switch {
		case alert.IsLatencyAnomaly():
			return ToneUp, "LATENCY NORMAL"
		case alert.IsHostMetric():
			return ToneUp, "BACK TO NORMAL"
		case alert.IsTLSExpiry():
			return ToneUp, "CERTIFICATE RENEWED"
		case alert.IsLocationOffline():
			if locationClosedWithoutRecovery(alert) {
				// Nothing came back: the alert just stopped applying.
				return ToneInfo, "CLOSED"
			}
			return ToneUp, "BACK ONLINE"
		}
		return ToneUp, "RECOVERED"
	}
	if tone, word, ok := relapseTone(alert, eventType, resolved); ok {
		return tone, word
	}
	if eventType == EventAcknowledged {
		return ToneInfo, "ACKNOWLEDGED"
	}
	if eventType == EventSuspectedCause {
		// Not "SUSPECTED CAUSE": Short() prints StatusWord · Title, and the
		// title already starts with the label.
		return ToneInfo, "AI TRIAGE"
	}
	// Degradations that are not hard outages read as warnings, not failures.
	switch {
	case alert.IsLatencyAnomaly():
		return ToneWarn, "DEGRADED"
	case alert.IsHostMetric():
		return ToneWarn, "THRESHOLD BREACHED"
	case alert.IsTLSExpiry():
		return ToneWarn, "EXPIRING SOON"
	case alert.IsMeshEdge():
		if eventType == EventReminder {
			return ToneDown, "PATH STILL DOWN"
		}
		return ToneDown, "PATH DOWN"
	case alert.IsLocationOffline():
		if eventType == EventReminder {
			return ToneDown, "STILL OFFLINE"
		}
		return ToneDown, "OFFLINE"
	}
	if eventType == EventReminder {
		return ToneDown, "STILL DOWN"
	}
	return ToneDown, "DOWN"
}

// Label is the kind- and event-aware headline label for an event, e.g.
// "Alert Triggered" or "CPU Usage High".
func Label(event notifications.AlertEvent) string {
	return labelFor(event.Alert, event.Type)
}

func labelFor(alert notifications.AlertDetails, eventType string) string {
	if label, ok := relapseLabel(alert, eventType); ok {
		return label
	}
	if eventType == EventAcknowledged {
		return "Alert Acknowledged"
	}
	if eventType == EventSuspectedCause {
		return "Suspected Cause"
	}
	switch {
	case alert.IsLatencyAnomaly():
		switch eventType {
		case EventResolved:
			return "Latency Recovered"
		case EventReminder:
			return "Latency Still Degraded"
		}
		return "Latency Degraded"
	case alert.IsHostMetric():
		return alert.HostMetricLabel(eventType)
	case alert.IsTLSExpiry():
		return alert.TLSExpiryLabel(eventType)
	case alert.IsMeshEdge():
		switch eventType {
		case EventResolved:
			return "Mesh Path Recovered"
		case EventReminder:
			return "Mesh Path Still Down"
		default:
			return "Mesh Path Down"
		}
	case alert.IsLocationOffline():
		switch eventType {
		case EventResolved:
			if locationClosedWithoutRecovery(alert) {
				return "Location Alert Closed"
			}
			return "Location Back Online"
		case EventReminder:
			return "Location Still Offline"
		default:
			return "Location Offline"
		}
	}
	switch eventType {
	case EventResolved:
		return "Alert Resolved"
	case EventReminder:
		return "Alert Still Active"
	default:
		return "Alert Triggered"
	}
}

func summaryFor(alert notifications.AlertDetails, eventType string, resolved bool, outage string) string {
	if summary, ok := relapseSummary(alert, eventType, resolved); ok {
		return summary
	}
	if eventType == EventAcknowledged && !resolved {
		if outage != "" {
			return fmt.Sprintf("Someone is on it — acknowledged %s after it opened.", outage)
		}
		return "Someone is on it — the alert was acknowledged."
	}
	if eventType == EventSuspectedCause && !resolved {
		if sc := alert.SuspectedCause; sc != nil && sc.Title != "" {
			conf := ""
			if sc.Confidence != "" {
				conf = fmt.Sprintf(" (%s confidence)", sc.Confidence)
			}
			return fmt.Sprintf("Local AI triage points to %s%s. It is a lead from recorded checks, not a confirmed cause: verify before acting.", sc.Title, conf)
		}
		return "Local AI triage finished for this alert."
	}
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
		if eventType == EventReminder && outage != "" {
			return fmt.Sprintf("Probes %s are still failing, %s after the first failure.", path, outage)
		}
		return fmt.Sprintf("Probes %s are failing.", path)

	case alert.IsLocationOffline():
		name := locationName(alert)
		if resolved {
			switch alert.ResolveReason {
			case notifications.ResolveReasonLocationDisabled:
				return fmt.Sprintf("Location %s was disabled, so it is no longer expected to report.", name)
			case notifications.ResolveReasonLocationDeleted:
				return fmt.Sprintf("Location %s was deleted.", name)
			case notifications.ResolveReasonAlertsDisabled:
				return fmt.Sprintf("Offline-location alerts were turned off for this workspace; %s may still be offline.", name)
			}
			if outage != "" {
				return fmt.Sprintf("Location %s is reporting again after %s offline.", name, outage)
			}
			return fmt.Sprintf("Location %s is reporting again.", name)
		}
		s := fmt.Sprintf("Location %s has stopped reporting", name)
		if alert.LocationLastSeenAt != nil {
			s += " (last heartbeat " + HumanTime(*alert.LocationLastSeenAt) + ")"
		}
		s += "."
		switch n := alert.LocationMonitorCount; {
		case n == 1:
			s += " 1 monitor runs from it and goes stale without fresh checks."
		case n > 1:
			s += fmt.Sprintf(" %d monitors run from it and go stale without fresh checks.", n)
		}
		return s
	}

	// Availability.
	if resolved {
		if outage != "" {
			return fmt.Sprintf("Responding again after %s of downtime.", outage)
		}
		return "Responding again."
	}
	checks := checkPhrase(alert.FailureCount)
	if eventType == EventReminder {
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

func metricFor(alert notifications.AlertDetails, resolved bool) *Metric {
	switch {
	case alert.IsLatencyAnomaly():
		if alert.ObservedLatencyMs == nil {
			return nil
		}
		p := &Metric{Label: "Response time", Value: msValue(*alert.ObservedLatencyMs)}
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
		p := &Metric{Label: capitalize(label), Value: hostMetricValue(alert, *alert.MetricValue)}
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
		p := &Metric{Label: "Certificate lifetime left", Value: dayValue(*alert.MetricValue)}
		if alert.ThresholdValue != nil {
			p.Note = "warns under " + dayValue(*alert.ThresholdValue)
		}
		return p
	case alert.IsLocationOffline():
		if resolved || alert.LocationMonitorCount <= 0 {
			return nil
		}
		return &Metric{Label: "Monitors affected", Value: fmt.Sprintf("%d", alert.LocationMonitorCount), Note: "run from this location"}
	}
	return nil
}

func detailRows(alert notifications.AlertDetails, elapsed string) []Row {
	var rows []Row

	if alert.IsMeshEdge() {
		if path := meshPath(alert); path != "" {
			rows = append(rows, Row{Label: "Path", Value: strings.TrimPrefix(path, "from ")})
		}
	}
	if alert.IsLocationOffline() {
		rows = append(rows, Row{Label: "Location", Value: locationName(alert)})
		if alert.LocationLastSeenAt != nil {
			rows = append(rows, Row{Label: "Last heartbeat", Value: HumanTime(*alert.LocationLastSeenAt)})
		}
	}
	if name := strings.TrimSpace(alert.PolicyName); name != "" {
		rows = append(rows, Row{Label: "Alert rule", Value: name})
	}
	if !alert.TriggeredAt.IsZero() {
		rows = append(rows, Row{Label: "Triggered", Value: HumanTime(alert.TriggeredAt)})
	}
	if alert.ResolvedAt != nil {
		rows = append(rows, Row{Label: "Resolved", Value: HumanTime(*alert.ResolvedAt)})
	}
	if d := elapsed; d != "" {
		label := "Open for"
		if alert.ResolvedAt != nil {
			label = "Total duration"
		}
		rows = append(rows, Row{Label: label, Value: d})
	}
	if alert.FailureCount > 0 {
		rows = append(rows, Row{Label: "Failed checks", Value: fmt.Sprintf("%d", alert.FailureCount)})
	}
	rows = append(rows, relapseRows(alert)...)
	return rows
}

// Duration returns the "Open for" / "Total duration" row value, or "".
func (m Message) Duration() string {
	for _, row := range m.Rows {
		if row.Label == "Open for" || row.Label == "Total duration" {
			return row.Value
		}
	}
	return ""
}

// actionFor builds the deep link into the operator UI. Returns empty strings
// when no app origin is configured, in which case no link is rendered.
func actionFor(alert notifications.AlertDetails, appBaseURL string) (string, string) {
	base := strings.TrimRight(strings.TrimSpace(appBaseURL), "/")
	if base == "" {
		return "", ""
	}
	if alert.IsMeshEdge() {
		return base + "/mesh", "Open the mesh matrix"
	}
	if alert.IsLocationOffline() {
		return base + "/locations", "Open locations"
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

// HumanTime renders a timestamp for humans instead of RFC3339. Always UTC with
// an explicit zone label — the recipient's timezone is unknowable.
func HumanTime(t time.Time) string {
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
	} else if eventType == EventCreated {
		// A freshly-created alert has no meaningful elapsed time yet.
		return ""
	}
	d := end.Sub(alert.TriggeredAt)
	if d < time.Second {
		return ""
	}
	return HumanDuration(d)
}

// HumanDuration renders a duration with at most two units, e.g. "1h 4m".
func HumanDuration(d time.Duration) string {
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

// locationClosedWithoutRecovery reports whether a resolved location_offline
// alert closed for a reason other than the location reporting again.
func locationClosedWithoutRecovery(alert notifications.AlertDetails) bool {
	switch alert.ResolveReason {
	case notifications.ResolveReasonLocationDisabled, notifications.ResolveReasonLocationDeleted,
		notifications.ResolveReasonAlertsDisabled:
		return true
	}
	return false
}

// locationName is an offline-location alert's subject.
func locationName(alert notifications.AlertDetails) string {
	if alert.SourceLocationName != nil {
		if name := strings.TrimSpace(*alert.SourceLocationName); name != "" {
			return name
		}
	}
	if name := strings.TrimSpace(strings.TrimPrefix(alert.MonitorName, "location: ")); name != "" {
		return name
	}
	return "unknown"
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// --- signed action links ----------------------------------------------------

// ActionAcknowledge is the action an acknowledge link's token authorizes
// (LinkSigner.ActionToken); actionlink.ActionAcknowledge mirrors it.
const ActionAcknowledge = "ack"

// AckPath is the operator-UI route an acknowledge link opens: a confirm page
// that acknowledges on a button press (POST), never on load, because mail
// scanners and chat unfurlers prefetch links.
const AckPath = "/alert-action/"

// ackFor mints the signed acknowledge link. Only a page that asks for action
// carries one: created and reminder events of an alert that is still active.
// It needs the operator-UI origin, a signer (callers leave Options.Links nil
// for test sends) and the delivering channel; any of them missing, or a
// signer that cannot mint, renders no link rather than a broken one.
func ackFor(event notifications.AlertEvent, eventType string, resolved bool, opts Options) (string, string) {
	if eventType != EventCreated && eventType != EventReminder {
		return "", ""
	}
	if resolved || event.Alert.Status == "acknowledged" || opts.Links == nil {
		return "", ""
	}
	base := strings.TrimRight(strings.TrimSpace(opts.AppBaseURL), "/")
	if base == "" || strings.TrimSpace(opts.ChannelID) == "" {
		return "", ""
	}
	token, err := opts.Links.ActionToken(ActionAcknowledge, event.TenantID, event.Alert.ID, opts.ChannelID)
	if err != nil || token == "" {
		return "", ""
	}
	return base + AckPath + url.PathEscape(token), "Acknowledge"
}

// ShortWithin is Short bounded to limit runes (0 means unbounded) for an SMS
// or push body. The links are kept whole, since a cut URL is useless: the
// title and summary are clipped to make room, and only when the links alone
// would not fit is the deep link dropped, then the acknowledge link.
func (m Message) ShortWithin(limit int) string {
	head := m.StatusWord + " · " + m.Title
	if m.Summary != "" {
		head += "\n" + m.Summary
	}
	var links []string
	if m.ActionURL != "" {
		links = append(links, m.ActionURL)
	}
	if m.AckURL != "" {
		links = append(links, m.AckLabel+": "+m.AckURL)
	}
	for {
		tail := ""
		for _, l := range links {
			tail += "\n" + l
		}
		if limit <= 0 {
			return head + tail
		}
		room := limit - utf8.RuneCountInString(tail)
		if room >= 16 || len(links) == 0 {
			return clipRunes(head, room) + tail
		}
		links = links[1:]
	}
}

// clipRunes bounds s to n runes, marking a cut with an ellipsis.
func clipRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

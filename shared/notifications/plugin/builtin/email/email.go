// Package email implements the SMTP email alert plugin. The plugin owns its
// template rendering and config parsing; SMTP delivery is supplied by an
// injected Mailer (typically the alerter binary's SMTPMailer wired in main).
package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

const pluginType = "email"

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	To               []string `json:"to"`
	SubjectTemplate  string   `json:"subject_template,omitempty"`
	BodyTemplate     string   `json:"body_template,omitempty"`
	BodyHTMLTemplate string   `json:"body_html_template,omitempty"`
}

// Templates carries the (already-resolved) subject and body templates handed
// to the Mailer. Empty strings mean "use the plugin's default rendering".
//
// Body overrides the plain-text part; HTML overrides the rich part. They are
// independent: setting only Body yields a text-only message (the override is
// authoritative and is never wrapped in unrelated chrome), while setting only
// HTML keeps the generated text part as the fallback for text-only clients.
type Templates struct {
	Subject string
	Body    string
	HTML    string
}

// Mailer is the SMTP backend the email plugin delegates to. The alerter
// binary installs an implementation at startup via SetMailer.
type Mailer interface {
	SendAlert(ctx context.Context, event notifications.AlertEvent, templates Templates, recipients []string) error
}

// Plugin is the email plugin implementation.
type Plugin struct {
	mu     sync.RWMutex
	mailer Mailer
}

// singleton is the registered instance. Tests that want isolation should
// construct their own via New().
var singleton = New()

// New returns a fresh plugin with no Mailer installed.
func New() *Plugin { return &Plugin{} }

// SetMailer installs the SMTP backend on the default singleton. Safe to call
// before or after init() — the plugin self-registers immediately, but Send
// returns an error until the mailer is wired.
func SetMailer(m Mailer) { singleton.SetMailer(m) }

// SetMailer installs the SMTP backend on this Plugin instance.
func (p *Plugin) SetMailer(m Mailer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mailer = m
}

// Manifest returns the email plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Email",
		Description: "Deliver alerts to one or more email addresses over SMTP.",
		IconKey:     "mail",
		Version:     "1.0.0",
		Capabilities: []plugin.Capability{
			plugin.CapabilityRenderedAlert,
			plugin.CapabilityRawEvent,
			plugin.CapabilityTestable,
		},
		Fields: []plugin.Field{
			{
				Key:      "to",
				Label:    "Recipients",
				Type:     plugin.FieldTypeEmailList,
				Required: true,
				Help:     "Comma-separated email addresses.",
			},
			{
				Key:         "subject_template",
				Label:       "Subject Template",
				Type:        plugin.FieldTypeString,
				Placeholder: "[{{.status}}] {{.monitor_name}}",
				Help:        "Optional Go text/template overriding the default subject line.",
			},
			{
				Key:   "body_template",
				Label: "Plain-text Body Template",
				Type:  plugin.FieldTypeTextarea,
				Help:  "Optional Go text/template overriding the default plain-text body. Setting this alone sends a text-only email — add an HTML body template to keep a rich part.",
			},
			{
				Key:   "body_html_template",
				Label: "HTML Body Template",
				Type:  plugin.FieldTypeTextarea,
				Help:  "Optional Go html/template replacing the default branded HTML email. Alert values are HTML-escaped; your markup is not. Leave both body fields empty for the built-in design.",
			},
		},
	}
}

// Validate checks the channel config before persisting.
func (p *Plugin) Validate(raw json.RawMessage) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	if len(cfg.To) == 0 {
		return errors.New("email channel requires at least one recipient")
	}
	for _, addr := range cfg.To {
		if !strings.Contains(addr, "@") {
			return fmt.Errorf("invalid email address: %q", addr)
		}
	}
	if cfg.SubjectTemplate != "" {
		if _, err := template.New("subject").Parse(cfg.SubjectTemplate); err != nil {
			return fmt.Errorf("invalid subject_template: %w", err)
		}
	}
	if cfg.BodyTemplate != "" {
		if _, err := template.New("body").Parse(cfg.BodyTemplate); err != nil {
			return fmt.Errorf("invalid body_template: %w", err)
		}
	}
	if cfg.BodyHTMLTemplate != "" {
		if _, err := htmltemplate.New("body_html").Parse(cfg.BodyHTMLTemplate); err != nil {
			return fmt.Errorf("invalid body_html_template: %w", err)
		}
	}
	return nil
}

// Send renders the configured templates and delegates to the installed Mailer.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	p.mu.RLock()
	mailer := p.mailer
	p.mu.RUnlock()
	if mailer == nil {
		return errors.New("email plugin: mailer not configured (call SetMailer at startup)")
	}

	cfg, err := configFromChannel(req.Channel.Config)
	if err != nil {
		return err
	}
	return mailer.SendAlert(ctx, req.Event, Templates{
		Subject: cfg.SubjectTemplate,
		Body:    cfg.BodyTemplate,
		HTML:    cfg.BodyHTMLTemplate,
	}, cfg.To)
}

// RenderSubject computes the email subject line for an event, applying the
// override template if present. Exposed for use by Mailer implementations.
func RenderSubject(event notifications.AlertEvent, templates Templates, appBaseURL string) (string, error) {
	fallback := DefaultSubject(event)
	if strings.TrimSpace(templates.Subject) == "" {
		return fallback, nil
	}
	return renderTemplate(templates.Subject, fallback, templateData(event, appBaseURL))
}

// RenderBody computes the plain-text email body for an event, applying the
// override template if present. Exposed for use by Mailer implementations.
func RenderBody(event notifications.AlertEvent, templates Templates, appBaseURL string) (string, error) {
	fallback := DefaultBodyWithLinks(event, appBaseURL)
	if strings.TrimSpace(templates.Body) == "" {
		return fallback, nil
	}
	return renderTemplate(templates.Body, fallback, templateData(event, appBaseURL))
}

// RenderHTML computes the rich HTML part for an event. It returns an empty
// string (and no error) when the channel supplies a plain-text override without
// an HTML one: that operator asked for a specific text message, and pairing it
// with unrelated generated markup would send two different emails in one.
func RenderHTML(event notifications.AlertEvent, templates Templates, appBaseURL string) (string, error) {
	if strings.TrimSpace(templates.HTML) != "" {
		return renderHTMLTemplate(templates.HTML, templateData(event, appBaseURL))
	}
	if strings.TrimSpace(templates.Body) != "" {
		return "", nil
	}
	return renderAlertHTML(newAlertView(event, appBaseURL))
}

// DefaultSubject is the subject line used when no override template is set.
func DefaultSubject(event notifications.AlertEvent) string {
	name := strings.TrimSpace(event.Alert.MonitorName)
	if name == "" {
		name = "Unnamed monitor"
	}
	return fmt.Sprintf("[%s] %s", DefaultLabel(event), name)
}

// DefaultBody is the plain-text body used when no override template is set.
func DefaultBody(event notifications.AlertEvent) string {
	return DefaultBodyWithLinks(event, "")
}

// DefaultBodyWithLinks is DefaultBody plus a deep link into the operator UI at
// appBaseURL. An empty appBaseURL omits the link.
func DefaultBodyWithLinks(event notifications.AlertEvent, appBaseURL string) string {
	return renderAlertText(newAlertView(event, appBaseURL))
}

func renderTemplate(text, fallback string, data map[string]any) (string, error) {
	tmpl, err := template.New("alert").Option("missingkey=zero").Parse(text)
	if err != nil {
		return fallback, err
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return fallback, err
	}
	return sb.String(), nil
}

// renderHTMLTemplate renders an operator-authored HTML body. html/template
// escapes the interpolated alert values (a probe error can contain anything)
// while leaving the author's markup intact. On failure the caller falls back to
// a text-only message rather than shipping half-rendered markup.
func renderHTMLTemplate(text string, data map[string]any) (string, error) {
	tmpl, err := htmltemplate.New("alert_html").Option("missingkey=zero").Parse(text)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", err
	}
	return sb.String(), nil
}

func templateData(event notifications.AlertEvent, appBaseURL string) map[string]any {
	var lastError, resolvedAt string
	if event.Alert.LastError != nil {
		lastError = *event.Alert.LastError
	}
	if event.Alert.ResolvedAt != nil {
		resolvedAt = event.Alert.ResolvedAt.Format(time.RFC3339)
	}
	var rootCauseName, rootCauseDownSince string
	if event.Alert.RootCauseMonitorName != nil {
		rootCauseName = *event.Alert.RootCauseMonitorName
	}
	if event.Alert.RootCauseDownSince != nil {
		rootCauseDownSince = event.Alert.RootCauseDownSince.Format(time.RFC3339)
	}
	var baselineLatency, observedLatency, anomalyScore any
	if event.Alert.BaselineLatencyMs != nil {
		baselineLatency = *event.Alert.BaselineLatencyMs
	}
	if event.Alert.ObservedLatencyMs != nil {
		observedLatency = *event.Alert.ObservedLatencyMs
	}
	if event.Alert.AnomalyScore != nil {
		anomalyScore = *event.Alert.AnomalyScore
	}
	var metricName, metricValue, thresholdValue any
	if event.Alert.MetricName != nil {
		metricName = *event.Alert.MetricName
	}
	if event.Alert.MetricValue != nil {
		metricValue = *event.Alert.MetricValue
	}
	if event.Alert.ThresholdValue != nil {
		thresholdValue = *event.Alert.ThresholdValue
	}
	var sourceLocation, targetLocation string
	if event.Alert.SourceLocationName != nil {
		sourceLocation = *event.Alert.SourceLocationName
	}
	if event.Alert.TargetLocationName != nil {
		targetLocation = *event.Alert.TargetLocationName
	}

	// Presentation values, so an override template can reuse the built-in
	// wording (human timestamps, the summary sentence, the deep link) instead of
	// re-deriving it from raw fields.
	view := newAlertView(event, appBaseURL)

	return map[string]any{
		"alert_id":      event.Alert.ID,
		"monitor_id":    event.Alert.MonitorID,
		"monitor_name":  event.Alert.MonitorName,
		"policy_id":     event.Alert.AlertPolicyID,
		"policy_name":   event.Alert.PolicyName,
		"kind":          event.Alert.Kind,
		"status":        event.Alert.Status,
		"triggered_at":  event.Alert.TriggeredAt.Format(time.RFC3339),
		"resolved_at":   resolvedAt,
		"failure_count": event.Alert.FailureCount,
		"last_error":    lastError,
		"tenant_id":     event.TenantID,
		"event_type":    event.Type,
		"timestamp":     event.Timestamp.Format(time.RFC3339),

		"root_cause_monitor_name": rootCauseName,
		"root_cause_down_since":   rootCauseDownSince,

		"baseline_latency_ms": baselineLatency,
		"observed_latency_ms": observedLatency,
		"anomaly_score":       anomalyScore,

		"metric_name":     metricName,
		"metric_value":    metricValue,
		"threshold_value": thresholdValue,

		"source_location_name": sourceLocation,
		"target_location_name": targetLocation,
		"failing_locations":    event.Alert.FailingLocationNames(),
		"impacted_monitors":    event.Alert.ImpactedMonitorNames(),
		"impacted_count":       event.Alert.ImpactedCount,

		"label":              view.Label,
		"status_label":       view.Tone.Label,
		"summary":            view.Summary,
		"duration":           durationOf(view),
		"triggered_at_human": humanTime(event.Alert.TriggeredAt),
		"resolved_at_human":  humanTimeOf(event.Alert.ResolvedAt),
		"action_url":         view.ActionURL,
		"action_label":       view.ActionLabel,
		"accent_color":       view.Tone.Accent,
	}
}

// durationOf pulls the "how long has this been open" row out of the resolved
// view so override templates get the same string the built-in email shows.
func durationOf(v alertView) string {
	for _, row := range v.Rows {
		if row.Label == "Open for" || row.Label == "Total duration" {
			return row.Value
		}
	}
	return ""
}

func humanTimeOf(t *time.Time) string {
	if t == nil {
		return ""
	}
	return humanTime(*t)
}

func parseConfig(raw json.RawMessage) (Config, error) {
	var cfg Config
	if len(raw) == 0 {
		return cfg, errors.New("email config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid email config: %w", err)
	}
	cfg.To = normalizeRecipients(cfg.To)
	return cfg, nil
}

func configFromChannel(m map[string]any) (Config, error) {
	if m == nil {
		return Config{}, errors.New("email channel config is empty")
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return Config{}, fmt.Errorf("re-marshal channel config: %w", err)
	}
	return parseConfig(raw)
}

func normalizeRecipients(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, addr := range in {
		trimmed := strings.TrimSpace(addr)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func init() {
	plugin.Register(singleton)
}

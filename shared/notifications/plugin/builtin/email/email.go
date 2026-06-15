// Package email implements the SMTP email alert plugin. The plugin owns its
// template rendering and config parsing; SMTP delivery is supplied by an
// injected Mailer (typically the alerter binary's SMTPMailer wired in main).
package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	To              []string `json:"to"`
	SubjectTemplate string   `json:"subject_template,omitempty"`
	BodyTemplate    string   `json:"body_template,omitempty"`
}

// Templates carries the (already-resolved) subject and body templates handed
// to the Mailer. Empty strings mean "use the plugin's default rendering".
type Templates struct {
	Subject string
	Body    string
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
				Label: "Body Template",
				Type:  plugin.FieldTypeTextarea,
				Help:  "Optional Go text/template overriding the default plain-text body.",
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
	}, cfg.To)
}

// RenderSubject computes the email subject line for an event, applying the
// override template if present. Exposed for use by Mailer implementations.
func RenderSubject(event notifications.AlertEvent, templates Templates) (string, error) {
	fallback := DefaultSubject(event)
	if strings.TrimSpace(templates.Subject) == "" {
		return fallback, nil
	}
	return renderTemplate(templates.Subject, fallback, templateData(event))
}

// RenderBody computes the email body for an event, applying the override
// template if present. Exposed for use by Mailer implementations.
func RenderBody(event notifications.AlertEvent, templates Templates) (string, error) {
	fallback := DefaultBody(event)
	if strings.TrimSpace(templates.Body) == "" {
		return fallback, nil
	}
	return renderTemplate(templates.Body, fallback, templateData(event))
}

// DefaultSubject is the subject line used when no override template is set.
func DefaultSubject(event notifications.AlertEvent) string {
	if event.Alert.IsLatencyAnomaly() {
		if event.Type == "resolved" {
			return fmt.Sprintf("[Latency Recovered] %s", event.Alert.MonitorName)
		}
		return fmt.Sprintf("[Latency Degraded] %s", event.Alert.MonitorName)
	}
	if event.Type == "resolved" {
		return fmt.Sprintf("[Alert Resolved] %s", event.Alert.MonitorName)
	}
	return fmt.Sprintf("[Alert Triggered] %s", event.Alert.MonitorName)
}

// DefaultBody is the plain-text body used when no override template is set.
func DefaultBody(event notifications.AlertEvent) string {
	lines := []string{
		fmt.Sprintf("Monitor: %s", event.Alert.MonitorName),
		fmt.Sprintf("Policy: %s", event.Alert.PolicyName),
		fmt.Sprintf("Status: %s", event.Alert.Status),
		fmt.Sprintf("Triggered At: %s", event.Alert.TriggeredAt.Format(time.RFC3339)),
		fmt.Sprintf("Failure Count: %d", event.Alert.FailureCount),
		fmt.Sprintf("Tenant ID: %s", event.TenantID),
	}
	if event.Alert.IsLatencyAnomaly() && event.Alert.ObservedLatencyMs != nil && event.Alert.BaselineLatencyMs != nil {
		line := fmt.Sprintf("Latency: %.0f ms observed vs ~%.0f ms baseline", *event.Alert.ObservedLatencyMs, *event.Alert.BaselineLatencyMs)
		if event.Alert.AnomalyScore != nil {
			line = fmt.Sprintf("%s (%.1fσ)", line, *event.Alert.AnomalyScore)
		}
		lines = append(lines, line)
	}
	if event.Alert.LastError != nil && *event.Alert.LastError != "" {
		lines = append(lines, fmt.Sprintf("Last Error: %s", *event.Alert.LastError))
	}
	if event.Alert.RootCauseMonitorName != nil && *event.Alert.RootCauseMonitorName != "" {
		line := fmt.Sprintf("Likely Caused By: %s", *event.Alert.RootCauseMonitorName)
		if event.Alert.RootCauseDownSince != nil {
			line = fmt.Sprintf("%s (down since %s)", line, event.Alert.RootCauseDownSince.Format(time.RFC3339))
		}
		lines = append(lines, line)
	}
	if event.Alert.ResolvedAt != nil {
		lines = append(lines, fmt.Sprintf("Resolved At: %s", event.Alert.ResolvedAt.Format(time.RFC3339)))
	}
	return strings.Join(lines, "\n")
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

func templateData(event notifications.AlertEvent) map[string]any {
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
	}
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

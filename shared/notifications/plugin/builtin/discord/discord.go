// Package discord implements the Discord incoming-webhook alert plugin.
//
// Discord renders rich messages via the "embeds" field
// (https://discord.com/developers/docs/resources/channel#embed-object).
// This plugin uses CapabilityRenderedAlert because the embed layout is a
// good fit for the shared RenderedAlert structure and doesn't need raw event
// access.
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

const pluginType = "discord"

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	WebhookURL string `json:"webhook_url"`
}

// Plugin is the Discord alert plugin implementation.
type Plugin struct {
	httpClient *http.Client
}

// New constructs a plugin with the default HTTP client (10s timeout).
func New() *Plugin {
	return &Plugin{httpClient: &http.Client{Timeout: 10 * time.Second}}
}

// Manifest returns the Discord plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Discord",
		Description: "Post alerts to a Discord channel using a webhook URL.",
		IconKey:     "discord",
		DocsURL:     "https://support.discord.com/hc/en-us/articles/228383668-Intro-to-Webhooks",
		Version:     "1.0.0",
		Capabilities: []plugin.Capability{
			plugin.CapabilityRenderedAlert,
			plugin.CapabilityTestable,
		},
		Fields: []plugin.Field{
			{
				Key:         "webhook_url",
				Label:       "Webhook URL",
				Type:        plugin.FieldTypeSecret,
				Required:    true,
				Secret:      true,
				Placeholder: "https://discord.com/api/webhooks/.../...",
				Help:        "In Discord: Server Settings → Integrations → Webhooks → New Webhook.",
			},
		},
	}
}

// Validate checks the raw config blob before persisting.
func (p *Plugin) Validate(raw json.RawMessage) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	if cfg.WebhookURL == "" {
		return errors.New("webhook_url is required")
	}
	u, err := url.Parse(cfg.WebhookURL)
	if err != nil {
		return fmt.Errorf("webhook_url is not a valid URL: %w", err)
	}
	if u.Scheme != "https" {
		return errors.New("webhook_url must use https")
	}
	if !strings.HasSuffix(u.Host, "discord.com") && !strings.HasSuffix(u.Host, "discordapp.com") {
		return errors.New("webhook_url must point at a discord.com host")
	}
	return nil
}

// Send posts an embed payload to the channel's webhook URL.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	webhook, ok := stringFromMap(req.Channel.Config, "webhook_url")
	if !ok || webhook == "" {
		return errors.New("discord channel missing webhook_url")
	}

	payload, err := json.Marshal(buildEmbed(req))
	if err != nil {
		return fmt.Errorf("marshal discord payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build discord request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("post discord webhook: %w", err)
	}
	defer resp.Body.Close()

	// Discord returns 204 No Content on success.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord webhook returned status %d", resp.StatusCode)
	}
	return nil
}

type discordPayload struct {
	Username string         `json:"username,omitempty"`
	Content  string         `json:"content,omitempty"`
	Embeds   []discordEmbed `json:"embeds,omitempty"`
}

type discordEmbed struct {
	Title     string              `json:"title,omitempty"`
	Color     int                 `json:"color,omitempty"`
	Timestamp string              `json:"timestamp,omitempty"`
	Footer    *discordEmbedFooter `json:"footer,omitempty"`
	Fields    []discordEmbedField `json:"fields,omitempty"`
}

type discordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type discordEmbedFooter struct {
	Text string `json:"text"`
}

func buildEmbed(req plugin.DispatchRequest) discordPayload {
	event := req.Event
	eventType := eventTypeOf(req)
	ts := event.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	fields := []discordEmbedField{
		{Name: "Monitor", Value: event.Alert.MonitorName, Inline: true},
		{Name: "Policy", Value: event.Alert.PolicyName, Inline: true},
		{Name: "Status", Value: event.Alert.Status, Inline: true},
		{Name: "Failure Count", Value: fmt.Sprintf("%d", event.Alert.FailureCount), Inline: true},
	}
	if event.Alert.LastError != nil && *event.Alert.LastError != "" {
		fields = append(fields, discordEmbedField{Name: "Last Error", Value: *event.Alert.LastError})
	}
	if event.Alert.RootCauseMonitorName != nil && *event.Alert.RootCauseMonitorName != "" {
		value := *event.Alert.RootCauseMonitorName
		if event.Alert.RootCauseDownSince != nil {
			value = fmt.Sprintf("%s (down since %s)", value, event.Alert.RootCauseDownSince.Format(time.RFC1123))
		}
		fields = append(fields, discordEmbedField{Name: "Likely Caused By", Value: value})
	}

	return discordPayload{
		Embeds: []discordEmbed{{
			Title:     titleFor(eventType, event.Alert.MonitorName, event.Alert.IsLatencyAnomaly()),
			Color:     colorFor(eventType),
			Timestamp: ts.UTC().Format(time.RFC3339),
			Footer:    &discordEmbedFooter{Text: fmt.Sprintf("event: %s · tenant: %s", eventType, event.TenantID)},
			Fields:    fields,
		}},
	}
}

func titleFor(eventType, monitorName string, latency bool) string {
	if latency {
		switch eventType {
		case "created":
			return fmt.Sprintf("Latency Degraded: %s", monitorName)
		case "resolved":
			return fmt.Sprintf("Latency Recovered: %s", monitorName)
		case "reminder":
			return fmt.Sprintf("Latency Still Degraded: %s", monitorName)
		default:
			return fmt.Sprintf("Latency Anomaly: %s", monitorName)
		}
	}
	switch eventType {
	case "created":
		return fmt.Sprintf("Alert Triggered: %s", monitorName)
	case "resolved":
		return fmt.Sprintf("Alert Resolved: %s", monitorName)
	case "reminder":
		return fmt.Sprintf("Alert Still Active: %s", monitorName)
	default:
		return fmt.Sprintf("Alert: %s", monitorName)
	}
}

// Discord embed color is a decimal integer encoding RGB hex. Picked to match
// common severity conventions: red for fired, green for resolved, amber for
// reminder.
func colorFor(eventType string) int {
	switch eventType {
	case "resolved":
		return 0x2ECC71
	case "reminder":
		return 0xF1C40F
	default:
		return 0xE74C3C
	}
}

func eventTypeOf(req plugin.DispatchRequest) string {
	if req.EventType != "" {
		return req.EventType
	}
	return req.Event.Type
}

func parseConfig(raw json.RawMessage) (Config, error) {
	var cfg Config
	if len(raw) == 0 {
		return cfg, errors.New("discord config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid discord config: %w", err)
	}
	cfg.WebhookURL = strings.TrimSpace(cfg.WebhookURL)
	return cfg, nil
}

func stringFromMap(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func init() {
	plugin.Register(New())
}

// Package slack implements the Slack incoming-webhook alert plugin. The
// plugin uses Slack's Block Kit format (CapabilityRawEvent) for rich
// rendering rather than the deprecated "attachments" array.
package slack

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

const pluginType = "slack"

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	WebhookURL string `json:"webhook_url"`
}

// Plugin is the Slack alert plugin implementation.
type Plugin struct {
	httpClient *http.Client
}

// New constructs a plugin with the default HTTP client (10s timeout).
func New() *Plugin {
	return &Plugin{httpClient: &http.Client{Timeout: 10 * time.Second}}
}

// Manifest returns the Slack plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Slack",
		Description: "Post alerts to a Slack channel using an Incoming Webhook URL.",
		IconKey:     "slack",
		DocsURL:     "https://api.slack.com/messaging/webhooks",
		Version:     "1.0.0",
		Capabilities: []plugin.Capability{
			plugin.CapabilityRawEvent,
			plugin.CapabilityTestable,
		},
		Fields: []plugin.Field{
			{
				Key:         "webhook_url",
				Label:       "Webhook URL",
				Type:        plugin.FieldTypeSecret,
				Required:    true,
				Secret:      true,
				Placeholder: "https://hooks.slack.com/services/T.../B.../...",
				Help:        "Create an Incoming Webhook in your Slack workspace and paste the URL here.",
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
	if !strings.Contains(u.Host, "slack.com") {
		return errors.New("webhook_url must point at a slack.com host")
	}
	return nil
}

// Send posts a Block Kit payload to the channel's webhook URL.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	webhook, ok := stringFromMap(req.Channel.Config, "webhook_url")
	if !ok || webhook == "" {
		return errors.New("slack channel missing webhook_url")
	}

	payload, err := json.Marshal(buildBlockKit(req))
	if err != nil {
		return fmt.Errorf("marshal slack payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build slack request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("post slack webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// Block Kit JSON: https://api.slack.com/reference/block-kit/blocks
type slackPayload struct {
	Text   string       `json:"text"`
	Blocks []slackBlock `json:"blocks,omitempty"`
}

type slackBlock struct {
	Type   string         `json:"type"`
	Text   *slackText     `json:"text,omitempty"`
	Fields []slackText    `json:"fields,omitempty"`
	Accessory any         `json:"accessory,omitempty"`
}

type slackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func buildBlockKit(req plugin.DispatchRequest) slackPayload {
	event := req.Event
	eventType := eventTypeOf(req)
	emoji := emojiFor(eventType)
	header := fmt.Sprintf("%s %s: %s", emoji, headerLabel(eventType), event.Alert.MonitorName)

	fields := []slackText{
		{Type: "mrkdwn", Text: fmt.Sprintf("*Monitor*\n%s", event.Alert.MonitorName)},
		{Type: "mrkdwn", Text: fmt.Sprintf("*Policy*\n%s", event.Alert.PolicyName)},
		{Type: "mrkdwn", Text: fmt.Sprintf("*Status*\n%s", event.Alert.Status)},
		{Type: "mrkdwn", Text: fmt.Sprintf("*Failure Count*\n%d", event.Alert.FailureCount)},
	}
	if event.Alert.LastError != nil && *event.Alert.LastError != "" {
		fields = append(fields, slackText{Type: "mrkdwn", Text: fmt.Sprintf("*Last Error*\n%s", *event.Alert.LastError)})
	}

	ts := event.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	return slackPayload{
		Text: header, // notification fallback for clients that don't render blocks
		Blocks: []slackBlock{
			{
				Type: "header",
				Text: &slackText{Type: "plain_text", Text: header},
			},
			{
				Type:   "section",
				Fields: fields,
			},
			{
				Type: "context",
				Fields: []slackText{
					{Type: "mrkdwn", Text: fmt.Sprintf("_Event: `%s` at %s_", eventType, ts.Format(time.RFC1123))},
				},
			},
		},
	}
}

func headerLabel(eventType string) string {
	switch eventType {
	case "created":
		return "Alert Triggered"
	case "resolved":
		return "Alert Resolved"
	case "reminder":
		return "Alert Still Active"
	default:
		return "Alert"
	}
}

func emojiFor(eventType string) string {
	switch eventType {
	case "resolved":
		return ":white_check_mark:"
	case "reminder":
		return ":hourglass_flowing_sand:"
	default:
		return ":rotating_light:"
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
		return cfg, errors.New("slack config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid slack config: %w", err)
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

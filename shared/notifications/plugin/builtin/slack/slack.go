// Package slack implements the Slack incoming-webhook alert plugin. It posts
// Block Kit messages (not the deprecated "attachments" array) built from the
// shared presentation in package present.
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/present"
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

// New constructs a plugin with the guarded notification HTTP client.
func New() *Plugin {
	return &Plugin{httpClient: plugin.NewHTTPClient(10 * time.Second)}
}

// Manifest returns the Slack plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Slack",
		Description: "Post alerts to a Slack channel using an Incoming Webhook URL.",
		IconKey:     "slack",
		DocsURL:     "https://api.slack.com/messaging/webhooks",
		Version:     "1.1.0",
		Capabilities: []plugin.Capability{
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
	u, err := plugin.ParseHTTPSURL(cfg.WebhookURL, "webhook_url")
	if err != nil {
		return err
	}
	if !plugin.HostMatches(u.Host, "slack.com") {
		return errors.New("webhook_url must point at a slack.com host")
	}
	return nil
}

// Send posts a Block Kit payload to the channel's webhook URL.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	webhook := req.Channel.String("webhook_url")
	if webhook == "" {
		return plugin.Permanent(errors.New("slack channel missing webhook_url"))
	}
	return plugin.PostJSON(ctx, p.httpClient, webhook, buildBlockKit(req.View()), nil, "slack webhook")
}

// Block Kit JSON: https://api.slack.com/reference/block-kit/blocks
type slackPayload struct {
	Text   string       `json:"text"`
	Blocks []slackBlock `json:"blocks,omitempty"`
}

type slackBlock struct {
	Type     string      `json:"type"`
	Text     *slackText  `json:"text,omitempty"`
	Fields   []slackText `json:"fields,omitempty"`
	Elements []any       `json:"elements,omitempty"`
}

type slackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type slackButton struct {
	Type string    `json:"type"`
	Text slackText `json:"text"`
	URL  string    `json:"url"`
}

// Slack caps a section at 10 fields and a header at 150 characters.
const (
	maxFields    = 10
	maxHeaderLen = 150
)

func buildBlockKit(m present.Message) slackPayload {
	header := fmt.Sprintf("%s %s", emojiFor(m.Tone), m.Title)

	blocks := []slackBlock{
		{Type: "header", Text: &slackText{Type: "plain_text", Text: truncate(header, maxHeaderLen)}},
		{Type: "section", Text: &slackText{Type: "mrkdwn", Text: escape(m.Summary)}},
	}

	var fields []slackText
	for _, f := range m.Facts() {
		if len(fields) == maxFields {
			break
		}
		fields = append(fields, slackText{Type: "mrkdwn", Text: fmt.Sprintf("*%s*\n%s", escape(f.Label), escape(f.Value))})
	}
	if len(fields) > 0 {
		blocks = append(blocks, slackBlock{Type: "section", Fields: fields})
	}
	if m.LastError != "" {
		blocks = append(blocks, slackBlock{
			Type: "section",
			Text: &slackText{Type: "mrkdwn", Text: "*Last error*\n```" + escape(truncate(m.LastError, 2800)) + "```"},
		})
	}
	if m.ActionURL != "" {
		blocks = append(blocks, slackBlock{
			Type: "actions",
			Elements: []any{slackButton{
				Type: "button",
				Text: slackText{Type: "plain_text", Text: m.ActionLabel},
				URL:  m.ActionURL,
			}},
		})
	}
	blocks = append(blocks, slackBlock{
		Type:     "context",
		Elements: []any{slackText{Type: "mrkdwn", Text: fmt.Sprintf("%s · %s", m.StatusWord, m.SentAt)}},
	})

	return slackPayload{
		Text:   header, // notification fallback for clients that don't render blocks
		Blocks: blocks,
	}
}

func emojiFor(t present.Tone) string {
	switch t {
	case present.ToneUp:
		return ":white_check_mark:"
	case present.ToneWarn:
		return ":warning:"
	case present.ToneInfo:
		return ":eyes:"
	default:
		return ":rotating_light:"
	}
}

// escape neutralises the three characters Slack mrkdwn treats as control
// sequences, so a probe error containing "<!channel>" cannot ping a channel.
func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
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

func init() {
	plugin.Register(New())
}

// Package discord implements the Discord incoming-webhook alert plugin.
//
// Discord renders rich messages via the "embeds" field
// (https://discord.com/developers/docs/resources/channel#embed-object),
// built here from the shared presentation in package present.
package discord

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

const pluginType = "discord"

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	WebhookURL string `json:"webhook_url"`
}

// Plugin is the Discord alert plugin implementation.
type Plugin struct {
	httpClient *http.Client
}

// New constructs a plugin with the guarded notification HTTP client.
func New() *Plugin {
	return &Plugin{httpClient: plugin.NewHTTPClient(10 * time.Second)}
}

// Manifest returns the Discord plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Discord",
		Description: "Post alerts to a Discord channel using a webhook URL.",
		IconKey:     "discord",
		DocsURL:     "https://support.discord.com/hc/en-us/articles/228383668-Intro-to-Webhooks",
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
	u, err := plugin.ParseHTTPSURL(cfg.WebhookURL, "webhook_url")
	if err != nil {
		return err
	}
	if !plugin.HostMatches(u.Host, "discord.com", "discordapp.com") {
		return errors.New("webhook_url must point at a discord.com host")
	}
	return nil
}

// Send posts an embed payload to the channel's webhook URL. Discord returns
// 204 No Content on success.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	webhook := req.Channel.String("webhook_url")
	if webhook == "" {
		return plugin.Permanent(errors.New("discord channel missing webhook_url"))
	}
	return plugin.PostJSON(ctx, p.httpClient, webhook, buildEmbed(req.View()), nil, "discord webhook")
}

type discordPayload struct {
	Username        string          `json:"username,omitempty"`
	Content         string          `json:"content,omitempty"`
	Embeds          []discordEmbed  `json:"embeds,omitempty"`
	AllowedMentions allowedMentions `json:"allowed_mentions"`
}

// allowedMentions with an empty Parse list stops any "@everyone" or role
// mention inside alert text from pinging.
type allowedMentions struct {
	Parse []string `json:"parse"`
}

type discordEmbed struct {
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	URL         string              `json:"url,omitempty"`
	Color       int                 `json:"color,omitempty"`
	Timestamp   string              `json:"timestamp,omitempty"`
	Footer      *discordEmbedFooter `json:"footer,omitempty"`
	Fields      []discordEmbedField `json:"fields,omitempty"`
}

type discordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type discordEmbedFooter struct {
	Text string `json:"text"`
}

// Discord embed limits.
const (
	maxTitle      = 256
	maxFieldValue = 1024
	maxFields     = 25
)

func buildEmbed(m present.Message) discordPayload {
	var fields []discordEmbedField
	for _, f := range m.Facts() {
		if len(fields) == maxFields {
			break
		}
		fields = append(fields, discordEmbedField{Name: f.Label, Value: truncate(f.Value, maxFieldValue), Inline: len(f.Value) <= 40})
	}
	if m.LastError != "" && len(fields) < maxFields {
		fields = append(fields, discordEmbedField{Name: "Last error", Value: truncate(m.LastError, maxFieldValue)})
	}

	return discordPayload{
		Embeds: []discordEmbed{{
			Title:       truncate(m.Title, maxTitle),
			Description: m.Summary,
			URL:         m.ActionURL,
			Color:       colorFor(m.Tone),
			Timestamp:   m.Timestamp.UTC().Format(time.RFC3339),
			Footer:      &discordEmbedFooter{Text: fmt.Sprintf("%s · workspace %s", m.StatusWord, m.TenantID)},
			Fields:      fields,
		}},
		AllowedMentions: allowedMentions{Parse: []string{}},
	}
}

// Discord embed color is a decimal integer encoding RGB hex.
func colorFor(t present.Tone) int {
	switch t {
	case present.ToneUp:
		return 0x2ECC71
	case present.ToneWarn:
		return 0xF1C40F
	case present.ToneInfo:
		return 0x3498DB
	default:
		return 0xE74C3C
	}
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
		return cfg, errors.New("discord config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid discord config: %w", err)
	}
	cfg.WebhookURL = strings.TrimSpace(cfg.WebhookURL)
	return cfg, nil
}

func init() {
	plugin.Register(New())
}

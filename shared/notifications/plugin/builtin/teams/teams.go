// Package teams implements the Microsoft Teams webhook alert plugin.
//
// Teams has two webhook generations. Office 365 connector webhooks
// (*.webhook.office.com, outlook.office.com) take the legacy MessageCard
// schema and are being retired by Microsoft; their replacement, a Power
// Automate "Workflows" webhook, takes an Adaptive Card wrapped in a message
// envelope and ignores MessageCard. The plugin picks the format from the URL
// host so existing connector channels keep working and new Workflows URLs
// render properly. Both are built from the shared presentation in package
// present.
package teams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

const pluginType = "teams"

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	WebhookURL string `json:"webhook_url"`
}

// Plugin is the Teams alert plugin implementation. Exported for direct
// instantiation in tests; production code goes through DefaultRegistry.
type Plugin struct {
	httpClient *http.Client
}

// New constructs a plugin with the guarded notification HTTP client.
func New() *Plugin {
	return &Plugin{httpClient: plugin.NewHTTPClient(10 * time.Second)}
}

// Manifest returns the Teams plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Microsoft Teams",
		Description: "Post alerts to a Microsoft Teams channel through a Workflows (Power Automate) webhook or a legacy incoming-webhook connector.",
		IconKey:     "teams",
		DocsURL:     "https://support.microsoft.com/office/create-incoming-webhooks-with-workflows-for-microsoft-teams-8ae491c7-0394-4861-ba59-055e33f75498",
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
				Placeholder: "https://…/workflows/…/triggers/manual/paths/invoke?…",
				Help:        "In Teams: channel ⋯ → Workflows → \"Post to a channel when a webhook request is received\", then copy the URL. Legacy connector URLs (webhook.office.com) still work.",
			},
		},
	}
}

// Validate checks the raw config blob before persisting. Teams webhook hosts
// are not pinned: Microsoft has moved Workflows URLs between domains more
// than once, and the egress policy already keeps them off private addresses.
func (p *Plugin) Validate(raw json.RawMessage) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	_, err = plugin.ParseHTTPSURL(cfg.WebhookURL, "webhook_url")
	return err
}

// Send posts the card format the webhook's host understands.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	webhook := req.Channel.String("webhook_url")
	if webhook == "" {
		return plugin.Permanent(errors.New("teams channel missing webhook_url"))
	}
	msg := req.View()
	var body any
	if isLegacyConnector(webhook) {
		body = buildMessageCard(msg)
	} else {
		body = buildAdaptiveCardMessage(msg)
	}
	return plugin.PostJSON(ctx, p.httpClient, webhook, body, nil, "teams webhook")
}

// isLegacyConnector reports whether target is an Office 365 connector
// webhook, which only renders MessageCard.
func isLegacyConnector(target string) bool {
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	return plugin.HostMatches(u.Host, "webhook.office.com", "outlook.office.com", "outlook.office365.com")
}

// --- legacy MessageCard ------------------------------------------------------

// messageCard is the Office 365 connector card schema.
// https://learn.microsoft.com/outlook/actionable-messages/message-card-reference
type messageCard struct {
	Type            string          `json:"@type"`
	Context         string          `json:"@context"`
	Summary         string          `json:"summary,omitempty"`
	ThemeColor      string          `json:"themeColor,omitempty"`
	Title           string          `json:"title,omitempty"`
	Text            string          `json:"text,omitempty"`
	Sections        []section       `json:"sections,omitempty"`
	PotentialAction []openURIAction `json:"potentialAction,omitempty"`
}

type section struct {
	Facts    []fact `json:"facts,omitempty"`
	Text     string `json:"text,omitempty"`
	Markdown bool   `json:"markdown,omitempty"`
}

type fact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type openURIAction struct {
	Type    string      `json:"@type"`
	Name    string      `json:"name"`
	Targets []uriTarget `json:"targets"`
}

type uriTarget struct {
	OS  string `json:"os"`
	URI string `json:"uri"`
}

func buildMessageCard(m present.Message) messageCard {
	var facts []fact
	for _, f := range m.Facts() {
		facts = append(facts, fact{Name: f.Label, Value: f.Value})
	}
	if m.LastError != "" {
		facts = append(facts, fact{Name: "Last error", Value: m.LastError})
	}
	card := messageCard{
		Type:       "MessageCard",
		Context:    "https://schema.org/extensions",
		Summary:    m.Title,
		ThemeColor: hexFor(m.Tone),
		Title:      m.Title,
		Text:       m.Summary,
		Sections:   []section{{Facts: facts, Markdown: false}},
	}
	if m.ActionURL != "" {
		card.PotentialAction = []openURIAction{{
			Type:    "OpenUri",
			Name:    m.ActionLabel,
			Targets: []uriTarget{{OS: "default", URI: m.ActionURL}},
		}}
	}
	return card
}

// --- Workflows Adaptive Card -------------------------------------------------

// adaptiveMessage is the envelope the Workflows "post to a channel when a
// webhook request is received" trigger expects.
type adaptiveMessage struct {
	Type        string               `json:"type"`
	Attachments []adaptiveAttachment `json:"attachments"`
}

type adaptiveAttachment struct {
	ContentType string       `json:"contentType"`
	Content     adaptiveCard `json:"content"`
}

type adaptiveCard struct {
	Schema  string           `json:"$schema"`
	Type    string           `json:"type"`
	Version string           `json:"version"`
	Body    []map[string]any `json:"body"`
	Actions []map[string]any `json:"actions,omitempty"`
	MSTeams map[string]any   `json:"msteams,omitempty"`
}

func buildAdaptiveCardMessage(m present.Message) adaptiveMessage {
	body := []map[string]any{
		{"type": "TextBlock", "text": m.StatusWord, "weight": "Bolder", "size": "Small", "color": adaptiveColor(m.Tone), "spacing": "None"},
		{"type": "TextBlock", "text": m.Title, "weight": "Bolder", "size": "Medium", "wrap": true, "spacing": "Small"},
		{"type": "TextBlock", "text": m.Summary, "wrap": true},
	}
	var facts []map[string]any
	for _, f := range m.Facts() {
		facts = append(facts, map[string]any{"title": f.Label, "value": f.Value})
	}
	if len(facts) > 0 {
		body = append(body, map[string]any{"type": "FactSet", "facts": facts})
	}
	if m.LastError != "" {
		body = append(body,
			map[string]any{"type": "TextBlock", "text": "Last error", "weight": "Bolder", "spacing": "Medium"},
			map[string]any{"type": "TextBlock", "text": m.LastError, "wrap": true, "fontType": "Monospace", "spacing": "Small"},
		)
	}
	card := adaptiveCard{
		Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
		Type:    "AdaptiveCard",
		Version: "1.4",
		Body:    body,
		MSTeams: map[string]any{"width": "Full"},
	}
	if m.ActionURL != "" {
		card.Actions = []map[string]any{{"type": "Action.OpenUrl", "title": m.ActionLabel, "url": m.ActionURL}}
	}
	return adaptiveMessage{
		Type: "message",
		Attachments: []adaptiveAttachment{{
			ContentType: "application/vnd.microsoft.card.adaptive",
			Content:     card,
		}},
	}
}

func adaptiveColor(t present.Tone) string {
	switch t {
	case present.ToneUp:
		return "Good"
	case present.ToneWarn:
		return "Warning"
	case present.ToneInfo:
		return "Accent"
	default:
		return "Attention"
	}
}

func hexFor(t present.Tone) string {
	switch t {
	case present.ToneUp:
		return "00A35F"
	case present.ToneWarn:
		return "D38D00"
	case present.ToneInfo:
		return "2563EB"
	default:
		return "DA1B69"
	}
}

func parseConfig(raw json.RawMessage) (Config, error) {
	var cfg Config
	if len(raw) == 0 {
		return cfg, errors.New("teams config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid teams config: %w", err)
	}
	cfg.WebhookURL = strings.TrimSpace(cfg.WebhookURL)
	return cfg, nil
}

func init() {
	plugin.Register(New())
}

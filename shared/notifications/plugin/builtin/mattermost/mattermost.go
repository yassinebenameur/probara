// Package mattermost implements the Mattermost incoming-webhook alert plugin
// (https://developers.mattermost.com/integrate/webhooks/incoming/). It posts a
// colour-coded message attachment built from the shared presentation in
// package present.
//
// Mattermost is self-hosted, so there is no host check: the notification
// egress policy is the boundary, and a server on a private network needs its
// range in NOTIFICATION_ALLOWED_CIDRS.
package mattermost

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

const (
	pluginType = "mattermost"
	// Mattermost caps a post at 16383 characters; these keep one alert well
	// inside it however many facts it carries.
	maxValue     = 1000
	maxLastError = 4000
)

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	WebhookURL string `json:"webhook_url"`
}

// Plugin is the Mattermost alert plugin implementation.
type Plugin struct {
	httpClient *http.Client
}

// New constructs a plugin with the guarded notification HTTP client.
func New() *Plugin {
	return &Plugin{httpClient: plugin.NewHTTPClient(10 * time.Second)}
}

// Manifest returns the Mattermost plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Mattermost",
		Description: "Post alerts to a Mattermost channel using an incoming webhook.",
		IconKey:     "mattermost",
		DocsURL:     "https://developers.mattermost.com/integrate/webhooks/incoming/",
		Version:     "1.0.0",
		Capabilities: []plugin.Capability{
			plugin.CapabilityTestable,
			plugin.CapabilityFollowUp,
		},
		Fields: []plugin.Field{
			{
				Key:         "webhook_url",
				Label:       "Webhook URL",
				Type:        plugin.FieldTypeSecret,
				Required:    true,
				Secret:      true,
				Placeholder: "https://mattermost.example.com/hooks/xxxxxxxxxxxxxxxxxxxxxxxxxx",
				Help:        "In Mattermost: Integrations → Incoming Webhooks → Add. A server on a private network must be allowed in NOTIFICATION_ALLOWED_CIDRS.",
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
	if !strings.Contains(u.Path, "/hooks/") {
		return errors.New("webhook_url must be a Mattermost incoming webhook URL (…/hooks/<id>)")
	}
	return nil
}

// Send posts one attachment message to the channel's webhook URL.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	webhook := req.Channel.String("webhook_url")
	if webhook == "" {
		return plugin.Permanent(errors.New("mattermost channel missing webhook_url"))
	}
	return plugin.PostJSON(ctx, p.httpClient, webhook, build(req.View()), nil, "mattermost webhook")
}

type payload struct {
	Attachments []attachment `json:"attachments"`
}

type attachment struct {
	Fallback  string  `json:"fallback"`
	Color     string  `json:"color"`
	Title     string  `json:"title"`
	TitleLink string  `json:"title_link,omitempty"`
	Text      string  `json:"text,omitempty"`
	Fields    []field `json:"fields,omitempty"`
	Footer    string  `json:"footer,omitempty"`
}

type field struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

func build(m present.Message) payload {
	title := emojiFor(m.Tone) + " " + m.Title
	text := escape(m.Summary)
	if m.LastError != "" {
		text += "\n\n**Last error**\n```\n" + codeSafe(truncate(m.LastError, maxLastError)) + "\n```"
	}
	if m.ActionURL != "" {
		text += fmt.Sprintf("\n\n[%s](%s)", escape(m.ActionLabel), m.ActionURL)
	}
	var fields []field
	for _, f := range m.Facts() {
		v := truncate(f.Value, maxValue)
		fields = append(fields, field{Title: noMention(f.Label), Value: escape(v), Short: len(v) <= 40})
	}
	return payload{Attachments: []attachment{{
		Fallback:  noMention(title),
		Color:     colorFor(m.Tone),
		Title:     noMention(title),
		TitleLink: m.ActionURL,
		Text:      strings.TrimSpace(text),
		Fields:    fields,
		Footer:    noMention(m.StatusWord + " · " + m.SentAt),
	}}}
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

func colorFor(t present.Tone) string {
	switch t {
	case present.ToneUp:
		return "#2ECC71"
	case present.ToneWarn:
		return "#F1C40F"
	case present.ToneInfo:
		return "#3498DB"
	default:
		return "#E74C3C"
	}
}

// markdownSpecials are the characters Mattermost's Markdown treats as syntax.
var markdownSpecials = strings.NewReplacer(
	`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "`", "\\`",
	"[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`, "#", `\#`,
	"<", `\<`, ">", `\>`, "|", `\|`,
)

// escape renders s as literal Markdown text with mentions neutralised, so a
// probe error or monitor name cannot format the post or ping anyone.
func escape(s string) string {
	return noMention(markdownSpecials.Replace(s))
}

// noMention puts a zero-width joiner after every "@", so "@channel", "@all"
// or "@someone" inside alert text renders the same but notifies nobody.
func noMention(s string) string {
	return strings.ReplaceAll(s, "@", "@‍")
}

// codeSafe keeps text inside a fenced code block, where Markdown is not
// parsed but a ``` in the text would close the fence early. Mentions are
// neutralised there too rather than relying on the client to skip them.
func codeSafe(s string) string {
	return noMention(strings.ReplaceAll(s, "```", "'''"))
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
		return cfg, errors.New("mattermost config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid mattermost config: %w", err)
	}
	cfg.WebhookURL = strings.TrimSpace(cfg.WebhookURL)
	return cfg, nil
}

func init() {
	plugin.Register(New())
}

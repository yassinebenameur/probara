// Package googlechat implements the Google Chat incoming-webhook alert plugin
// (https://developers.google.com/workspace/chat/quickstart/webhooks). It posts
// a cardsV2 card built from the shared presentation in package present.
//
// Every event of one alert carries the same thread key, so the trigger,
// reminders and the recovery of one outage stay in one thread of the space.
package googlechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

const (
	pluginType = "google_chat"
	// Google Chat caps a message at 32,000 bytes; these keep one card well
	// inside it.
	maxValue     = 1000
	maxLastError = 4000
)

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	WebhookURL string `json:"webhook_url"`
}

// Plugin is the Google Chat alert plugin implementation.
type Plugin struct {
	httpClient *http.Client
}

// New constructs a plugin with the guarded notification HTTP client.
func New() *Plugin {
	return &Plugin{httpClient: plugin.NewHTTPClient(10 * time.Second)}
}

// Manifest returns the Google Chat plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Google Chat",
		Description: "Post alerts to a Google Chat space using an incoming webhook.",
		IconKey:     "google_chat",
		DocsURL:     "https://developers.google.com/workspace/chat/quickstart/webhooks",
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
				Placeholder: "https://chat.googleapis.com/v1/spaces/.../messages?key=...&token=...",
				Help:        "In the space: Apps & integrations → Webhooks → Add webhook, then copy its URL.",
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
	if !plugin.HostMatches(u.Host, "chat.googleapis.com") {
		return errors.New("webhook_url must point at chat.googleapis.com")
	}
	if !strings.HasPrefix(u.Path, "/v1/spaces/") {
		return errors.New("webhook_url must be a Google Chat space webhook (/v1/spaces/…)")
	}
	return nil
}

// Send posts one card into the alert's thread.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	webhook := req.Channel.String("webhook_url")
	if webhook == "" {
		return plugin.Permanent(errors.New("google chat channel missing webhook_url"))
	}
	u, err := url.Parse(webhook)
	if err != nil {
		// The parse error quotes the URL, whose key and token are credentials.
		return plugin.Permanent(errors.New("google chat webhook_url is not a valid URL"))
	}
	// Reply into the alert's thread, or start it.
	q := u.Query()
	q.Set("messageReplyOption", "REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD")
	u.RawQuery = q.Encode()
	return plugin.PostJSON(ctx, p.httpClient, u.String(), build(req.View()), nil, "google chat webhook")
}

type message struct {
	FallbackText string   `json:"fallbackText"`
	CardsV2      []cardV2 `json:"cardsV2"`
	Thread       *thread  `json:"thread,omitempty"`
}

type thread struct {
	ThreadKey string `json:"threadKey"`
}

type cardV2 struct {
	CardID string `json:"cardId"`
	Card   card   `json:"card"`
}

type card struct {
	Header   header    `json:"header"`
	Sections []section `json:"sections"`
}

type header struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
}

type section struct {
	Header  string   `json:"header,omitempty"`
	Widgets []widget `json:"widgets"`
}

type widget struct {
	TextParagraph *textParagraph `json:"textParagraph,omitempty"`
	DecoratedText *decoratedText `json:"decoratedText,omitempty"`
	ButtonList    *buttonList    `json:"buttonList,omitempty"`
}

type textParagraph struct {
	Text string `json:"text"`
}

type decoratedText struct {
	TopLabel string `json:"topLabel"`
	Text     string `json:"text"`
	WrapText bool   `json:"wrapText"`
}

type buttonList struct {
	Buttons []button `json:"buttons"`
}

type button struct {
	Text    string  `json:"text"`
	OnClick onClick `json:"onClick"`
}

type onClick struct {
	OpenLink openLink `json:"openLink"`
}

type openLink struct {
	URL string `json:"url"`
}

func build(m present.Message) message {
	title := emojiFor(m.Tone) + " " + m.Title
	var sections []section
	if m.Summary != "" {
		sections = append(sections, section{Widgets: []widget{{TextParagraph: &textParagraph{Text: text(m.Summary)}}}})
	}
	var facts []widget
	for _, f := range m.Facts() {
		facts = append(facts, widget{DecoratedText: &decoratedText{
			TopLabel: f.Label,
			Text:     text(truncate(f.Value, maxValue)),
			WrapText: true,
		}})
	}
	if len(facts) > 0 {
		sections = append(sections, section{Widgets: facts})
	}
	if m.LastError != "" {
		sections = append(sections, section{
			Header:  "Last error",
			Widgets: []widget{{TextParagraph: &textParagraph{Text: text(truncate(m.LastError, maxLastError))}}},
		})
	}
	var buttons []button
	if m.AckURL != "" {
		buttons = append(buttons, button{Text: m.AckLabel, OnClick: onClick{OpenLink: openLink{URL: m.AckURL}}})
	}
	if m.ActionURL != "" {
		buttons = append(buttons, button{Text: m.ActionLabel, OnClick: onClick{OpenLink: openLink{URL: m.ActionURL}}})
	}
	if len(buttons) > 0 {
		sections = append(sections, section{Widgets: []widget{{ButtonList: &buttonList{Buttons: buttons}}}})
	}
	out := message{
		FallbackText: plainSafe(title),
		CardsV2: []cardV2{{
			CardID: "probara-alert",
			Card: card{
				Header:   header{Title: title, Subtitle: m.StatusWord + " · " + m.SentAt},
				Sections: sections,
			},
		}},
	}
	if m.AlertID != "" {
		out.Thread = &thread{ThreadKey: "probara-" + m.AlertID}
	}
	return out
}

// text escapes s for a card text field, which renders a small HTML subset,
// and keeps its line breaks.
func text(s string) string {
	return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>")
}

// plainSafe keeps a plain-text field from carrying Chat's <users/all>
// mention syntax.
func plainSafe(s string) string {
	return strings.NewReplacer("<", "‹", ">", "›").Replace(s)
}

func emojiFor(t present.Tone) string {
	switch t {
	case present.ToneUp:
		return "✅"
	case present.ToneWarn:
		return "⚠️"
	case present.ToneInfo:
		return "👀"
	default:
		return "🚨"
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
		return cfg, errors.New("google chat config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid google chat config: %w", err)
	}
	cfg.WebhookURL = strings.TrimSpace(cfg.WebhookURL)
	return cfg, nil
}

func init() {
	plugin.Register(New())
}

// Package slackapp implements the slack_app alert channel: a Slack app (bot
// token) instead of an incoming webhook. It posts each alert as a Block Kit
// card with chat.postMessage and saves the card's ts
// (plugin.CapabilityMessageUpdate), posts reminders, the recovery and
// follow-ups as replies in the card's thread, and rewrites the card with
// chat.update on the alerter's "updated" event, so the card always shows the
// alert's current state. Its Acknowledge button
// (plugin.CapabilityInteractive) posts back to the API's public
// /api/v1/integrations/slack/interactions route (interactions.go).
//
// The webhook-based "slack" plugin is a separate, unchanged channel type.
package slackapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

const pluginType = "slack_app"

// APIBase is the Slack Web API root.
const APIBase = "https://slack.com/api"

var (
	teamIDPattern    = regexp.MustCompile(`^[TE][A-Z0-9]{2,}$`)
	channelIDPattern = regexp.MustCompile(`^[CGD][A-Z0-9]{2,}$`)
)

// Plugin is the Slack app alert plugin.
type Plugin struct {
	httpClient *http.Client
	base       string
}

// New constructs a plugin with the guarded notification HTTP client.
func New() *Plugin {
	return &Plugin{httpClient: plugin.NewHTTPClient(10 * time.Second), base: APIBase}
}

// Manifest returns the Slack app plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Slack app",
		Description: "Post alert cards with a Slack app: acknowledge from Slack, and the card follows the alert while reminders and the recovery thread under it.",
		IconKey:     "slack",
		DocsURL:     "https://api.slack.com/apps",
		Version:     "1.0.0",
		Capabilities: []plugin.Capability{
			plugin.CapabilityTestable,
			plugin.CapabilityFollowUp,
			plugin.CapabilityInteractive,
			plugin.CapabilityMessageUpdate,
		},
		Fields: []plugin.Field{
			{
				Key:         "bot_token",
				Label:       "Bot token",
				Type:        plugin.FieldTypeSecret,
				Required:    true,
				Secret:      true,
				Placeholder: "xoxb-…",
				Help:        "The app's Bot User OAuth Token. Add to Slack fills this in.",
			},
			{
				Key:         "team_id",
				Label:       "Workspace ID",
				Type:        plugin.FieldTypeString,
				Required:    true,
				Placeholder: "T0123456789",
				Help:        "The Slack workspace the app is installed in. Button presses from any other workspace are refused.",
			},
			{
				Key:         "channel_id",
				Label:       "Channel ID",
				Type:        plugin.FieldTypeString,
				Required:    true,
				Placeholder: "C0123456789",
				Help:        "The channel cards are posted to. Invite the app to a private channel first.",
			},
			{
				Key:     "interactive",
				Label:   "Acknowledge in Slack",
				Type:    plugin.FieldTypeBool,
				Default: true,
				Help:    "The Acknowledge button acts in Slack. Needs the app's interactivity URL and SLACK_SIGNING_SECRET on the api; off, it opens the signed acknowledge link instead.",
			},
		},
	}
}

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	BotToken    string `json:"bot_token"`
	TeamID      string `json:"team_id"`
	ChannelID   string `json:"channel_id"`
	Interactive *bool  `json:"interactive,omitempty"`
}

// Validate checks the raw config blob before persisting.
func (p *Plugin) Validate(raw json.RawMessage) error {
	var cfg Config
	if len(raw) == 0 {
		return errors.New("slack app config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("invalid slack app config: %w", err)
	}
	token := strings.TrimSpace(cfg.BotToken)
	if !strings.HasPrefix(token, "xoxb-") && !strings.HasPrefix(token, "xoxe.xoxb-") {
		return errors.New("bot_token must be a Slack bot token (xoxb-…)")
	}
	if !teamIDPattern.MatchString(strings.TrimSpace(cfg.TeamID)) {
		return errors.New("team_id must be a Slack workspace ID (T…)")
	}
	if !channelIDPattern.MatchString(strings.TrimSpace(cfg.ChannelID)) {
		return errors.New("channel_id must be a Slack channel ID (C…), not a channel name")
	}
	return nil
}

// messageRef is the card's handle saved in the message store.
type messageRef struct {
	Channel string `json:"channel"`
	TS      string `json:"ts"`
}

// Send posts, threads or rewrites the alert's card depending on the event.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	token := req.Channel.String("bot_token")
	channel := req.Channel.String("channel_id")
	if token == "" || channel == "" {
		return plugin.Permanent(errors.New("slack app channel missing bot_token or channel_id"))
	}
	opts := renderOptions{
		interactive: interactive(req.Channel.Config),
		ackable:     ackable(req),
		channelID:   req.Channel.ID,
	}
	m := req.View()

	switch req.Type() {
	case present.EventCreated:
		return p.postCard(ctx, token, channel, m, opts, req.Messages)
	case present.EventReminder, present.EventResolved, present.EventSuspectedCause:
		ref, ok, err := load(ctx, req.Messages)
		if err != nil {
			// Never fall back to a second card: the retry finds it.
			return fmt.Errorf("load slack card: %w", err)
		}
		if !ok {
			// No card to thread under (posted before this channel could
			// save one, or a test send): this message becomes the card.
			return p.postCard(ctx, token, channel, m, opts, req.Messages)
		}
		_, err = p.call(ctx, token, "chat.postMessage", postMessage{
			Channel:     ref.Channel,
			ThreadTS:    ref.TS,
			Text:        fallbackText(m),
			Blocks:      threadReply(m, opts),
			UnfurlLinks: false,
			UnfurlMedia: false,
		})
		return err
	case present.EventUpdated:
		ref, ok, err := load(ctx, req.Messages)
		if err != nil || !ok {
			return err
		}
		_, err = p.call(ctx, token, "chat.update", updateMessage{
			Channel: ref.Channel,
			TS:      ref.TS,
			Text:    fallbackText(m),
			Blocks:  card(m, opts),
		})
		return err
	default:
		return nil
	}
}

// postCard posts m as the alert's card and saves its handle.
func (p *Plugin) postCard(ctx context.Context, token, channel string, m present.Message, opts renderOptions, store plugin.MessageStore) error {
	resp, err := p.call(ctx, token, "chat.postMessage", postMessage{
		Channel:     channel,
		Text:        fallbackText(m),
		Blocks:      card(m, opts),
		UnfurlLinks: false,
		UnfurlMedia: false,
	})
	if err != nil || store == nil {
		return err
	}
	if resp.TS == "" {
		return plugin.Permanent(errors.New("slack chat.postMessage returned no ts"))
	}
	ch := resp.Channel
	if ch == "" {
		ch = channel
	}
	raw, err := json.Marshal(messageRef{Channel: ch, TS: resp.TS})
	if err != nil {
		return plugin.Permanent(err)
	}
	if err := store.Save(ctx, raw); err != nil {
		// The card is out. A retry would post a second one, so keep the
		// claim; the card just stops following the alert.
		return plugin.Permanent(fmt.Errorf("slack card posted but its reference was not saved: %w", err))
	}
	return nil
}

func load(ctx context.Context, store plugin.MessageStore) (messageRef, bool, error) {
	if store == nil {
		return messageRef{}, false, nil
	}
	raw, ok, err := store.Load(ctx)
	if err != nil || !ok {
		return messageRef{}, false, err
	}
	var ref messageRef
	if err := json.Unmarshal(raw, &ref); err != nil || ref.Channel == "" || ref.TS == "" {
		// An unreadable reference: treat as no card rather than failing
		// every follow-up.
		return messageRef{}, false, nil
	}
	return ref, true, nil
}

// interactive reads the channel's interactive flag (default on).
func interactive(cfg map[string]any) bool {
	v, ok := cfg["interactive"].(bool)
	return !ok || v
}

// ackable reports whether this send's card asks for an acknowledgement: an
// alert that is still active, on a real (not test) send.
func ackable(req plugin.DispatchRequest) bool {
	if req.Test || req.Event.Alert.ID == "" {
		return false
	}
	if req.Event.Alert.Status != "active" && req.Event.Alert.Status != "" {
		return false
	}
	switch req.Type() {
	case present.EventCreated, present.EventReminder, present.EventUpdated:
		return true
	}
	return false
}

func init() {
	plugin.Register(New())
}

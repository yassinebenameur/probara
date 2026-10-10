// Package telegram implements the Telegram Bot API alert plugin
// (https://core.telegram.org/bots/api#sendmessage). Messages use Telegram's
// HTML parse mode, built from the shared presentation in package present.
//
// The bot token travels in the request path, so it is a credential in the
// URL; plugin.Post strips URLs from transport errors so it cannot reach logs.
package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

const (
	pluginType = "telegram"
	apiBase    = "https://api.telegram.org"
	// maxMessage is Telegram's limit on sendMessage text, in characters.
	maxMessage = 4096
)

var (
	botTokenPattern = regexp.MustCompile(`^[0-9]{5,}:[A-Za-z0-9_-]{30,}$`)
	chatIDPattern   = regexp.MustCompile(`^(-?[0-9]+|@[A-Za-z][A-Za-z0-9_]{3,})$`)
	threadIDPattern = regexp.MustCompile(`^[0-9]+$`)
)

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
	ThreadID string `json:"message_thread_id,omitempty"`
}

// Plugin is the Telegram alert plugin implementation.
type Plugin struct {
	httpClient *http.Client
	// base overrides the Bot API origin in tests.
	base string
}

// New constructs a plugin with the guarded notification HTTP client.
func New() *Plugin {
	return &Plugin{httpClient: plugin.NewHTTPClient(10 * time.Second), base: apiBase}
}

// Manifest returns the Telegram plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Telegram",
		Description: "Send alerts to a Telegram chat, group or channel through your own bot.",
		IconKey:     "telegram",
		DocsURL:     "https://core.telegram.org/bots/features#botfather",
		Version:     "1.0.0",
		Capabilities: []plugin.Capability{
			plugin.CapabilityTestable,
		},
		Fields: []plugin.Field{
			{
				Key:         "bot_token",
				Label:       "Bot token",
				Type:        plugin.FieldTypeSecret,
				Required:    true,
				Secret:      true,
				Placeholder: "123456789:AA...",
				Help:        "Create a bot with @BotFather and paste the token it gives you. Add the bot to the target group or channel.",
			},
			{
				Key:         "chat_id",
				Label:       "Chat ID",
				Type:        plugin.FieldTypeString,
				Required:    true,
				Placeholder: "-1001234567890 or @my_channel",
				Help:        "Numeric chat ID (groups and supergroups are negative) or a public channel's @username.",
			},
			{
				Key:         "message_thread_id",
				Label:       "Topic ID",
				Type:        plugin.FieldTypeString,
				Placeholder: "(optional) forum topic thread ID",
				Help:        "Post into a specific topic of a forum-enabled supergroup.",
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
	if !botTokenPattern.MatchString(cfg.BotToken) {
		return errors.New("bot_token must look like 123456789:AA… as issued by @BotFather")
	}
	if !chatIDPattern.MatchString(cfg.ChatID) {
		return errors.New("chat_id must be a numeric chat ID or a @channel username")
	}
	if cfg.ThreadID != "" && !threadIDPattern.MatchString(cfg.ThreadID) {
		return errors.New("message_thread_id must be numeric")
	}
	return nil
}

type sendMessage struct {
	ChatID             string `json:"chat_id"`
	MessageThreadID    int64  `json:"message_thread_id,omitempty"`
	Text               string `json:"text"`
	ParseMode          string `json:"parse_mode"`
	LinkPreviewOptions struct {
		IsDisabled bool `json:"is_disabled"`
	} `json:"link_preview_options"`
}

// Send posts one HTML-formatted message.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	token := req.Channel.String("bot_token")
	chatID := req.Channel.String("chat_id")
	if token == "" || chatID == "" {
		return plugin.Permanent(errors.New("telegram channel missing bot_token or chat_id"))
	}
	body := sendMessage{ChatID: chatID, Text: render(req.View()), ParseMode: "HTML"}
	body.LinkPreviewOptions.IsDisabled = true
	if thread := req.Channel.String("message_thread_id"); thread != "" {
		if _, err := fmt.Sscan(thread, &body.MessageThreadID); err != nil {
			return plugin.Permanent(fmt.Errorf("telegram message_thread_id %q is not numeric", thread))
		}
	}
	target := fmt.Sprintf("%s/bot%s/sendMessage", p.base, token)
	return plugin.PostJSON(ctx, p.httpClient, target, body, nil, "telegram bot api")
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

// render builds the HTML message. Every interpolated value is escaped and
// every length budget is measured on the escaped text: Telegram rejects the
// whole message (a 400, treated as permanent) on malformed markup, so
// truncation must never split an entity or cut a closing tag off. Optional
// parts are dropped whole — error block first, then facts from the end —
// until the message fits.
func render(m present.Message) string {
	head := fmt.Sprintf("%s <b>%s</b>\n", emojiFor(m.Tone), clip(m.Title, 256))
	if m.Summary != "" {
		head += clip(m.Summary, 1024) + "\n"
	}
	var facts []string
	for _, f := range m.Facts() {
		facts = append(facts, fmt.Sprintf("<b>%s:</b> %s\n", clip(f.Label, 64), clip(f.Value, 512)))
	}
	var tail string
	if m.ActionURL != "" {
		tail = fmt.Sprintf("\n<a href=\"%s\">%s</a>", html.EscapeString(m.ActionURL), clip(m.ActionLabel, 64))
	}

	compose := func(facts []string, errBlock string) string {
		var b strings.Builder
		b.WriteString(head)
		if len(facts) > 0 {
			b.WriteString("\n")
			for _, f := range facts {
				b.WriteString(f)
			}
		}
		b.WriteString(errBlock)
		b.WriteString(tail)
		return b.String()
	}

	for {
		base := compose(facts, "")
		var errBlock string
		if m.LastError != "" {
			const wrapper = "\n<pre></pre>\n"
			if room := maxMessage - runeLen(base) - runeLen(wrapper); room > 16 {
				errBlock = "\n<pre>" + clip(m.LastError, room) + "</pre>\n"
			}
		}
		if out := compose(facts, errBlock); runeLen(out) <= maxMessage || len(facts) == 0 {
			return out
		}
		facts = facts[:len(facts)-1]
	}
}

// clip HTML-escapes s and bounds the escaped result to n runes, cutting only
// between whole entities and marking the cut with an ellipsis.
func clip(s string, n int) string {
	escaped := html.EscapeString(s)
	r := []rune(escaped)
	if len(r) <= n {
		return escaped
	}
	cut := string(r[:n-1])
	if i := strings.LastIndexByte(cut, '&'); i >= 0 && !strings.Contains(cut[i:], ";") {
		cut = cut[:i] // do not leave a partial "&am"
	}
	return cut + "…"
}

func runeLen(s string) int { return len([]rune(s)) }

func parseConfig(raw json.RawMessage) (Config, error) {
	var cfg Config
	if len(raw) == 0 {
		return cfg, errors.New("telegram config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid telegram config: %w", err)
	}
	cfg.BotToken = strings.TrimSpace(cfg.BotToken)
	cfg.ChatID = strings.TrimSpace(cfg.ChatID)
	cfg.ThreadID = strings.TrimSpace(cfg.ThreadID)
	return cfg, nil
}

func init() {
	plugin.Register(New())
}

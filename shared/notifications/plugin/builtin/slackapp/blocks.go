package slackapp

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/notifications/present"
)

// Block Kit JSON: https://api.slack.com/reference/block-kit/blocks

type postMessage struct {
	Channel     string  `json:"channel"`
	ThreadTS    string  `json:"thread_ts,omitempty"`
	Text        string  `json:"text"`
	Blocks      []block `json:"blocks,omitempty"`
	UnfurlLinks bool    `json:"unfurl_links"`
	UnfurlMedia bool    `json:"unfurl_media"`
}

type updateMessage struct {
	Channel string  `json:"channel"`
	TS      string  `json:"ts"`
	Text    string  `json:"text"`
	Blocks  []block `json:"blocks"`
}

type block struct {
	Type     string `json:"type"`
	BlockID  string `json:"block_id,omitempty"`
	Text     *text  `json:"text,omitempty"`
	Fields   []text `json:"fields,omitempty"`
	Elements []any  `json:"elements,omitempty"`
}

type text struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type button struct {
	Type     string `json:"type"`
	ActionID string `json:"action_id,omitempty"`
	Text     text   `json:"text"`
	Value    string `json:"value,omitempty"`
	URL      string `json:"url,omitempty"`
	Style    string `json:"style,omitempty"`
}

// Slack caps a section at 10 fields, a header at 150 characters and a
// section's text at 3000. An over-long block fails the whole message with
// invalid_blocks, which is permanent, so budget the variable parts.
const (
	maxFields     = 10
	maxHeaderLen  = 150
	maxSectionLen = 3000
	maxLastError  = maxSectionLen - 100
)

// renderOptions is what the card layout needs beyond the Message.
type renderOptions struct {
	// interactive renders the in-Slack Acknowledge button; off, the button
	// opens the signed acknowledge link (Message.AckURL) when there is one.
	interactive bool
	// ackable: the alert is active and this is a real send.
	ackable bool
	// channelID is the alert channel's ID, carried in the button's value
	// so the API can check the press against the channel's workspace.
	channelID string
}

// card is the alert's card: posted on the trigger, rewritten on "updated".
func card(m present.Message, opts renderOptions) []block {
	blocks := []block{
		{Type: "header", Text: &text{Type: "plain_text", Text: truncateRunes(headline(m), maxHeaderLen)}},
	}
	if m.Summary != "" {
		blocks = append(blocks, block{Type: "section", Text: &text{Type: "mrkdwn", Text: escapeTruncate(m.Summary, maxSectionLen)}})
	}
	if fields := facts(m); len(fields) > 0 {
		blocks = append(blocks, block{Type: "section", Fields: fields})
	}
	if m.LastError != "" && !m.Resolved {
		blocks = append(blocks, block{
			Type: "section",
			Text: &text{Type: "mrkdwn", Text: "*Last error*\n```" + escapeTruncate(m.LastError, maxLastError) + "```"},
		})
	}
	if buttons := actions(m, opts, true); len(buttons) > 0 {
		blocks = append(blocks, block{Type: "actions", BlockID: "probara_actions", Elements: buttons})
	}
	blocks = append(blocks, block{
		Type:     "context",
		Elements: []any{text{Type: "mrkdwn", Text: escape(fmt.Sprintf("%s · %s", m.StatusWord, m.SentAt))}},
	})
	return blocks
}

// threadReply is a reminder, recovery or follow-up under the card: the
// headline and summary, the follow-up's rows, and the acknowledge link when
// the card's own button cannot act in Slack.
func threadReply(m present.Message, opts renderOptions) []block {
	body := "*" + escapeTruncate(headline(m), maxHeaderLen) + "*"
	if m.Summary != "" {
		body += "\n" + escapeTruncate(m.Summary, maxSectionLen-maxHeaderLen-10)
	}
	blocks := []block{{Type: "section", Text: &text{Type: "mrkdwn", Text: body}}}
	if m.EventType == present.EventSuspectedCause {
		if fields := facts(m); len(fields) > 0 {
			blocks = append(blocks, block{Type: "section", Fields: fields})
		}
	}
	if buttons := actions(m, opts, false); len(buttons) > 0 {
		blocks = append(blocks, block{Type: "actions", Elements: buttons})
	}
	blocks = append(blocks, block{
		Type:     "context",
		Elements: []any{text{Type: "mrkdwn", Text: escape(fmt.Sprintf("%s · %s", m.StatusWord, m.SentAt))}},
	})
	return blocks
}

// actions are the card's buttons. onCard: the in-Slack Acknowledge button
// belongs on the card only (one place to press it); a thread reply carries
// the acknowledge link only as the fallback for a non-interactive channel.
func actions(m present.Message, opts renderOptions, onCard bool) []any {
	var out []any
	if opts.ackable {
		switch {
		case opts.interactive && onCard:
			out = append(out, button{
				Type:     "button",
				ActionID: ActionAcknowledge,
				Text:     text{Type: "plain_text", Text: "Acknowledge"},
				Value:    AckValue(m.AlertID, opts.channelID),
				Style:    "primary",
			})
		case !opts.interactive && m.AckURL != "":
			out = append(out, button{
				Type:  "button",
				Text:  text{Type: "plain_text", Text: m.AckLabel},
				URL:   m.AckURL,
				Style: "primary",
			})
		}
	}
	if m.ActionURL != "" && onCard {
		out = append(out, button{
			Type:     "button",
			ActionID: "probara_open",
			Text:     text{Type: "plain_text", Text: m.ActionLabel},
			URL:      m.ActionURL,
		})
	}
	return out
}

func facts(m present.Message) []text {
	var fields []text
	for _, f := range m.Facts() {
		if len(fields) == maxFields {
			break
		}
		fields = append(fields, text{Type: "mrkdwn", Text: fmt.Sprintf("*%s*\n%s", escape(f.Label), escape(f.Value))})
	}
	return fields
}

func headline(m present.Message) string {
	return fmt.Sprintf("%s %s", emojiFor(m.Tone), m.Title)
}

// fallbackText is the notification text for clients that do not render
// blocks (and the push notification itself).
func fallbackText(m present.Message) string {
	return truncateRunes(headline(m), maxHeaderLen)
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

// escapeTruncate escapes s and cuts the result to at most n runes without
// splitting an entity.
func escapeTruncate(s string, n int) string {
	if e := escape(s); utf8.RuneCountInString(e) <= n {
		return e
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		piece := escape(string(r))
		w := utf8.RuneCountInString(piece)
		if used+w > n-1 {
			break
		}
		b.WriteString(piece)
		used += w
	}
	return b.String() + "…"
}

// truncateRunes cuts plain text to n runes.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

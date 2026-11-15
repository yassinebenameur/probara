package present

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/notifications"
)

// ActionAcknowledge is the action an acknowledge link's token authorizes
// (LinkSigner.ActionToken); actionlink.ActionAcknowledge mirrors it.
const ActionAcknowledge = "ack"

// AckPath is the operator-UI page an acknowledge link opens, with the token
// after it. The token rides in the URL fragment, which browsers never send to
// a server: it stays out of web and ingress access logs and Referer headers,
// and the page hands it to the API in a request header. The page reads the
// alert on load and acknowledges only on a button press, because mail
// scanners and chat unfurlers fetch every link they see.
const AckPath = "/alert-action#"

// AckLabel is the caption every channel gives the acknowledge link.
const AckLabel = "Acknowledge"

// LinkSigner mints the signed token behind a one-click action link for one
// alert as delivered on one channel. shared/actionlink implements it; a nil
// signer means no action links.
type LinkSigner interface {
	// ActionToken returns an opaque, URL-safe token authorizing action on
	// alertID in tenantID as delivered on channelID, valid for a fixed time
	// from issuedAt, or an error when it cannot mint one.
	ActionToken(action, tenantID, alertID, channelID string, issuedAt time.Time) (string, error)
}

// Options is the delivery context BuildWith renders links from.
type Options struct {
	// AppBaseURL is the public operator-UI origin; empty omits every link.
	AppBaseURL string
	// Links signs the acknowledge link. Nil omits it; plugin.DispatchRequest
	// leaves it nil on channel test sends.
	Links LinkSigner
	// ChannelID is the channel this delivery goes to. The acknowledge link is
	// bound to it, so an acknowledgement says which channel's link was used.
	ChannelID string
}

// ackFor mints the acknowledge link. Only a page carries one: a created or
// reminder event of an alert that is not resolved. The senders build those
// events with status "active" and stop sending them once an alert is
// acknowledged, so the "acknowledged" check below is a defensive guard, not
// something production relies on. It also needs the operator-UI origin, a
// signer and the delivering channel; with any of them missing, or a signer
// that cannot mint (no key, an alert ID that is not a real alert's), there is
// no link rather than a broken one.
func ackFor(event notifications.AlertEvent, eventType string, resolved bool, opts Options) (string, string) {
	if eventType != EventCreated && eventType != EventReminder {
		return "", ""
	}
	if resolved || event.Alert.Status == "acknowledged" || opts.Links == nil {
		return "", ""
	}
	base := strings.TrimRight(strings.TrimSpace(opts.AppBaseURL), "/")
	if base == "" || strings.TrimSpace(opts.ChannelID) == "" {
		return "", ""
	}
	token, err := opts.Links.ActionToken(ActionAcknowledge, event.TenantID, event.Alert.ID, opts.ChannelID, event.Timestamp)
	if err != nil || token == "" {
		return "", ""
	}
	return base + AckPath + token, AckLabel
}

// ShortWithin is Short bounded to limit runes (0 means unbounded), for an SMS
// or push body. Links are kept whole, since a cut URL is useless: the title
// and summary are clipped to make room, and only when the links alone would
// not fit is the deep link dropped, then the acknowledge link.
func (m Message) ShortWithin(limit int) string {
	head := m.StatusWord + " · " + m.Title
	if m.Summary != "" {
		head += "\n" + m.Summary
	}
	var links []string
	if m.ActionURL != "" {
		links = append(links, m.ActionURL)
	}
	if m.AckURL != "" {
		links = append(links, m.AckLabel+": "+m.AckURL)
	}
	for {
		tail := ""
		for _, l := range links {
			tail += "\n" + l
		}
		if limit <= 0 {
			return head + tail
		}
		// Keep at least a readable headline beside the links.
		room := limit - utf8.RuneCountInString(tail)
		if room >= 16 || len(links) == 0 {
			return clipRunes(head, room) + tail
		}
		links = links[1:]
	}
}

// clipRunes bounds s to n runes, marking a cut with an ellipsis.
func clipRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

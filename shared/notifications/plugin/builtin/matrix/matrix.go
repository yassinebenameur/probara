// Package matrix implements the Matrix alert plugin: one m.room.message per
// event, sent with the client-server API's idempotent PUT
// (https://spec.matrix.org/latest/client-server-api/#put_matrixclientv3roomsroomidsendeventtypetxnid).
// The wording comes from the shared presentation in package present.
//
// The transaction ID is derived from the alert, channel, event type and event
// timestamp, so a retry of a send the homeserver already accepted is
// de-duplicated by the homeserver instead of posted twice, while each
// reminder round (a new event timestamp) is a new message.
//
// Homeservers are often self-hosted, so there is no host check: the
// notification egress policy is the boundary, and a homeserver on a private
// network needs its range in NOTIFICATION_ALLOWED_CIDRS. The access token
// travels in the Authorization header, never the URL.
package matrix

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	pluginType = "matrix"
	// Matrix caps an event at 64 KiB; these keep the plain and HTML bodies
	// of one alert far inside it.
	maxValue     = 512
	maxLastError = 4000
)

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	HomeserverURL string `json:"homeserver_url"`
	AccessToken   string `json:"access_token"`
	RoomID        string `json:"room_id"`
}

// Plugin is the Matrix alert plugin implementation.
type Plugin struct {
	httpClient *http.Client
}

// New constructs a plugin with the guarded notification HTTP client.
func New() *Plugin {
	return &Plugin{httpClient: plugin.NewHTTPClient(10 * time.Second)}
}

// Manifest returns the Matrix plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Matrix",
		Description: "Post alerts to a Matrix room as a bot account.",
		IconKey:     "matrix",
		DocsURL:     "https://spec.matrix.org/latest/client-server-api/",
		Version:     "1.0.0",
		Capabilities: []plugin.Capability{
			plugin.CapabilityTestable,
			plugin.CapabilityFollowUp,
		},
		Fields: []plugin.Field{
			{
				Key:         "homeserver_url",
				Label:       "Homeserver URL",
				Type:        plugin.FieldTypeURL,
				Required:    true,
				Placeholder: "https://matrix.example.org",
				Help:        "The client-server API address of the bot account's homeserver (for matrix.org accounts, https://matrix-client.matrix.org). A homeserver on a private network must be allowed in NOTIFICATION_ALLOWED_CIDRS.",
			},
			{
				Key:         "access_token",
				Label:       "Access token",
				Type:        plugin.FieldTypeSecret,
				Required:    true,
				Secret:      true,
				Placeholder: "syt_...",
				Help:        "An access token for a dedicated bot account. The account must already be joined to the room.",
			},
			{
				Key:         "room_id",
				Label:       "Room ID",
				Type:        plugin.FieldTypeString,
				Required:    true,
				Placeholder: "!AbCdEfGhIjKlMnOp:example.org",
				Help:        "The internal room ID (Room settings → Advanced), not a #room:server alias.",
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
	if _, err := plugin.ParseHTTPSURL(cfg.HomeserverURL, "homeserver_url"); err != nil {
		return err
	}
	if cfg.AccessToken == "" {
		return errors.New("access_token is required")
	}
	if !validRoomID(cfg.RoomID) {
		return errors.New("room_id must be a room ID starting with ! (an alias like #room:server is not accepted)")
	}
	return nil
}

// validRoomID accepts "!opaque:server" and the newer server-less "!opaque"
// form, rejecting whitespace and path separators.
func validRoomID(id string) bool {
	if len(id) < 2 || len(id) > 255 || id[0] != '!' {
		return false
	}
	return !strings.ContainsAny(id, " \t\r\n/?#")
}

// content is an m.room.message event body.
type content struct {
	MsgType       string `json:"msgtype"`
	Body          string `json:"body"`
	Format        string `json:"format"`
	FormattedBody string `json:"formatted_body"`
	// Mentions is always empty: its presence switches clients and push
	// rules to intentional mentions, so "@room" or a user ID inside alert
	// text notifies nobody.
	Mentions struct{} `json:"m.mentions"`
}

// Send PUTs one message into the room under a deterministic transaction ID.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	homeserver := req.Channel.String("homeserver_url")
	token := req.Channel.String("access_token")
	room := req.Channel.String("room_id")
	if homeserver == "" || token == "" || room == "" {
		return plugin.Permanent(errors.New("matrix channel missing homeserver_url, access_token or room_id"))
	}
	m := req.View()
	target := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message/%s",
		strings.TrimRight(homeserver, "/"), url.PathEscape(room), txnID(req, m))
	headers := map[string]string{"Authorization": "Bearer " + token}
	return plugin.PutJSON(ctx, p.httpClient, target, build(m), headers, "matrix homeserver")
}

// txnID names one send of one event on one channel. A retry of that send
// (same alert, channel, event type and event timestamp) reuses it, which the
// homeserver de-duplicates; the next reminder carries a new timestamp.
func txnID(req plugin.DispatchRequest, m present.Message) string {
	key := strings.Join([]string{
		req.Event.Alert.ID, req.Channel.ID, req.Type(),
		m.Timestamp.UTC().Format(time.RFC3339Nano),
	}, "|")
	sum := sha256.Sum256([]byte(key))
	return "probara-" + hex.EncodeToString(sum[:16])
}

func build(m present.Message) content {
	var plain, rich strings.Builder
	title := emojiFor(m.Tone) + " " + m.Title
	plain.WriteString(title)
	fmt.Fprintf(&rich, "<strong>%s</strong>", html.EscapeString(title))
	if m.Summary != "" {
		plain.WriteString("\n" + m.Summary)
		rich.WriteString("<br>" + html.EscapeString(m.Summary))
	}
	if facts := m.Facts(); len(facts) > 0 {
		plain.WriteString("\n")
		rich.WriteString("<br>")
		for _, f := range facts {
			v := truncate(f.Value, maxValue)
			plain.WriteString("\n" + f.Label + ": " + v)
			fmt.Fprintf(&rich, "<br><strong>%s:</strong> %s", html.EscapeString(f.Label), html.EscapeString(v))
		}
	}
	if m.LastError != "" {
		e := truncate(m.LastError, maxLastError)
		plain.WriteString("\n\nLast error:\n" + e)
		fmt.Fprintf(&rich, "<br><br><strong>Last error</strong><pre><code>%s</code></pre>", html.EscapeString(e))
	}
	if m.AckURL != "" {
		plain.WriteString("\n\n" + m.AckLabel + ": " + m.AckURL)
		fmt.Fprintf(&rich, "<br><br><a href=\"%s\"><strong>%s</strong></a>", html.EscapeString(m.AckURL), html.EscapeString(m.AckLabel))
	}
	if m.ActionURL != "" {
		plain.WriteString("\n\n" + m.ActionLabel + ": " + m.ActionURL)
		fmt.Fprintf(&rich, "<br><a href=\"%s\">%s</a>", html.EscapeString(m.ActionURL), html.EscapeString(m.ActionLabel))
	}
	return content{
		// m.text, not m.notice: the default push rules silence notices,
		// and an alert must notify.
		MsgType:       "m.text",
		Body:          plain.String(),
		Format:        "org.matrix.custom.html",
		FormattedBody: rich.String(),
	}
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
		return cfg, errors.New("matrix config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid matrix config: %w", err)
	}
	cfg.HomeserverURL = strings.TrimSpace(cfg.HomeserverURL)
	cfg.AccessToken = strings.TrimSpace(cfg.AccessToken)
	cfg.RoomID = strings.TrimSpace(cfg.RoomID)
	return cfg, nil
}

func init() {
	plugin.Register(New())
}

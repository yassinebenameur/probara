// Package gotify implements the Gotify push-notification alert plugin
// (https://gotify.net/api-docs#/message/createMessage). Gotify is always
// self-hosted, so there is no host check: the notification egress policy is
// the boundary, and a server on a private network needs its range in
// NOTIFICATION_ALLOWED_CIDRS. The wording comes from the shared presentation
// in package present.
//
// The application token travels in the X-Gotify-Key header rather than the
// ?token= query parameter, so it never sits in a URL.
package gotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/pushtext"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

const (
	pluginType = "gotify"
	maxMessage = 4000
	maxTitle   = 250
)

// Gotify priorities run 0-10. The Android client shows 8 and above as a
// heads-up notification, 4-7 with sound, 1-3 silently.
const (
	priorityLow     = 2
	priorityRecover = 4
	priorityNormal  = 5
	priorityHigh    = 8
	priorityHighest = 10
)

// defaultOutagePriority is what an outage is sent at unless the channel
// raises or lowers it.
const defaultOutagePriority = priorityHigh

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	ServerURL      string `json:"server_url"`
	AppToken       string `json:"app_token"`
	OutagePriority string `json:"priority,omitempty"`
}

// Plugin is the Gotify alert plugin implementation.
type Plugin struct {
	httpClient *http.Client
}

// New constructs a plugin with the guarded notification HTTP client.
func New() *Plugin {
	return &Plugin{httpClient: plugin.NewHTTPClient(10 * time.Second)}
}

// Manifest returns the Gotify plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Gotify",
		Description: "Push alerts to a self-hosted Gotify server.",
		IconKey:     "gotify",
		DocsURL:     "https://gotify.net/docs/pushmsg",
		Version:     "1.0.0",
		Capabilities: []plugin.Capability{
			plugin.CapabilityTestable,
			plugin.CapabilityFollowUp,
		},
		Fields: []plugin.Field{
			{
				Key:         "server_url",
				Label:       "Server URL",
				Type:        plugin.FieldTypeURL,
				Required:    true,
				Placeholder: "https://gotify.example.com",
				Help:        "Your Gotify server's HTTPS address. A server on a private network is refused unless its range is listed in NOTIFICATION_ALLOWED_CIDRS.",
			},
			{
				Key:         "app_token",
				Label:       "Application token",
				Type:        plugin.FieldTypeSecret,
				Required:    true,
				Secret:      true,
				Placeholder: "A...",
				Help:        "In Gotify: Apps → Create Application, then copy its token.",
			},
			{
				Key:     "priority",
				Label:   "Outage priority",
				Type:    plugin.FieldTypeSelect,
				Default: strconv.Itoa(defaultOutagePriority),
				Options: []plugin.Option{
					{Value: strconv.Itoa(priorityHighest), Label: "Highest (10)"},
					{Value: strconv.Itoa(priorityHigh), Label: "High (8)"},
					{Value: strconv.Itoa(priorityNormal), Label: "Normal (5)"},
				},
				Help: "Priority for outages. Degradations (latency, host metrics, certificate and domain expiry) are sent one level lower, recoveries at 4, acknowledgements and AI triage notes at 2.",
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
	if _, err := plugin.ParseHTTPSURL(cfg.ServerURL, "server_url"); err != nil {
		return err
	}
	if cfg.AppToken == "" {
		return errors.New("app_token is required")
	}
	if strings.ContainsAny(cfg.AppToken, " \t\r\n") {
		return errors.New("app_token must not contain whitespace")
	}
	if _, ok := outagePriority(cfg.OutagePriority); !ok {
		return fmt.Errorf("priority must be 5, 8 or 10 (got %q)", cfg.OutagePriority)
	}
	return nil
}

type message struct {
	Title    string         `json:"title"`
	Message  string         `json:"message"`
	Priority int            `json:"priority"`
	Extras   map[string]any `json:"extras,omitempty"`
}

// Send posts one message to the server's /message endpoint. A test send is
// an ordinary message: it is the only thing it leaves on the server.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	server := req.Channel.String("server_url")
	token := req.Channel.String("app_token")
	if server == "" || token == "" {
		return plugin.Permanent(errors.New("gotify channel missing server_url or app_token"))
	}
	target, err := url.JoinPath(server, "message")
	if err != nil {
		return plugin.Permanent(errors.New("gotify channel has an invalid server_url"))
	}
	// Validate refuses an unknown priority, so this only falls back for a
	// config written around the API; a priority is never worth dropping an
	// alert over.
	outage, _ := outagePriority(req.Channel.String("priority"))
	headers := map[string]string{"X-Gotify-Key": token}
	return plugin.PostJSON(ctx, p.httpClient, target, build(outage, req.View()), headers, "gotify")
}

func build(outage int, m present.Message) message {
	// The web UI and the desktop clients ignore the click extra, so every
	// link is also printed under the body, which is clipped to leave room.
	var links strings.Builder
	for _, l := range pushtext.Links(m) {
		links.WriteString("\n\n" + l.Label + ": " + l.URL)
	}
	body := pushtext.Body(m, max(maxMessage-links.Len(), 0))
	if body == "" {
		body = m.StatusWord
	}
	out := message{
		Title:    pushtext.Clip(m.Title, maxTitle),
		Message:  body + links.String(),
		Priority: priorityFor(m.Tone, outage),
		Extras: map[string]any{
			// Plain text: alert text (monitor names, probe errors) must not
			// be interpreted as markdown.
			"client::display": map[string]string{"contentType": "text/plain"},
		},
	}
	if m.ActionURL != "" {
		// Tapping the Android notification opens the monitor.
		out.Extras["client::notification"] = map[string]any{
			"click": map[string]string{"url": m.ActionURL},
		}
	}
	return out
}

// outagePriority parses the channel's outage priority. Empty means the
// default, so a channel created through the API without the field still
// validates.
func outagePriority(v string) (int, bool) {
	switch strings.TrimSpace(v) {
	case "":
		return defaultOutagePriority, true
	case "10":
		return priorityHighest, true
	case "8":
		return priorityHigh, true
	case "5":
		return priorityNormal, true
	}
	return defaultOutagePriority, false
}

// priorityFor maps the tone onto Gotify's scale: an outage at the channel's
// outage priority, a degradation one level below it (never below normal), a
// recovery with sound but no heads-up, and an advisory (acknowledgement, AI
// triage) silently.
func priorityFor(t present.Tone, outage int) int {
	switch t {
	case present.ToneDown:
		return outage
	case present.ToneWarn:
		switch outage {
		case priorityHighest:
			return priorityHigh
		default:
			return priorityNormal
		}
	case present.ToneUp:
		return priorityRecover
	default:
		return priorityLow
	}
}

func parseConfig(raw json.RawMessage) (Config, error) {
	var cfg Config
	if len(raw) == 0 {
		return cfg, errors.New("gotify config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid gotify config: %w", err)
	}
	cfg.ServerURL = strings.TrimSpace(cfg.ServerURL)
	cfg.AppToken = strings.TrimSpace(cfg.AppToken)
	cfg.OutagePriority = strings.TrimSpace(cfg.OutagePriority)
	return cfg, nil
}

func init() {
	plugin.Register(New())
}

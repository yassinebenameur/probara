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

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	ServerURL string `json:"server_url"`
	AppToken  string `json:"app_token"`
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
				Help:        "Your Gotify server's HTTPS address. A server on a private network must be allowed in NOTIFICATION_ALLOWED_CIDRS.",
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
	return nil
}

type message struct {
	Title    string         `json:"title"`
	Message  string         `json:"message"`
	Priority int            `json:"priority"`
	Extras   map[string]any `json:"extras,omitempty"`
}

// Send posts one message to the server's /message endpoint.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	server := req.Channel.String("server_url")
	token := req.Channel.String("app_token")
	if server == "" || token == "" {
		return plugin.Permanent(errors.New("gotify channel missing server_url or app_token"))
	}
	target := strings.TrimRight(server, "/") + "/message"
	headers := map[string]string{"X-Gotify-Key": token}
	return plugin.PostJSON(ctx, p.httpClient, target, build(req.View()), headers, "gotify")
}

func build(m present.Message) message {
	body := pushtext.BodyWithAck(m, maxMessage)
	if m.ActionURL != "" {
		// The web UI and desktop clients ignore the click extra, so the link
		// is also printed.
		body += "\n\n" + m.ActionLabel + ": " + m.ActionURL
	}
	out := message{
		Title:    pushtext.Clip(m.Title, maxTitle),
		Message:  strings.TrimSpace(body),
		Priority: priorityFor(m.Tone),
		Extras: map[string]any{
			"client::display": map[string]string{"contentType": "text/plain"},
		},
	}
	if out.Message == "" {
		out.Message = m.StatusWord
	}
	if m.ActionURL != "" {
		out.Extras["client::notification"] = map[string]any{
			"click": map[string]string{"url": m.ActionURL},
		}
	}
	return out
}

// priorityFor maps the tone onto Gotify's 0-10 scale. The Android client
// shows 8 and above as a heads-up notification, 4-7 with sound, 1-3 silently.
func priorityFor(t present.Tone) int {
	switch t {
	case present.ToneDown:
		return 8
	case present.ToneWarn:
		return 5
	case present.ToneUp:
		return 4
	default:
		return 2
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
	return cfg, nil
}

func init() {
	plugin.Register(New())
}

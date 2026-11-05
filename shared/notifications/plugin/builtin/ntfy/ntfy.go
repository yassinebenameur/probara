// Package ntfy implements the ntfy push-notification alert plugin
// (https://docs.ntfy.sh/publish/). It publishes through ntfy's JSON endpoint
// (a POST to the server root naming the topic in the body), which carries a
// UTF-8 title without the header encoding the per-topic endpoint needs. The
// wording comes from the shared presentation in package present.
//
// The server defaults to the public ntfy.sh but can be any self-hosted
// instance, so there is no host check: the notification egress policy is the
// boundary, and a server on a private network needs its range in
// NOTIFICATION_ALLOWED_CIDRS.
package ntfy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/pushtext"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

const (
	pluginType = "ntfy"
	// DefaultServer is the public ntfy instance, used when server_url is
	// left empty.
	DefaultServer = "https://ntfy.sh"
	// maxMessage stays under ntfy's default 4096-byte message limit; a
	// longer body is turned into an attachment or rejected.
	maxMessage = 4000
	maxTitle   = 250
)

// topicPattern is ntfy's own topic name rule.
var topicPattern = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	ServerURL   string `json:"server_url,omitempty"`
	Topic       string `json:"topic"`
	AccessToken string `json:"access_token,omitempty"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
}

// Plugin is the ntfy alert plugin implementation.
type Plugin struct {
	httpClient *http.Client
}

// New constructs a plugin with the guarded notification HTTP client.
func New() *Plugin {
	return &Plugin{httpClient: plugin.NewHTTPClient(10 * time.Second)}
}

// Manifest returns the ntfy plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "ntfy",
		Description: "Push alerts to phones and desktops through an ntfy topic, on ntfy.sh or your own server.",
		IconKey:     "ntfy",
		DocsURL:     "https://docs.ntfy.sh/",
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
				Default:     DefaultServer,
				Placeholder: DefaultServer,
				Help:        "Leave as https://ntfy.sh for the public server, or enter your own instance. A server on a private network must be allowed in NOTIFICATION_ALLOWED_CIDRS.",
			},
			{
				Key:         "topic",
				Label:       "Topic",
				Type:        plugin.FieldTypeSecret,
				Required:    true,
				Secret:      true,
				Placeholder: "probara-alerts-7f3k9",
				Help:        "Letters, digits, - and _ (up to 64). On a server without access control the topic name is the only thing protecting it, so it is stored encrypted: pick one that is hard to guess.",
			},
			{
				Key:         "access_token",
				Label:       "Access token",
				Type:        plugin.FieldTypeSecret,
				Secret:      true,
				Placeholder: "(optional) tk_...",
				Help:        "For a topic that requires authentication. Use either a token or a username and password.",
			},
			{
				Key:         "username",
				Label:       "Username",
				Type:        plugin.FieldTypeString,
				Placeholder: "(optional)",
			},
			{
				Key:         "password",
				Label:       "Password",
				Type:        plugin.FieldTypeSecret,
				Secret:      true,
				Placeholder: "(optional)",
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
	if _, err := plugin.ParseHTTPSURL(cfg.server(), "server_url"); err != nil {
		return err
	}
	if !topicPattern.MatchString(cfg.Topic) {
		return errors.New("topic must be 1-64 letters, digits, - or _")
	}
	if cfg.AccessToken != "" && (cfg.Username != "" || cfg.Password != "") {
		return errors.New("use either access_token or username and password, not both")
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return errors.New("username and password must be set together")
	}
	return nil
}

// message is ntfy's JSON publish body.
type message struct {
	Topic    string   `json:"topic"`
	Title    string   `json:"title,omitempty"`
	Message  string   `json:"message"`
	Priority int      `json:"priority"`
	Tags     []string `json:"tags,omitempty"`
	Click    string   `json:"click,omitempty"`
	Actions  []action `json:"actions,omitempty"`
}

type action struct {
	Action string `json:"action"`
	Label  string `json:"label"`
	URL    string `json:"url"`
}

// Send publishes one notification to the configured topic.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	cfg := Config{
		ServerURL:   req.Channel.String("server_url"),
		Topic:       req.Channel.String("topic"),
		AccessToken: req.Channel.String("access_token"),
		Username:    req.Channel.String("username"),
		Password:    req.Channel.String("password"),
	}
	if cfg.Topic == "" {
		return plugin.Permanent(errors.New("ntfy channel missing topic"))
	}
	headers := map[string]string{}
	switch {
	case cfg.AccessToken != "":
		headers["Authorization"] = "Bearer " + cfg.AccessToken
	case cfg.Username != "":
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.Username+":"+cfg.Password))
	}
	target := strings.TrimRight(cfg.server(), "/") + "/"
	return plugin.PostJSON(ctx, p.httpClient, target, build(cfg.Topic, req.View()), headers, "ntfy")
}

func build(topic string, m present.Message) message {
	out := message{
		Topic:    topic,
		Title:    pushtext.Clip(m.Title, maxTitle),
		Message:  pushtext.Body(m, maxMessage),
		Priority: priorityFor(m.Tone),
		Tags:     []string{tagFor(m.Tone)},
		Click:    m.ActionURL,
	}
	if out.Message == "" {
		// ntfy substitutes "triggered" for an empty body.
		out.Message = m.StatusWord
	}
	if m.ActionURL != "" {
		out.Actions = []action{{Action: "view", Label: m.ActionLabel, URL: m.ActionURL}}
	}
	return out
}

// priorityFor maps the tone onto ntfy's 1 (min) to 5 (max) scale: an outage
// is urgent, a degradation high, a recovery default, an advisory low.
func priorityFor(t present.Tone) int {
	switch t {
	case present.ToneDown:
		return 5
	case present.ToneWarn:
		return 4
	case present.ToneUp:
		return 3
	default:
		return 2
	}
}

// tagFor picks the emoji tag ntfy shows before the title.
func tagFor(t present.Tone) string {
	switch t {
	case present.ToneUp:
		return "white_check_mark"
	case present.ToneWarn:
		return "warning"
	case present.ToneInfo:
		return "eyes"
	default:
		return "rotating_light"
	}
}

func (c Config) server() string {
	if c.ServerURL == "" {
		return DefaultServer
	}
	return c.ServerURL
}

func parseConfig(raw json.RawMessage) (Config, error) {
	var cfg Config
	if len(raw) == 0 {
		return cfg, errors.New("ntfy config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid ntfy config: %w", err)
	}
	cfg.ServerURL = strings.TrimSpace(cfg.ServerURL)
	cfg.Topic = strings.TrimSpace(cfg.Topic)
	cfg.AccessToken = strings.TrimSpace(cfg.AccessToken)
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.Password = strings.TrimSpace(cfg.Password)
	return cfg, nil
}

func init() {
	plugin.Register(New())
}

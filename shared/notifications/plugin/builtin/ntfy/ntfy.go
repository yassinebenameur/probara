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
	"strconv"
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
	// maxActions is ntfy's limit on action buttons per message.
	maxActions = 3
)

// ntfy priorities: 1 min, 2 low, 3 default, 4 high, 5 urgent.
const (
	priorityLow     = 2
	priorityDefault = 3
	priorityHigh    = 4
	priorityUrgent  = 5
)

// defaultOutagePriority is what an outage is sent at unless the channel
// raises or lowers it.
const defaultOutagePriority = priorityHigh

// topicPattern is ntfy's own topic name rule.
var topicPattern = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	ServerURL      string `json:"server_url,omitempty"`
	Topic          string `json:"topic"`
	AccessToken    string `json:"access_token,omitempty"`
	Username       string `json:"username,omitempty"`
	Password       string `json:"password,omitempty"`
	OutagePriority string `json:"priority,omitempty"`
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
				Help:        "Keep https://ntfy.sh for the public server, or enter your own instance's HTTPS address. A server on a private network is refused unless its range is listed in NOTIFICATION_ALLOWED_CIDRS.",
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
			{
				Key:     "priority",
				Label:   "Outage priority",
				Type:    plugin.FieldTypeSelect,
				Default: strconv.Itoa(defaultOutagePriority),
				Options: []plugin.Option{
					{Value: strconv.Itoa(priorityUrgent), Label: "Urgent (5)"},
					{Value: strconv.Itoa(priorityHigh), Label: "High (4)"},
					{Value: strconv.Itoa(priorityDefault), Label: "Default (3)"},
				},
				Help: "Priority for outages. Degradations (latency, host metrics, certificate and domain expiry) are sent one level lower, recoveries at default (3), acknowledgements and AI triage notes at low (2).",
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
	if strings.ContainsAny(cfg.AccessToken, " \t\r\n") {
		return errors.New("access_token must not contain whitespace")
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return errors.New("username and password must be set together")
	}
	if strings.Contains(cfg.Username, ":") {
		return errors.New("username must not contain ':'")
	}
	if _, ok := outagePriority(cfg.OutagePriority); !ok {
		return fmt.Errorf("priority must be 3, 4 or 5 (got %q)", cfg.OutagePriority)
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

// Send publishes one notification to the configured topic. A test send is an
// ordinary publish: the message is the only thing it leaves on the server.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	cfg := Config{
		ServerURL:   req.Channel.String("server_url"),
		Topic:       req.Channel.String("topic"),
		AccessToken: req.Channel.String("access_token"),
		Username:    req.Channel.String("username"),
		// Read untrimmed: a password is used exactly as it was entered.
		Password:       rawString(req.Channel, "password"),
		OutagePriority: req.Channel.String("priority"),
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
	// Validate refuses an unknown priority, so this only falls back for a
	// config written around the API; a priority is never worth dropping an
	// alert over.
	outage, _ := outagePriority(cfg.OutagePriority)
	target := strings.TrimRight(cfg.server(), "/") + "/"
	return plugin.PostJSON(ctx, p.httpClient, target, build(cfg.Topic, outage, req.View()), headers, "ntfy")
}

func build(topic string, outage int, m present.Message) message {
	out := message{
		Topic:    topic,
		Title:    pushtext.Clip(m.Title, maxTitle),
		Message:  pushtext.Body(m, maxMessage),
		Priority: priorityFor(m.Tone, outage),
		Tags:     []string{tagFor(m.Tone)},
		Click:    m.ActionURL,
	}
	if out.Message == "" {
		// ntfy substitutes "triggered" for an empty body.
		out.Message = m.StatusWord
	}
	// "view" opens the link in a browser. An "http" action would make the
	// phone call the URL itself, skipping whatever confirmation the page
	// asks for.
	for _, l := range pushtext.Links(m) {
		if len(out.Actions) == maxActions {
			break
		}
		out.Actions = append(out.Actions, action{Action: "view", Label: l.Label, URL: l.URL})
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
	case "5":
		return priorityUrgent, true
	case "4":
		return priorityHigh, true
	case "3":
		return priorityDefault, true
	}
	return defaultOutagePriority, false
}

// priorityFor maps the tone onto ntfy's 1-5 scale: an outage at the
// channel's outage priority, a degradation one level below it (never below
// default), a recovery at default and an advisory (acknowledgement, AI
// triage) at low, so it never buzzes like a page.
func priorityFor(t present.Tone, outage int) int {
	switch t {
	case present.ToneDown:
		return outage
	case present.ToneWarn:
		return max(outage-1, priorityDefault)
	case present.ToneUp:
		return priorityDefault
	default:
		return priorityLow
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

func rawString(c plugin.ChannelRef, key string) string {
	s, _ := c.Config[key].(string)
	return s
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
	cfg.OutagePriority = strings.TrimSpace(cfg.OutagePriority)
	return cfg, nil
}

func init() {
	plugin.Register(New())
}

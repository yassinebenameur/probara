// Package pushover implements the Pushover alert plugin
// (https://pushover.net/api). The API endpoint is fixed, so a channel
// configures credentials only and there is no host to check. The wording
// comes from the shared presentation in package present.
//
// Emergency priority (2) is never used: it makes Pushover re-alert until
// someone acknowledges the receipt, which would leave provider state behind a
// test send and would need receipt cancellation on resolve. Outages go out at
// high priority (1), which already bypasses the recipient's quiet hours.
package pushover

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
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/pushtext"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

const (
	pluginType = "pushover"
	apiBase    = "https://api.pushover.net"
	// Pushover's documented limits.
	maxMessage  = 1024
	maxTitle    = 250
	maxURL      = 512
	maxURLTitle = 100
)

var (
	// Application tokens and user/group keys are 30 alphanumerics.
	keyPattern    = regexp.MustCompile(`^[A-Za-z0-9]{30}$`)
	devicePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,25}(,[A-Za-z0-9_-]{1,25})*$`)
)

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	AppToken string `json:"app_token"`
	UserKey  string `json:"user_key"`
	Device   string `json:"device,omitempty"`
}

// Plugin is the Pushover alert plugin implementation.
type Plugin struct {
	httpClient *http.Client
	// base overrides the API origin in tests.
	base string
}

// New constructs a plugin with the guarded notification HTTP client.
func New() *Plugin {
	return &Plugin{httpClient: plugin.NewHTTPClient(10 * time.Second), base: apiBase}
}

// Manifest returns the Pushover plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Pushover",
		Description: "Push alerts to Pushover users or delivery groups.",
		IconKey:     "pushover",
		DocsURL:     "https://pushover.net/api",
		Version:     "1.0.0",
		Capabilities: []plugin.Capability{
			plugin.CapabilityTestable,
			plugin.CapabilityFollowUp,
		},
		Fields: []plugin.Field{
			{
				Key:         "app_token",
				Label:       "Application token",
				Type:        plugin.FieldTypeSecret,
				Required:    true,
				Secret:      true,
				Placeholder: "30-character application token",
				Help:        "Register an application at pushover.net/apps and paste its API token.",
			},
			{
				Key:         "user_key",
				Label:       "User or group key",
				Type:        plugin.FieldTypeSecret,
				Required:    true,
				Secret:      true,
				Placeholder: "30-character user or group key",
				Help:        "The recipient's user key, or a delivery group key to reach several people.",
			},
			{
				Key:         "device",
				Label:       "Devices",
				Type:        plugin.FieldTypeString,
				Placeholder: "(optional) iphone,desktop",
				Help:        "Limit delivery to these device names, comma-separated. Empty sends to all of the user's devices.",
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
	if !keyPattern.MatchString(cfg.AppToken) {
		return errors.New("app_token must be the 30-character application token from pushover.net")
	}
	if !keyPattern.MatchString(cfg.UserKey) {
		return errors.New("user_key must be a 30-character Pushover user or group key")
	}
	if cfg.Device != "" && !devicePattern.MatchString(cfg.Device) {
		return errors.New("device must be comma-separated device names (letters, digits, - and _, up to 25 each)")
	}
	return nil
}

type message struct {
	Token     string `json:"token"`
	User      string `json:"user"`
	Device    string `json:"device,omitempty"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	Priority  int    `json:"priority"`
	Timestamp int64  `json:"timestamp,omitempty"`
	URL       string `json:"url,omitempty"`
	URLTitle  string `json:"url_title,omitempty"`
}

// Send posts one message to the Pushover messages API.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	token := req.Channel.String("app_token")
	user := req.Channel.String("user_key")
	if token == "" || user == "" {
		return plugin.Permanent(errors.New("pushover channel missing app_token or user_key"))
	}
	body := build(req.View())
	body.Token, body.User, body.Device = token, user, req.Channel.String("device")
	return plugin.PostJSON(ctx, p.httpClient, p.base+"/1/messages.json", body, nil, "pushover api")
}

func build(m present.Message) message {
	out := message{
		Title:    pushtext.Clip(m.Title, maxTitle),
		Message:  pushtext.Body(m, maxMessage),
		Priority: priorityFor(m.Tone),
	}
	if out.Message == "" {
		out.Message = m.StatusWord
	}
	if !m.Timestamp.IsZero() {
		// Shows when the event happened, not when a retry delivered it.
		out.Timestamp = m.Timestamp.Unix()
	}
	if m.ActionURL != "" && len(m.ActionURL) <= maxURL {
		out.URL = m.ActionURL
		out.URLTitle = pushtext.Clip(m.ActionLabel, maxURLTitle)
	}
	return out
}

// priorityFor maps the tone onto Pushover's -2..2 scale without ever using
// emergency (2): an outage is high (sounds through quiet hours), degradation
// and recovery normal, an advisory quiet (-1).
func priorityFor(t present.Tone) int {
	switch t {
	case present.ToneDown:
		return 1
	case present.ToneWarn, present.ToneUp:
		return 0
	default:
		return -1
	}
}

func parseConfig(raw json.RawMessage) (Config, error) {
	var cfg Config
	if len(raw) == 0 {
		return cfg, errors.New("pushover config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid pushover config: %w", err)
	}
	cfg.AppToken = strings.TrimSpace(cfg.AppToken)
	cfg.UserKey = strings.TrimSpace(cfg.UserKey)
	cfg.Device = strings.TrimSpace(cfg.Device)
	return cfg, nil
}

func init() {
	plugin.Register(New())
}

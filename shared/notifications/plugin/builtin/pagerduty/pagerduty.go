// Package pagerduty implements the PagerDuty alert plugin over the Events API
// v2 (https://developer.pagerduty.com/docs/events-api-v2-overview).
//
// Each Probara alert maps to one PagerDuty alert through a deterministic
// dedup_key, so the lifecycle stays in sync without storing any provider
// reference: created → trigger, acknowledged → acknowledge, resolved →
// resolve. Reminders are skipped — PagerDuty runs its own escalation and
// re-notification, and a repeated trigger would only be deduplicated.
package pagerduty

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

const pluginType = "pagerduty"

// Events API endpoints per PagerDuty service region.
var endpoints = map[string]string{
	"us": "https://events.pagerduty.com/v2/enqueue",
	"eu": "https://events.eu.pagerduty.com/v2/enqueue",
}

// routingKeyPattern matches an Events API v2 integration (routing) key.
var routingKeyPattern = regexp.MustCompile(`^[A-Za-z0-9]{32}$`)

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	RoutingKey string `json:"routing_key"`
	Region     string `json:"region,omitempty"`
}

// Plugin is the PagerDuty alert plugin implementation.
type Plugin struct {
	httpClient *http.Client
	// endpoint overrides the region lookup in tests.
	endpoint string
}

// New constructs a plugin with the guarded notification HTTP client.
func New() *Plugin {
	return &Plugin{httpClient: plugin.NewHTTPClient(10 * time.Second)}
}

// Manifest returns the PagerDuty plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "PagerDuty",
		Description: "Open, acknowledge and resolve PagerDuty incidents through an Events API v2 integration.",
		IconKey:     "pagerduty",
		DocsURL:     "https://support.pagerduty.com/main/docs/services-and-integrations#create-a-generic-events-api-integration",
		Version:     "1.0.0",
		Capabilities: []plugin.Capability{
			plugin.CapabilityTestable,
			plugin.CapabilityAcknowledge,
		},
		Fields: []plugin.Field{
			{
				Key:         "routing_key",
				Label:       "Integration key",
				Type:        plugin.FieldTypeSecret,
				Required:    true,
				Secret:      true,
				Placeholder: "32-character Events API v2 integration key",
				Help:        "In PagerDuty: Service → Integrations → Add integration → Events API V2, then copy the Integration Key.",
			},
			{
				Key:     "region",
				Label:   "Service region",
				Type:    plugin.FieldTypeSelect,
				Default: "us",
				Options: []plugin.Option{
					{Value: "us", Label: "US (events.pagerduty.com)"},
					{Value: "eu", Label: "EU (events.eu.pagerduty.com)"},
				},
				Help: "Match the region your PagerDuty account is hosted in.",
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
	if cfg.RoutingKey == "" {
		return errors.New("routing_key is required")
	}
	if !routingKeyPattern.MatchString(cfg.RoutingKey) {
		return errors.New("routing_key must be a 32-character Events API v2 integration key")
	}
	if _, ok := endpoints[cfg.Region]; !ok {
		return fmt.Errorf("region must be one of us, eu (got %q)", cfg.Region)
	}
	return nil
}

// Send maps the event onto an Events API v2 action.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	routingKey := req.Channel.String("routing_key")
	if routingKey == "" {
		return plugin.Permanent(errors.New("pagerduty channel missing routing_key"))
	}
	endpoint := p.endpoint
	if endpoint == "" {
		region := req.Channel.String("region")
		if region == "" {
			region = "us"
		}
		var ok bool
		if endpoint, ok = endpoints[region]; !ok {
			return plugin.Permanent(fmt.Errorf("pagerduty channel has unknown region %q", region))
		}
	}

	msg := req.View()
	dedupKey := DedupKey(req.Event.Alert.ID)

	var action string
	switch req.Type() {
	case present.EventCreated:
		action = "trigger"
	case present.EventAcknowledged:
		action = "acknowledge"
	case present.EventResolved:
		action = "resolve"
	default:
		return nil // reminders: PagerDuty re-notifies on its own schedule
	}

	if req.Test {
		// A test proves the key works end to end without leaving an open
		// incident: trigger as info, then resolve the same dedup key.
		msg.Tone = present.ToneInfo
		if err := plugin.PostJSON(ctx, p.httpClient, endpoint, buildEvent(routingKey, dedupKey, "trigger", msg), nil, "pagerduty events api"); err != nil {
			return err
		}
		action = "resolve"
	}
	return plugin.PostJSON(ctx, p.httpClient, endpoint, buildEvent(routingKey, dedupKey, action, msg), nil, "pagerduty events api")
}

// DedupKey is the PagerDuty dedup_key for a Probara alert. It is stable for
// the alert's lifetime, which is what lets acknowledge and resolve find the
// incident the trigger opened.
func DedupKey(alertID string) string {
	return "probara:" + alertID
}

type event struct {
	RoutingKey  string   `json:"routing_key"`
	EventAction string   `json:"event_action"`
	DedupKey    string   `json:"dedup_key"`
	Payload     *payload `json:"payload,omitempty"`
	Client      string   `json:"client,omitempty"`
	ClientURL   string   `json:"client_url,omitempty"`
	Links       []link   `json:"links,omitempty"`
}

type payload struct {
	Summary       string            `json:"summary"`
	Source        string            `json:"source"`
	Severity      string            `json:"severity"`
	Timestamp     string            `json:"timestamp,omitempty"`
	Component     string            `json:"component,omitempty"`
	Class         string            `json:"class,omitempty"`
	CustomDetails map[string]string `json:"custom_details,omitempty"`
}

type link struct {
	Href string `json:"href"`
	Text string `json:"text"`
}

// maxSummary is the Events API v2 limit on payload.summary.
const maxSummary = 1024

func buildEvent(routingKey, dedupKey, action string, m present.Message) event {
	ev := event{RoutingKey: routingKey, EventAction: action, DedupKey: dedupKey}
	if action != "trigger" {
		// acknowledge/resolve carry only the key and the dedup_key.
		return ev
	}

	details := map[string]string{"summary": m.Summary}
	for _, f := range m.Facts() {
		details[f.Label] = f.Value
	}
	if m.LastError != "" {
		details["Last error"] = m.LastError
	}
	if m.TenantID != "" {
		details["Workspace"] = m.TenantID
	}

	summary := m.Title
	if m.Summary != "" {
		summary += " — " + m.Summary
	}
	ev.Payload = &payload{
		Summary:       truncate(summary, maxSummary),
		Source:        m.MonitorName,
		Severity:      m.Severity(),
		Timestamp:     m.Timestamp.UTC().Format(time.RFC3339),
		Component:     m.MonitorName,
		Class:         strings.ToLower(strings.ReplaceAll(m.Label, " ", "_")),
		CustomDetails: details,
	}
	ev.Client = "Probara"
	if m.ActionURL != "" {
		ev.ClientURL = m.ActionURL
		ev.Links = []link{{Href: m.ActionURL, Text: m.ActionLabel}}
	}
	return ev
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
		return cfg, errors.New("pagerduty config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid pagerduty config: %w", err)
	}
	cfg.RoutingKey = strings.TrimSpace(cfg.RoutingKey)
	cfg.Region = strings.TrimSpace(cfg.Region)
	if cfg.Region == "" {
		cfg.Region = "us"
	}
	return cfg, nil
}

func init() {
	plugin.Register(New())
}

// Package teams implements the Microsoft Teams incoming-webhook alert plugin.
package teams

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

const pluginType = "teams"

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	WebhookURL string `json:"webhook_url"`
}

// Plugin is the Teams alert plugin implementation. Exported for direct
// instantiation in tests; production code goes through DefaultRegistry.
type Plugin struct {
	httpClient *http.Client
}

// New constructs a plugin with default HTTP client (10s timeout).
func New() *Plugin {
	return &Plugin{httpClient: &http.Client{Timeout: 10 * time.Second}}
}

// Manifest returns the Teams plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Microsoft Teams",
		Description: "Post alerts to a Microsoft Teams channel using an incoming webhook URL.",
		IconKey:     "teams",
		DocsURL:     "https://learn.microsoft.com/microsoftteams/platform/webhooks-and-connectors/how-to/add-incoming-webhook",
		Version:     "1.0.0",
		Capabilities: []plugin.Capability{
			plugin.CapabilityRawEvent,
			plugin.CapabilityTestable,
		},
		Fields: []plugin.Field{
			{
				Key:         "webhook_url",
				Label:       "Webhook URL",
				Type:        plugin.FieldTypeSecret,
				Required:    true,
				Secret:      true,
				Placeholder: "https://outlook.office.com/webhook/...",
				Help:        "Generate this in Teams via Channel → Connectors → Incoming Webhook.",
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
	if cfg.WebhookURL == "" {
		return errors.New("webhook_url is required")
	}
	u, err := url.Parse(cfg.WebhookURL)
	if err != nil {
		return fmt.Errorf("webhook_url is not a valid URL: %w", err)
	}
	if u.Scheme != "https" {
		return errors.New("webhook_url must use https")
	}
	return nil
}

// Send posts a MessageCard payload to the channel's webhook URL.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	webhook, ok := stringFromMap(req.Channel.Config, "webhook_url")
	if !ok || webhook == "" {
		return errors.New("teams channel missing webhook_url")
	}

	card := buildMessageCard(req)
	payload, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshal teams card: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build teams request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("post teams webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("teams webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// messageCard is the Office 365 connector card schema accepted by Teams
// incoming webhooks. https://learn.microsoft.com/outlook/actionable-messages/message-card-reference
type messageCard struct {
	Type     string    `json:"@type"`
	Context  string    `json:"@context"`
	Summary  string    `json:"summary,omitempty"`
	Title    string    `json:"title,omitempty"`
	Text     string    `json:"text,omitempty"`
	Sections []section `json:"sections,omitempty"`
}

type section struct {
	Facts    []fact `json:"facts,omitempty"`
	Text     string `json:"text,omitempty"`
	Markdown bool   `json:"markdown,omitempty"`
}

type fact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func buildMessageCard(req plugin.DispatchRequest) messageCard {
	event := req.Event
	prefix := titlePrefix(eventTypeOf(req))
	if event.Alert.IsLatencyAnomaly() {
		prefix = latencyTitlePrefix(eventTypeOf(req))
	} else if event.Alert.IsHostMetric() {
		prefix = event.Alert.HostMetricLabel(eventTypeOf(req))
	} else if event.Alert.IsTLSExpiry() {
		prefix = event.Alert.TLSExpiryLabel(eventTypeOf(req))
	}
	title := fmt.Sprintf("%s: %s", prefix, event.Alert.MonitorName)
	timestamp := event.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	facts := []fact{
		{Name: "Monitor", Value: event.Alert.MonitorName},
		{Name: "Policy", Value: event.Alert.PolicyName},
		{Name: "Status", Value: event.Alert.Status},
		{Name: "Failure Count", Value: fmt.Sprintf("%d", event.Alert.FailureCount)},
	}
	if event.Alert.LastError != nil && *event.Alert.LastError != "" {
		facts = append(facts, fact{Name: "Last Error", Value: *event.Alert.LastError})
	}
	if summary := event.Alert.MetricSummary(); summary != "" {
		facts = append(facts, fact{Name: event.Alert.MetricLabel(), Value: summary})
	}
	if names := event.Alert.FailingLocationNames(); names != "" {
		facts = append(facts, fact{Name: "Failing Locations", Value: names})
	}
	if event.Alert.RootCauseMonitorName != nil && *event.Alert.RootCauseMonitorName != "" {
		value := *event.Alert.RootCauseMonitorName
		if event.Alert.RootCauseDownSince != nil {
			value = fmt.Sprintf("%s (down since %s)", value, event.Alert.RootCauseDownSince.Format(time.RFC1123))
		}
		facts = append(facts, fact{Name: "Likely Caused By", Value: value})
	}

	return messageCard{
		Type:    "MessageCard",
		Context: "https://schema.org/extensions",
		Summary: title,
		Title:   title,
		Text:    fmt.Sprintf("Event: **%s** at %s", eventTypeOf(req), timestamp.Format(time.RFC1123)),
		Sections: []section{{
			Facts:    facts,
			Markdown: true,
		}},
	}
}

func titlePrefix(eventType string) string {
	switch eventType {
	case "created":
		return "Alert Triggered"
	case "resolved":
		return "Alert Resolved"
	case "reminder":
		return "Alert Still Active"
	default:
		return "Alert"
	}
}

func latencyTitlePrefix(eventType string) string {
	switch eventType {
	case "created":
		return "Latency Degraded"
	case "resolved":
		return "Latency Recovered"
	case "reminder":
		return "Latency Still Degraded"
	default:
		return "Latency Anomaly"
	}
}

func eventTypeOf(req plugin.DispatchRequest) string {
	if req.EventType != "" {
		return req.EventType
	}
	return req.Event.Type
}

func parseConfig(raw json.RawMessage) (Config, error) {
	var cfg Config
	if len(raw) == 0 {
		return cfg, errors.New("teams config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid teams config: %w", err)
	}
	cfg.WebhookURL = strings.TrimSpace(cfg.WebhookURL)
	return cfg, nil
}

func stringFromMap(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func init() {
	plugin.Register(New())
}

// _ ensures the notifications package is referenced even if no other symbol
// from it appears in this file (defensive — the type may currently be reached
// only via plugin.DispatchRequest.Event indirection).
var _ = notifications.AlertEvent{}

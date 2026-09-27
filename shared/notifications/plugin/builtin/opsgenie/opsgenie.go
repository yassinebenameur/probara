// Package opsgenie implements the Opsgenie / Jira Service Management alert
// plugin over the Alert API (https://docs.opsgenie.com/docs/alert-api).
//
// Atlassian is folding Opsgenie into Jira Service Management, whose ops
// integrations expose the same Alert API under api.atlassian.com, so one
// plugin serves both: the "site" field picks the endpoint and everything else
// is identical. Each Probara alert maps to one provider alert through a
// deterministic alias: created → create, acknowledged → acknowledge,
// resolved → close. Reminders are skipped — the provider re-notifies through
// its own escalation policies, and a repeated create would only be
// deduplicated.
package opsgenie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

const pluginType = "opsgenie"

// Alert API base URLs per site.
var bases = map[string]string{
	"us":  "https://api.opsgenie.com/v2/alerts",
	"eu":  "https://api.eu.opsgenie.com/v2/alerts",
	"jsm": "https://api.atlassian.com/jsm/ops/integration/v2/alerts",
}

var priorities = map[string]bool{"P1": true, "P2": true, "P3": true, "P4": true, "P5": true}

// Config is the channel config persisted in alert_channels.config.
type Config struct {
	APIKey   string `json:"api_key"`
	Site     string `json:"site,omitempty"`
	Priority string `json:"priority,omitempty"`
}

// Plugin is the Opsgenie / JSM alert plugin implementation.
type Plugin struct {
	httpClient *http.Client
	// base overrides the site lookup in tests.
	base string
}

// New constructs a plugin with the guarded notification HTTP client.
func New() *Plugin {
	return &Plugin{httpClient: plugin.NewHTTPClient(10 * time.Second)}
}

// Manifest returns the plugin self-description.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type:        pluginType,
		DisplayName: "Opsgenie / Jira Service Management",
		Description: "Create, acknowledge and close Opsgenie or Jira Service Management alerts through an API integration.",
		IconKey:     "opsgenie",
		DocsURL:     "https://support.atlassian.com/opsgenie/docs/create-a-default-api-integration/",
		Version:     "1.0.0",
		Capabilities: []plugin.Capability{
			plugin.CapabilityTestable,
			plugin.CapabilityAcknowledge,
		},
		Fields: []plugin.Field{
			{
				Key:         "api_key",
				Label:       "Integration API key",
				Type:        plugin.FieldTypeSecret,
				Required:    true,
				Secret:      true,
				Placeholder: "API integration key",
				Help:        "Opsgenie: Settings → Integrations → API. Jira Service Management: Operations → Integrations → API. Copy the integration's API key.",
			},
			{
				Key:     "site",
				Label:   "Site",
				Type:    plugin.FieldTypeSelect,
				Default: "us",
				Options: []plugin.Option{
					{Value: "us", Label: "Opsgenie (api.opsgenie.com)"},
					{Value: "eu", Label: "Opsgenie EU (api.eu.opsgenie.com)"},
					{Value: "jsm", Label: "Jira Service Management (api.atlassian.com)"},
				},
			},
			{
				Key:     "priority",
				Label:   "Outage priority",
				Type:    plugin.FieldTypeSelect,
				Default: "P1",
				Options: []plugin.Option{
					{Value: "P1", Label: "P1 — Critical"},
					{Value: "P2", Label: "P2 — High"},
					{Value: "P3", Label: "P3 — Moderate"},
					{Value: "P4", Label: "P4 — Low"},
					{Value: "P5", Label: "P5 — Informational"},
				},
				Help: "Priority for hard outages. Degradations (latency, host metrics, certificate expiry) are sent one level lower, never above P3.",
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
	if cfg.APIKey == "" {
		return errors.New("api_key is required")
	}
	if strings.ContainsAny(cfg.APIKey, " \t\r\n") {
		return errors.New("api_key must not contain whitespace")
	}
	if _, ok := bases[cfg.Site]; !ok {
		return fmt.Errorf("site must be one of us, eu, jsm (got %q)", cfg.Site)
	}
	if !priorities[cfg.Priority] {
		return fmt.Errorf("priority must be one of P1..P5 (got %q)", cfg.Priority)
	}
	return nil
}

// Send maps the event onto an Alert API call.
func (p *Plugin) Send(ctx context.Context, req plugin.DispatchRequest) error {
	raw, err := json.Marshal(req.Channel.Config)
	if err != nil {
		return plugin.Permanent(err)
	}
	cfg, err := parseConfig(raw)
	if err != nil {
		return plugin.Permanent(err)
	}
	if cfg.APIKey == "" {
		return plugin.Permanent(errors.New("opsgenie channel missing api_key"))
	}
	base := p.base
	if base == "" {
		var ok bool
		if base, ok = bases[cfg.Site]; !ok {
			return plugin.Permanent(fmt.Errorf("opsgenie channel has unknown site %q", cfg.Site))
		}
	}
	headers := map[string]string{"Authorization": "GenieKey " + cfg.APIKey}
	alias := Alias(req.Event.Alert.ID)
	msg := req.View()

	switch req.Type() {
	case present.EventCreated:
		if req.Test {
			msg.Tone = present.ToneInfo
		}
		if err := plugin.PostJSON(ctx, p.httpClient, base, buildCreate(alias, cfg.Priority, msg), headers, "opsgenie alert api"); err != nil {
			return err
		}
		if !req.Test {
			return nil
		}
		// A test proves the key works without leaving an open alert behind.
		return plugin.PostJSON(ctx, p.httpClient, actionURL(base, alias, "close"), note("Probara test notification — closed automatically."), headers, "opsgenie alert api")
	case present.EventAcknowledged:
		return plugin.PostJSON(ctx, p.httpClient, actionURL(base, alias, "acknowledge"), note("Acknowledged in Probara."), headers, "opsgenie alert api")
	case present.EventResolved:
		return plugin.PostJSON(ctx, p.httpClient, actionURL(base, alias, "close"), note(msg.Summary), headers, "opsgenie alert api")
	default:
		return nil // reminders: the provider re-notifies on its own schedule
	}
}

// Alias is the provider alias for a Probara alert: stable for the alert's
// lifetime, which is what lets acknowledge and close find the alert create
// opened.
func Alias(alertID string) string {
	return "probara-" + alertID
}

func actionURL(base, alias, action string) string {
	return fmt.Sprintf("%s/%s/%s?identifierType=alias", base, url.PathEscape(alias), action)
}

type createRequest struct {
	Message     string            `json:"message"`
	Alias       string            `json:"alias"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Details     map[string]string `json:"details,omitempty"`
	Entity      string            `json:"entity,omitempty"`
	Source      string            `json:"source"`
	Priority    string            `json:"priority"`
}

type actionRequest struct {
	Source string `json:"source"`
	Note   string `json:"note,omitempty"`
}

func note(text string) actionRequest {
	return actionRequest{Source: "Probara", Note: truncate(text, 25000)}
}

// Alert API field limits.
const (
	maxMessage     = 130
	maxDescription = 15000
)

func buildCreate(alias, outagePriority string, m present.Message) createRequest {
	details := map[string]string{}
	for _, f := range m.Facts() {
		details[f.Label] = f.Value
	}
	if m.TenantID != "" {
		details["Workspace"] = m.TenantID
	}
	if m.ActionURL != "" {
		details[m.ActionLabel] = m.ActionURL
	}

	var desc strings.Builder
	desc.WriteString(m.Summary)
	if m.LastError != "" {
		desc.WriteString("\n\nLast error:\n")
		desc.WriteString(m.LastError)
	}
	if m.ActionURL != "" {
		desc.WriteString("\n\n")
		desc.WriteString(m.ActionURL)
	}

	return createRequest{
		Message:     truncate(m.Title, maxMessage),
		Alias:       alias,
		Description: truncate(desc.String(), maxDescription),
		Tags:        []string{"probara", strings.ToLower(m.StatusWord)},
		Details:     details,
		Entity:      m.MonitorName,
		Source:      "Probara",
		Priority:    priorityFor(m.Tone, outagePriority),
	}
}

// priorityFor uses the configured priority for hard outages and a lower one
// for degradations, never above P3; tests and acknowledgements are P5.
func priorityFor(t present.Tone, outage string) string {
	switch t {
	case present.ToneDown:
		return outage
	case present.ToneWarn:
		n := int(outage[1]-'0') + 1
		if n < 3 {
			n = 3
		}
		if n > 5 {
			n = 5
		}
		return fmt.Sprintf("P%d", n)
	default:
		return "P5"
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
		return cfg, errors.New("opsgenie config is required")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid opsgenie config: %w", err)
	}
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.Site = strings.TrimSpace(cfg.Site)
	if cfg.Site == "" {
		cfg.Site = "us"
	}
	cfg.Priority = strings.ToUpper(strings.TrimSpace(cfg.Priority))
	if cfg.Priority == "" {
		cfg.Priority = "P1"
	}
	return cfg, nil
}

func init() {
	plugin.Register(New())
}

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/validation"
)

// instructions is the server-level guide an MCP client hands its model.
func instructions(c capability) string {
	var b strings.Builder
	b.WriteString(`Probara is an uptime and voice observability platform. Monitors run checks (HTTP, DNS, TCP, ` +
		`databases, SIP, synthetic API/browser, Prometheus queries, ...) from a worker fleet and from private ` +
		`locations; host agents push OpenTelemetry metrics; push monitors receive heartbeats.

Concepts:
- Monitor state: up, down, degraded (some locations failing, quorum not reached), paused, maintenance, unknown.
- Alerts open when a monitor goes down (or a host metric rule, mesh edge or latency anomaly fires). "active" = ` +
		`firing and unacknowledged; "acknowledged" = someone owns it; "resolved" = over. Notifications go to the ` +
		`workspace default channels or a monitor's custom channels; alerts caused by a down upstream dependency can ` +
		`be suppressed.
- Incidents group alerts and monitors into an operator-managed narrative and can be published on public status pages.
- Groups aggregate member monitors; dependencies (monitor depends on upstream) drive root-cause suppression.
- SLAs measure availability targets over periods; issued reports are frozen records.

Working well:
- Start with get_dashboard_overview for fleet health, list_alerts status=active for what is firing.
- Before creating or editing a monitor, call describe_monitor_type for its config schema, and try the config ` +
		`with test_monitor_config.
- IDs are UUIDs; resolve names with list_* tools first.
- Errors come back as tool errors carrying the API's message; fix the arguments and retry.
- Secrets read back as "***". Sending "***" keeps a stored secret.
`)
	if c.apiKey {
		b.WriteString("\nThis connection uses a workspace API key: it acts in that one workspace, and tenant-admin " +
			"or superadmin actions are unavailable.\n")
	} else {
		b.WriteString("\nThis connection acts as a signed-in user. Tenant-scoped tools take an optional tenant_id " +
			"(list_tenants shows your workspaces); without it they act in the workspace chosen at authorization.\n")
	}
	if !c.write {
		b.WriteString("This connection is read-only: tools that change anything are not offered.\n")
	} else {
		b.WriteString("Tools marked destructive delete data or change who gets paged; confirm with the user before calling them.\n")
	}
	return b.String()
}

func registerResources(s *mcp.Server) {
	s.AddResource(&mcp.Resource{
		URI:         "probara://monitor-types",
		Name:        "monitor-types",
		Title:       "Monitor types",
		Description: "Every supported monitor type with its secret fields and whether it is an active check.",
		MIMEType:    "application/json",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		var out []monitorTypeInfo
		for _, t := range monitorTypes() {
			info, err := typeInfo(t, false)
			if err != nil {
				return nil, err
			}
			out = append(out, info)
		}
		return jsonResource(req.Params.URI, out)
	})
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "probara://monitor-types/{type}",
		Name:        "monitor-type",
		Title:       "Monitor type config schema",
		Description: "A monitor type's config JSON schema and secret fields.",
		MIMEType:    "application/json",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		t := strings.TrimPrefix(req.Params.URI, "probara://monitor-types/")
		if !validation.DefaultRegistry.Has(models.MonitorType(t)) {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		info, err := typeInfo(t, true)
		if err != nil {
			return nil, err
		}
		return jsonResource(req.Params.URI, info)
	})
}

func jsonResource(uri string, v any) (*mcp.ReadResourceResult, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(raw)}}}, nil
}

type promptDef struct {
	prompt *mcp.Prompt
	write  bool
	text   func(args map[string]string) string
}

var prompts = []promptDef{
	{
		prompt: &mcp.Prompt{
			Name:        "triage_alert",
			Title:       "Triage an alert",
			Description: "Investigate one alert down to a likely cause and a next step.",
			Arguments:   []*mcp.PromptArgument{{Name: "alert_id", Description: "Alert ID", Required: true}},
		},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`Triage Probara alert %s.
1. get_alert: what fired, since when, routing, suppression, and the AI triage if present.
2. get_monitor and get_monitor_results (limit 20) for the failing checks and their errors; compare locations.
3. list_monitor_dependencies and list_monitor_dependents: is an upstream down (root cause elsewhere) or are downstreams affected?
4. get_monitor_analytics range=24h for when the trouble started and how it looks over time.
5. list_alerts status=active to see whether this is part of a wider outage.
Conclude with: the most likely cause with the evidence for it, the blast radius, and the next step. `+
				`Do not acknowledge, resolve or change anything unless asked.`, a["alert_id"])
		},
	},
	{
		prompt: &mcp.Prompt{
			Name:        "incident_report",
			Title:       "Write an incident report",
			Description: "Draft a post-incident report from an incident's timeline and evidence.",
			Arguments:   []*mcp.PromptArgument{{Name: "incident_id", Description: "Incident ID", Required: true}},
		},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`Write a post-incident report for Probara incident %s.
Use get_incident (timeline, linked alerts and monitors), get_incident_ai_analysis, and for each linked monitor `+
				`get_monitor_analytics over the incident window. Report: summary, impact (which services, how long, `+
				`which locations), timeline, root cause, what went well, what to improve. Cite the data you used.`, a["incident_id"])
		},
	},
	{
		prompt: &mcp.Prompt{
			Name:        "fleet_health_review",
			Title:       "Review fleet health",
			Description: "A health review of the whole workspace with concrete improvements.",
		},
		text: func(map[string]string) string {
			return `Review the health of this Probara workspace.
Use get_dashboard_overview (range 7d), list_problem_monitors, list_alerts status=active, get_notification_settings, ` +
				`list_slas and get_mesh. Report: what is broken now; flapping or slow monitors; monitors whose alerts reach ` +
				`nobody (routing); SLAs at risk; location or mesh problems. End with a prioritized list of improvements. ` +
				`Do not change anything.`
		},
	},
	{
		write: true,
		prompt: &mcp.Prompt{
			Name:        "onboard_service",
			Title:       "Onboard a service",
			Description: "Set up monitoring for a new service end to end.",
			Arguments: []*mcp.PromptArgument{
				{Name: "target", Description: "URL, host:port or other endpoint of the service", Required: true},
				{Name: "name", Description: "Service name"},
			},
		},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`Set up Probara monitoring for %q (service name: %q).
1. Pick the right monitor type(s) with list_monitor_types and describe_monitor_type.
2. Validate each config with test_monitor_config before creating anything.
3. Propose the monitors (type, interval, timeout, tags, locations, alert routing) and ask for confirmation.
4. On confirmation, create them with create_monitor; group them with a group monitor if there is more than one.
5. Check get_notification_settings so alerts reach someone, and offer to add the service to a status page and an SLA.`,
				a["target"], a["name"])
		},
	},
}

func registerPrompts(s *mcp.Server, c capability) {
	for _, p := range prompts {
		if p.write && !c.write {
			continue
		}
		p := p
		s.AddPrompt(p.prompt, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			args := req.Params.Arguments
			for _, a := range p.prompt.Arguments {
				if a.Required && args[a.Name] == "" {
					return nil, fmt.Errorf("missing required argument %q", a.Name)
				}
			}
			return &mcp.GetPromptResult{
				Description: p.prompt.Description,
				Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: p.text(args)}}},
			}, nil
		})
	}
}

import type { DocPage } from "../types";

export const MCP_PAGE: DocPage = {
  slug: "mcp",
  group: "Build & operate",
  title: "MCP server",
  description:
    "Connect Claude, Cursor and any Model Context Protocol client to Probara: every API capability as a tool, with your role, tenant scoping and audit trail.",
  eyebrow: "AI agents",
  readingTime: "9 min read",
  keywords: [
    "MCP",
    "Model Context Protocol",
    "Claude",
    "Cursor",
    "AI agent",
    "OAuth",
    "tools",
  ],
  sections: [
    {
      id: "overview",
      title: "What it is",
      blocks: [
        {
          type: "paragraph",
          text:
            "The API serves an MCP endpoint at `{API_ORIGIN}/api/v1/mcp` (Streamable HTTP, stateless, JSON responses). It exposes every operator capability of the [REST API](/docs/api/) as an MCP tool: monitors and their analytics, alerts, incidents, maintenance, locations and mesh, SLAs, status pages and templates, notification routing and channels, local AI, API keys, audit log, users and SSO mappings.",
        },
        {
          type: "paragraph",
          text:
            "It is a protocol adapter, not a second implementation. Each tool stands for one REST route, and a tool call is dispatched in process through the same router as a direct API call, carrying the caller's credential. Authentication, tenant isolation, the write gate, the tenant-admin and superadmin gates, validation, secret masking (`***`) and the audit trail therefore apply exactly as they do for the API. Tool argument schemas are reflected from the request types the handlers decode, so they follow the API automatically.",
        },
        {
          type: "callout",
          tone: "info",
          title: "No model in the loop on Probara's side",
          text:
            "Probara never calls a hosted model. Your agent calls Probara; what it sends to its own model is its own choice. Probara's own AI features remain the [local model](/docs/alerting/#ai-analysis).",
        },
      ],
    },
    {
      id: "connect",
      title: "Connect a client",
      blocks: [
        {
          type: "paragraph",
          text:
            "Settings → MCP in the operator UI shows the endpoint URL and these snippets. Clients that support MCP OAuth open a browser to Probara's consent screen the first time; you sign in (password or SSO), pick a workspace and an access level, and the client receives its own tokens.",
        },
        {
          type: "code",
          language: "bash",
          title: "Claude Code",
          code:
            "claude mcp add --transport http probara https://monitoring.example.com/api/v1/mcp\n# then run /mcp in Claude Code and choose Authenticate",
        },
        {
          type: "code",
          language: "json",
          title: "Cursor (.cursor/mcp.json) and other clients",
          code: '{\n  "mcpServers": {\n    "probara": { "url": "https://monitoring.example.com/api/v1/mcp" }\n  }\n}',
        },
        {
          type: "paragraph",
          text:
            "Claude.ai and Claude Desktop: Settings → Connectors → Add custom connector, paste the endpoint URL, then Connect.",
        },
        {
          type: "code",
          language: "bash",
          title: "Headless agents and scripts: a workspace API key",
          code:
            "claude mcp add --transport http probara https://monitoring.example.com/api/v1/mcp \\\n  --header \"Authorization: Bearer pk_<secret>\"",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Local development",
          text:
            "Point clients at the operator UI origin (`http://localhost:3000/api/v1/mcp`): the UI proxies `/api` and the root `/.well-known/` OAuth discovery paths to the API, and the consent screen lives there. Talking to the API port directly (`:8080`) works with API keys; for OAuth set `APP_BASE_URL` to the UI origin so the consent redirect reaches it.",
        },
      ],
    },
    {
      id: "authentication",
      title: "Authentication and access",
      blocks: [
        {
          type: "definitions",
          items: [
            {
              term: "Sign-in (OAuth 2.1)",
              description:
                "The connection acts as you. Roles are resolved from the database on every call, exactly as for a browser session, so a role change or a disabled account takes effect immediately. Admin and superadmin tools work if you have those roles.",
            },
            {
              term: "Workspace API key",
              description:
                "`Authorization: Bearer pk_<secret>`. The connection acts in the key's one workspace with its read or write scope. As everywhere in the API, keys never pass tenant-admin or superadmin gates, so those tools are not offered.",
            },
            {
              term: "Browser cookies",
              description:
                "Never accepted. The MCP endpoint strips the session cookie before authenticating, so a web page cannot drive MCP with your browser session.",
            },
          ],
        },
        {
          type: "table",
          columns: ["Consent choice", "Effect"],
          rows: [
            ["Workspace", "The default tenant of the connection. Tenant-scoped tools take an optional `tenant_id` to act in another workspace you belong to; `list_tenants` lists them."],
            ["Read-only", "Only read tools are offered, and the API refuses any write. Narrows every role, superadmin included."],
            ["Read & write", "Tools are limited by your role: a viewer stays read-only whatever is chosen."],
          ],
        },
        {
          type: "table",
          columns: ["Protocol detail", "Value"],
          rows: [
            ["Discovery", "A 401 from the endpoint carries `WWW-Authenticate: Bearer resource_metadata=…`; protected-resource metadata (RFC 9728) at `/.well-known/oauth-protected-resource/api/v1/mcp`; authorization-server metadata (RFC 8414) for issuer `{API_ORIGIN}/api/v1/oauth` at the path-insertion, path-appending and OpenID well-known locations"],
            ["Client registration", "Dynamic (RFC 7591) at `POST /api/v1/oauth/register`; public clients only; redirect URIs must be https, http on a loopback host, or a private-use scheme such as `cursor://`. Registrations never used for a sign-in are deleted after a day"],
            ["Flow", "Authorization code with PKCE `S256` (mandatory); codes are single-use and live 10 minutes; replaying a code revokes the connection it created"],
            ["Tokens", "Opaque, stored as SHA-256 hashes. Access tokens (`pbm_at_…`) live 1 hour; refresh tokens (`pbm_rt_…`) 30 days and rotate on every use; presenting a rotated refresh token revokes the connection"],
            ["Audience", "Access tokens are valid on the MCP endpoint only; a direct REST call with one gets 401"],
            ["Revocation", "Settings → MCP lists and disconnects your connections; RFC 7009 revocation at `POST /api/v1/oauth/revoke`"],
          ],
        },
        {
          type: "paragraph",
          text:
            "Set `PUBLIC_BASE_URL` (API origin) and `APP_BASE_URL` (operator UI origin) in production. Without them the endpoints derive URLs from the request's `Host` and forwarded headers, which works behind the bundled ingress and UI proxy but leaves the advertised issuer to whatever the proxy forwards.",
        },
      ],
    },
    {
      id: "tools",
      title: "Tools",
      blocks: [
        {
          type: "paragraph",
          text:
            "`tools/list` is tailored to the connection: read-only connections see no write tools, API keys see no tenant-admin or superadmin tools, and only superadmins see user and SSO-mapping tools. This is a convenience; the API enforces access on every call regardless. Tools carry MCP annotations (`readOnlyHint`, `destructiveHint`, `idempotentHint`) derived from the route, so clients can ask before running deletes.",
        },
        {
          type: "table",
          columns: ["Family", "Tools (examples)"],
          rows: [
            ["Fleet", "`whoami`, `get_dashboard_overview`, `list_problem_monitors`, `list_recent_failures`, `get_group_sparkline`"],
            ["Monitors", "`list_monitors`, `get_monitor`, `create_monitor`, `update_monitor`, `delete_monitor`, `test_monitor_config`, `run_monitor_now`, `snooze_monitor`, `bulk_update_alerting`, `bulk_delete_monitors`"],
            ["History", "`get_monitor_analytics`, `get_monitor_results`, `get_monitor_metrics_snapshots`, `get_synthetic_screenshot` (returns the image), `delete_monitor_history`"],
            ["Structure", "group members, `get_dependency_graph`, dependency add/remove, `suggest_dependencies` (local AI)"],
            ["Host agents", "`list_metric_series`, `query_metrics`, collector config and install/uninstall scripts, `get_push_info`"],
            ["Import / export", "`export_monitors`, `preview_monitor_import` (send the file's text), `import_monitors`"],
            ["Alerts", "`list_alerts`, `get_alert` (with AI triage), `acknowledge_alert`, `resolve_alert`, `label_alert_triage`, `wait_for_alert_events`"],
            ["Incidents", "create, update, state transitions, timeline entries, alert/monitor links, status-page publishing, AI analysis request/read/label"],
            ["Operations", "maintenance windows, private locations and their deploy info, `get_mesh`, `get_mesh_edge_history`, `probe_mesh_edge`"],
            ["Reporting", "SLAs, live reports (`format` json/csv/pdf returns a file), issued reports"],
            ["Status pages", "pages, sections and settings, custom template draft/publish/revert, template library"],
            ["Notifications", "alert channels, channel plugins (config schemas), notification settings, `test_alert_channel` (sends a real message)"],
            ["Workspace", "local AI settings, `export_ai_labels`, API keys, workspace settings, audit log"],
            ["Platform", "`list_tenants`, users, SSO group mappings (superadmins)"],
          ],
        },
        {
          type: "definitions",
          items: [
            {
              term: "describe_monitor_type / list_monitor_types",
              description:
                "Return a monitor type's `config` JSON schema (reflected from the struct its validator decodes), its secret fields, and whether it is an active check. Agents call it before creating or editing a monitor; `test_monitor_config` then validates a config without saving.",
            },
            {
              term: "wait_for_alert_events",
              description:
                "Listens to the workspace's live alert stream for up to 60 seconds and returns the alert events that arrive, as soon as one does. It stands in for `GET /alerts/stream`, which as server-sent events cannot be a tool.",
            },
          ],
        },
        {
          type: "paragraph",
          text:
            "Results are the API's JSON as text. API errors come back as tool errors carrying the HTTP status and the API's message, so the agent can correct its arguments. Images come back as image content and file downloads (PDF and CSV reports, the AI label export) as embedded resources. Text over 256 KiB is cut with a note to narrow the call.",
        },
        {
          type: "paragraph",
          text:
            "Not exposed, with the reason: browser login, refresh, logout and SSO routes (MCP has its own sign-in); first-run bootstrap; telemetry ingest (`/otlp/v1/metrics`, the legacy agent endpoint, `/push/{token}`); the retired alert-policy routes (410); and the OAuth protocol routes themselves.",
        },
      ],
    },
    {
      id: "resources-prompts",
      title: "Resources and prompts",
      blocks: [
        {
          type: "table",
          columns: ["Kind", "Name", "Purpose"],
          rows: [
            ["Resource", "`probara://monitor-types`", "Every monitor type with secret fields and active-check flag"],
            ["Resource template", "`probara://monitor-types/{type}`", "One type's config schema"],
            ["Prompt", "`triage_alert`", "Investigate an alert to a likely cause, blast radius and next step, without changing anything"],
            ["Prompt", "`incident_report`", "Draft a post-incident report from the incident, its timeline and analytics"],
            ["Prompt", "`fleet_health_review`", "Review the workspace: what is broken, flapping, unrouted, SLAs at risk"],
            ["Prompt", "`onboard_service`", "Choose monitor types, test configs, propose and (on confirmation) create monitoring for a new service; write connections only"],
          ],
        },
      ],
    },
    {
      id: "audit-security",
      title: "Audit and security",
      blocks: [
        {
          type: "list",
          items: [
            "Every write a tool performs is audited under the real route and action (for example `monitor.create`), attributed to the signed-in user or the API key, with `details.via = \"mcp\"`. Authorizing and disconnecting connections are audited as `mcp_connection.authorize` and `mcp_connection.revoke`.",
            "Agent install scripts embed the caller's credential only when it is a workspace API key. Over a sign-in connection they carry no key, so a short-lived personal token is never written onto a host.",
            "The consent decision accepts only a signed-in browser session from the API's or the UI's own origin; API keys and MCP tokens cannot mint or manage connections.",
            "Destructive tools are marked `destructiveHint`; the server's instructions ask the agent to confirm them with you first. Use a read-only connection or a read API key for exploratory agents.",
          ],
        },
      ],
    },
    {
      id: "limitations",
      title: "Limitations",
      blocks: [
        {
          type: "list",
          items: [
            "The endpoint is stateless (no MCP sessions), so the server never pushes notifications or list-changed events; `wait_for_alert_events` is the way to watch alerts.",
            "Reflected schemas do not mark which body fields are required (only the ones the catalog states); the API's validation error names what is missing.",
            "Each OAuth connection belongs to one user and one default workspace; there is no organization-wide connection policy and no allow-list of client applications.",
            "OAuth client registration is open (as the MCP specification expects); unused registrations are pruned after a day.",
          ],
        },
      ],
    },
  ],
};

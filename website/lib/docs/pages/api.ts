import type { DocPage } from "../types";

export const API_PAGE: DocPage = {
  slug: "api",
  group: "Build & operate",
  title: "API reference",
  description:
    "Authenticate, select a tenant, and integrate with Probara's monitoring, alerting, incident, and administration APIs.",
  eyebrow: "Developer reference",
  readingTime: "38 min read",
  keywords: [
    "REST API",
    "API key",
    "endpoints",
    "SSE",
    "pagination",
  ],
  sections: [
    {
      id: "base-url",
      title: "Base URL and conventions",
      blocks: [
        {
          type: "paragraph",
          text:
            "The control-plane API is rooted at `{API_ORIGIN}/api/v1`. The API serves `/healthz`, `/readyz`, and `/metrics` on `HTTP_PORT` (8080 in the bundled deployments); its declared `METRICS_PORT` does not have a separate listener. Public rendered status pages are served by the separate [status-page service](/docs/status-pages/#public-routes).",
        },
        {
          type: "code",
          language: "bash",
          title: "Set an origin for examples",
          code:
            "export PROBARA_API_ORIGIN='https://monitoring.example.com'\n\ncurl \"$PROBARA_API_ORIGIN/healthz\"\ncurl \"$PROBARA_API_ORIGIN/readyz\"",
        },
        {
          type: "table",
          columns: ["Convention", "Behavior"],
          rows: [
            [
              "Content type",
              "Send `application/json` for JSON request bodies; import endpoints also accept supported file content",
            ],
            [
              "Identifiers",
              "Resources use UUID-shaped IDs unless the endpoint uses a public slug or capability token",
            ],
            [
              "Times",
              "Use RFC 3339 timestamps for filters and scheduled fields",
            ],
            [
              "Tenant scope",
              "Resolved from an admin membership selection or permanently from the API key",
            ],
            [
              "Deletion",
              "Many operational resources are soft-deleted first and cleaned asynchronously",
            ],
            [
              "Errors",
              "Most application handlers return JSON error information, but a few low-level validation paths can return plain HTTP error text",
            ],
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Do not parse one universal error body",
          text:
            "Always branch on HTTP status first, then parse JSON when the response content type is JSON. Preserve the body for diagnostics. Some domain errors include a stable code such as `dependency_cycle`; not every handler uses the same envelope.",
        },
      ],
    },
    {
      id: "authentication",
      title: "Authentication",
      blocks: [
        {
          type: "definitions",
          items: [
            {
              term: "Administrator session",
              description:
                "Login establishes an HTTP-only access cookie and a rotating refresh session. It is the credential used by the web application and can switch among allowed tenants.",
            },
            {
              term: "API key",
              description:
                "Send `Authorization: Bearer pk_<secret>`. The key has [read or write scope](/docs/administration/#api-keys) and is bound to its creation tenant.",
            },
            {
              term: "Push token",
              description:
                "Used only in `/api/v1/push/{token}`. No cookie or bearer key is required because possession of the URL token authorizes a heartbeat.",
            },
          ],
        },
        {
          type: "code",
          language: "bash",
          title: "API-key request",
          code:
            "curl \\\n  -H 'Authorization: Bearer pk_<secret>' \\\n  -H 'Accept: application/json' \\\n  \"$PROBARA_API_ORIGIN/api/v1/monitors?page=1&page_size=20\"",
        },
        {
          type: "table",
          columns: ["Public authentication route", "Purpose"],
          rows: [
            ["`POST /api/v1/auth/login`", "Local username/password login"],
            ["`POST /api/v1/auth/refresh`", "Rotate and refresh an admin session"],
            ["`POST /api/v1/auth/logout`", "Revoke the current refresh session and clear cookies"],
            ["`GET /api/v1/auth/me`", "Read the current session user when present"],
            ["`GET /api/v1/auth/oidc/status`", "Report OIDC availability"],
            ["`GET /api/v1/auth/oidc/start`", "Begin authorization-code + PKCE login"],
            ["`GET /api/v1/auth/oidc/callback`", "Complete the provider callback"],
            [
              "`GET /api/v1/users/bootstrap/status`",
              "Check whether first-user bootstrap remains available",
            ],
            [
              "`POST /api/v1/users/bootstrap/first`",
              "Create the one-time first superadministrator",
            ],
          ],
        },
      ],
    },
    {
      id: "tenant-and-permissions",
      title: "Tenant selection and permissions",
      blocks: [
        {
          type: "paragraph",
          text:
            "For an administrator cookie, send `X-Tenant-ID: <tenant-id>` to select an allowed membership. A supported `tenant_id` query fallback can select context where middleware permits it. API keys ignore caller-selected tenant values and remain pinned to one tenant.",
        },
        {
          type: "code",
          language: "bash",
          title: "Administrator-session tenant header",
          code:
            "curl \\\n  --cookie cookie-jar.txt \\\n  -H 'X-Tenant-ID: <tenant-id>' \\\n  \"$PROBARA_API_ORIGIN/api/v1/auth-context\"",
        },
        {
          type: "table",
          columns: ["Principal", "Reads", "Mutations"],
          rows: [
            [
              "[Tenant viewer](/docs/administration/#roles) / read API key",
              "Tenant data",
              "Only approved compute-only POST diagnostics",
            ],
            [
              "Tenant editor / write API key",
              "Tenant data",
              "Normal operational writes; not tenant-admin-only governance",
            ],
            [
              "Tenant admin",
              "Tenant data and audit",
              "All tenant writes, API-key management, and tenant settings",
            ],
            [
              "Platform superadmin",
              "Any selected tenant plus platform users",
              "Platform user administration and tenant operations",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Compute-only POST exceptions include `/monitors/test`, `/monitors/import/preview`, `/monitors/dependency-suggestions`, `/mesh/probe`, and `/ai-settings/test`. They do not persist monitor/result/graph state. Channel test sends a real outbound notification and therefore is not read-only.",
        },
      ],
    },
    {
      id: "pagination",
      title: "Pagination and common filters",
      blocks: [
        {
          type: "paragraph",
          text:
            "Collection APIs generally accept 1-based `page` and `page_size`. The common maximum is 100; locations allow up to 200, and dashboard recent-item limits are capped at 50. Use returned total/page metadata where provided rather than inferring completion from a short page.",
        },
        {
          type: "table",
          columns: ["Resource", "Important filters"],
          rows: [
            [
              "Monitors",
              "`tag` (repeatable where supported), `enabled`, `page`, `page_size` (default 20, max 100)",
            ],
            [
              "Monitor results",
              "`limit`, optional RFC 3339 `since`",
            ],
            [
              "Monitor analytics",
              "`range`: `1h`, `6h`, `24h`, `7d`, `30d`, `90d`, `365d`",
            ],
            [
              "Alerts",
              "`status`, `monitor_id`, `since`, `page`, `page_size` (max 100)",
            ],
            ["Maintenance", "`status`, `monitor_id`, `page`, `page_size`"],
            ["Mesh history", "`source`, `target`, and `hours`"],
            [
              "Dashboard",
              "`range`, repeatable `tag`, and bounded recent-item limits",
            ],
            [
              "Audit log",
              "`action`, `outcome`, `actor_id`, RFC 3339 `from` / `to`, pagination",
            ],
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Validate timestamps client-side",
          text:
            "Some optional filters are ignored when they cannot be parsed rather than failing the whole request. Emit canonical RFC 3339 timestamps and log the exact query used for reproducible automation.",
        },
      ],
    },
    {
      id: "monitor-endpoints",
      title: "Monitor endpoints",
      blocks: [
        {
          type: "table",
          columns: ["Method and path", "Purpose"],
          rows: [
            ["`POST /api/v1/monitors`", "Create a monitor"],
            ["`GET /api/v1/monitors`", "List tenant monitors"],
            [
              "`POST /api/v1/monitors/test`",
              "Execute a supplied config without persistence or alerting",
            ],
            ["`GET /api/v1/monitors/{id}`", "Get full monitor detail"],
            ["`PATCH /api/v1/monitors/{id}`", "Update monitor fields/config"],
            ["`DELETE /api/v1/monitors/{id}`", "Soft-delete a monitor"],
            [
              "`POST /api/v1/monitors/{id}/run`",
              "Queue a persisted on-demand check",
            ],
            [
              "`GET /api/v1/monitors/{id}/results`",
              "List raw check results",
            ],
            [
              "`GET /api/v1/monitors/{id}/analytics`",
              "Get summary, series, latency, downtime, and coverage",
            ],
            [
              "`DELETE /api/v1/monitors/{id}/history`",
              "Delete the monitor's stored result history",
            ],
            [
              "`GET /api/v1/monitors/{id}/artifacts/screenshot?path=...`",
              "Retrieve an authorized [synthetic-browser](/docs/monitors/#synthetic-browser) screenshot path",
            ],
            [
              "`POST /api/v1/monitors/{id}/snooze`",
              "Create a one-monitor snooze by duration or end time",
            ],
          ],
        },
        {
          type: "code",
          language: "json",
          title: "Create an HTTP monitor",
          code:
            '{\n  "name": "Public API",\n  "type": "http",\n  "config": {\n    "url": "https://api.example.com/health",\n    "method": "GET",\n    "expected_status_classes": ["2xx"],\n    "json_assertions": [\n      {"path": "status", "op": "equals", "value": "ok"}\n    ],\n    "max_latency_ms": 1500\n  },\n  "interval_seconds": 60,\n  "timeout_seconds": 15,\n  "enabled": true,\n  "tags": ["api", "production"],\n  "consecutive_failures_threshold": 2,\n  "notification_mode": "default",\n  "location_ids": [],\n  "location_quorum": 1\n}',
        },
        {
          type: "callout",
          tone: "warning",
          title: "Test and run have different side effects",
          text:
            "`POST /monitors/test` is allowed for viewers/read keys because it is compute-only. `POST /monitors/{id}/run` publishes a normal job, stores the result, changes state, and can trigger alerts, so it requires write permission.",
        },
      ],
    },
    {
      id: "monitor-relationships",
      title: "Monitor relationships, bulk, and portability",
      blocks: [
        {
          type: "table",
          columns: ["Method and path", "Purpose"],
          rows: [
            [
              "`GET /api/v1/monitors/{id}/members`",
              "List group members",
            ],
            [
              "`POST /api/v1/monitors/{id}/members`",
              "Add monitors to a group",
            ],
            [
              "`DELETE /api/v1/monitors/{id}/members`",
              "Remove supplied monitors from a group",
            ],
            [
              "`GET /api/v1/monitors/{id}/dependencies`",
              "List upstream dependencies",
            ],
            [
              "`POST /api/v1/monitors/{id}/dependencies`",
              "Create an upstream edge",
            ],
            [
              "`DELETE /api/v1/monitors/{id}/dependencies/{dependsOnId}`",
              "Remove an upstream edge",
            ],
            [
              "`GET /api/v1/monitors/{id}/dependents`",
              "List downstream dependents",
            ],
            [
              "`GET /api/v1/monitors/dependency-graph`",
              "Get all participating nodes and directed edges",
            ],
            [
              "`POST /api/v1/monitors/dependency-suggestions`",
              "Compute advisory AI suggestions without saving edges",
            ],
            [
              "`POST /api/v1/monitors/bulk/alerting`",
              "Update notification routing for selected monitors",
            ],
            [
              "`POST /api/v1/monitors/bulk/delete`",
              "Soft-delete selected monitors",
            ],
            [
              "`GET /api/v1/monitors/export`",
              "Export a versioned YAML configuration bundle",
            ],
            [
              "`POST /api/v1/monitors/import/preview`",
              "Parse, map, and validate without creating resources",
            ],
            [
              "`POST /api/v1/monitors/import`",
              "Execute import and return per-row outcomes",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Dependency creation rejects self-reference and transitive cycles with HTTP 409. Export/import is not a universal secret-preserving round trip: masked fields and newer monitor types require explicit preview and remediation.",
        },
      ],
    },
    {
      id: "locations-and-mesh",
      title: "Locations, mesh, and maintenance",
      blocks: [
        {
          type: "table",
          columns: ["Method and path", "Purpose"],
          rows: [
            ["`POST /api/v1/locations`", "Create a private location and credential"],
            ["`GET /api/v1/locations`", "List tenant locations"],
            ["`GET /api/v1/locations/{id}`", "Get location detail"],
            ["`PATCH /api/v1/locations/{id}`", "Update location metadata/state"],
            ["`DELETE /api/v1/locations/{id}`", "Delete and detach a location"],
            [
              "`GET /api/v1/locations/{id}/deploy`",
              "Generate credential-scoped [deployment information](/docs/locations/#create-and-deploy)",
            ],
            ["`GET /api/v1/mesh`", "Read current directional mesh topology/state"],
            [
              "`GET /api/v1/mesh/history`",
              "Read filtered edge history",
            ],
            [
              "`POST /api/v1/mesh/probe`",
              "Run a compute-only immediate directional diagnostic",
            ],
            [
              "`POST /api/v1/maintenance-windows`",
              "Create scheduled maintenance",
            ],
            [
              "`GET /api/v1/maintenance-windows`",
              "List/filter maintenance",
            ],
            [
              "`GET /api/v1/maintenance-windows/{id}`",
              "Get a maintenance window",
            ],
            [
              "`PATCH /api/v1/maintenance-windows/{id}`",
              "Update a maintenance window",
            ],
            [
              "`DELETE /api/v1/maintenance-windows/{id}`",
              "Delete a maintenance window",
            ],
          ],
        },
        {
          type: "code",
          language: "json",
          title: "Create maintenance",
          code:
            '{\n  "title": "Database upgrade",\n  "description": "Planned primary database maintenance",\n  "starts_at": "2026-08-01T22:00:00Z",\n  "ends_at": "2026-08-01T23:30:00Z",\n  "monitor_ids": ["<monitor-id>"]\n}',
        },
      ],
    },
    {
      id: "dashboard",
      title: "Dashboard endpoints",
      blocks: [
        {
          type: "table",
          columns: ["Method and path", "Response focus"],
          rows: [
            [
              "`GET /api/v1/dashboard/overview`",
              "Stats, trend, activity, platform health, problems, recent failures/alerts, tags",
            ],
            [
              "`GET /api/v1/dashboard/summary`",
              "Curated tag-group service summary and ungrouped monitors",
            ],
            [
              "`GET /api/v1/dashboard/problem-monitors`",
              "Monitors needing attention",
            ],
            [
              "`GET /api/v1/dashboard/recent-failures`",
              "Bounded recent failing results",
            ],
            [
              "`GET /api/v1/dashboard/recent-alerts`",
              "Bounded recent alert records",
            ],
            [
              "`GET /api/v1/dashboard/group-sparkline`",
              "Deferred time series for one dashboard group",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Overview ranges are `1h`, `24h`, `7d`, `30d`, `90d`, and `365d`, defaulting to `24h`. Dashboard group rows are tag-derived presentation, not group-monitor resources.",
        },
      ],
    },
    {
      id: "alerts",
      title: "Alert and notification endpoints",
      blocks: [
        {
          type: "table",
          columns: ["Method and path", "Purpose"],
          rows: [
            ["`GET /api/v1/alerts`", "Paginated/filterable alert list"],
            ["`GET /api/v1/alerts/recent`", "Recent alerts with bounded limit"],
            [
              "`GET /api/v1/alerts/stream`",
              "Authenticated server-sent event stream",
            ],
            ["`GET /api/v1/alerts/{id}`", "Alert detail"],
            [
              "`POST /api/v1/alerts/{id}/acknowledge`",
              "Mark an open condition acknowledged",
            ],
            [
              "`POST /api/v1/alerts/{id}/resolve`",
              "Manually close an alert lifecycle",
            ],
            [
              "`GET /api/v1/alert-channels`",
              "List configured channels",
            ],
            [
              "`POST /api/v1/alert-channels`",
              "Create a plugin-backed channel",
            ],
            [
              "`GET /api/v1/alert-channels/{id}`",
              "Read channel with protected config masked",
            ],
            [
              "`PATCH /api/v1/alert-channels/{id}`",
              "Update config or active state",
            ],
            [
              "`DELETE /api/v1/alert-channels/{id}`",
              "Delete a channel",
            ],
            [
              "`POST /api/v1/alert-channels/{id}/test`",
              "Send a real test notification",
            ],
            [
              "`GET /api/v1/alert-channel-plugins`",
              "List channel plugin form manifests",
            ],
            [
              "`GET /api/v1/alert-channel-plugins/{type}`",
              "Get one plugin manifest",
            ],
            [
              "`GET /api/v1/notification-settings`",
              "Read tenant defaults, reminders, anomaly and auto-incident settings",
            ],
            [
              "`PUT /api/v1/notification-settings`",
              "Replace tenant notification settings",
            ],
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Alert policies are gone",
          text:
            "All `/api/v1/alert-policies` methods return HTTP `410 Gone`. The policy-count compatibility endpoints under `/alerts/counts/...` should not be used as a supported policy-management model. See [retired alert policies](/docs/alerting/#retired-policies).",
        },
      ],
    },
    {
      id: "incidents",
      title: "Incident endpoints",
      blocks: [
        {
          type: "table",
          columns: ["Method and path", "Purpose"],
          rows: [
            ["`GET /api/v1/incidents`", "List incidents"],
            ["`POST /api/v1/incidents`", "Create an incident"],
            ["`GET /api/v1/incidents/{id}`", "Get full incident context"],
            ["`PATCH /api/v1/incidents/{id}`", "Update metadata"],
            [
              "`POST /api/v1/incidents/{id}/state`",
              "Transition lifecycle state",
            ],
            [
              "`POST /api/v1/incidents/{id}/timeline`",
              "Add an internal note, public update, or supported timeline event",
            ],
            [
              "`POST /api/v1/incidents/{id}/alerts`",
              "Attach an alert",
            ],
            [
              "`DELETE /api/v1/incidents/{id}/alerts/{alertId}`",
              "Detach an alert",
            ],
            [
              "`POST /api/v1/incidents/{id}/monitors`",
              "Attach a monitor",
            ],
            [
              "`DELETE /api/v1/incidents/{id}/monitors/{monitorId}`",
              "Detach a monitor",
            ],
            [
              "`PUT /api/v1/incidents/{id}/status-pages/{statusPageId}`",
              "Publish incident to one status page",
            ],
            [
              "`DELETE /api/v1/incidents/{id}/status-pages/{statusPageId}`",
              "Unpublish from one status page",
            ],
            [
              "`POST /api/v1/incidents/{id}/ai-analysis`",
              "Queue tenant-configured asynchronous analysis",
            ],
            [
              "`GET /api/v1/incidents/{id}/ai-analysis`",
              "Read pending, ready, or failed analysis state",
            ],
          ],
        },
      ],
    },
    {
      id: "status-page-api",
      title: "Status page endpoints",
      blocks: [
        {
          type: "table",
          columns: ["Method and path", "Purpose"],
          rows: [
            ["`POST /api/v1/status-pages`", "Create a page"],
            ["`GET /api/v1/status-pages`", "List pages"],
            ["`GET /api/v1/status-pages/{id}`", "Get page configuration"],
            ["`PATCH /api/v1/status-pages/{id}`", "Update page and settings"],
            ["`DELETE /api/v1/status-pages/{id}`", "Delete a page"],
            [
              "`GET /api/v1/status-pages/{id}/template`",
              "Read published/draft/version state",
            ],
            [
              "`DELETE /api/v1/status-pages/{id}/template`",
              "Reset to the built-in template as a new version",
            ],
            [
              "`GET /api/v1/status-pages/{id}/template/default`",
              "Export current built-in source",
            ],
            [
              "`GET /api/v1/status-pages/{id}/template/versions/{version}/source`",
              "Read archived version source",
            ],
            [
              "`PUT /api/v1/status-pages/{id}/template/draft`",
              "Validate and save the one draft",
            ],
            [
              "`DELETE /api/v1/status-pages/{id}/template/draft`",
              "Discard draft",
            ],
            [
              "`POST /api/v1/status-pages/{id}/template/publish`",
              "Publish draft and archive current source",
            ],
            [
              "`POST /api/v1/status-pages/{id}/template/revert`",
              "Republish archived source as a new version",
            ],
          ],
        },
        {
          type: "table",
          columns: ["Template-library route", "Purpose"],
          rows: [
            ["`GET /api/v1/status-page-templates`", "List tenant template metadata"],
            ["`POST /api/v1/status-page-templates`", "Create a validated library entry"],
            [
              "`GET /api/v1/status-page-templates/{templateId}`",
              "Get entry metadata",
            ],
            [
              "`GET /api/v1/status-page-templates/{templateId}/source`",
              "Retrieve source",
            ],
            [
              "`PATCH /api/v1/status-page-templates/{templateId}`",
              "Rename/update source",
            ],
            [
              "`DELETE /api/v1/status-page-templates/{templateId}`",
              "Delete library entry",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "The separate status-page service exposes unauthenticated `GET /public/status/{slug}`, `/data`, and `/stream`, plus [draft preview](/docs/status-pages/#preview-security). Do not use those public routes to mutate configuration.",
        },
      ],
    },
    {
      id: "passive-api",
      title: "Agent and push endpoints",
      blocks: [
        {
          type: "table",
          columns: ["Method and path", "Authorization and purpose"],
          rows: [
            [
              "`POST /api/v1/agent/metrics`",
              "Tenant write API key; submit agent identity and host telemetry",
            ],
            [
              "`GET /api/v1/monitors/{id}/agent/install`",
              "Authenticated; generated install command/information",
            ],
            [
              "`GET /api/v1/monitors/{id}/agent/install/script.sh`",
              "Authenticated Unix installer",
            ],
            [
              "`GET /api/v1/monitors/{id}/agent/install/script.ps1`",
              "Authenticated Windows installer",
            ],
            [
              "`GET /api/v1/monitors/{id}/agent/uninstall/script.sh`",
              "Authenticated Unix uninstaller",
            ],
            [
              "`GET /api/v1/monitors/{id}/agent/uninstall/script.ps1`",
              "Authenticated Windows uninstaller",
            ],
            [
              "`GET /api/v1/monitors/{id}/push/info`",
              "Authenticated generated push URL/config information",
            ],
            [
              "`GET /api/v1/push/{token}`",
              "Public capability token; success heartbeat plus query metrics",
            ],
            [
              "`POST /api/v1/push/{token}`",
              "Public capability token; status/error plus arbitrary metric values",
            ],
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Capability URLs are credentials",
          text:
            "Redact push tokens from logs and tracing. [Agent installer](/docs/agents/#install) responses can embed a tenant API key supplied for installation and must be handled as secret output.",
        },
      ],
    },
    {
      id: "admin-api",
      title: "Administration endpoints",
      blocks: [
        {
          type: "table",
          columns: ["Method and path", "Required role and purpose"],
          rows: [
            [
              "`GET /api/v1/auth-context`",
              "Authenticated; effective credential, tenant, role, and scope",
            ],
            [
              "`GET /api/v1/tenants`",
              "Admin session; list membership-filtered tenants or all for superadmin",
            ],
            [
              "`GET /api/v1/tenant-settings`",
              "Authenticated; read retention and dashboard tags",
            ],
            [
              "`PATCH /api/v1/tenant-settings`",
              "Tenant admin; update retention/dashboard tags",
            ],
            [
              "`GET /api/v1/api-keys`",
              "Authenticated tenant context; list safe key metadata",
            ],
            [
              "`POST /api/v1/api-keys`",
              "Tenant admin; create and return secret once",
            ],
            [
              "`DELETE /api/v1/api-keys/{id}`",
              "Tenant admin; revoke",
            ],
            [
              "`GET /api/v1/audit-log`",
              "Tenant admin; filtered [audit records](/docs/administration/#audit)",
            ],
            [
              "`GET /api/v1/audit-log/actions`",
              "Tenant admin; distinct action catalog",
            ],
            [
              "`GET /api/v1/ai-settings`",
              "Authenticated; effective tenant-safe settings",
            ],
            [
              "`PUT /api/v1/ai-settings`",
              "Authorized write; replace tenant AI configuration",
            ],
            [
              "`POST /api/v1/ai-settings/test`",
              "Compute-only provider connection test",
            ],
            [
              "`GET /api/v1/users`",
              "Superadmin; list platform users",
            ],
            [
              "`POST /api/v1/users`",
              "Superadmin; create user and memberships",
            ],
            [
              "`GET /api/v1/users/{id}`",
              "Superadmin; user detail",
            ],
            [
              "`PATCH /api/v1/users/{id}`",
              "Superadmin; update role/auth/membership fields",
            ],
            [
              "`DELETE /api/v1/users/{id}`",
              "Superadmin; delete subject to last-admin/self safeguards",
            ],
          ],
        },
      ],
    },
    {
      id: "sse",
      title: "Server-sent events",
      blocks: [
        {
          type: "paragraph",
          text:
            "`GET /api/v1/alerts/stream` is an authenticated tenant alert stream. The public status-page service exposes a different slug-scoped stream at `/public/status/{slug}/stream`. Use a native EventSource-compatible client where cookie authentication is required, or a streaming HTTP client that can attach the bearer key.",
        },
        {
          type: "code",
          language: "bash",
          title: "Inspect an alert stream",
          code:
            "curl -N \\\n  -H 'Authorization: Bearer pk_<secret>' \\\n  \"$PROBARA_API_ORIGIN/api/v1/alerts/stream\"",
        },
        {
          type: "list",
          items: [
            "Reconnect with backoff after network or proxy interruption.",
            "Treat events as refresh/invalidation signals and refetch durable resource state.",
            "Keep proxy buffering disabled for the streaming route.",
            "Do not assume the stream is a replayable audit log.",
          ],
        },
      ],
    },
    {
      id: "safe-clients",
      title: "Build a safe client",
      blocks: [
        {
          type: "list",
          items: [
            "Set explicit HTTP timeouts and retry only operations whose idempotency you understand.",
            "Retry GET requests with bounded exponential backoff; do not blindly retry create, run-now, import execution, notification tests, or incident timeline writes.",
            "Use import preview before import execution and monitor test before saving complex check configuration.",
            "Respect `401`, `403`, `404`, `409`, `410`, `422`, and rate/transport failures as distinct conditions.",
            "Preserve `***` when updating masked designated secret fields; omitting a field from a replaced type config can clear it.",
            "Page collections and retain returned coverage metadata for analytics.",
            "Store full API keys only in a secret manager; the API returns them once.",
            "Log resource IDs and status codes without logging bearer keys, push tokens, installer output, WebSocket headers, connection strings, or webhook secrets.",
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "Use auth-context for diagnostics",
          text:
            "When a request is unexpectedly forbidden, `GET /api/v1/auth-context` shows the effective credential type, tenant, tenant role, and key scope without requiring a guess about middleware precedence.",
        },
      ],
    },
  ],
};

import type { DocPage } from "../types";

export const ARCHITECTURE_PAGE: DocPage = {
  slug: "architecture",
  group: "Start here",
  title: "Architecture",
  description:
    "Understand Probara's services, data paths, state model, queue contracts, and tenant boundaries.",
  eyebrow: "Concepts",
  readingTime: "18 min read",
  keywords: [
    "architecture",
    "services",
    "PostgreSQL",
    "NATS JetStream",
    "monitor state",
  ],
  sections: [
    {
      id: "system-shape",
      title: "System shape",
      intro:
        "Probara separates control-plane APIs, scheduling, check execution, alert evaluation, and public presentation. PostgreSQL is the source of truth; NATS carries asynchronous work and live invalidation.",
      blocks: [
        {
          type: "code",
          language: "text",
          title: "Primary data flow",
          code:
            "Web / API client\n      │\n      ▼\n     API ───────────────► PostgreSQL ◄────────────── Alerter\n      │                         ▲                        │\n      │                         │                        ├─ notifications\n      ▼                         │                        └─ incidents\nScheduler ── check.jobs ──► Worker(s)\n      ▲                         │\n      └──── check.results ◄─────┘\n\nPostgreSQL + statuspage.updates ──► Status-page service ──► Public visitors",
        },
        {
          type: "paragraph",
          text:
            "The boundaries are deliberate. The scheduler does not execute checks or evaluate alert policies. Workers do not need direct PostgreSQL access. The alerter derives lifecycle decisions from stored state rather than from an in-memory result stream alone.",
        },
      ],
    },
    {
      id: "services",
      title: "Service responsibilities",
      blocks: [
        {
          type: "table",
          columns: ["Service", "Owns", "Does not own"],
          rows: [
            [
              "`api/`",
              "CRUD, authentication, tenants, dashboard/admin flows, imports, push and agent ingestion, status update publication",
              "Scheduled active checks or notification dispatch",
            ],
            [
              "`scheduler/`",
              "Due-monitor claiming, per-location fan-out, result ingestion, aggregate state, rollups, retention, asynchronous purge, mesh scheduling",
              "Network check execution or alert decisions",
            ],
            [
              "`worker/`",
              "HTTP, ping, DNS, gRPC, SIP, TCP, database, broker, WebSocket, and synthetic execution",
              "Application CRUD or direct database state mutation",
            ],
            [
              "`alerter/`",
              "Availability, anomaly, host-metric, and mesh alert lifecycle; routing; reminders; notification deduplication; incidents",
              "Running monitors",
            ],
            [
              "`status-page/`",
              "Public rendering, public data, draft preview, cache invalidation, live SSE updates",
              "The authenticated product UI",
            ],
            [
              "`web/`",
              "The Next.js product interface",
              "Public Go-rendered status pages",
            ],
            [
              "`collector/`",
              "OpenTelemetry Collector Builder manifest for the `probara-collector` host-metrics distribution",
              "Remote active checks or private-location worker behavior",
            ],
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "The host agent is a built OpenTelemetry distribution",
          text:
            "There is no custom agent codebase or nested Go module: `scripts/build-collector.sh` generates and cross-compiles the collector from `collector/manifest.yaml` with a pinned OpenTelemetry Collector Builder version, and the API serves the binaries plus `checksums.txt` from `/static/collector/`.",
        },
      ],
    },
    {
      id: "source-of-truth",
      title: "Persistence and consistency",
      blocks: [
        {
          type: "definitions",
          items: [
            {
              term: "PostgreSQL",
              description:
                "Stores tenant-scoped configuration, check results, current and per-location state, alerts, incidents, maintenance, notification settings, rollups, and audit records.",
            },
            {
              term: "NATS JetStream",
              description:
                "Provides durable streams for check jobs, check results, alert events, and optional asynchronous analysis jobs.",
            },
            {
              term: "Core NATS",
              description:
                "Provides low-latency status-page invalidation and worker location heartbeats where replay is not required.",
            },
          ],
        },
        {
          type: "paragraph",
          text:
            "Schedulers claim due rows with database locking that skips rows already claimed by another scheduler. The queue path is at-least-once, so result ingestion is designed to deduplicate repeated job/location results. Clients must not infer exactly-once execution from a single successful publish.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Subject names are configuration",
          text:
            "Code defaults include `CHECK_JOBS` / `check.jobs`, `CHECK_RESULTS` / `check.results`, `ALERTS` / `alerts`, and the core subject `statuspage.updates`. Deployment manifests can override names, and location jobs derive location-specific subjects. Keep publishers, consumers, Docker Compose, and Helm values aligned.",
        },
      ],
    },
    {
      id: "monitor-execution",
      title: "Monitor execution lifecycle",
      blocks: [
        {
          type: "list",
          ordered: true,
          items: [
            "The scheduler selects an enabled, due active monitor and advances its next due time.",
            "For a monitor without selected locations, it publishes one job to the default fleet. With selected locations, it publishes one job per enabled location.",
            "A worker validates the target and configuration, performs the check within the monitor timeout, and publishes a result.",
            "The scheduler persists each result and updates per-location state.",
            "For multi-location monitoring, it derives a monitor-level state using the configured [failure quorum](/docs/locations/#assignment).",
            "The stored state is exposed to dashboards and status pages and evaluated by the alerter.",
          ],
        },
        {
          type: "paragraph",
          text:
            "`Run now` follows the persisted job/result path. `Test` is an API request/reply path designed for configuration validation and does not create history or alerts.",
        },
      ],
    },
    {
      id: "state-model",
      title: "Monitor state model",
      intro:
        "State is intentionally richer than a binary up/down flag. Temporal confirmation and location quorum answer different questions and are applied at different layers.",
      blocks: [
        {
          type: "table",
          columns: ["State", "Meaning", "Availability alert effect"],
          rows: [
            [
              "`unknown`",
              "No conclusive current result, or a topology/configuration transition reset state",
              "Does not open an availability alert",
            ],
            [
              "`up`",
              "The latest effective outcome is healthy",
              "Resolves an open availability alert",
            ],
            [
              "`suspect`",
              "A failure has occurred but has not yet reached the consecutive-failure threshold",
              "Does not open an availability alert",
            ],
            [
              "`down`",
              "The temporal threshold or location quorum confirms failure",
              "Opens an alert when not muted by maintenance or rollup",
            ],
            [
              "`degraded`",
              "Some locations are down, but fewer than the quorum",
              "Does not open a new availability alert",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "For a single execution stream, `failure` and `error` outcomes increment the [consecutive-failure counter](/docs/monitors/#scheduling-and-state). Before the configured threshold the monitor is `suspect`; at the threshold it is `down`. A successful result resets the counter and returns the state to `up`.",
        },
        {
          type: "paragraph",
          text:
            "For multiple locations, the aggregate is `down` when the number of down locations reaches quorum, `degraded` when at least one is down but quorum is not reached, `suspect` when none is down but at least one is suspect, and `up` when a healthy report is available without a failing aggregate. Locations that have not reported do not independently trip quorum.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Degraded is not a resolved down alert",
          text:
            "A degraded aggregate does not open a new availability alert. If an alert already opened while the monitor was down, it remains open until the monitor is fully up; degraded state does not resolve it.",
        },
      ],
    },
    {
      id: "result-sources",
      title: "Result sources and analytics",
      blocks: [
        {
          type: "definitions",
          items: [
            {
              term: "`monitor`",
              description:
                "A result produced by the monitor execution path and eligible for monitor availability and latency analytics.",
            },
            {
              term: "`derived`",
              description:
                "A state or result produced by aggregation rather than a direct network execution.",
            },
            {
              term: "`platform`",
              description:
                "A platform-generated operational result, such as a stale passive monitor. These records are excluded from normal uptime analytics.",
            },
          ],
        },
        {
          type: "paragraph",
          text:
            "Recent ranges can be calculated from raw results. Longer windows use [hourly and daily rollups](/docs/dependencies/#rollups) plus an unrolled raw tail. Analytics responses identify their source and coverage and can report partial coverage when retained data does not span the requested range.",
        },
      ],
    },
    {
      id: "alert-and-publication-flow",
      title: "Alert and public status flow",
      blocks: [
        {
          type: "list",
          ordered: true,
          items: [
            "The alerter evaluates stored monitor, location, host-agent, and mesh state.",
            "It opens, acknowledges, reminds, and resolves [alert records](/docs/alerting/#model) while deduplicating lifecycle notifications.",
            "Notification routing comes from tenant defaults or per-monitor custom channel assignments, including per-channel delay.",
            "Selected alerts can be attached to incidents. Incidents can publish updates to selected [status pages](/docs/status-pages/#incidents).",
            "The status-page service loads durable page data from the API/database path and uses `statuspage.updates` to invalidate caches and push live SSE refreshes.",
          ],
        },
        {
          type: "paragraph",
          text:
            "Live NATS messages are an acceleration mechanism, not the durable source of truth. Public templates also refresh on a fallback interval so a missed core-NATS message does not permanently freeze a page.",
        },
      ],
    },
    {
      id: "tenancy",
      title: "Tenant and authorization boundaries",
      blocks: [
        {
          type: "paragraph",
          text:
            "Application data is tenant-scoped throughout the model. Administrator sessions can select a tenant for which the user has membership; [API keys](/docs/api/#tenant-and-permissions) are permanently pinned to the tenant that created them.",
        },
        {
          type: "table",
          columns: ["Credential", "Tenant selection", "Typical use"],
          rows: [
            [
              "Admin session cookie",
              "`X-Tenant-ID` with a membership-aware fallback",
              "Interactive UI and platform administration",
            ],
            [
              "API key",
              "Fixed at key creation",
              "Automation and integrations with read or write scope",
            ],
            [
              "Push token",
              "Resolved from the passive monitor",
              "Unauthenticated-by-session heartbeat endpoint; possession is authority",
            ],
            [
              "Location credential",
              "Fixed to one private location",
              "NATS authentication and per-location worker work",
            ],
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Preserve tenant filters in extensions",
          text:
            "A globally unique resource ID is not an authorization check. New queries and service methods must bind resources to the authenticated tenant before reading or mutating them.",
        },
      ],
    },
    {
      id: "operational-contract",
      title: "Operational service contract",
      blocks: [
        {
          type: "list",
          items: [
            "Standard services expose `/healthz`, `/readyz`, and `/metrics` on their configured listeners.",
            "Graceful shutdown stops new work, drains or closes queue consumers where applicable, and allows in-flight HTTP work to complete within configured limits.",
            "Remote workers require NATS but do not receive direct PostgreSQL credentials.",
            "Location-specific monitor secrets are encrypted for the location credential before they cross the queue; the platform master encryption key is not sent to a worker.",
            "PostgreSQL schema, queue subjects, credentials, ports, and public origins must be changed consistently across local Compose and Helm deployment configuration.",
          ],
        },
      ],
    },
  ],
};

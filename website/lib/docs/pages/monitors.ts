import type { DocPage } from "../types";

export const MONITORS_PAGE: DocPage = {
  slug: "monitors",
  group: "Use Probara",
  title: "Monitors",
  description:
    "Configure active, passive, grouped, database, broker, WebSocket, and synthetic monitors.",
  eyebrow: "Product guide",
  readingTime: "35 min read",
  keywords: [
    "HTTP monitor",
    "synthetic monitoring",
    "database monitoring",
    "monitor configuration",
    "uptime",
  ],
  sections: [
    {
      id: "monitor-model",
      title: "Monitor model",
      intro:
        "A monitor combines a type-specific check configuration with scheduling, state confirmation, [notification routing](/docs/alerting/#routing), dependencies, and optional [execution locations](/docs/locations/).",
      blocks: [
        {
          type: "table",
          columns: ["Field", "Meaning", "Constraints and defaults"],
          rows: [
            ["`name`", "Tenant-visible monitor name", "Required"],
            [
              "`type`",
              "Execution or aggregation implementation",
              "Immutable in normal editing; see supported types below",
            ],
            [
              "`config`",
              "Type-specific JSON object",
              "Validated independently for every type",
            ],
            [
              "`interval_seconds`",
              "Time between scheduled runs or expected passive reports",
              "10–86,400 seconds",
            ],
            [
              "`timeout_seconds`",
              "Maximum active-check duration",
              "Positive and shorter than `interval_seconds` for active types",
            ],
            [
              "`enabled`",
              "Whether the monitor is scheduled/evaluated",
              "Disabled monitors do not execute or maintain availability alerts",
            ],
            [
              "`tags`",
              "Search, dashboard-grouping, and filtering labels",
              "Stored as a tenant-scoped list",
            ],
            [
              "`consecutive_failures_threshold`",
              "Failures required before temporal state becomes down",
              "1–10",
            ],
            [
              "`notification_mode`",
              "Use tenant defaults or monitor-specific routes",
              "`default` or `custom`",
            ],
            [
              "`notification_channels`",
              "Custom channel assignments and escalation delay",
              "Each item has `channel_id` and non-negative `delay_seconds`",
            ],
            [
              "`alert_routing`",
              "Read-only reachability: whether this monitor's alerts notify anyone, and why not — see [notification routing](/docs/alerting/#routing)",
              "Computed per request; never accepted on write",
            ],
            [
              "`depends_on_ids`",
              "Upstream monitors used for root-cause annotation",
              "Tenant-scoped, live monitors; cycles rejected",
            ],
            [
              "`location_ids`",
              "Private worker locations selected for active execution",
              "Not supported for group, agent, or push monitors",
            ],
            [
              "`location_quorum`",
              "Down locations required for aggregate down state",
              "Clamped to 1 through the number of selected locations",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Read responses also expose identity and timestamps, `next_run_at`, the effective current state, maintenance status, member and dependency information, passive credentials where applicable, and per-location state on the detailed monitor view.",
        },
      ],
    },
    {
      id: "supported-types",
      title: "Supported monitor types",
      blocks: [
        {
          type: "table",
          columns: ["Type", "What it verifies", "Scheduled active check"],
          rows: [
            ["`http`", "HTTP response, content, headers, JSON, TLS, and latency", "Yes"],
            ["`ping`", "ICMP reachability", "Yes"],
            ["`dns`", "DNS resolution and optional expected answers", "Yes"],
            ["`grpc`", "Standard gRPC health service status", "Yes"],
            ["`tcp`", "TCP connection and optional TLS handshake", "Yes"],
            ["`sip`", "SIP `OPTIONS` ping or `REGISTER` auth probe", "Yes"],
            ["`websocket`", "WebSocket upgrade and optional message exchange", "Yes"],
            ["`redis`", "Redis authentication, `PING`, and optional role", "Yes"],
            ["`postgres`", "PostgreSQL connect and optional query assertion", "Yes"],
            ["`mysql`", "MySQL connect and optional query assertion", "Yes"],
            ["`mongodb`", "MongoDB connectivity, optional topology constraint, and optional clusterMonitor cluster checks", "Yes"],
            ["`rabbitmq`", "AMQP handshake, authentication, and virtual-host access", "Yes"],
            ["`synthetic_api`", "A sequence of templated HTTP API steps", "Yes"],
            ["`synthetic_browser`", "A Chromium browser journey", "Yes"],
            ["`group`", "Derived state from member monitors", "No"],
            ["`agent`", "Host metrics and freshness pushed by an [installed OpenTelemetry collector](/docs/agents/#install)", "No"],
            ["`push`", "Token-based [passive heartbeat](/docs/agents/#push-config) freshness", "No"],
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "Import accepts every type in this table",
          text:
            "All types above can be created through the normal monitor API/UI, and the raw portable import path accepts the same registry of types. Exported secrets are masked, so re-imported monitors that rely on credentials still need those values re-entered before their checks pass.",
        },
      ],
    },
    {
      id: "scheduling-and-state",
      title: "Scheduling, confirmation, and quorum",
      blocks: [
        {
          type: "paragraph",
          text:
            "An active monitor runs on its interval. Both `failure` and `error` outcomes count toward consecutive failure confirmation. Before the threshold it is `suspect`; at the threshold it becomes `down`. Any successful effective result resets the temporal counter and returns it to `up`. Confirmed transitions drive the [availability alert lifecycle](/docs/alerting/#availability).",
        },
        {
          type: "paragraph",
          text:
            "A monitor selected for multiple locations maintains independent location state. The aggregate becomes `down` when down locations reach `location_quorum`; it becomes `degraded` when at least one location is down but the quorum is not met. If no location is down but one is suspect, the aggregate is suspect. Locations that have not reported are not counted as down.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Quorum is failure quorum",
          text:
            "A quorum of 2 means two locations must be down—not that two locations must be up. Disabled or deleted locations are removed from scheduling and the stored quorum is reduced when necessary.",
        },
      ],
    },
    {
      id: "http",
      title: "HTTP monitors",
      intro:
        "HTTP monitors support transport diagnostics and layered assertions on one response.",
      blocks: [
        {
          type: "table",
          columns: ["Variable", "Purpose"],
          rows: [
            ["`url`", "Required absolute `http://` or `https://` target"],
            [
              "`method`",
              "Required: `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD`, or `OPTIONS`",
            ],
            ["`headers`", "Request header map"],
            ["`body`", "Optional request body"],
            [
              "`expected_status`",
              "One accepted HTTP status code between 100 and 599",
            ],
            [
              "`expected_statuses`",
              "Additional individually accepted status codes",
            ],
            [
              "`expected_status_ranges`",
              "Accepted inclusive `{min,max}` ranges between 100 and 599",
            ],
            [
              "`expected_status_classes`",
              "Accepted classes such as `2xx`, up to `5xx`",
            ],
            [
              "`expected_body_substring` / `expected_body_regex`",
              "Legacy single body checks",
            ],
            [
              "`body_assertions`",
              "`contains`, `not_contains`, `regex`, or `not_regex` checks",
            ],
            [
              "`response_header_assertions`",
              "Header name plus `exists`, equality, containment, regex, and negative variants",
            ],
            [
              "`json_assertions`",
              "GJSON path plus existence, equality, text, regex, numeric, or boolean operation",
            ],
            [
              "`case_insensitive`",
              "Optional case-insensitive text comparison where supported",
            ],
            [
              "`max_latency_ms`",
              "Fail the monitor when total response latency exceeds this limit",
            ],
            [
              "`follow_redirects` / `max_redirects`",
              "Redirect policy; maximum is 0–50",
            ],
            [
              "`tls_skip_verify`",
              "Skip server certificate verification for HTTPS; use only for controlled targets",
            ],
            [
              "`tls_min_days_valid`",
              "Open a [`tls_expiry` alert](/docs/alerting/#host-and-mesh) when the leaf certificate has fewer remaining validity days; the check itself still passes",
            ],
            [
              "`tls_server_name`",
              "Override the TLS server name used for verification",
            ],
            ["`tls_ca_pem`", "Additional PEM-encoded certificate authority"],
            [
              "`collect_timing`",
              "Collect detailed DNS, connect, TLS, TTFB, and total timing values",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "When no status expectation is supplied, the accepted default is the `2xx` class. When multiple status criteria are supplied, satisfying any accepted code, range, or class is sufficient. Body, header, JSON, and latency assertions are then evaluated in addition to status. The `tls_min_days_valid` window is the exception: it never fails the check and instead raises a dedicated `tls_expiry` alert (the check still fails if the certificate cannot be inspected at all, e.g. a non-HTTPS URL with the field set).",
        },
        {
          type: "paragraph",
          text:
            "JSON assertions support `exists`, `equals`, `not_equals`, `contains`, `not_contains`, `regex`, `number_gt`, `number_gte`, `number_lt`, `number_lte`, and `bool_is`. Result metrics can include the final URL, redirect count, phase timings, TLS version and cipher, certificate validity, issuer, subject names, expiry, and assertion detail.",
        },
        {
          type: "code",
          language: "json",
          title: "HTTP monitor config",
          code:
            '{\n  "url": "https://api.example.com/health",\n  "method": "GET",\n  "headers": {"Accept": "application/json"},\n  "expected_status_classes": ["2xx"],\n  "json_assertions": [\n    {"path": "status", "op": "equals", "value": "ok"},\n    {"path": "queue_depth", "op": "number_lt", "value": 100}\n  ],\n  "max_latency_ms": 1500,\n  "follow_redirects": true,\n  "max_redirects": 5,\n  "tls_min_days_valid": 14,\n  "collect_timing": true\n}',
        },
      ],
    },
    {
      id: "network-protocols",
      title: "Ping, DNS, gRPC, TCP, and SIP",
      blocks: [
        {
          type: "definitions",
          items: [
            {
              term: "Ping",
              description:
                "Set `host` to a hostname or IP address. The worker performs an ICMP reachability check; firewalls and worker privileges can affect results independently of application health.",
            },
            {
              term: "DNS",
              description:
                "Set `host`, optional `record_type`, optional `expected_answers`, and optional `nameserver`. Supported record types are `A`, `AAAA`, `CNAME`, `TXT`, `MX`, and `NS`; the record type defaults to `A`. A nameserver can be a host or `host:port`, with port 53 used when omitted.",
            },
            {
              term: "gRPC",
              description:
                "Set `host`, optional `port`, optional `service`, and `use_tls`. Probara calls the standard `grpc.health.v1.Health/Check` method and succeeds only for `SERVING`. The default port is 443 with TLS and 80 without it.",
            },
            {
              term: "TCP",
              description:
                "Set `host` and `port`; optionally enable TLS and `tls_skip_verify`. A successful connection, plus a successful TLS handshake when enabled, is healthy. No application payload is exchanged.",
            },
            {
              term: "SIP",
              description:
                "Set `host`, optional `port`, `transport` (`udp`, `tcp`, or `tls`), `method`, and `expected_status`. `method: options` (default) sends a SIP `OPTIONS` availability ping; `method: register` sends a query-style `REGISTER` (no Contact header) that exercises the registrar and its digest authentication without creating, refreshing, or removing bindings. Configure `username`/`password` to answer digest challenges (MD5 and SHA-256, qop=auth) on either method, and `domain` to set the address-of-record for REGISTER. Port defaults to 5060 (5061 for TLS), status to 200; `tls_skip_verify` accepts lab certificates.",
            },
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "Protocol health is deliberately narrow",
          text:
            "A successful TCP connection does not prove an application protocol is healthy. A gRPC monitor requires the standard health service, and a SIP monitor checks an `OPTIONS` or `REGISTER` response rather than completing a call flow with media.",
        },
      ],
    },
    {
      id: "websocket",
      title: "WebSocket monitors",
      blocks: [
        {
          type: "table",
          columns: ["Variable", "Purpose"],
          rows: [
            ["`url`", "Required `ws://` or `wss://` endpoint"],
            ["`headers`", "Optional upgrade request headers"],
            ["`tls_skip_verify`", "Disable WSS certificate verification"],
            ["`send_message`", "Optional text frame sent after the upgrade"],
            [
              "`expected_substring`",
              "Require the first received message to contain this text",
            ],
            [
              "`max_latency_ms`",
              "Fail if connection/exchange latency exceeds the maximum",
            ],
            [
              "`warn_latency_ms`",
              "Record a warning metric below the hard maximum without failing the monitor",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Without a message or expected substring, a successful WebSocket upgrade is sufficient. With an expectation, the worker reads the first response message and performs a substring check.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Header values are secret material",
          text:
            "WebSocket header values are encrypted when a secrets key is configured and returned as `***` through the API. Header names remain visible.",
        },
      ],
    },
    {
      id: "data-services",
      title: "Redis, PostgreSQL, MySQL, MongoDB, and RabbitMQ",
      intro:
        "Data-service monitors perform a real protocol handshake. Database monitors can optionally run a read query and assert its first returned value.",
      blocks: [
        {
          type: "table",
          columns: ["Type", "Connection variables", "Additional verification"],
          rows: [
            [
              "`redis`",
              "`connection_string` (`redis://` or `rediss://`) or `host`, `port` (6379), `username`, `password`, `db` (0–15), TLS",
              "`PING` and optional `expected_role` of `master` or `replica`",
            ],
            [
              "`postgres`",
              "PostgreSQL URI/key-value DSN or `host`, `port` (5432), `database` (`postgres`), `username`, `password`, `ssl_mode`",
              "Optional `query` and first-column value assertion",
            ],
            [
              "`mysql`",
              "MySQL URI/driver DSN or `host`, `port` (3306), `database`, `username`, `password`, TLS",
              "Optional `query` and first-column value assertion",
            ],
            [
              "`mongodb`",
              "`mongodb://` / `mongodb+srv://` URI or `host`, `port` (27017), paired username/password, `auth_source`, TLS",
              "Optional `replica_set` topology and reachable-primary requirement; optional cluster checks via `collect_replication`, `collect_connections`, `collect_cache`, `collect_memory`, `collect_network`, `collect_cpu`",
            ],
            [
              "`rabbitmq`",
              "`amqp://` / `amqps://` URI or `host`, port (5672 or 5671 with TLS), username/password, `vhost`",
              "AMQP handshake, authentication, and virtual-host access; it does not publish a message",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "PostgreSQL and MySQL query assertions use `query_value_op` with `equals`, `not_equals`, `contains`, or numeric comparison operations against the first column of the first returned row. A configured query must return a row.",
        },
        {
          type: "paragraph",
          text:
            "These monitors share optional `max_latency_ms` and `warn_latency_ms`. The maximum is a hard failure; the warning threshold annotates metrics without changing a successful check to failure.",
        },
        {
          type: "paragraph",
          text:
            "MongoDB monitors can additionally enable per-feature cluster checks, each an individually toggleable read-only admin command: `collect_replication` runs `replSetGetStatus` (member states, health, and replication lag), while `collect_connections`, `collect_cache`, `collect_memory`, `collect_network`, and `collect_cpu` read their sections from a single `serverStatus` call (connections, WiredTiger cache, resident/virtual memory, network I/O and opcounters, and — on Linux servers — mongod process CPU time; host-level CPU is agent-monitor territory). Both commands are covered by MongoDB's built-in `clusterMonitor` role — no `clusterAdmin`, `root`, or write privileges. When the monitoring user lacks the role, the affected data is skipped and flagged in the check's metrics (`unavailable`) without failing the check; on a standalone server the replication check reports \"not a replica set\". Replication lag supports the same warn/max split as latency: `warn_replication_lag_seconds` annotates, `max_replication_lag_seconds` fails the check and flows through normal availability alerting — and it fails closed, so if replication status becomes unreadable (role revoked, command timeout, standalone target, no primary) the check fails rather than silently passing.",
        },
        {
          type: "paragraph",
          text:
            "The monitor detail page charts the collected cluster metrics over recent checks — replication lag (with the warn/max thresholds drawn as reference lines), connections, WiredTiger cache, memory, and, for the cumulative operation, network, and process-CPU counters, per-second rates derived between consecutive checks.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Granting clusterMonitor",
          text:
            "`db.grantRolesToUser(\"monitoring\", [{ role: \"clusterMonitor\", db: \"admin\" }])` is the only grant the cluster checks need. Without it the basic connect/ping/latency monitoring keeps working unchanged.",
        },
        {
          type: "table",
          columns: ["TLS variable", "Meaning"],
          rows: [
            [
              "`tls_enabled`",
              "Enable transport TLS where the monitor is not controlled by a PostgreSQL `ssl_mode`",
            ],
            ["`tls_skip_verify`", "Skip server certificate verification"],
            ["`tls_ca_pem`", "Additional trusted CA certificate(s)"],
            ["`tls_client_cert_pem`", "Client certificate for mutual TLS"],
            [
              "`tls_client_key_pem`",
              "Client private key; must be paired with the client certificate",
            ],
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Use read-only query credentials",
          text:
            "Optional queries are sent as configured. Give Probara a minimally privileged, read-only database account and use bounded diagnostic queries. The check timeout is not a substitute for database-side query limits.",
        },
      ],
    },
    {
      id: "synthetic-api",
      title: "Synthetic API journeys",
      intro:
        "A synthetic API monitor runs 1–20 ordered HTTP steps with variables, assertions, and extraction from earlier responses.",
      blocks: [
        {
          type: "table",
          columns: ["Level", "Variables"],
          rows: [
            [
              "Journey",
              "`base_url`, `failure_mode`, `variables`, and ordered `steps`",
            ],
            [
              "Step",
              "Unique `id`, optional `name`, `request`, `assert`, and `extract` definitions",
            ],
            [
              "Request",
              "`method`, `url`, headers, body, 1–300 second timeout, redirect policy, maximum 0–50 redirects",
            ],
            [
              "Extraction",
              "Globally unique variable `name`, source `json` or `header`, source path/name, and optional `sensitive` marker",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "URLs may be absolute, or relative when `base_url` is present. Static and extracted variables are referenced with `{{variable_name}}` in subsequent supported fields. `failure_mode` is `fail_fast` by default; use `continue` when later diagnostic steps should still execute after a failed step.",
        },
        {
          type: "paragraph",
          text:
            "Assertions can target status, response headers, body text, or JSON paths. Status supports equality, inequality, and membership. Text and header checks support existence, equality, containment, regular expressions, and negative forms. JSON checks additionally support numeric and boolean operations.",
        },
        {
          type: "code",
          language: "json",
          title: "Two-step API journey",
          code:
            '{\n  "base_url": "https://api.example.com",\n  "failure_mode": "fail_fast",\n  "variables": {"user": "monitor@example.com"},\n  "steps": [\n    {\n      "id": "login",\n      "request": {\n        "method": "POST",\n        "url": "/login",\n        "headers": {"Content-Type": "application/json"},\n        "body": "{\\"email\\":\\"{{user}}\\"}"\n      },\n      "assert": [\n        {"target": "status", "op": "equals", "value": 200}\n      ],\n      "extract": [\n        {"name": "token", "from": "json", "path": "token", "sensitive": true}\n      ]\n    },\n    {\n      "id": "profile",\n      "request": {\n        "method": "GET",\n        "url": "/me",\n        "headers": {"Authorization": "Bearer {{token}}"}\n      },\n      "assert": [\n        {"target": "json", "path": "email", "op": "equals", "value": "{{user}}"}\n      ]\n    }\n  ]\n}',
        },
        {
          type: "paragraph",
          text:
            "Result metrics include completed-step count, total latency, and the failed step identifier. Give every step a stable unique ID so failures remain intelligible after renaming display labels.",
        },
      ],
    },
    {
      id: "synthetic-browser",
      title: "Synthetic browser journeys",
      intro:
        "Browser monitors run 1–12 ordered actions in Chromium through Playwright.",
      blocks: [
        {
          type: "table",
          columns: ["Action", "Required variables", "Behavior"],
          rows: [
            ["`goto`", "`url`", "Navigate to a page"],
            ["`click`", "`selector`", "Click the matching element"],
            ["`fill`", "`selector`, `value`", "Replace an input value"],
            ["`wait_for`", "`selector`", "Wait until an element is available"],
            ["`assert_visible`", "`selector`", "Require an element to be visible"],
            [
              "`assert_text`",
              "`selector`, `value`",
              "Require the selected element to contain expected text",
            ],
            ["`assert_url`", "`value`", "Require the current URL to match"],
          ],
        },
        {
          type: "paragraph",
          text:
            "Journey variables include `start_url`, optional device profile, `failure_mode`, static `variables`, artifact settings, and steps. Each step has a unique ID and a 1–180 second timeout. The configuration model exposes screenshot, Playwright trace, and HAR-on-failure switches, but the current worker implements screenshots only; trace and HAR requests produce warnings rather than artifacts.",
        },
        {
          type: "paragraph",
          text:
            "Failure screenshots are exposed by the authenticated [monitor artifact endpoint](/docs/api/#monitor-endpoints) when the result contains a valid stored artifact path. Artifact availability depends on shared worker/API storage and retention. Do not build automation that expects trace or HAR files until worker support is implemented.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Selectors are part of your monitoring contract",
          text:
            "Prefer stable application-owned attributes over visual CSS structure. A browser journey can fail because the user interface changed even when the underlying API is healthy; pair it with narrower protocol monitors for diagnosis.",
        },
      ],
    },
    {
      id: "groups",
      title: "Monitor groups",
      blocks: [
        {
          type: "paragraph",
          text:
            "A `group` monitor stores member monitor IDs instead of executing a check. It is down when any enabled, non-deleted member is down. It is up when all effective member states are up, suspect, or degraded; otherwise it is unknown.",
        },
        {
          type: "definitions",
          items: [
            {
              term: "`per_monitor` rollup",
              description:
                "The default for newly created groups. Member monitors alert independently and the group does not create an additional availability alert.",
            },
            {
              term: "`group` rollup",
              description:
                "Suppresses member availability alerts covered by that group and creates one group-level availability alert. Suppression ignores the group's own enabled flag, so pausing such a group silences its members too — see [group alert rollup](/docs/alerting/#group-rollup).",
            },
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "Groups are not locations or dependencies",
          text:
            "A group presents and rolls up a set of peers. A location controls where a check runs. A dependency expresses an upstream causal relationship used for [root-cause annotation](/docs/dependencies/#root-cause).",
        },
      ],
    },
    {
      id: "secrets",
      title: "Secret fields and masked updates",
      blocks: [
        {
          type: "paragraph",
          text:
            "When `PROBARA_SECRETS_KEY` is configured, verified monitor secret handling covers `password`, `connection_string`, and `tls_client_key_pem` for Redis, PostgreSQL, MySQL, MongoDB, and RabbitMQ, the SIP digest `password`, plus every WebSocket header value. API reads replace protected values with `***`.",
        },
        {
          type: "list",
          items: [
            "Submit `***` again to preserve the already stored value.",
            "Submit a new value to replace and encrypt it.",
            "Because a type config is replaced as a whole, omitting or clearing a protected field removes that value rather than implicitly retaining it.",
            "WebSocket header names remain visible while their values are masked.",
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Masking is field-specific",
          text:
            "Do not assume every arbitrary config string is automatically secret. Use the product's designated protected fields and keep credentials out of names, tags, URLs, assertion messages, and other visible configuration.",
        },
      ],
    },
    {
      id: "operations",
      title: "Test, run, inspect, and delete",
      blocks: [
        {
          type: "definitions",
          items: [
            {
              term: "Test configuration",
              description:
                "Executes a provided configuration without creating or updating a monitor. It is compute-only and does not write result history or trigger alert lifecycle.",
            },
            {
              term: "Run now",
              description:
                "Queues an existing monitor for normal execution. The returned result is persisted and can change state and alerts.",
            },
            {
              term: "Results",
              description:
                "Raw chronological check records. List calls accept a limit and optional RFC 3339 `since` timestamp.",
            },
            {
              term: "Analytics",
              description:
                "Summaries, time series, and downtime periods for `1h`, `6h`, `24h`, `7d`, `30d`, `90d`, or `365d`.",
            },
            {
              term: "Delete history",
              description:
                "Removes stored results for a monitor without deleting the monitor definition.",
            },
            {
              term: "Delete monitor",
              description:
                "Soft-deletes the monitor immediately. Scheduler cleanup later purges dependent historical data.",
            },
          ],
        },
        {
          type: "paragraph",
          text:
            "Analytics report uptime, SLA/availability summary fields, downtime duration, average/median/p95/latest latency, series data, downtime periods, source, coverage start, and whether the requested window is partially covered. Long ranges combine rollups with the current raw tail. The summary carries `has_data`: when no checks ran in the window the percentage fields are meaningless zeros and clients must render a no-data state, never 0% or 100%. It also carries `method`: `interval` means `availability_pct` is a time integration over the monitor's state timeline — unknown and paused time leave the denominator and surface as `coverage_pct`, and downtime inside maintenance windows counts as planned rather than unavailability; `sampled` means the window predates the timeline and the legacy success/total rate stands in.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Deleting history is irreversible",
          text:
            "History deletion removes the evidence used for charts and long-term analysis. Export or retain external telemetry first if you need it for compliance or post-incident review.",
        },
      ],
    },
    {
      id: "bulk-and-portability",
      title: "Bulk changes, import, and export",
      blocks: [
        {
          type: "list",
          items: [
            "Bulk alerting changes can update notification mode and channel assignments across selected monitors.",
            "Bulk delete soft-deletes selected monitors and schedules cleanup.",
            "Import preview parses and validates without creating monitors. Execution reports a per-row outcome rather than treating every file as all-or-nothing.",
            "JSON, YAML, and CSV formats are accepted, including common wrapper keys and a versioned portable YAML bundle.",
            "Duplicate matching is case-insensitive on the combination of monitor name and type; duplicates are skipped.",
            "Groups are resolved in a second pass by member name, and ambiguous or missing names are reported.",
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Secrets do not round-trip",
          text:
            "Raw portable import accepts every monitor type in the live registry, including Redis, PostgreSQL, MySQL, MongoDB, RabbitMQ, and WebSocket. Exported masked secrets are not usable credentials, so re-enter passwords, connection strings, and client keys after import; verify every preview before execution.",
        },
      ],
    },
  ],
};

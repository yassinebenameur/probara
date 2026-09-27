import type { DocPage } from "../types";

export const AGENTS_PAGE: DocPage = {
  slug: "agents",
  group: "Use Probara",
  title: "Agents and push monitors",
  description:
    "Collect host telemetry with the OpenTelemetry Collector or accept simple token-based passive heartbeats.",
  eyebrow: "Passive monitoring",
  readingTime: "24 min read",
  keywords: [
    "opentelemetry collector",
    "otlp",
    "push monitor",
    "passive monitoring",
    "host metrics",
    "metric rules",
  ],
  sections: [
    {
      id: "choose-passive-type",
      title: "Choose an agent or push monitor",
      blocks: [
        {
          type: "table",
          columns: ["Capability", "Agent monitor", "Push monitor", "Private location"],
          rows: [
            [
              "Execution model",
              "OpenTelemetry Collector on the host pushes OTLP metrics",
              "Caller sends heartbeat",
              "Worker pulls queued active jobs",
            ],
            [
              "Host metrics",
              "OS metrics via `hostmetrics`; any OTel metric a collector sends is stored",
              "Arbitrary submitted key/value metrics",
              "Worker process metrics only",
            ],
            [
              "Freshness state",
              "Scheduler watchdog on the expected report interval",
              "Expected heartbeat interval plus grace",
              "Location heartbeat connection",
            ],
            ["Runs HTTP/DNS/etc. checks", "No", "No", "Yes"],
            ["Credential", "Tenant API key plus agent ID", "Monitor-specific push token", "Location credential"],
          ],
        },
        {
          type: "paragraph",
          text:
            "Agent monitors are served by `probara-collector`, a minimal OpenTelemetry Collector distribution that scrapes host metrics and pushes them to Probara's OTLP ingest endpoint. Because the wire protocol is standard OTLP/HTTP, a stock upstream collector build works too — see [migrating and extending](/docs/agents/#migrating-legacy).",
        },
        {
          type: "callout",
          tone: "info",
          title: "An agent is not a private worker",
          text:
            "Install the collector when you want telemetry about that host. Deploy a [private location worker](/docs/locations/) when you want Probara to execute active monitor checks from that network.",
        },
      ],
    },
    {
      id: "agent-config",
      title: "Agent monitor configuration",
      blocks: [
        {
          type: "table",
          columns: ["Variable", "Meaning"],
          rows: [
            [
              "`agent_id`",
              "Stable generated identity matched against inbound reports",
            ],
            [
              "`expected_interval_seconds`",
              "Expected reporting interval, 10–86,400 seconds",
            ],
            [
              "`metric_rules`",
              "Optional array of up to 50 threshold rules; each breached rule opens `host_metric` alerts independently",
            ],
            [
              "`metric_rules[].metric_name`",
              "Any metric name the collector reports, for example `system.cpu.utilization`",
            ],
            [
              "`metric_rules[].attribute_filters`",
              "Optional attribute equality filters (for example `mountpoint`). A rule without filters fans out per matching series",
            ],
            [
              "`metric_rules[].operator`",
              "`>=` or `<=`",
            ],
            [
              "`metric_rules[].threshold`",
              "Threshold in the metric's native unit — `*.utilization` metrics are ratios, so 90% is `0.9`, not `90`",
            ],
            [
              "`metric_rules[].for_duration_seconds`",
              "Optional sustained-for window: the alert opens only if every evaluated point breached for the whole window (Prometheus-style)",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "The monitor receives availability success whenever an OTLP export is accepted. Metric rules are evaluated separately and open [host-metric alerts](/docs/alerting/#host-and-mesh) rather than turning the availability result into failure.",
        },
        {
          type: "paragraph",
          text:
            "Rule metric names must match what the host's scrapers actually emit. The generated collector config enables a per-OS scraper set:",
        },
        {
          type: "table",
          columns: ["Platform", "Enabled `hostmetrics` scrapers", "Consequence"],
          rows: [
            [
              "Linux",
              "cpu, memory, paging, filesystem, network, disk, load, processes",
              "All documented metrics, including `system.processes.count`",
            ],
            [
              "macOS",
              "cpu, memory, paging, filesystem, network, disk, load",
              "No `processes` scraper, so no process count",
            ],
            [
              "Windows",
              "cpu, memory, paging, filesystem, network, disk",
              "Neither `load` nor `processes`; no load averages or process count",
            ],
          ],
        },
      ],
    },
    {
      id: "install",
      title: "Install the collector",
      intro:
        "The authenticated agent endpoints generate per-OS install and uninstall instructions containing the monitor's agent ID, API origin, the rendered collector configuration, and a caller-provided API key.",
      blocks: [
        {
          type: "list",
          ordered: true,
          items: [
            "Create an agent monitor and choose its expected interval and optional metric rules.",
            "Create a [tenant API key](/docs/administration/#api-keys) with `write` scope. Read scope cannot submit metrics.",
            "Open the monitor's agent install instructions and select the target platform.",
            "Run the generated script with the privileges required by that platform.",
            "Confirm the first report appears immediately, then verify periodic reports and current state.",
          ],
        },
        {
          type: "table",
          columns: ["Platform", "Service mechanism", "Files and secrets"],
          rows: [
            [
              "Linux",
              "systemd system service `probara-collector.service` (root)",
              "Config at `/etc/probara-collector/config.yaml` (0644, references `${env:…}` only); `EnvironmentFile=/etc/probara-collector/collector.env` (0600) holds `PROBARA_API_KEY` and `PROBARA_AGENT_ID`",
            ],
            [
              "macOS",
              "per-user launchd agent `com.probara.collector`",
              "A runner script sources the environment file before starting the collector; install for the user account that should own the process",
            ],
            [
              "Windows",
              "native service `ProbaraCollector` registered via `sc.exe`",
              "Run from elevated PowerShell; secrets live in the service's registry `Environment` value, not in the config file",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Install scripts download the platform binary from `GET /static/collector/probara-collector-{linux,darwin}-{amd64,arm64}` (or `-windows-amd64.exe`), verify its SHA-256 against the published `checksums.txt`, and first remove any legacy `probara-agent` installation (systemd/launchd/NSSM service, binary, and config) before installing. Re-running the installer on a host is therefore also the [migration path](/docs/agents/#migrating-legacy). Uninstall scripts clean up both generations.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Generated installers contain a credential",
          text:
            "The script/config embeds the tenant API key and agent identity. Deliver it over a trusted channel, avoid shell history and ticket attachments, and preserve the installer's restrictive file permissions. Revoke the API key if the artifact is exposed.",
        },
        {
          type: "paragraph",
          text:
            "`GET /api/v1/monitors/{id}/agent/install` returns the same material as JSON, including the rendered `collector_config` YAML and the `collector_version`. Configuration-management users can fetch the raw collector YAML directly from `GET /api/v1/monitors/{id}/agent/config.yaml?platform=linux|darwin|windows` and distribute it themselves.",
        },
        {
          type: "paragraph",
          text:
            "Generated URLs depend on `PUBLIC_BASE_URL`, which must be an absolute `http://` or `https://` origin without user information, query, or fragment. The corresponding platform binary must exist in the API's static collector directory. See the [public URL configuration](/docs/configuration/#public-urls-and-private-location-auth) for how this value is wired.",
        },
      ],
    },
    {
      id: "manual-run",
      title: "Run the collector manually",
      blocks: [
        {
          type: "code",
          language: "bash",
          title: "Run against the generated configuration",
          code:
            "export PROBARA_API_KEY=<write-scope-api-key>\nexport PROBARA_AGENT_ID=<agent-id>\n\nprobara-collector --config /etc/probara-collector/config.yaml",
        },
        {
          type: "table",
          columns: ["Environment variable", "Purpose"],
          rows: [
            ["`PROBARA_API_KEY`", "Tenant write-scope API key sent as the `Authorization: Bearer` header"],
            ["`PROBARA_AGENT_ID`", "Agent monitor identity sent as the `X-Probara-Agent-Id` header"],
          ],
        },
        {
          type: "paragraph",
          text:
            "The generated configuration file contains no secrets: it references credentials exclusively through `${env:…}` expansion, so the same file can be committed to configuration management while the environment file (or the Windows service registry value) carries the API key.",
        },
        {
          type: "callout",
          tone: "info",
          title: "It is a standard OpenTelemetry Collector",
          text:
            "`probara-collector` accepts the usual collector flags, and the generated config runs unchanged on a stock `otelcol-contrib` build if you prefer to distribute upstream binaries — see [migrating and extending](/docs/agents/#migrating-legacy).",
        },
      ],
    },
    {
      id: "reporting",
      title: "Reporting behavior",
      blocks: [
        {
          type: "paragraph",
          text:
            "The collector scrapes host metrics on its configured interval and pushes them as OTLP/HTTP to `POST /api/v1/otlp/v1/metrics` (protobuf or JSON, gzip supported), authenticated with `Authorization: Bearer <tenant API key>`. Monitor identity comes from the `X-Probara-Agent-Id` header, or from a `probara.agent.id` resource attribute when a gateway collector multiplexes metrics for several hosts through one connection. Responses follow the OTLP specification, including `partial_success` when only some points were rejected.",
        },
        {
          type: "paragraph",
          text:
            "Each accepted export doubles as one availability heartbeat: a success check result stamped with the server's receipt time, so collector clock skew can never affect state or history.",
        },
        {
          type: "table",
          columns: ["Metric", "Notes"],
          rows: [
            [
              "`system.cpu.utilization{state=used}`",
              "A `metricstransform` processor aggregates per-CPU non-idle states into one used ratio",
            ],
            ["`system.memory.utilization` / `system.memory.usage`", "Memory ratio and bytes"],
            ["`system.paging.*`", "Swap utilization and usage"],
            [
              "`system.filesystem.utilization` / `system.filesystem.usage`",
              "Per-mountpoint ratio and bytes; every mountpoint is a separate series. Pseudo-filesystems (devfs, tmpfs, squashfs, overlay, …) are excluded at collection — they read 100% forever and carry no real capacity. On macOS the APFS boot/firmware system volumes (Preboot, Hardware, Update, VM, …) are excluded too; `/System/Volumes/Data` alone represents the disk, since APFS volumes share the container's free space",
            ],
            [
              "`system.network.io` / `system.disk.io`",
              "Cumulative byte counters; the platform derives rates from them. Per-interface `system.network.packets`/`errors`/`dropped` are disabled by default — they are the biggest series-cardinality driver; re-enable them in the config if you need them",
            ],
            ["`system.cpu.load_average.{1m,5m,15m}`", "Not available on Windows"],
            ["`system.uptime`", "Host uptime"],
            ["`system.cpu.logical.count`", "Logical core count"],
            ["`system.processes.count`", "Linux only"],
          ],
        },
        {
          type: "paragraph",
          text:
            "Metrics land in a generic metric store: a per-series registry, raw samples in daily partitions kept for `METRIC_RAW_RETENTION_DAYS` (default 30; a tenant's `data_retention_days` can tighten it further), and hourly rollups (min/max/avg/sum/first/last plus reset-aware increase for counters) pruned after 400 days. Any OTel metric a collector sends is stored, chartable in the UI's metric explorer, and usable in metric rules — not only the host metrics above. `GET /api/v1/monitors/{id}/metrics/series` discovers stored series and `POST /api/v1/monitors/{id}/metrics/query` runs [batch range queries](/docs/api/#passive-api).",
        },
        {
          type: "table",
          columns: ["Limit", "Behavior"],
          rows: [
            [
              "Gauges and sums only",
              "Histograms and summaries are rejected in v1",
            ],
            [
              "Request body 4 MiB (20 MiB decompressed), ≤10,000 data points",
              "Oversized requests are refused",
            ],
            [
              "Series cardinality per monitor: 2,000 (`OTLP_MAX_SERIES_PER_MONITOR`)",
              "Overflow points are rejected via `partial_success`; accepted points still count",
            ],
            [
              "Rate limit: 60 requests/min per monitor (`OTLP_MONITOR_RATE_PER_MIN`)",
              "`429` with `Retry-After`",
            ],
            [
              "Unknown agent ID / disabled monitor / transient failure",
              "`404` / `403` / `503`",
            ],
          ],
        },
      ],
    },
    {
      id: "freshness",
      title: "Agent freshness and recovery",
      blocks: [
        {
          type: "paragraph",
          text:
            "A scheduler-side watchdog marks an agent monitor failed after no accepted report for three times the monitor interval, with a 90-second floor. The watchdog re-fires each window while the silence persists, so consecutive missed windows accumulate toward `down` exactly like consecutive failed checks. A later valid export creates a success result and recovers availability.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Align both intervals",
          text:
            "Configure the collector to export at or faster than the monitor's expected interval. Network retries consume time, so leave enough margin before the interval×3 threshold for transient failures.",
        },
        {
          type: "paragraph",
          text:
            "Stale transitions are recorded with `result_source = monitor`, so they affect current state and availability alerting and are included in standard [uptime analytics](/docs/monitors/#operations). Only expired-job results use the `platform` source that analytics exclude.",
        },
      ],
    },
    {
      id: "thresholds",
      title: "Metric rules and host-metric alerts",
      blocks: [
        {
          type: "paragraph",
          text:
            "When a [metric rule](/docs/agents/#agent-config) is breached, Probara opens a separate [host-metric alert](/docs/alerting/#host-and-mesh). Alerts are keyed by the canonical series key, for example `system.filesystem.utilization{device=/dev/sda1,mode=rw,mountpoint=/data,type=ext4}`, so each series maintains its own independent lifecycle.",
        },
        {
          type: "list",
          items: [
            "A rule without attribute filters fans out per matching series: one filesystem rule opens one alert per breaching mountpoint, removing the old single-disk-path limitation.",
            "`for_duration_seconds` requires the breach to hold for the whole window before the alert opens.",
            "Evaluation is freshness-bounded (interval×3, 90-second floor): a dead collector's stale readings can no longer keep metric alerts open — availability alerting covers the outage instead.",
            "A metric-rule breach does not mark agent availability down; a metric alert resolves when the series returns within the rule, and removing a rule resolves its conditions.",
            "[Notification routing](/docs/alerting/#routing) follows the monitor's default or custom channel assignments.",
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "Legacy thresholds were migrated automatically",
          text:
            "The former `metric_thresholds` object (cpu/memory/disk/swap percent) is retired. An automatic migration converted CPU, memory, and swap thresholds into `*.utilization{state=used}` rules and the disk threshold into a `system.filesystem.utilization{mode=rw}` rule, so previously single-path disk alerting is now per-writable-mountpoint (the `mode=rw` filter keeps permanently-full read-only mounts like squashfs images from paging). Open alerts were carried over.",
        },
      ],
    },
    {
      id: "remote-disable",
      title: "Uninstall (remote disable removed)",
      blocks: [
        {
          type: "paragraph",
          text:
            "The legacy agent's opt-in remote-disable mechanism — a `410 Gone` response triggering a local self-uninstall — is removed. The collector never executes removal actions on behalf of the backend. Deleting the monitor stops the backend from accepting its reports but leaves the collector service installed and exporting into rejections.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Uninstall is operator-run",
          text:
            "Remove the collector with the generated platform uninstall script or your configuration-management system. The uninstall scripts remove the service, binary, configuration, and environment file — and also clean up a legacy `probara-agent` installation if one is still present.",
        },
        {
          type: "paragraph",
          text:
            "Legacy agents that were explicitly started with `--allow-remote-disable` still honor the old `410` self-uninstall during the [deprecation window](/docs/agents/#migrating-legacy) when their monitor is deleted; all others simply log and retry until manually uninstalled.",
        },
      ],
    },
    {
      id: "migrating-legacy",
      title: "Migrating from the legacy agent",
      blocks: [
        {
          type: "paragraph",
          text:
            "The custom `probara-agent` binary is replaced by the OpenTelemetry Collector. `POST /api/v1/agent/metrics` still accepts legacy agents through a deprecation window ending **Wednesday, 18 November 2026**; every response carries `Deprecation`, `Sunset`, and `Link` headers, and each legacy report is logged server-side so operators can find stragglers. After the window the endpoint becomes a `410` tombstone. Old agent binaries are no longer distributed.",
        },
        {
          type: "list",
          ordered: true,
          items: [
            "Re-run the install one-liner on each host: the installer removes the legacy agent (service, binary, config) and installs the collector in one pass.",
            "Verify the monitor keeps reporting; the same monitor, agent ID, and API key carry over unchanged.",
            "Check server logs (or the sunset headers in your agent's responses) to enumerate hosts still on the legacy path.",
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Old metric history does not carry over",
          text:
            "Pre-migration metric history stored by the legacy agent is not backfilled into the new metric store: charts start from the first collector export. Availability history is unaffected.",
        },
        {
          type: "paragraph",
          text:
            "Air-gapped or mirror-constrained environments do not need the `probara-collector` binary at all: the generated configuration is a standard OpenTelemetry Collector config, and a stock `otelcol-contrib` build runs it unchanged. Fetch the YAML from `GET /api/v1/monitors/{id}/agent/config.yaml` and distribute upstream binaries from your own mirror.",
        },
        {
          type: "paragraph",
          text:
            "The configuration is also an extension point: add more `hostmetrics` scrapers, or additional receivers entirely (with `otelcol-contrib`), pushing to the same OTLP endpoint. Any metric that arrives is stored, chartable, and alertable through metric rules — subject to the [per-monitor series and rate limits](/docs/agents/#reporting).",
        },
      ],
    },
    {
      id: "push-config",
      title: "Push monitor configuration",
      blocks: [
        {
          type: "table",
          columns: ["Variable", "Meaning"],
          rows: [
            [
              "`push_token`",
              "Generated bearer-style credential embedded in the public heartbeat URL",
            ],
            [
              "`expected_interval_seconds`",
              "Expected time between pushes, 10–86,400 seconds",
            ],
            [
              "`grace_period_seconds`",
              "Additional non-negative delay before the heartbeat is considered stale",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Use a push monitor for cron jobs, backup jobs, queue consumers, or external systems that can call an HTTP URL but do not need host metrics. The endpoint accepts both `GET` and `POST` without an admin session because the token itself authorizes the report.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "The push URL is a secret",
          text:
            "Anyone with the token can submit state and metrics for that monitor. Keep it out of logs, public CI output, browser analytics, and committed scripts.",
        },
      ],
    },
    {
      id: "push-examples",
      title: "Send push heartbeats",
      blocks: [
        {
          type: "code",
          language: "bash",
          title: "Simple success",
          code: "curl -fsS 'https://monitoring.example.com/api/v1/push/<token>'",
        },
        {
          type: "code",
          language: "bash",
          title: "Structured failure and metrics",
          code:
            "curl -fsS -X POST \\\n  -H 'Content-Type: application/json' \\\n  -d '{\"status\":\"down\",\"error\":\"backup upload failed\",\"files\":182,\"bytes\":987654321}' \\\n  'https://monitoring.example.com/api/v1/push/<token>'",
        },
        {
          type: "paragraph",
          text:
            "Reserved JSON keys are `status` and `error`; remaining keys are stored as metrics. Query-string values on GET are converted to integers, floating-point values, booleans, or strings when possible.",
        },
        {
          type: "table",
          columns: ["Submitted `status`", "Stored outcome"],
          rows: [
            ["`up`", "Success"],
            ["`down`", "Failure"],
            ["`error`", "Error"],
            [
              "Omitted or any other value",
              "Success; validate client spelling instead of expecting rejection",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "A disabled push monitor returns HTTP 403 and a missing/deleted token returns 404. A successful push updates freshness and can recover an existing stale/down availability condition.",
        },
      ],
    },
    {
      id: "push-freshness",
      title: "Push freshness",
      blocks: [
        {
          type: "paragraph",
          text:
            "A push monitor becomes stale after `expected_interval_seconds + grace_period_seconds` without an accepted report. While it remains stale, the platform limits repeated generated failures to avoid unbounded duplicate history. A new accepted heartbeat restores up state.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Set grace from real job behavior",
          text:
            "Include scheduler jitter, job duration, network retries, and normal variance. A daily job that often finishes 20 minutes late needs an explicit grace period; a very large grace period delays genuine missed-job alerts.",
        },
      ],
    },
  ],
};

import type { DocPage } from "../types";

export const AGENTS_PAGE: DocPage = {
  slug: "agents",
  group: "Use Probara",
  title: "Agents and push monitors",
  description:
    "Collect host telemetry with an installed agent or accept simple token-based passive heartbeats.",
  eyebrow: "Passive monitoring",
  readingTime: "24 min read",
  keywords: [
    "host agent",
    "push monitor",
    "passive monitoring",
    "host metrics",
    "remote disable",
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
            ["Execution model", "Host reports telemetry", "Caller sends heartbeat", "Worker pulls queued active jobs"],
            ["Host metrics", "Rich OS metrics", "Arbitrary submitted key/value metrics", "Worker process metrics only"],
            ["Freshness state", "Expected report interval", "Expected heartbeat interval plus grace", "Location heartbeat connection"],
            ["Runs HTTP/DNS/etc. checks", "No", "No", "Yes"],
            ["Credential", "Tenant API key plus agent ID", "Monitor-specific push token", "Location credential"],
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "An agent is not a private worker",
          text:
            "Install the host agent when you want telemetry about that host. Deploy a [private location worker](/docs/locations/) when you want Probara to execute active monitor checks from that network.",
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
              "`metric_thresholds`",
              "Optional nested object of per-metric alert thresholds; omit a key to disable that metric's alert",
            ],
            [
              "`metric_thresholds.cpu_percent`",
              "Optional positive percentage that opens a CPU host-metric alert",
            ],
            [
              "`metric_thresholds.memory_percent`",
              "Optional positive percentage that opens a memory alert",
            ],
            [
              "`metric_thresholds.disk_percent`",
              "Optional positive percentage evaluated for reported disk use",
            ],
            [
              "`metric_thresholds.swap_percent`",
              "Optional positive percentage that opens a swap alert",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "The monitor receives availability success whenever a valid report is accepted. Resource thresholds are evaluated separately and produce one `host_metric` alert per breached metric rather than turning the availability result into failure.",
        },
      ],
    },
    {
      id: "install",
      title: "Install the host agent",
      intro:
        "The authenticated agent endpoints generate install and uninstall instructions containing the monitor's agent ID, API origin, report interval, and a caller-provided API key.",
      blocks: [
        {
          type: "list",
          ordered: true,
          items: [
            "Create an agent monitor and choose its expected interval and optional thresholds.",
            "Create a [tenant API key](/docs/administration/#api-keys) with `write` scope. Read scope cannot submit metrics.",
            "Open the monitor's agent install instructions and select the target platform.",
            "Run the generated script with the privileges required by that platform.",
            "Confirm the first report appears immediately, then verify periodic reports and current state.",
          ],
        },
        {
          type: "table",
          columns: ["Platform", "Service mechanism", "Operational note"],
          rows: [
            [
              "Linux",
              "systemd service",
              "Installer requires root for system installation",
            ],
            [
              "macOS",
              "per-user launchd agent",
              "Install for the user account that should own the process",
            ],
            [
              "Windows",
              "NSSM-managed service",
              "Run from an elevated PowerShell session",
            ],
          ],
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
            "Generated URLs depend on `PUBLIC_BASE_URL`, which must be an absolute `http://` or `https://` origin without user information, query, or fragment. The corresponding platform binary must exist in the API static-agent directory. See the [public URL configuration](/docs/configuration/#public-urls-and-private-location-auth) for how this value is wired.",
        },
      ],
    },
    {
      id: "manual-run",
      title: "Run the agent manually",
      blocks: [
        {
          type: "code",
          language: "bash",
          title: "Current command-line interface",
          code:
            "probara-agent \\\n  --backend-url https://monitoring.example.com \\\n  --agent-id <agent-id> \\\n  --api-key <write-scope-api-key> \\\n  --interval 60 \\\n  --disk-path /",
        },
        {
          type: "table",
          columns: ["Flag", "Purpose"],
          rows: [
            ["`--backend-url`", "Required API origin"],
            ["`--agent-id`", "Required agent monitor identity"],
            ["`--api-key`", "Required tenant write-scope API key"],
            ["`--interval`", "Report cadence; defaults to 60 seconds"],
            [
              "`--disk-path`",
              "Primary path to inspect for filesystem use; defaults to `/`",
            ],
            [
              "`--allow-remote-disable`",
              "Permit an explicit backend removal response to run the configured local uninstall action",
            ],
            [
              "`--remote-disable-command`",
              "Local command used only when remote disable is explicitly allowed",
            ],
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Use the current flags, not legacy environment examples",
          text:
            "The current agent entrypoint defines these command-line flags. Do not rely on older documentation that describes unimplemented environment variables unless your service wrapper translates them into flags.",
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
            "The agent reports immediately at startup and then on its configured interval. Each HTTP request has a 10-second timeout and is attempted up to three times with short exponential backoff. A successful response resets delivery failures and continues the normal loop.",
        },
        {
          type: "table",
          columns: ["Metric", "Reported values"],
          rows: [
            ["CPU", "Utilization percentage and logical core count"],
            ["Memory", "Used and total bytes"],
            ["Swap", "Used and total bytes"],
            [
              "Filesystems",
              "Configured/visible mount path, used bytes, total bytes, and filesystem type",
            ],
            [
              "Disk I/O",
              "Cumulative read and write byte counters",
            ],
            [
              "Network",
              "Cumulative received and transmitted byte counters",
            ],
            ["Load", "1-, 5-, and 15-minute load averages when available"],
            ["Processes", "Process count"],
            ["Uptime", "Host uptime"],
            ["Timestamp", "Agent observation timestamp"],
          ],
        },
        {
          type: "paragraph",
          text:
            "Some operating systems cannot supply every metric. CPU may be reported as `-1` when unavailable, and consumers should treat unavailable values as missing rather than as a real negative utilization.",
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
            "An API-side stale worker runs frequently and marks an agent monitor failed after no accepted report for approximately twice the monitor interval. It avoids writing the same stale transition continuously; a later valid report creates a success result and recovers availability.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Align both intervals",
          text:
            "Configure the agent process to report at or faster than the monitor's expected interval. Network retries consume time, so leave enough margin before the stale threshold for transient failures.",
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
      title: "Host metric alerts",
      blocks: [
        {
          type: "paragraph",
          text:
            "When a percentage threshold is present and the corresponding reported utilization exceeds it, Probara opens a separate [host-metric alert](/docs/alerting/#host-and-mesh). CPU, memory, disk, and swap alerts can be open independently.",
        },
        {
          type: "list",
          items: [
            "A resource breach does not mark agent availability down.",
            "A metric alert resolves when the utilization falls below the threshold.",
            "Removing a configured threshold resolves the corresponding condition.",
            "[Notification routing](/docs/alerting/#routing) follows the monitor's default or custom channel assignments.",
          ],
        },
      ],
    },
    {
      id: "remote-disable",
      title: "Remote disable and uninstall",
      blocks: [
        {
          type: "paragraph",
          text:
            "When an agent posts for a monitor that the backend reports as removed, the backend can return HTTP `410 Gone`. The agent interprets that response as a remote-disable signal only when it was started with explicit remote-disable permission and a local removal action.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Deletion does not always uninstall software",
          text:
            "Without `--allow-remote-disable`, deleting the monitor stops backend acceptance but leaves the local agent/service installed. Uninstall through the generated platform script or your configuration-management system.",
        },
        {
          type: "paragraph",
          text:
            "Enable remote removal only if the API trust boundary is allowed to execute the predefined local uninstall command. The server does not send arbitrary shell text for the agent to execute.",
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
            "Use a push monitor for cron jobs, backup jobs, queue consumers, or external systems that can call an HTTP URL but do not need the full host agent. The endpoint accepts both `GET` and `POST` without an admin session because the token itself authorizes the report.",
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

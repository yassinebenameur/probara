import type { DocPage } from "../types";

export const ALERTING_PAGE: DocPage = {
  slug: "alerting",
  group: "Use Probara",
  title: "Alerting and incidents",
  description:
    "Route notifications, understand alert lifecycles, schedule maintenance, and coordinate incidents.",
  eyebrow: "Response",
  readingTime: "30 min read",
  keywords: [
    "alerts",
    "notification channels",
    "maintenance",
    "incidents",
    "latency anomaly",
  ],
  sections: [
    {
      id: "model",
      title: "Alert model",
      intro:
        "The alerter evaluates durable database state and creates one lifecycle record for each active condition. Alert policies are retired; routing now lives in [tenant notification settings](/docs/administration/#tenant-settings) and each monitor.",
      blocks: [
        {
          type: "table",
          columns: ["Alert kind", "Signal", "Resolution"],
          rows: [
            [
              "`availability`",
              "A monitor reaches effective `down` state",
              "Monitor becomes `up`, is disabled/deleted, or condition is manually resolved",
            ],
            [
              "`latency_anomaly`",
              "Recent successful latency breaches a learned baseline",
              "Recent latency returns below the detector threshold",
            ],
            [
              "`host_metric`",
              "Agent CPU, memory, disk, or swap exceeds its configured threshold",
              "The metric returns below threshold or its threshold is removed",
            ],
            [
              "`mesh_edge`",
              "A directional location-mesh edge reaches down",
              "That direction recovers",
            ],
            [
              "`tls_expiry`",
              "An HTTP monitor's certificate has fewer remaining validity days than `tls_min_days_valid`",
              "A renewed certificate is observed, or the threshold is removed",
            ],
          ],
        },
        {
          type: "table",
          columns: ["Status", "Meaning"],
          rows: [
            ["`active`", "Condition is open and unacknowledged"],
            [
              "`acknowledged`",
              "An operator has acknowledged the alert; the underlying condition remains open",
            ],
            ["`resolved`", "Alert lifecycle is closed"],
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Acknowledgement does not silence reminders",
          text:
            "Acknowledging records operator ownership but leaves the condition open. Reminder notifications continue at the configured interval until resolution or until reminders are disabled.",
        },
      ],
    },
    {
      id: "availability",
      title: "Availability lifecycle",
      blocks: [
        {
          type: "list",
          ordered: true,
          items: [
            "A check failure first passes through the monitor's consecutive-failure confirmation and, when locations are selected, its [location failure quorum](/docs/locations/#assignment).",
            "The alerter opens an availability alert only when effective state is `down`, the monitor is enabled, and maintenance/rollup rules do not mute it.",
            "The initial notification is sent through eligible routes after each route's configured delay.",
            "While the condition remains down, reminders are sent at the tenant reminder interval.",
            "When the monitor is fully `up`, the alert resolves and resolution notifications go only to channels that previously received that alert.",
          ],
        },
        {
          type: "paragraph",
          text:
            "`suspect`, `unknown`, and a newly `degraded` monitor do not open an availability alert. If a monitor moves from down to degraded after an alert opened, the alert remains open until the aggregate is fully up.",
        },
        {
          type: "paragraph",
          text:
            "Location-aware availability alerts include failing-location context so operators can distinguish a regional failure from a broad outage.",
        },
      ],
    },
    {
      id: "routing",
      title: "Default and custom notification routing",
      blocks: [
        {
          type: "definitions",
          items: [
            {
              term: "Tenant default routes",
              description:
                "An ordered list of active alert channels, each with `channel_id`, display metadata, and a non-negative `delay_seconds`.",
            },
            {
              term: "Monitor `default` mode",
              description:
                "The monitor inherits the tenant's current default routes.",
            },
            {
              term: "Monitor `custom` mode",
              description:
                "Only the monitor's assigned active channels are considered, with the delay stored on each assignment.",
            },
          ],
        },
        {
          type: "paragraph",
          text:
            "Delays implement escalation: a zero-delay route fires immediately, while a later route fires only if the alert is still open when its delay elapses. Resolution notifications are limited to routes that actually fired, avoiding a recovery message on a channel that never saw the outage.",
        },
        {
          type: "table",
          columns: ["Tenant notification setting", "Default / constraints"],
          rows: [
            [
              "`default_channels`",
              "Ordered channel assignments with non-negative delays",
            ],
            [
              "`alert_reminder_seconds`",
              "Defaults to 3,600; set 0 to disable reminders",
            ],
            [
              "`auto_create_incident`",
              "Defaults to false; verified for availability and latency-anomaly opening paths",
            ],
            [
              "`latency_anomaly_enabled`",
              "Defaults to false and also depends on the platform-level detector switch",
            ],
            ["`latency_baseline_window_hours`", "Defaults to 168"],
            ["`latency_anomaly_sensitivity`", "Defaults to 3.5"],
            ["`latency_anomaly_min_breach_seconds`", "Defaults to 120"],
            ["`latency_anomaly_min_delta_pct`", "Defaults to 20 (percent)"],
          ],
        },
      ],
    },
    {
      id: "channels",
      title: "Notification channels",
      blocks: [
        {
          type: "table",
          columns: ["Plugin type", "Configuration", "Delivery"],
          rows: [
            [
              "`email`",
              "`to` list and optional subject / plain-text / HTML body templates",
              "Branded HTML plus plain-text email over the [platform SMTP configuration](/docs/configuration/#alerter-and-smtp)",
            ],
            [
              "`slack`",
              "Approved HTTPS `webhook_url` on Slack webhook hosts",
              "Slack incoming webhook",
            ],
            [
              "`discord`",
              "Approved Discord HTTPS webhook URL",
              "Discord webhook",
            ],
            [
              "`teams`",
              "Approved Microsoft webhook URL",
              "Teams/Workflow-compatible webhook",
            ],
            [
              "`generic_webhook`",
              "HTTPS `url`, optional `hmac_secret`, optional custom headers encoded as JSON",
              "Structured Probara event JSON",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Generic webhooks include `X-Probara-Event-Type` and `X-Probara-Idempotency-Key`. When an HMAC secret is configured they also include `X-Probara-Signature` in `sha256=...` form. Receivers should verify the signature against the raw body and deduplicate by the idempotency key.",
        },
        {
          type: "list",
          items: [
            "Create a channel from a registered plugin and save its plugin-specific configuration.",
            "Use the channel test action before assigning production monitors. Tests send a real notification and require write permission. Email tests are served by the API process, so they need the same [platform SMTP configuration](/docs/configuration/#alerter-and-smtp) as the alerter; without it the test reports `mailer not configured` even when alert email is being delivered.",
            "Activate or deactivate the channel. Inactive channels remain configured but are skipped for delivery.",
            "Assign it in tenant defaults or in a monitor's custom routing.",
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Webhook hosts are validated",
          text:
            "Built-in chat plugins accept only their approved HTTPS webhook hosts, and generic webhooks require HTTPS. Redirects and DNS/network policy are still security-sensitive; keep egress narrowly controlled.",
        },
      ],
    },
    {
      id: "email-rendering",
      title: "Email rendering and templates",
      blocks: [
        {
          type: "paragraph",
          text:
            "With no template overrides, an email channel sends a `multipart/alternative` message: a branded HTML part and a plain-text part rendered from the same data, so text-only clients and mail archives never see markup. The HTML part is self-contained — inline styles, no remote images, no `data:` URIs — and adapts to the reader's light or dark theme. Both parts lead with a one-sentence summary of what happened, then the measurement that tripped the alert (observed vs. baseline latency, metric vs. threshold, certificate days remaining), the verbatim probe error, the likely root cause when the dependency graph identifies one, the failing locations of a multi-location monitor, and a metadata block with the trigger time, elapsed duration, and failed-check count.",
        },
        {
          type: "paragraph",
          text:
            'Set `APP_BASE_URL` to the public origin of the operator UI and every alert email carries an "open the monitor" button (mesh-edge alerts link to the mesh matrix instead). Without it the button is omitted rather than pointing at a guessed host. `SMTP_FROM_NAME` sets the From display name and defaults to `Probara Alerts`.',
        },
        {
          type: "table",
          columns: ["Channel field", "Effect"],
          rows: [
            [
              "`subject_template`",
              "Go `text/template` replacing the default `[Alert Triggered] <monitor>` subject.",
            ],
            [
              "`body_template`",
              "Go `text/template` replacing the plain-text body. Set on its own it sends a text-only message: an operator who wrote a specific text alert should not also receive unrelated generated markup alongside it.",
            ],
            [
              "`body_html_template`",
              "Go `html/template` replacing the built-in HTML design. Alert values are HTML-escaped; your markup is not. Combine it with `body_template` to control both parts.",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Templates receive the raw alert fields (`monitor_name`, `status`, `kind`, `failure_count`, `last_error`, `triggered_at`, `resolved_at`, `tenant_id`, `metric_name`, `metric_value`, `threshold_value`, `baseline_latency_ms`, `observed_latency_ms`, `anomaly_score`, `root_cause_monitor_name`, `source_location_name`, `target_location_name`, `failing_locations`) plus the presentation values the built-in email uses, so an override can reuse the same wording: `label`, `status_label`, `summary`, `duration`, `triggered_at_human`, `resolved_at_human`, `action_url`, `action_label`, and `accent_color`. A template that fails to parse or execute falls back to the built-in rendering — the alert still goes out and the error is logged.",
        },
      ],
    },
    {
      id: "group-rollup",
      title: "Group alert rollup",
      blocks: [
        {
          type: "paragraph",
          text:
            "A group monitor chooses one of two availability alert strategies. `per_monitor` lets members alert normally and suppresses an additional group alert. `group` suppresses member availability alerts covered by the group and emits one group-level alert while any effective member is down.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Rollup only changes group/member availability alerts",
          text:
            "It is not a general dependency suppression mechanism and does not merge unrelated latency, host metric, or mesh alerts.",
        },
      ],
    },
    {
      id: "latency-anomalies",
      title: "Latency anomaly alerts",
      blocks: [
        {
          type: "paragraph",
          text:
            "The anomaly detector compares recent successful monitor-source latency with hourly successful-history baselines. It opens an alert only when latency exceeds the sensitivity threshold, minimum percent delta, and minimum breach duration configured for the tenant.",
        },
        {
          type: "list",
          items: [
            "The platform-wide latency detector must be enabled.",
            "The tenant's `latency_anomaly_enabled` setting must be true.",
            "The monitor must be enabled, must not be a group, and must not be in maintenance.",
            "Enough retained successful data must exist to form a baseline.",
            "The alert resolves automatically when the breach is no longer present.",
          ],
        },
        {
          type: "paragraph",
          text:
            "Latency anomalies are independent from availability. A monitor can be up while unusually slow and therefore have a latency alert without an availability alert.",
        },
      ],
    },
    {
      id: "host-and-mesh",
      title: "Host metric, mesh, and TLS expiry alerts",
      blocks: [
        {
          type: "paragraph",
          text:
            "An [agent monitor](/docs/agents/#thresholds) can define positive percentage thresholds for CPU, memory, disk, and swap. Each breached metric opens its own `host_metric` alert and resolves independently. Removing a threshold resolves an alert that no longer has a configured condition.",
        },
        {
          type: "paragraph",
          text:
            "A location mesh edge opens a `mesh_edge` alert after its directional failure threshold. The reverse direction is a different condition. Mesh alerts use tenant default channels.",
        },
        {
          type: "paragraph",
          text:
            "An HTTP monitor with `tls_min_days_valid` opens a `tls_expiry` alert when its most recently observed certificate has fewer remaining validity days than the threshold. The certificate window does not fail the check: the endpoint stays `up` and the warning is a distinct alert kind, so an aging certificate is distinguishable from an outage. The alert resolves when a renewed certificate is observed or the threshold is removed. A fully expired certificate fails the TLS handshake itself and therefore surfaces as a regular availability alert.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Automatic incident coverage is specific",
          text:
            "The verified automatic-incident paths cover availability and latency anomaly alerts. Do not assume the tenant switch automatically creates incidents for every host-metric, mesh-edge, or TLS-expiry alert.",
        },
      ],
    },
    {
      id: "maintenance",
      title: "Maintenance windows and snoozes",
      blocks: [
        {
          type: "table",
          columns: ["Field", "Meaning"],
          rows: [
            ["`title`", "Required operator-facing title"],
            ["`description`", "Optional maintenance context"],
            ["`starts_at`", "RFC 3339 start timestamp"],
            ["`ends_at`", "RFC 3339 end timestamp, after the start"],
            [
              "`monitor_ids`",
              "Selected tenant monitors; selecting a group also covers its members for muting",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Maintenance windows are classified as upcoming, active, or past. Checks continue to execute and states continue to change; the alert opening/notification path is muted for covered monitors during the active interval.",
        },
        {
          type: "paragraph",
          text:
            "A monitor can also be snoozed until an explicit timestamp or for a duration in minutes. Snooze is a concise one-monitor maintenance action, not a pause in data collection.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Failure can surface when maintenance ends",
          text:
            "Because checks continue, a monitor that is still down at the end of maintenance can immediately open or resume an alert. Schedule enough time for validation or extend the window before it expires.",
        },
      ],
    },
    {
      id: "dependencies",
      title: "Dependency-aware root cause",
      blocks: [
        {
          type: "paragraph",
          text:
            "When a downstream availability alert opens, Probara inspects its [dependency graph](/docs/dependencies/#dependency-model) and annotates the alert with the deepest currently down upstream monitor. Ties prefer the upstream condition that became down earlier.",
        },
        {
          type: "paragraph",
          text:
            "The annotation is recomputed as upstream state changes and is cleared when no qualifying cause remains. It improves triage context but does not suppress the downstream alert or its notifications.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Dependencies annotate; group rollup suppresses",
          text:
            "If you need one alert for a set of member monitors, use group rollup. Dependency edges preserve downstream alerts so operators can still see impact.",
        },
      ],
    },
    {
      id: "incidents",
      title: "Incidents",
      blocks: [
        {
          type: "table",
          columns: ["Variable", "Values or purpose"],
          rows: [
            ["`title` / `summary`", "Required incident description"],
            [
              "`severity`",
              "`critical`, `high`, `medium`, or `low`",
            ],
            ["`owner`", "Optional ownership label"],
            [
              "`status`",
              "`investigating`, `identified`, `monitoring`, or `resolved`",
            ],
            ["`source`", "`manual` or automatically created"],
            [
              "Linked resources",
              "Attach or detach alerts and monitors as investigation changes",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Timeline entries can be system events, internal notes, or public updates. Publishing an incident to a [status page](/docs/status-pages/#incidents) exposes only the page-relevant subset: the incident and page must share selected linked monitors. Unpublishing removes it from that page without deleting the incident.",
        },
        {
          type: "list",
          ordered: true,
          items: [
            "Create an incident manually, or enable automatic creation for supported alert paths.",
            "Attach the alerts and affected monitors used for internal context.",
            "Set severity, owner, and lifecycle status as the response progresses.",
            "Add internal notes for operators and public updates for subscribers.",
            "Publish to each relevant status page, then unpublish or resolve when appropriate.",
          ],
        },
      ],
    },
    {
      id: "ai-analysis",
      title: "AI-assisted incident analysis",
      blocks: [
        {
          type: "paragraph",
          text:
            "When an effective [tenant LLM configuration](/docs/administration/#ai-settings) is enabled, an incident analysis request queues asynchronous work. Its result progresses through `pending`, `ready`, or `failed` and can include a summary, probable root cause, contributing factors, recommended actions, confidence, and evidence.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Analysis is advisory",
          text:
            "Generated findings are not a state transition or remediation action. Operators must validate them against result history, dependency evidence, deployment events, and system telemetry before acting.",
        },
      ],
    },
    {
      id: "retired-policies",
      title: "Retired alert-policy API",
      blocks: [
        {
          type: "paragraph",
          text:
            "Legacy `/alert-policies` endpoints return HTTP `410 Gone`. Configure tenant notification defaults, monitor-specific routing, consecutive failure thresholds, agent metric thresholds, and latency-anomaly settings instead.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Remove policy automation",
          text:
            "Older integrations that create or attach alert-policy records must be migrated. Compatibility-shaped fields or count endpoints should not be treated as a supported policy-management workflow.",
        },
      ],
    },
  ],
};

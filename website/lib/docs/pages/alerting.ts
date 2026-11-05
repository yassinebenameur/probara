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
    "alert triage",
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
              "A metric series reported by an agent's collector breaches a configured metric rule",
              "The series returns within the rule, the rule is removed, or the readings go stale",
            ],
            [
              "`mesh_edge`",
              "A directional location-mesh edge reaches down",
              "That direction recovers",
            ],
            [
              "`tls_expiry`",
              "An HTTPS, SIP-over-TLS or TLS-wrapped TCP monitor's certificate (with `tls_check_chain`, the earliest-expiring certificate of its chain) has fewer remaining validity days than `tls_min_days_valid`",
              "A renewed certificate is observed, or the threshold is removed",
            ],
            [
              "`location_offline`",
              "A check location that reported in the last 7 days sends no heartbeat for the workspace's `location_offline_after_seconds` ([details](/docs/locations/#heartbeats)); routed to the default channels, and its mesh paths go quiet meanwhile",
              "The location reports again, is disabled or deleted, or the workspace turns these alerts off; the closing notification says which",
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
          tone: "info",
          title: "Acknowledging stops the paging",
          text:
            "Acknowledging records who owns the alert (`acknowledged_by_user_id`, and `acknowledged_via`: `ui`, `api` or `mcp`) and leaves the condition open. By default it also stops the paging: escalation tiers that have not fired yet never fire, and reminders stop, until the alert resolves or is unacknowledged. Paging channels (PagerDuty, Opsgenie / JSM) also receive the acknowledgement itself, once, so the provider stops its own escalation. A route marked **keep notifying after ack** (`notify_after_ack`, on a tenant default route or a monitor's custom route) keeps its tiers and reminders, for example a status-room channel. Turning off the workspace setting `ack_stops_notifications` restores the older behaviour, where reminders continued after an acknowledgement. Suspected-cause follow-ups still reach acknowledged alerts. With asynchronous dispatch, a tier or reminder already queued when the acknowledgement lands is dropped by the delivery worker, and a dropped tier counts as not sent: it pages after an unacknowledge like any unfired tier.",
        },
        {
          type: "paragraph",
          text:
            "**Unacknowledge** (`POST /alerts/{id}/unacknowledge`, or in bulk) returns an alert to `active` and resumes paging. On every route the acknowledgement held, the clocks restart from the unacknowledge: an unfired tier fires its delay later and the next reminder comes one reminder interval later, so nothing fires all at once. Routes it never held (`notify_after_ack`, or every route when `ack_stops_notifications` is off) keep their schedule. A later acknowledgement reaches paging channels again. Known limitation: a PagerDuty or Opsgenie alert that was acknowledged stays acknowledged at the provider, because their event APIs cannot un-acknowledge.",
        },
        {
          type: "paragraph",
          text:
            "**Snooze** (`POST /alerts/{id}/snooze` with `minutes` or `until`, 1 minute to 7 days; `POST /alerts/{id}/unsnooze` ends it) mutes one open alert: no escalation tier, reminder or suspected-cause follow-up goes out on any route, `notify_after_ack` routes included, until the snooze ends. Its clocks then restart, as after an unacknowledge. A snooze is independent of acknowledgement, a resolve ends it, and it never creates a maintenance window, so SLA planned downtime is untouched (snoozing the *monitor* does open one). Acknowledgements and resolves of a snoozed alert still notify. Alerts report why they are quiet in `muted_reason`: `snoozed`, or `acknowledged` when the workspace stops paging on ack.",
        },
        {
          type: "paragraph",
          text:
            "On the Alerts page, users who can write work the queue in bulk: tick open alerts (Shift-click selects a range, the header box selects every open alert on the page, Escape clears), then Acknowledge or Resolve from the bar. Each action sends only the selected alerts it applies to, and alerts that changed state meanwhile are reported as skipped rather than failing the batch. Bulk acknowledge and resolve notify and record incident recovery as the single actions do; they skip alerts of deleted monitors (reported as not found), which the list no longer shows. With a row focused, `A` acknowledges and `R` resolves that alert; these keys work only while focus is on an alert row, never while typing or with Ctrl, Alt or Cmd held, and a press on an alert that is already past that step says so instead of acting. Every view except Open sorts on the server, so a column sort orders every matching alert, not just the page; those views default to newest first, while Open (which loads the whole queue) defaults to active first. Under a one-status filter (Active, Acknowledged, Resolved) the Status column does not sort, since every row has the same status.",
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
            "A check failure first passes through the monitor's consecutive-failure confirmation and, when locations are selected, its [location failure quorum](/docs/locations/#assignment). A check's `error` and `failure` are both failing evidence, including a check's own assertions: a SIP test call failing a [call-quality](/docs/monitors/#network-protocols) threshold (`max_loss_pct`, `min_mos`, …) is a failure like any other, while its `warn_*` thresholds only add warnings to the check and never alert.",
            "The alerter opens an availability alert only when effective state is `down`, the monitor is enabled, and maintenance/rollup rules do not mute it.",
            "The initial notification is sent through eligible routes after each route's configured delay.",
            "While the condition remains down, reminders are sent at the tenant reminder interval, until the alert is acknowledged (unless the workspace keeps paging after an acknowledgement) or while it is snoozed.",
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
        {
          type: "paragraph",
          text:
            "**Reopen within cooldown (flap suppression).** With the workspace setting `alert_reopen_cooldown_seconds` above 0 (it defaults to 0, off), a monitor that goes down again less than that long after the alerter resolved its last availability alert gets that alert back instead of a new one: the alert returns to `active`, `relapse_count` goes up by one and `reopened_at` records when. A flapping target therefore keeps one alert, and one automatic incident, for the whole flapping period. Each reopen is a new paging episode: it clears the previous episode's acknowledgement, unacknowledge and snooze (they were for the outage that recovered), escalation tiers count from `reopened_at`, and every route pages again, a route that already heard the previous recovery included. The page reads **Down again (relapse n)** and is delivered as a `created` event whose `alert.relapse_count` is above 0; reminders and the next recovery follow as usual. Only the alerter's own resolves reopen: an alert resolved by hand (`resolved_via` is `ui`, `api` or `mcp`, with `resolved_by_user_id` for an operator) never does, and only the monitor's latest availability alert is considered. Other alert kinds never reopen. Each open incident linked to the alert gets a system timeline entry when it relapses, and the Alerts page marks relapsed alerts with their count. With asynchronous dispatch, a recovery notice still waiting on the delivery worker's retry backoff when its alert reopens is dropped, as is anything else published in an earlier episode, so a late recovery can never close the provider incident of the relapse. The route that recovery was meant for never heard it, so it counts as still paged for the relapse: it is not paged again, gets the relapse's reminders, and its incident is closed by the relapse's recovery, even when that comes before the route's escalation delay.",
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
                "An ordered list of active alert channels, each with `channel_id`, display metadata, a non-negative `delay_seconds`, and `notify_after_ack` (keep paging this route while the alert is acknowledged).",
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
            "Delays implement escalation: a zero-delay route fires immediately, while a later route fires only if the alert is still open, and not acknowledged or snoozed, when its delay elapses. Resolution notifications are limited to routes that actually fired, avoiding a recovery message on a channel that never saw the outage.",
        },
        {
          type: "callout",
          tone: "info",
          title: "One notification per event, however many alerters run",
          text:
            "Before it sends, the alerter claims the (alert, channel, event) slot in `alert_notification_states` inside a transaction and commits only after the channel accepted the message. A second alerter replica racing on the same alert blocks on that row, then finds the slot taken and stays quiet, so `alerter.replicas` can be raised for availability without duplicating deliveries. A failed send rolls the claim back and is retried on the next cycle. The same guarded update makes exactly one replica resolve an alert and send its recovery notice.",
        },
        {
          type: "paragraph",
          text:
            "Routing that resolves to nothing is not an error — an alert still opens, appears on the dashboard, and drives incidents and status pages; it simply notifies nobody. Because that is silent by design, every monitor read reports its effective reachability in `alert_routing`, and the dashboard counts affected monitors in `ops_summary.unrouted_monitors`.",
        },
        {
          type: "table",
          columns: ["`alert_routing` field", "Meaning"],
          rows: [
            [
              "`reachable`",
              "False when an alert on this monitor would notify nobody",
            ],
            [
              "`source`",
              "`custom` or `tenant_default` when the monitor's own routing applies; `group_rollup` when a group rolls its alerts up, so that group's routing decides; `members` for a group that never alerts itself",
            ],
            [
              "`active_channels` / `assigned_channels`",
              "Routed channels that would deliver, and the total including inactive ones — an assignment to an inactive channel counts as assigned but never delivers",
            ],
            [
              "`reason`",
              "Why an unreachable monitor is unreachable: `no_custom_channels`, `custom_channels_disabled`, `no_tenant_default_channels`, `tenant_default_channels_disabled`, or `group_rollup_unrouted`",
            ],
            [
              "`rollup_group_id` / `rollup_group_name`",
              "The group whose routing applies, present only for `group_rollup`",
            ],
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Three ways a monitor ends up notifying nobody",
          text:
            "Custom routing with no channel assigned; `default` mode while the workspace has no default routes; and routing whose every channel is deactivated — deactivation is not visible in the assignment list, only in the channel. A member of a group that rolls alerts up adds a fourth: its own channels never fire, so the group's routing is the one that must be set. The operator UI explains each case on the monitor's detail page, the dashboard counts them, and the monitors list's **No alert route** filter narrows to the affected monitors.",
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
            [
              "`dependency_suppression_enabled`",
              "Defaults to false. When true, a monitor whose upstream dependency is down opens its alert but sends no notification — see [dependency-aware alerting](/docs/alerting/#dependencies). Monitors override it with `dependency_suppression`",
            ],
            [
              "`dependency_suppression_grace_seconds`",
              "Defaults to 120; 0 to 86,400. How long a suppressed downstream stays quiet after its upstream recovers before paging if still down",
            ],
            [
              "`location_offline_alerts_enabled` / `location_offline_after_seconds`",
              "Default true / 180 (60 to 86,400). Alert once per [offline check location](/docs/locations/#heartbeats), to the default channels",
            ],
            [
              "`ack_stops_notifications`",
              "Defaults to true, for existing workspaces too. Acknowledging an alert stops its unfired escalation tiers and its reminders; routes with `notify_after_ack` keep them. Set false to keep paging acknowledged alerts",
            ],
            [
              "`alert_reopen_cooldown_seconds`",
              "Defaults to 0 (off); 0 to 86,400. A monitor down again within this long of its availability alert auto-resolving [reopens that alert](/docs/alerting/#availability) as a relapse instead of opening a new one",
            ],
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
              "HTTPS `webhook_url` on `slack.com` or a subdomain of it",
              "Slack incoming webhook (Block Kit, with an open-the-monitor button when `APP_BASE_URL` is set)",
            ],
            [
              "`discord`",
              "HTTPS `webhook_url` on `discord.com` / `discordapp.com` or a subdomain",
              "Discord webhook embed; mentions inside alert text never ping",
            ],
            [
              "`teams`",
              "Any HTTPS `webhook_url`",
              "Adaptive Card for a Teams Workflows (Power Automate) webhook; legacy MessageCard for Office 365 connector URLs (`*.webhook.office.com`, `outlook.office.com`)",
            ],
            [
              "`pagerduty`",
              "32-character Events API v2 `routing_key`, `region` `us` or `eu`",
              "Triggers, acknowledges and resolves one PagerDuty alert per Probara alert (`dedup_key` `probara:<alert id>`)",
            ],
            [
              "`opsgenie`",
              "Integration `api_key`, `site` `us`, `eu` or `jsm` (Jira Service Management), outage `priority` `P1`–`P5`",
              "Creates, acknowledges and closes one alert per Probara alert (alias `probara-<alert id>`). Degradations are sent one level below the outage priority, never above `P3`",
            ],
            [
              "`telegram`",
              "Bot `bot_token`, `chat_id` (numeric or `@channel`), optional forum `message_thread_id`",
              "HTML-formatted message through your own bot",
            ],
            [
              "`twilio_sms`",
              "`account_sid`, optional `api_key_sid`, `auth_token` (API key secret or account token), `from` number or Messaging Service SID, up to 10 E.164 `to` numbers",
              "One SMS per recipient per event, the short form of the alert plus the deep link",
            ],
            [
              "`ntfy`",
              "HTTPS `server_url` (defaults to `https://ntfy.sh`), `topic` (stored encrypted, since on a server without access control the name is the only protection), and optionally an `access_token` or a `username` / `password` pair",
              "Push notification through ntfy's JSON publish endpoint; priority 5 (urgent) for an outage, 4 for a degradation, 3 for a recovery, 2 for an advisory; tapping it opens the deep link",
            ],
            [
              "`gotify`",
              "HTTPS `server_url` of your Gotify server and an application `app_token` (sent in the `X-Gotify-Key` header, never the URL)",
              "Push message at priority 8 for an outage, 5 for a degradation, 4 for a recovery, 2 for an advisory, with the deep link in the text and as the Android click action",
            ],
            [
              "`pushover`",
              "Application `app_token` and a user or delivery-group `user_key` (both 30 characters, both stored encrypted), optional comma-separated `device` names",
              "Push message at high priority (1, sounds through quiet hours) for an outage, normal (0) for a degradation or recovery, quiet (-1) for an advisory. Emergency priority is never used, so nothing is left retrying at Pushover after a test or a recovery",
            ],
            [
              "`mattermost`",
              "HTTPS incoming `webhook_url` (`…/hooks/<id>`) on any host",
              "Colour-coded message attachment; Markdown in alert text is escaped and `@channel`, `@all` or `@user` inside it never notifies",
            ],
            [
              "`matrix`",
              "HTTPS client-server `homeserver_url`, a bot account's `access_token` (sent as a bearer header), and a `room_id` (`!…`, not a `#alias`) the account has already joined",
              "HTML and plain-text `m.text` message sent with an idempotent `PUT` whose transaction ID is derived from the alert, channel, event and event time, so a retried send the homeserver already accepted is not posted twice. Mentions inside alert text never notify",
            ],
            [
              "`google_chat`",
              "HTTPS space `webhook_url` on `chat.googleapis.com` (`/v1/spaces/…`)",
              "Card message; every event of one alert replies in the same thread, so the trigger, reminders and recovery stay together",
            ],
            [
              "`generic_webhook`",
              "HTTPS `url`, optional `hmac_secret`, optional custom headers encoded as JSON (stored encrypted and write-only; leaving the field blank on edit keeps them, **Remove stored value** deletes them)",
              "Structured Probara event JSON",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Every channel renders the same wording — headline, one-sentence summary, measurement, failing locations, likely root cause and blast radius — so a page, a chat message and an email about one alert never disagree. The opt-in [suspected-cause follow-up](#ai-triage) is worded the same way on every channel that receives it. Deep links into the operator UI appear on every channel when `APP_BASE_URL` is set and are omitted when it is not.",
        },
        {
          type: "table",
          columns: ["Event", "Chat, push, email, SMS, webhook", "PagerDuty, Opsgenie / JSM"],
          rows: [
            ["`created`", "Message", "Trigger / create"],
            ["`reminder`", "Message at the reminder interval, until acknowledged or while snoozed", "Skipped — the provider escalates and re-notifies on its own"],
            ["`acknowledged`", "Not sent", "Acknowledge, once per paged channel"],
            ["`suspected_cause`", "One follow-up message per paged channel when triage names a cause ([opt-in](#ai-triage))", "Not sent: their APIs add content by re-triggering, and an advisory hint must never re-page"],
            ["`resolved`", "Message", "Resolve / close"],
          ],
        },
        {
          type: "paragraph",
          text:
            "Generic webhooks include `X-Probara-Event-Type` and `X-Probara-Idempotency-Key`. The event type is `created`, `reminder`, `resolved` or, when a workspace opts in, `suspected_cause`, whose body adds `alert.suspected_cause` (`triage_id`, `key`, `title`, `confidence`, `actions`) and keeps `alert.status` at the alert's own `active` or `acknowledged`. Branch on the event type, not the status, and ignore event types you do not handle. When an HMAC secret is configured they also include `X-Probara-Signature` in `sha256=...` form. Receivers should verify the signature against the raw body and deduplicate by the idempotency key, which is `<alert>:<channel>:<event type>`, followed by `:<n>` once the alert has [relapsed](/docs/alerting/#availability) n times, since a reopened alert sends `created` and `resolved` again under the same alert ID. Custom headers cannot set these three in any letter case: saving such a channel is rejected, and a configuration stored before that check delivers without them.",
        },
        {
          type: "list",
          items: [
            "Create a channel from a registered plugin and save its plugin-specific configuration. Updates are validated by the plugin against the merged configuration, including kept secrets, so an edit cannot move a channel to a host create would have rejected.",
            "Use the channel test action before assigning production monitors. Tests send a real notification and require write permission. PagerDuty and Opsgenie tests open an informational alert and resolve it immediately, which may still notify whoever is on call. Opsgenie processes requests asynchronously, so its test waits (up to four seconds) for the alert to exist before closing it; if Opsgenie is slower than that, the test reports the alias so you can close the alert by hand. Email tests are served by the API process, so they need the same [platform SMTP configuration](/docs/configuration/#alerter-and-smtp) as the alerter; without it the test reports `mailer not configured` even when alert email is being delivered.",
            "Activate or deactivate the channel. Inactive channels remain configured but are skipped for delivery.",
            "Assign it in tenant defaults or in a monitor's custom routing.",
          ],
        },
        {
          type: "paragraph",
          text:
            "Delivery failures are classified. A rejected configuration — revoked key, deleted webhook, unknown chat, invalid number, any other 4xx — is permanent: it is logged once and not retried, so a broken channel does not re-fail every evaluation cycle. Timeouts, 5xx and 429 responses are retried; a provider `Retry-After` is honoured (capped at ten minutes) on the asynchronous path. An SMS send is retried whenever any recipient failed transiently, even if another was rejected for good, and waits for the longest delay any recipient's provider response asked for. The retry texts only the recipients Twilio has not accepted yet: each accepted recipient is recorded in the database the moment Twilio answers, so it is skipped whichever alerter replica or delivery worker picks the retry up, after a restart included. Redirects are never followed and count as permanent failures. With asynchronous dispatch, a retried trigger, reminder or acknowledgement is dropped once its alert has resolved, and one already being delivered holds the alert until it finishes, so resolving waits for it (at most the 15-second delivery timeout). Either way the resolve reaches the provider last, so a delayed or concurrent trigger can never leave a PagerDuty or Opsgenie alert open after recovery. A retried trigger or reminder is likewise dropped once the alert is snoozed, or acknowledged on a route that stops paging on ack, and so is a suspected-cause follow-up on a snoozed alert; a dropped trigger or follow-up is released, so it goes out once the snooze ends or the alert is unacknowledged. A retried acknowledgement is dropped once the alert is unacknowledged. On a route that keeps paging after an ack, acknowledgement is ordered as before: if an operator acknowledges while a paging trigger is still waiting on a retry, the acknowledgement that went ahead is a no-op at the provider, so the worker acknowledges again right after the late trigger lands, and the incident does not keep escalating.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Channel destinations are egress-guarded",
          text:
            "Every channel dials through the [notification egress policy](/docs/security/#ssrf-network-policy): private, loopback, link-local (including cloud metadata) and reserved addresses are refused by default, checked against the resolved IP at connect time. A webhook receiver on an internal network needs its range in `NOTIFICATION_ALLOWED_CIDRS`, and so does a self-hosted ntfy, Gotify, Mattermost or Matrix server there. Slack, Discord and Google Chat additionally require their own HTTPS hosts, and Telegram, Twilio and Pushover only ever call their fixed provider APIs; Teams, Mattermost, Matrix, ntfy, Gotify and generic webhooks accept any HTTPS host.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Known gaps",
          text:
            "Acknowledgement syncs one way: acknowledging in Probara acknowledges the PagerDuty or Opsgenie alert, but acknowledging or resolving in the provider does not change the Probara alert. Retrying an SMS send for one recipient does not text the others again: every number Twilio accepted for that event (or that reminder round) is skipped. A number can still get a duplicate when Twilio accepted the message but its answer never arrived (a timeout or a crash mid-request), or when recording the acceptance failed while another number's failure forced a retry.",
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
            'Set `APP_BASE_URL` to the public origin of the operator UI and every alert email carries an "open the monitor" button (mesh-edge alerts link to the mesh matrix instead; a suspected-cause follow-up links to that one alert on the Alerts page, in its workspace). Without it the button is omitted rather than pointing at a guessed host. `SMTP_FROM_NAME` sets the From display name and defaults to `Probara Alerts`.',
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
            "Templates receive the raw alert fields (`event_type`, `monitor_name`, `status`, `kind`, `failure_count`, `last_error`, `triggered_at`, `resolved_at`, `tenant_id`, `metric_name`, `metric_value`, `threshold_value`, `baseline_latency_ms`, `observed_latency_ms`, `anomaly_score`, `root_cause_monitor_name`, `source_location_name`, `target_location_name`, `failing_locations`) plus the presentation values the built-in email uses, so an override can reuse the same wording: `label`, `status_label`, `summary`, `duration`, `triggered_at_human`, `resolved_at_human`, `action_url`, `action_label`, and `accent_color`. A template that fails to parse or execute falls back to the built-in rendering — the alert still goes out, once: the failure is logged (and returned by a test send, so you see it there) but never retried, because retrying would only mail the whole `to` list the same fallback again. Overrides apply to pages only: a [suspected-cause follow-up](#ai-triage) always uses the built-in rendering and the subject `[Suspected Cause] <monitor>`, so a template written for outages (status is still `active` on a follow-up) never turns it into a second DOWN email.",
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
        {
          type: "paragraph",
          text:
            "Under `group` rollup the group's routing is the routing that matters: a suppressed member's own channels never fire, so its `alert_routing.source` reports `group_rollup` and names the group. A member covered by several rollup groups is reachable if any of them delivers, since each group alerts on its own.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Pausing a `group` rollup group silences its members",
          text:
            "Member suppression does not consider the group's enabled flag, so a paused rollup group keeps suppressing member alerts while never emitting its own — that whole subtree notifies nobody. Reachability reports it as `group_rollup_paused`; resume the group, or switch it to `per_monitor` so members alert for themselves.",
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
            "An [agent monitor](/docs/agents/#thresholds) defines `metric_rules`: up to 50 threshold rules over any metric its collector reports, compared in the metric's native unit (`*.utilization` metrics are ratios, so 90% is `0.9`), with optional attribute filters and an optional sustained-for duration that must hold for the whole window before the alert opens. A rule without filters fans out per matching series — one filesystem rule opens one `host_metric` alert per breaching mountpoint.",
        },
        {
          type: "paragraph",
          text:
            "Each alert is keyed by its canonical series key (for example `system.filesystem.utilization{device=/dev/sda1,mode=rw,mountpoint=/data,type=ext4}`) and resolves independently when the series returns within the rule or the rule is removed. Evaluation is freshness-bounded to three monitor intervals (90-second floor), so a dead collector's last stale readings cannot keep metric alerts open — the availability alert covers that outage. Alerts migrated from the retired `metric_thresholds` fields keep working; their legacy names still render on existing alert records.",
        },
        {
          type: "paragraph",
          text:
            "A location mesh edge opens a `mesh_edge` alert after its directional failure threshold. The reverse direction is a different condition. Mesh alerts use tenant default channels.",
        },
        {
          type: "paragraph",
          text:
            "An HTTPS, SIP-over-TLS or TLS-wrapped TCP monitor with `tls_min_days_valid` opens a `tls_expiry` alert when its most recently observed certificate has fewer remaining validity days than the threshold. With `tls_check_chain: true` the measure is the earliest expiry across the certificate chain, the leaf and every intermediate certificate (never a self-signed root, whose expiry is the trust store's), so an intermediate that lapses before the leaf alerts in time. The chain is the verified one; under `tls_skip_verify`, where nothing is verified, it is the issuer path from the leaf through the certificates the server sent, ending at a self-signed certificate or an issuer it did not send, and an expired cross-sign of a root (such as a stale cross-signed root a server still sends) is left out, since clients that hold the root never use it; certificates recorded before chain capture existed fall back to the leaf. The operator UI turns it on for new monitors; monitors saved without it keep measuring the leaf. The status page and the monitor detail page show the same date the alert measures. The certificate window does not fail the check: the endpoint stays `up` and the warning is a distinct alert kind, so an aging certificate is distinguishable from an outage. The alert resolves when a renewed certificate is observed or the threshold is removed. A fully expired certificate fails the TLS handshake itself and therefore surfaces as a regular availability alert.",
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
            "Maintenance windows are classified as upcoming, active, or past. Checks continue to execute and states continue to change; the alert opening/notification path is muted for covered monitors during the active interval. `GET /maintenance-windows?status=` lists each class in the order an operator needs it: active windows by soonest end, upcoming by soonest start, past (and the unfiltered list) by most recent end, so a page cut never drops the next window.",
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
      title: "Dependency-aware alerting",
      blocks: [
        {
          type: "paragraph",
          text:
            "When a downstream availability alert opens, Probara inspects its [dependency graph](/docs/dependencies/#dependency-model) and annotates the alert with the deepest currently down upstream monitor. Ties prefer the upstream condition that became down earlier. The annotation is recomputed every evaluation tick and cleared when no qualifying cause remains; it is always on and always visible on the alert, in notifications (\"likely root cause\") and in the operator UI.",
        },
        {
          type: "paragraph",
          text:
            "Whether the annotation also **suppresses the downstream page** is a policy. It is off by default: enable `dependency_suppression_enabled` in the workspace [notification settings](/docs/alerting/#notification-routing) to page root causes only, and override it per monitor with `dependency_suppression` (`inherit`, `on`, `off`) in the monitor's Alerting section (shown once the monitor has upstream dependencies, or when a non-default value is stored) — `off` keeps a critical monitor paging whatever the workspace says.",
        },
        {
          type: "list",
          items: [
            "A suppressed downstream alert still opens, still counts on the dashboard, still resolves and still drives incidents and status pages; only its notifications are withheld. The API reports it with `suppression_reason: \"dependency\"`, the alert list and dashboard flag it, and `GET /api/v1/alerts?suppressed=true` lists exactly those alerts.",
            "The root cause's own DOWN and reminder notifications carry an **Also affecting** section naming the suppressed downstream monitors (`impacted_monitors` / `impacted_count`; the API exposes `impacted_count` on the alert), recomputed on every send.",
            "When the upstream recovers but the downstream is still down, the downstream stays quiet for `dependency_suppression_grace_seconds` (default 120) so a monitor that recovers one check later never pages. After the grace it pages as a normal DOWN, with escalation-tier delays counted from that moment rather than from when the outage began.",
            "A downstream that recovers while suppressed was never announced, so no recovery notification is sent for it. A downstream that paged before its upstream was detected keeps its recovery notification but stops sending reminders while the upstream explains it.",
            "Suppression is decided at dispatch, from live state, by the same predicate the API uses to compute `suppression_reason`; it is not part of `alert_routing`, which describes static routing configuration.",
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "Dependencies and group rollup compose",
          text:
            "Group rollup suppresses a member because its group alerts instead; dependency suppression suppresses a downstream because its upstream alerts instead. Both predicates apply at dispatch, so a rolled-up member with a down upstream is silent under either rule. Use rollup for one alert per composite service, dependencies for one alert per root cause.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Known gaps",
          text:
            "Only availability alerts are annotated and suppressed; latency, host-metric, certificate and mesh alerts are unaffected. Suppression follows `monitors.current_state = down` on the upstream — a degraded or suspect upstream does not suppress anything. A late-paging downstream's notification does not say which upstream it had been attributed to.",
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
              "Attach or detach alerts and monitors as investigation changes. The alert picker searches the workspace's whole alert history by monitor or location name, or alert ID, newest first, so an older alert can be attached too",
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
            "When [workspace local AI](/docs/administration/#ai-settings) is enabled, incident analysis queues asynchronous work through `AI_RCA`. Evidence spans 15 minutes before the earliest linked monitor alert (incident creation when none is linked) through 60 minutes after it, capped at analysis request time. Results from jobs that expired before a worker ran them (`result_source=platform`) are not evidence. Linked mesh-edge alerts are not analysed; they are listed by alert and location ID under incomplete evidence. Full-window summaries, sampled transitions, configured upstream checks, overlapping maintenance, allowlisted timings/certificate facts and fresh host utilization support monitor-level decisions. Results retain summary, suspected cause, contributing factors, actions, confidence and evidence, with structured model/score/reference metadata. Ambiguous or unsupported incidents complete as `ready` with insufficient evidence. A local AI that is temporarily unavailable (queue full with HTTP 429, model loading or restarting with 502/503/504, connection refused) is retried automatically, 1 and then 2 minutes later or after the service's `Retry-After` (at most 2 minutes), for up to 3 deliveries; other service failures, and an analysis still unavailable on its third delivery, become `failed` with a retry action. Each delivery runs under one deadline (`LOCAL_AI_TIMEOUT_SECONDS` plus 30 seconds); after repeated errors the run is recorded as failed, and database-connected workers fail any analysis still pending after 20 minutes (30 minutes if a run still holds it), which outlasts the unavailable-service retries. With no such worker running, pending analyses wait until one returns. Historical provider results remain visible as legacy analyses.",
        },
        {
          type: "paragraph",
          text:
            "Below a ready analysis, editors and admins answer **Was this analysis right?** to record a training label for the local model. **Confirm** keeps the suspected cause and each monitor's shown answer. **Correct** picks the actual cause; each monitor's answer follows it only where that monitor's own evidence supports it, otherwise it becomes insufficient evidence. With more than one monitor, per-monitor answers can be set individually. The label stores the exact evidence the model saw, copied server-side, and a later change replaces it. Viewers see the existing label read-only. Legacy analyses, analyses that sent no monitor decisions, and analyses completed before labels existed cannot be labelled; run a new analysis. Labels do not change the incident, alerts or monitor state. See [AI label endpoints](/docs/api/#ai-labels).",
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
      id: "ai-triage",
      title: "Automatic alert triage",
      intro:
        "With **Automatic alert triage** turned on in [Settings → AI](/docs/administration/#ai-settings), each new open alert gets one suspected cause from the same local model, catalogue and evidence checks as [incident analysis](#ai-analysis). Triage is shown next to the alert, and can optionally be sent once to the channels already paged for it (see suspected-cause follow-ups below). [SLA reports](/docs/slas/#suspected-causes) roll monitor outages up by these suspected causes at medium or high confidence, operator labels included (a low-confidence one counts as inconclusive).",
      blocks: [
        {
          type: "callout",
          tone: "warning",
          title: "Triage never changes paging",
          text:
            "Triage never delays, routes, suppresses or re-prioritises a page, and never changes an alert's severity or status. Created, reminder, acknowledged and resolved notifications are identical with triage on or off. The only thing it can send is the opt-in follow-up below, at most once per alert per channel. If local AI is temporarily unavailable (queue full, model loading or restarting, connection refused), the triage is retried up to 3 deliveries over about 3 minutes; if it is still unavailable, the triage is recorded as failed. Alerting carries on unchanged either way. Validate a suspected cause like any other evidence before acting on it.",
        },
        {
          type: "paragraph",
          text:
            "Database-connected platform workers run a relay every 15 seconds. It claims alerts that are open (`active` or `acknowledged`, so acknowledging in the first minute still gets a triage), triggered within the last 6 hours, and older than the settle wait. The settle wait is the workspace delay, but at least two check intervals of the alert's monitor, so the alert's own failing checks are part of the evidence. The two-interval part is capped at one hour, so monitors that check less often than every 30 minutes are triaged an hour after the alert opens. An alert that resolves within the wait is never triaged, and alerts from before auto-triage was turned on are not triaged once they are more than 6 hours old. Claiming is oldest alert first. Each workspace has an hourly cap on triages, counted over the last hour with skipped ones excluded. All workspaces also share a platform-wide limit of 200 queued triages, so an alert storm cannot queue more work than the local AI can process before the jobs expire.",
        },
        {
          type: "paragraph",
          text:
            "Evidence covers the alert's monitor (a group alert expands to its members), its configured upstreams, and the dependency root that suppressed it, if any. The window runs from 15 minutes before the alert triggered to 60 minutes after, capped at claim time. Window, sampling and exclusion rules are the same as for incident analysis. Each alert has one triage, and it is not re-run while the alert stays open. A relapse within the [reopen cooldown](#availability) reuses the alert and its existing triage; only a condition that opens a new alert is triaged again.",
        },
        {
          type: "table",
          columns: ["Status", "Shown as", "Meaning"],
          rows: [
            ["`pending`", "Triaging… (muted)", "Claimed and queued, or running"],
            ["`ready`", "Suspected <cause> · <confidence>, or No clear cause", "A catalogue hypothesis with low/medium/high confidence, or the model abstained (insufficient evidence). The tooltip holds the summary"],
            ["`failed`", "Triage failed (muted)", "The local AI returned invalid decisions or was still unavailable after 3 deliveries, the run failed after repeated errors, the run was still pending 30 minutes after a worker started it, or no worker started it within 70 minutes of the claim (queued jobs expire after an hour)"],
            ["`skipped` / `resolved`", "Not triaged (muted)", "The alert resolved before the triage ran; the model is not called"],
            ["`skipped` / `cooldown`", "Not triaged (muted)", "The same monitor had a ready triage within the workspace cooldown"],
            ["`skipped` / `disabled`", "Not triaged (muted)", "Auto-triage, workspace AI or installation AI was turned off (or `AI_TRIAGE_ENABLED=false`) while the job was queued"],
            ["`skipped` / `not_modeled`", "Hidden", "Mesh-edge alerts have no monitor to analyse"],
          ],
        },
        {
          type: "paragraph",
          text:
            "The Alerts table shows the triage as a pill in the monitor cell, and incident detail shows it next to each linked alert. Pills update the next time the list loads, not live: the alert stream carries alert changes only. Triage runs only while auto-triage and workspace AI are both on. Turning it off, setting `AI_TRIAGE_ENABLED=false`, or setting `LOCAL_AI_DISABLED=true` stops new claims, and triages already queued finish as skipped without calling the model. A job that never reached the queue is published again up to twice, 3 minutes apart. Two alerts of different kinds on one monitor triaged at the same moment can both run, because the cooldown only counts triages that are already ready.",
        },
        {
          type: "paragraph",
          text:
            "**Suspected-cause follow-ups.** With **Send suspected causes to alert channels** also on (it needs auto-triage and workspace AI), the alerter sends one advisory follow-up per channel that was already paged for the alert, after the page went out. Pages are never held back for it. The table lists when it is sent and when it is not.",
        },
        {
          type: "table",
          columns: ["Condition", "Follow-up"],
          rows: [
            ["Ready triage naming a catalogue cause with medium or high confidence, completed after the workspace turned follow-ups on", "Sent once per channel"],
            ["No clear cause (abstention), low confidence, or a cause the running catalogue does not know", "Not sent; the pill still shows it"],
            ["An operator corrected the cause before it went out", "Not sent; confirming keeps it"],
            ["Channel not paged for the alert (unrouted, still inside its escalation delay, or suppressed from the start), no longer routed to the monitor, or inactive", "Not sent. A later escalation tier gets its own follow-up once its page goes out"],
            ["Less than 3 minutes since that channel's page", "Waits, so it reads as a follow-up"],
            ["Triage and the channel's page both more than an hour old", "Not sent"],
            ["Alert resolved or snoozed, monitor in maintenance, member of a roll-up group, or dependency-suppressed (muted exactly like a reminder)", "Not sent"],
            ["Alert acknowledged", "Sent: the hint helps whoever is working it"],
            ["Mesh-edge alert, or triage skipped (cooldown, resolved)", "Not sent: there is no ready triage"],
            ["`LOCAL_AI_DISABLED=true` or `AI_TRIAGE_ENABLED=false` on the alerter", "Not sent, including from triages that were already ready"],
            ["PagerDuty or Opsgenie / JSM channel", "Not sent (chat, push, email, SMS and webhook channels get it)"],
          ],
        },
        {
          type: "paragraph",
          text:
            "The follow-up is titled **Suspected Cause: <monitor>** and says which page it follows up on (for example 'Down alert' or 'CPU Usage High', so two host-metric alerts on one host stay distinct), the suspected cause, its confidence, the catalogue's suggested first check, the trigger time and how long the alert has been open, plus the dependency root cause when one is known. It ends with a link to that alert on the Alerts page when `APP_BASE_URL` is set. Email follow-ups are separate messages with the subject `[Suspected Cause] <monitor>`, never threaded replies, and ignore channel template overrides. SMS channels send it as one more text per recipient. Reminders, acknowledgements and the resolve keep their own schedule. Under asynchronous dispatch the order is best effort: a page still on the delivery worker's retry backoff after 3 minutes can arrive after its follow-up, and a page the worker dropped for good still counts as paged.",
        },
        {
          type: "paragraph",
          text:
            "Editors and admins can label a ready triage from the alert's row menu (⋮): **Confirm cause** or **Correct cause…**, or **Confirm no cause** and **Name the cause…** when the model abstained. Once labelled, a **Change label…** button (tag icon) takes the menu's place. Correcting opens the same per-monitor control as incident labels. The label stores the evidence the model saw, copied server-side, is included in the [training export](/docs/administration/#ai-settings), and shares a split group with the earliest incident that links the alert. A triage that sent no decisions cannot be labelled. Labels never change the alert; a correction made before the follow-up went out cancels it. A label does replace the model's cause in [SLA report suspected causes](/docs/slas/#suspected-causes) computed afterwards (an issued report keeps the cause it froze). See [AI label endpoints](/docs/api/#ai-labels).",
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

import type { DocPage } from "../types";

export const SLAS_PAGE: DocPage = {
  slug: "slas",
  group: "Use Probara",
  title: "SLA reporting",
  description: "Define availability targets for sets of monitors, track error budgets and their burn per calendar period, export or freeze reports, and email them when each period closes.",
  eyebrow: "Availability objectives",
  readingTime: "8 min read",
  keywords: ["SLA", "SLO", "error budget", "availability report", "uptime report", "MTTR", "MTTA", "PDF", "CSV", "suspected cause", "root cause", "burn rate", "scheduled report"],
  sections: [
    {
      id: "define",
      title: "Define an SLA",
      blocks: [
        {
          type: "paragraph",
          text: "An SLA is a named target over a set of monitors. Open SLAs → New SLA as an editor or admin. Viewers and read-scope API keys can read SLAs and export reports but cannot create, change or issue them. The API requires migration 000090.",
        },
        {
          type: "table",
          columns: ["Setting", "Meaning"],
          rows: [
            ["Target", "Availability percentage the period must reach, strictly between 0 and 100, stored with four decimals (99.95 stays 99.95)."],
            ["Monitors and tags", "Explicit monitors plus every monitor carrying any of the tags, resolved each time a report runs. A group monitor contributes its members recursively, never itself. Deleted monitors drop out."],
            ["Period", "Weekly (ISO weeks starting Monday), monthly or quarterly calendar periods."],
            ["Timezone", "IANA name that sets period and day boundaries. A month that crosses a daylight-saving change is 743 or 745 hours long."],
            ["Aggregation", "How monitors combine into one number: serial or mean (see below)."],
            ["Degraded counts as down", "Off by default: time a monitor spends degraded (failing in some but not enough locations) counts as available. On: it counts as downtime."],
          ],
        },
      ],
    },
    {
      id: "measurement",
      title: "How availability is measured",
      blocks: [
        {
          type: "paragraph",
          text: "Every number comes from the monitors' state history (the timeline behind the monitor detail headline), never from counting check results. For each monitor, time in the period falls into exactly one class. Available: up, suspect, or degraded unless the SLA counts it as down. Unplanned down: down outside every maintenance window that covers the monitor, directly or through one of its groups. Planned down: down inside such a window. Paused. Unknown: no fresh evidence. Untracked: no state history yet. A monitor is down for whatever failed its checks, its own assertions included: a SIP test call failing a call-quality threshold counts as downtime like one that was not answered, and its warnings never do.",
        },
        {
          type: "paragraph",
          text: "Availability = available ÷ (available + unplanned down). Planned downtime, paused, unknown and untracked time leave the denominator. Coverage (available plus all down time, ÷ the period) reports how much of the period was actually observed; read a high availability with low coverage accordingly. Overlapping maintenance windows are merged before they are subtracted, so shared time counts once.",
        },
        {
          type: "definitions",
          items: [
            {
              term: "Serial (default)",
              description: "The service is down whenever any member is in unplanned downtime; excluded whenever no member is down but one is planned-down, paused, unknown or untracked; otherwise available. This is how most contracts are worded, and it is the stricter mode.",
            },
            {
              term: "Mean",
              description: "The unweighted average of each member's own availability, the same convention the dashboard uses for scopes. The error budget is derived from that average, so budget left ≥ 0 exactly when the target is met.",
            },
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "History starts with the timeline",
          text: "Monitors only have state history from when the timeline was introduced (or from their creation). Earlier time is reported as untracked with a note naming the monitor, and is never filled in from sampled check counts. An SLA over a single monitor over the same window matches that monitor's own availability headline.",
        },
      ],
    },
    {
      id: "reports",
      title: "Reports",
      blocks: [
        {
          type: "paragraph",
          text: "The SLA page opens on the running period, reported to date. Step back through earlier periods, or pick a custom range of whole local days (up to 400). A report shows availability against the target and a met/breached verdict, coverage, and the error budget: the downtime the target allows so far (1 − target × eligible time), how much of it was consumed, and the allowance for the full calendar period.",
        },
        {
          type: "list",
          items: [
            "Daily availability in the SLA's timezone, coloured at or above target / below target but ≥ 99% / below 99% / no data.",
            "A per-monitor table with availability, coverage, unplanned and planned downtime, paused time and outage count.",
            "Service outages (serial SLAs): the spans during which the composite was down, maintenance excluded.",
            "Monitor outages: each monitor's merged down spans with their planned share. Clipped spans are marked as starting earlier or ongoing. The list shows the first 500; counts and exports cover all.",
            "Response: outage count, mean time to recover (outages wholly inside the period with unplanned time), availability alerts triggered in the period, and mean time to acknowledge, measured to each alert's first acknowledgement (an alert unacknowledged later still counts as acknowledged). An alert that [relapsed](/docs/alerting/#relapses) counts once, and its first acknowledgement is measured from the start of the episode it answered: the trigger, or the reopen when it was first acknowledged after relapsing, so the recovered gap is never counted.",
            "Suspected causes: monitor outages bucketed by the [triage](/docs/alerting/#ai-triage) of their overlapping availability alerts, into named suspected causes, inconclusive, triage pending, not triaged and no alert (see [Suspected causes](#suspected-causes)). Columns: cause (marked * when it includes operator-labelled outages), outages, downtime, share of the attributed downtime, monitors, and the operator-confirmed and operator-corrected counts, with a note that these are observed symptoms, not confirmed root causes. Counts are monitor outages, not alerts, and the monitor-outage list gains a suspected-cause column. Like the PDF table, the panel and the column appear only when at least one monitor outage was triaged (suspected or inconclusive), so a workspace without auto-triage sees neither. Reports issued before this breakdown existed have none.",
          ],
        },
        {
          type: "paragraph",
          text: "Export any report as PDF (an A4 document with the headline, budget, daily strip, monitor table, suspected causes, outages and method note), CSV (summary, daily, monitor, outage and suspected-cause sections separated by blank lines, times in the SLA's timezone) or JSON (the full report document). The PDF's suspected-cause table appears under the same rule as the page's panel, only when at least one monitor outage was triaged (suspected or inconclusive), so a workspace without auto-triage never sends a table of \"Not triaged\". The CSV is the raw-data export, so its shape does not depend on AI settings: every report computed since the breakdown existed, AI on or off, gains `Cause key` and `Suspected cause` columns on its outage rows and a trailing suspected-cause section (attributed, triaged and pending outage counts, summed unplanned seconds, then one row per bucket), with Not triaged or No alert where nothing was triaged. Reports issued before the breakdown existed export exactly as they did. The monitor detail page uses the strictest target of the SLAs covering that monitor, falling back to 99.9%.",
        },
        {
          type: "paragraph",
          text: "Reports computed since [budget burn](#burn) existed also carry it in every format: the PDF adds the burn rates and the exhaustion verdict to its error-budget section, with a small burn-down chart, and the CSV appends a last section (one row per burn window with its availability and burn rate, the `Budget exhaustion` row, then the daily burn-down: consumed seconds and the share of the budget left). Reports issued before it existed export exactly as they did.",
        },
      ],
    },
    {
      id: "burn",
      title: "Budget burn",
      intro:
        "How fast the error budget is being spent, whether it lasts the period, and how it went down day by day. Everything is derived from the same timeline integration as the rest of the report.",
      blocks: [
        {
          type: "list",
          items: [
            "Burn rate over a window = (1 − availability in the window) ÷ (1 − target). 1× spends the budget exactly at the even pace that uses it all by the period end, 2× twice as fast, 0× not at all. A window without data has no burn rate. The page colours it green at or under 1×, amber up to 2× and red above.",
            "Windows: the last hour and the last 24 hours, both ending now and both only while the period runs, then the period to date (the whole period once it closed). The short windows never reach back before the period start, so on the first day of a period they cover only that period.",
            "The period's budget is the downtime the target allows over the time observed so far plus the time still to come: (1 − target) × (eligible time so far + time left). Paused, unknown and untracked time earn no budget, exactly as in the headline, so on a closed period it is the headline's allowed downtime, and the verdict, the budget used, the burn rate and the exhaustion state always agree.",
            "Burn-down: after each local day of the period, the share of that budget left, 100 × (1 − consumed so far ÷ the budget as it stood that day). It goes negative once overspent. The chart runs across the whole period against a dashed even-spend line from 100% to 0%; days before anything was observed are left blank. A closed period's last point equals the headline's remaining budget.",
            "Exhaustion: Exhausted once consumption reaches the period's budget, so that even a clean rest of the period cannot meet the target; on a closed period that is exactly a breach. Otherwise, for a running period, the budget left is divided by the budget consumed per hour over the last 24 hours: when that runs out before the period ends the report gives the date and time (Runs out …), else Not projected to run out this period. A closed period that met its target reads Lasted the period. No data to project when nothing was observed in the period, or in the last 24 hours of a running one.",
          ],
        },
      ],
    },
    {
      id: "suspected-causes",
      title: "Suspected causes",
      intro:
        "With [automatic alert triage](/docs/alerting/#ai-triage) on, Probara's local AI names a suspected cause for each new alert. SLA reports roll the period's monitor outages up by those causes, so a report says what kind of failure consumed the error budget as well as how much.",
      blocks: [
        {
          type: "list",
          items: [
            "Each per-monitor outage with unplanned time takes the effective cause of the availability alerts on its own monitor that overlap it, with 2 minutes of slack for an alert that triggered just after a short outage closed. When several alerts overlap, a named cause wins over an inconclusive triage, which wins over a pending one, which wins over none. The whole outage's unplanned time goes to that one cause.",
            "The model's hypothesis counts as a suspected cause only when the triage rated it medium or high confidence, the same bar as the [suspected-cause follow-up](/docs/alerting/#ai-triage); a low-confidence hypothesis is inconclusive. An operator's label on the triage (confirm or correct) wins over the model's hypothesis, whatever its confidence. The breakdown counts how many outages per cause were operator-confirmed or operator-corrected, and the PDF marks such causes with an asterisk.",
            "Counts are monitor outages, not alerts. Downtime is summed per monitor, so when monitors fail together it can exceed the service's downtime.",
            "Composite (service) outages, group roll-up alerts and incident analyses are not attributed.",
          ],
        },
        {
          type: "table",
          columns: ["Bucket", "Meaning"],
          rows: [
            ["Suspected cause (`suspected`)", "A named catalogue cause, from a ready triage at medium or high confidence or an operator's label on it. One row per cause, largest downtime first."],
            ["Inconclusive (`inconclusive`)", "The triage ran and found no supported cause, named one only at low confidence, or an operator labelled it insufficient evidence."],
            ["Triage pending (`pending`)", "The triage was queued or running, or the alert was still open, within the 6-hour triage lookback and in a workspace with auto-triage on, when the report was computed. With the installation's triage switched off (`AI_TRIAGE_ENABLED=false` or `LOCAL_AI_DISABLED=true`) an alert with no triage is never pending. The report adds a note saying how many outages were pending; when no outage was triaged, so the page and PDF show no breakdown, the note says so instead of pointing at the Triage pending row."],
            ["Not triaged (`untriaged`)", "The overlapping alerts were never triaged: no triage (auto-triage off at the time, resolved within the settle wait, cooldown, hourly cap or backlog), a failed triage, a skipped one, or an open alert older than the 6-hour lookback."],
            ["No alert (`no_alert`)", "No availability alert of the monitor overlapped the outage."],
          ],
        },
        {
          type: "callout",
          tone: "warning",
          title: "Symptoms, not root causes",
          text: "Suspected causes are observed symptoms from triage evidence, not confirmed root causes. Validate them before quoting them to a customer. Alerts are only triaged automatically (there is no manual trigger), so with auto-triage off the period's outages land in Not triaged or No alert.",
        },
      ],
    },
    {
      id: "issued",
      title: "Issued reports",
      blocks: [
        {
          type: "paragraph",
          text: "A live report of a past period is recomputed on every view, so it changes when someone creates a maintenance window that starts in the past (it counts as planned downtime from that start), adds a monitor to a group that a maintenance window targets (group membership is read as it is now, so the monitor takes on that window's past coverage), wipes a monitor's history, or changes the SLA itself, and as triages complete and operators label or relabel alerts (which moves outages between suspected-cause buckets). Issue a report to freeze a closed period. Probara stores the complete report, including the SLA settings and resolved monitor list at that moment, and every later download of it renders from that snapshot. That includes each suspected cause's bucket and title, whatever the catalogue later calls the key; outages still pending when it was issued stay pending. Only closed calendar periods can be issued, once each. Deleting the SLA deletes its issued reports; purging a monitor does not.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Limits in this release",
          text: "Availability alerts count toward MTTA only when they are on a member monitor or on a group monitor listed explicitly on the SLA. The PDF uses built-in fonts, so characters outside Western European scripts may not render.",
        },
      ],
    },
    {
      id: "schedules",
      title: "Scheduled emails",
      intro:
        "The Schedules tab of an SLA emails each closed period's report to a recipient list. One schedule per SLA; recipients can be anyone, inside the workspace or not.",
      blocks: [
        {
          type: "list",
          items: [
            "When a period closes, Probara waits an hour for late results, issues the period's report (or uses the copy someone already issued) and emails it to every recipient with the PDF attached, and the CSV too when asked for. The schedule shows the next send, the last period sent and each recipient's delivery.",
            "Each closed period is sent once. If the API was down across several closes, the last three closed periods are sent when it is back, oldest first; older ones are skipped (and logged).",
            "Each recipient gets their own message: the period, availability against the target, the share of the error budget used, the burn rate and exhaustion verdict, a link to the SLA's issued reports for workspace members (it switches to the SLA's workspace first), and an unsubscribe link. Up to 50 plain addresses per schedule.",
            "A schedule created or re-enabled mid-period first sends at that period's close; periods that closed while it was off are never sent. Changing the SLA's period or timezone moves the next send to the close of the running period under the new definition, and periods of the new definition that closed before the change are not sent. A period of the old definition that closed within the hour before the change and was not sent yet still goes out once, as it was defined.",
            "Unsubscribing takes one click from the link in the email (a confirm page, so link scanners cannot unsubscribe anyone) or from the mail client's own unsubscribe button. The address stays on the list, marked unsubscribed, and is skipped. Removing it and adding it again, or Re-subscribe on the Schedules tab, sends it the report again. Links work for a year; deleting the schedule revokes them.",
            "Every API replica runs the sender; each period is claimed once, and each recipient's copy is marked as sending only immediately before its message goes out, so no one gets a period twice. A failed send is retried after 5 minutes, 30 minutes, 2 hours and 6 hours, then marked failed; a refusal by the mail server fails at once. If the connection drops after the message was transmitted, or an API dies while a message is going out, that copy is marked uncertain and not sent again, since it may have arrived. Copies a stopped or slow replica had not started on yet are simply sent later. The schedule's error line says, per period still being delivered, how many recipients failed, are uncertain or are waiting for a retry.",
          ],
        },
        {
          type: "callout",
          tone: "info",
          title: "Needs mail settings on the API",
          text: "Scheduled emails need SMTP (`SMTP_HOST` and `SMTP_FROM`), `APP_BASE_URL` (the emails link there) and `PROBARA_SECRETS_KEY` (unsubscribe links are signed with it) on the API. Without any of them a schedule can be saved switched off but not enabled, and the Schedules tab says which setting is missing. See [Configuration](/docs/configuration/).",
        },
      ],
    },
    {
      id: "api",
      title: "API",
      blocks: [
        {
          type: "table",
          columns: ["Endpoint", "Purpose"],
          rows: [
            ["GET /api/v1/slas[?monitor_id=]", "List SLAs with the running period's status; filter to SLAs covering a monitor."],
            ["POST /api/v1/slas, GET/PATCH/DELETE /api/v1/slas/{id}", "Manage definitions (PATCH is partial; monitor_ids replaces the explicit set)."],
            ["GET /api/v1/slas/{id}/report", "Live report: period=2026-09 | 2026-Q3 | 2026-W39, or from=YYYY-MM-DD&to=YYYY-MM-DD, or neither for the running period. Add format=json|csv|pdf for a download."],
            ["GET/POST /api/v1/slas/{id}/reports", "List issued reports / issue one ({\"period\": \"2026-09\"}; empty body means the last closed period). 409 if already issued."],
            ["GET /api/v1/sla-reports/{id}[?format=]", "An issued report exactly as frozen."],
            ["GET/PUT/DELETE /api/v1/sla-report-schedules/{sla_id}", "The SLA's emailed report schedule. GET returns `schedule` (null when none) with `mail_ready` and `mail_problems`; PUT creates or replaces it (`enabled`, default true; `recipients`; `include_csv`; `resubscribe`)."],
            ["GET/POST /api/v1/report-unsubscribe", "Public, for the unsubscribe link: the token goes in the `X-Action-Token` header (or, for a mail client's one-click POST, the `token` query parameter). GET describes, POST unsubscribes."],
          ],
        },
        {
          type: "paragraph",
          text: "The report document carries `suspected_causes` (`outage_count`, `triaged_outage_count`, `pending_outage_count`, `unplanned_seconds`, and `buckets` with `kind`, `key`, `title`, `outage_count`, `alert_count`, `monitor_count`, `unplanned_seconds`, `confirmed_outage_count`, `corrected_outage_count`), and each attributed monitor outage carries `cause_kind`, `cause_key` and `cause_title`. `key` is the catalogue key for suspected causes and the bucket kind otherwise. All of these are absent on reports issued before the breakdown existed; treat them as optional. Reports computed since budget burn existed carry `burn`: `windows` (`key` 1h, 24h or period, `start`, `end`, `availability_pct`, `burn_rate`), `exhaustion` (exhausted, projected, not_projected or no_data), `projected_exhaustion_at` when projected, and `burn_down` (`date`, `end`, `consumed_seconds`, `remaining_pct`). It is absent on older issued reports.",
        },
      ],
    },
  ],
};

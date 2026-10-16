import type { DocPage } from "../types";

export const SLAS_PAGE: DocPage = {
  slug: "slas",
  group: "Use Probara",
  title: "SLA reporting",
  description: "Define availability targets for sets of monitors, track error budgets per calendar period, and export or freeze reports.",
  eyebrow: "Availability objectives",
  readingTime: "8 min read",
  keywords: ["SLA", "SLO", "error budget", "availability report", "uptime report", "MTTR", "MTTA", "PDF", "CSV", "suspected cause", "root cause"],
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
          text: "Every number comes from the monitors' state history (the timeline behind the monitor detail headline), never from counting check results. For each monitor, time in the period falls into exactly one class. Available: up, suspect, or degraded unless the SLA counts it as down. Unplanned down: down outside every maintenance window that covers the monitor, directly or through one of its groups. Planned down: down inside such a window. Paused. Unknown: no fresh evidence. Untracked: no state history yet.",
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
            "Response: outage count, mean time to recover (outages wholly inside the period with unplanned time), availability alerts triggered in the period, and mean time to acknowledge.",
            "Suspected causes: monitor outages bucketed by the [triage](/docs/alerting/#ai-triage) of their overlapping availability alerts, into named suspected causes, inconclusive, triage pending, not triaged and no alert (see [Suspected causes](#suspected-causes)). Columns: cause (marked * when it includes operator-labelled outages), outages, downtime, share of the attributed downtime, monitors, and the operator-confirmed and operator-corrected counts, with a note that these are observed symptoms, not confirmed root causes. Counts are monitor outages, not alerts, and the monitor-outage list gains a suspected-cause column. Like the PDF table, the panel and the column appear only when at least one monitor outage was triaged (suspected or inconclusive), so a workspace without auto-triage sees neither. Reports issued before this breakdown existed have none.",
          ],
        },
        {
          type: "paragraph",
          text: "Export any report as PDF (an A4 document with the headline, budget, daily strip, monitor table, suspected causes, outages and method note), CSV (summary, daily, monitor, outage and suspected-cause sections separated by blank lines, times in the SLA's timezone) or JSON (the full report document). The PDF's suspected-cause table appears under the same rule as the page's panel, only when at least one monitor outage was triaged (suspected or inconclusive), so a workspace without auto-triage never sends a table of \"Not triaged\". The CSV is the raw-data export, so its shape does not depend on AI settings: every report computed since the breakdown existed, AI on or off, gains `Cause key` and `Suspected cause` columns on its outage rows and a trailing suspected-cause section (attributed, triaged and pending outage counts, summed unplanned seconds, then one row per bucket), with Not triaged or No alert where nothing was triaged. Reports issued before the breakdown existed export exactly as they did. The monitor detail page uses the strictest target of the SLAs covering that monitor, falling back to 99.9%.",
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
            ["Triage pending (`pending`)", "The triage was queued or running, or the alert was still open, within the 6-hour triage lookback and in a workspace with auto-triage on, when the report was computed. With the installation's triage switched off (`AI_TRIAGE_ENABLED=false` or `LOCAL_AI_DISABLED=true`) an alert with no triage is never pending. The report adds a note saying how many outages were pending."],
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
          text: "A live report of a past period is recomputed on every view, so it changes when someone edits or deletes a maintenance window, wipes a monitor's history, or changes the SLA itself, and as triages complete and operators label or relabel alerts (which moves outages between suspected-cause buckets). Issue a report to freeze a closed period. Probara stores the complete report, including the SLA settings and resolved monitor list at that moment, and every later download of it renders from that snapshot. That includes each suspected cause's bucket and title, whatever the catalogue later calls the key; outages still pending when it was issued stay pending. Only closed calendar periods can be issued, once each. Deleting the SLA deletes its issued reports; purging a monitor does not.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Limits in this release",
          text: "Reports are not emailed or scheduled; issue and download them from the UI or API. Availability alerts count toward MTTA only when they are on a member monitor or on a group monitor listed explicitly on the SLA. The PDF uses built-in fonts, so characters outside Western European scripts may not render.",
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
          ],
        },
        {
          type: "paragraph",
          text: "The report document carries `suspected_causes` (`outage_count`, `triaged_outage_count`, `pending_outage_count`, `unplanned_seconds`, and `buckets` with `kind`, `key`, `title`, `outage_count`, `alert_count`, `monitor_count`, `unplanned_seconds`, `confirmed_outage_count`, `corrected_outage_count`), and each attributed monitor outage carries `cause_kind`, `cause_key` and `cause_title`. `key` is the catalogue key for suspected causes and the bucket kind otherwise. All of these are absent on reports issued before the breakdown existed; treat them as optional.",
        },
      ],
    },
  ],
};

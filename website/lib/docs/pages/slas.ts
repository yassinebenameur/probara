import type { DocPage } from "../types";

export const SLAS_PAGE: DocPage = {
  slug: "slas",
  group: "Use Probara",
  title: "SLA reporting",
  description: "Define availability targets for sets of monitors, track error budgets per calendar period, and export or freeze reports.",
  eyebrow: "Availability objectives",
  readingTime: "7 min read",
  keywords: ["SLA", "SLO", "error budget", "availability report", "uptime report", "MTTR", "MTTA", "PDF", "CSV"],
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
          ],
        },
        {
          type: "paragraph",
          text: "Export any report as PDF (an A4 document with the headline, budget, daily strip, monitor table, outages and method note), CSV (summary, daily, monitor and outage sections separated by blank lines, times in the SLA's timezone) or JSON (the full report document). The monitor detail page uses the strictest target of the SLAs covering that monitor, falling back to 99.9%.",
        },
      ],
    },
    {
      id: "issued",
      title: "Issued reports",
      blocks: [
        {
          type: "paragraph",
          text: "A live report of a past period is recomputed on every view, so it changes when someone edits or deletes a maintenance window, wipes a monitor's history, or changes the SLA itself. Issue a report to freeze a closed period. Probara stores the complete report, including the SLA settings and resolved monitor list at that moment, and every later download of it renders from that snapshot. Only closed calendar periods can be issued, once each. Deleting the SLA deletes its issued reports; purging a monitor does not.",
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
      ],
    },
  ],
};

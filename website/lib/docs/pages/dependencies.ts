import type { DocPage } from "../types";

export const DEPENDENCIES_PAGE: DocPage = {
  slug: "dependencies",
  group: "Use Probara",
  title: "Dependencies and analytics",
  description:
    "Model upstream relationships, interpret root-cause annotations, and use dashboards and rollups.",
  eyebrow: "Diagnosis",
  readingTime: "21 min read",
  keywords: [
    "dependency graph",
    "root cause",
    "dashboard",
    "analytics",
    "retention",
  ],
  sections: [
    {
      id: "dependency-model",
      title: "Dependency model",
      blocks: [
        {
          type: "paragraph",
          text:
            "A dependency edge means a downstream monitor relies on an upstream monitor. In storage and API operations, `monitor_id` is downstream and `depends_on_id` is upstream.",
        },
        {
          type: "code",
          language: "text",
          title: "Direction",
          code:
            "Checkout API (downstream) ──depends on──► PostgreSQL (upstream)\n\nGraph edge response:\n{ \"from\": \"<checkout-id>\", \"to\": \"<postgres-id>\" }",
        },
        {
          type: "callout",
          tone: "warning",
          title: "The arrow describes reliance, not data flow",
          text:
            "Read `A → B` as “A depends on B.” If B is down, A may fail as a consequence and receive B as root-cause context.",
        },
      ],
    },
    {
      id: "validation",
      title: "Dependency validation",
      blocks: [
        {
          type: "list",
          items: [
            "Both monitors must be live resources in the authenticated tenant.",
            "A monitor cannot depend on itself.",
            "Any direct or transitive cycle is rejected with HTTP `409` and the `dependency_cycle` error code.",
            "Adding an existing edge is deduplicated instead of creating parallel edges.",
            "Removing a nonexistent edge is idempotent.",
          ],
        },
        {
          type: "code",
          language: "text",
          title: "Rejected cycle",
          code:
            "A depends on B\nB depends on C\nC depends on A   ← rejected",
        },
        {
          type: "paragraph",
          text:
            "The dependency graph response contains only monitors that participate in at least one edge. Node records include ID, name, type, current state, and last state change; an unconnected monitor is absent rather than returned as an isolated node.",
        },
      ],
    },
    {
      id: "root-cause",
      title: "Root-cause annotation",
      blocks: [
        {
          type: "paragraph",
          text:
            "When an [availability alert](/docs/alerting/#availability) opens or is reevaluated, Probara traverses upstream dependencies and selects the deepest currently down candidate. When candidates are at the same depth, the one that became down first wins.",
        },
        {
          type: "code",
          language: "text",
          title: "Example",
          code:
            "Public API (down)\n└── Checkout service (down)\n    └── Primary database (down)\n\nAnnotated root cause: Primary database",
        },
        {
          type: "paragraph",
          text:
            "The annotation is updated as states change and cleared when no down upstream candidate remains. It is evidence for prioritization, not proof of causality.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Downstream alerts are not suppressed",
          text:
            "Dependencies enrich each downstream availability alert with likely cause; they do not prevent that alert or its notifications. Use [monitor-group alert rollup](/docs/alerting/#group-rollup) when the intended behavior is one alert for a member set.",
        },
      ],
    },
    {
      id: "suggestions",
      title: "AI-assisted dependency suggestions",
      blocks: [
        {
          type: "paragraph",
          text:
            "With an effective [tenant AI configuration](/docs/administration/#ai-settings), the suggestion endpoint analyzes recent alert co-occurrence and asks the configured model for plausible upstream relationships. The current analysis window is 30 days, co-firing events are paired within 10 minutes, and a candidate pair needs at least two observations.",
        },
        {
          type: "table",
          columns: ["Bound", "Current limit"],
          rows: [
            ["Monitors considered", "Up to 250"],
            ["Candidate pairs sent for analysis", "Up to 50"],
            ["Minimum observed co-firings", "2"],
            ["Historical window", "30 days"],
          ],
        },
        {
          type: "paragraph",
          text:
            "Suggestions include a reason, confidence, model information, and analyzed-pair context. The endpoint is compute-only: it does not write an edge until an operator explicitly accepts and creates it.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Correlation is not topology",
          text:
            "Services can fail together because of a shared unmodeled dependency, deployment, or network partition. Review suggested direction with service owners and rely on cycle validation before saving.",
        },
      ],
    },
    {
      id: "dashboard-overview",
      title: "Dashboard overview",
      blocks: [
        {
          type: "paragraph",
          text:
            "The overview combines monitor and alert totals, state distribution, historical trend, 24-hour activity, platform operational health, problem monitors, recent failures, recent alerts, and available tags for the authenticated tenant.",
        },
        {
          type: "table",
          columns: ["Query variable", "Behavior"],
          rows: [
            [
              "`range`",
              "`1h`, `24h`, `7d`, `30d`, `90d`, or `365d`; defaults to `24h`",
            ],
            [
              "`tag`",
              "Repeatable tag filter used to narrow the tenant dashboard",
            ],
            [
              "`failures_limit`",
              "Recent failure count, constrained to a maximum of 50",
            ],
            [
              "`alerts_limit`",
              "Recent alert count, constrained to a maximum of 50",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Problem-monitor, recent-failure, recent-alert, and summary endpoints are also available independently so clients can refresh a focused panel without reloading the complete overview.",
        },
      ],
    },
    {
      id: "service-summary",
      title: "Service summary and groups",
      blocks: [
        {
          type: "paragraph",
          text:
            "Tenant settings define a curated ordered list of dashboard group tags. The summary uses those tags to build service rows and adds an ungrouped bucket for monitors that do not match. Each row includes member state, uptime/attention signals, and a worst-state summary.",
        },
        {
          type: "paragraph",
          text:
            "The group-sparkline endpoint returns the historical series for one dashboard group, allowing a client to defer the more expensive time-series request until the group is visible or expanded.",
        },
        {
          type: "callout",
          tone: "info",
          title: "Dashboard groups are tag-driven views",
          text:
            "They are not `group` monitor resources, notification rollups, or dependency nodes. Changing curated tags affects presentation but does not alter monitor execution or alerts.",
        },
      ],
    },
    {
      id: "monitor-analytics",
      title: "Per-monitor analytics",
      blocks: [
        {
          type: "table",
          columns: ["Response area", "Included data"],
          rows: [
            [
              "Summary",
              "Uptime/SLA fields, downtime duration, check counts, and latest state",
            ],
            [
              "Latency",
              "Average, median, p95, and latest latency where the monitor produces latency",
            ],
            ["Series", "Time-bucketed status and performance data"],
            ["Downtime periods", "Detected periods of unavailable state"],
            [
              "Coverage",
              "Data source, coverage start, and `is_partial` when retention does not cover the entire request",
            ],
          ],
        },
        {
          type: "paragraph",
          text:
            "Allowed ranges are `1h`, `6h`, `24h`, `7d`, `30d`, `90d`, and `365d`, with `24h` as the default. Not every monitor type emits every metric, so consumers should handle absent latency or TLS values rather than displaying zero.",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Unknown is not automatically downtime",
          text:
            "Use the returned summary and coverage metadata rather than deriving uptime by treating every non-up presentation state as a failed sample. Missing, platform-source, and partially retained data have separate semantics.",
        },
      ],
    },
    {
      id: "rollups",
      title: "Rollups and data coverage",
      blocks: [
        {
          type: "paragraph",
          text:
            "The scheduler builds hourly and daily rollups behind a high-water cursor. Long-range analytics combine completed rollups with a raw-result tail so recent checks appear before the next aggregation interval completes.",
        },
        {
          type: "definitions",
          items: [
            {
              term: "Raw source",
              description:
                "Fine-grained recent check results, suitable for short ranges and detailed failure inspection.",
            },
            {
              term: "Rollup source",
              description:
                "Aggregated hourly or daily buckets used for efficient longer-range queries.",
            },
            {
              term: "Partial coverage",
              description:
                "The requested range begins before the oldest retained usable result or rollup.",
            },
          ],
        },
        {
          type: "paragraph",
          text:
            "Results marked with `result_source=platform` are excluded from standard monitor analytics. This applies to expired-job results; generated [stale-agent/push state](/docs/agents/#freshness) records are written with `result_source=monitor` and are counted like ordinary results.",
        },
      ],
    },
    {
      id: "retention",
      title: "Retention",
      blocks: [
        {
          type: "paragraph",
          text:
            "Tenant telemetry retention is `0` for unlimited retention or a value from 30 through 3,650 days. Cleanup covers check results, mesh history, and rollup data according to the scheduler's [retention tasks](/docs/operations/#retention).",
        },
        {
          type: "callout",
          tone: "warning",
          title: "Lower retention changes historical answers",
          text:
            "After pruning, a longer requested range can still be syntactically valid but return partial coverage. Export required evidence before shortening retention, and show `is_partial` in external reporting.",
        },
      ],
    },
    {
      id: "diagnostic-workflow",
      title: "Diagnostic workflow",
      blocks: [
        {
          type: "list",
          ordered: true,
          items: [
            "Start with the problem-monitor and recent-alert views to determine current impact.",
            "Open the dependency graph and validate whether the annotated upstream monitor is genuinely causal.",
            "Compare raw recent failures across locations; a degraded regional result can precede a quorum-confirmed outage.",
            "Inspect per-monitor latency, downtime periods, and coverage metadata.",
            "Check maintenance and deployment history before concluding that correlated alerts share a dependency.",
            "Attach relevant alerts and monitors to an [incident](/docs/alerting/#incidents), preserving hypotheses as internal notes.",
          ],
        },
      ],
    },
  ],
};

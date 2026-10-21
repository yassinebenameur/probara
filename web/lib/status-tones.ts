// What colour a state or a number gets. One rule per concept, so an amber
// value means the same thing on every page. Text formatting lives in
// lib/format.ts. Tone names match the Pill primitive's (components/ui/Pill.tsx).

import type { AlertStatus, IncidentSeverity, IncidentState, MetricRule } from './types';
import type { MonitorDisplayStatus } from './monitor-utils';

export type StatusTone = 'success' | 'danger' | 'warning' | 'info' | 'neutral';

// ── Uptime ───────────────────────────────────────────────────────────────

export type UptimeTone = 'good' | 'warn' | 'bad' | 'none';

/** Default target when no SLA covers the value (matches lib/sla's default band). */
export const DEFAULT_UPTIME_TARGET = 99;

/** How far below the target a value is still a warning rather than a breach. */
export const UPTIME_WARN_BAND = 4;

/**
 * good = at or above the target; warn = within UPTIME_WARN_BAND points below
 * it; bad = further below; none = no data. With the default target that is
 * the historical 99 / 95 split. Pass the SLA target when one applies.
 */
export function uptimeTone(pct: number | null | undefined, target: number = DEFAULT_UPTIME_TARGET): UptimeTone {
  if (pct === null || pct === undefined || !Number.isFinite(pct)) return 'none';
  if (pct + 1e-9 >= target) return 'good';
  if (pct >= target - UPTIME_WARN_BAND) return 'warn';
  return 'bad';
}

/** Text colour for an uptime figure. Light-theme safe (the -300 steps darken). */
export const UPTIME_TONE_TEXT: Record<UptimeTone, string> = {
  good: 'text-emerald-300',
  warn: 'text-amber-300',
  bad: 'text-rose-300',
  none: 'text-slate-500',
};

/**
 * An empty trace slot or a no-data bucket: a hollow bar, same size as the
 * others, whose 1px edge (slate-500 at 75%) keeps >= 3:1 on glass, wells and
 * hovered or checked rows in both themes (WCAG 1.4.11). Hollow rather than a
 * faint fill, so a gap reads as a gap and never as the solid grey "not run"
 * bar. The trace's text alternative carries the count.
 */
export const NO_DATA_BAR = 'bg-transparent shadow-[inset_0_0_0_1px_rgb(var(--tw-slate-500)/0.75)]';

/** Fill for bars, dots and trace segments. */
export const UPTIME_TONE_BAR: Record<UptimeTone, string> = {
  good: 'bg-emerald-500',
  warn: 'bg-amber-500',
  bad: 'bg-rose-500',
  none: NO_DATA_BAR,
};

/** CSS colour for charts (recharts strokes, SVG fills). */
export const UPTIME_TONE_VAR: Record<UptimeTone, string> = {
  good: 'var(--success)',
  warn: 'var(--warning)',
  bad: 'var(--danger)',
  none: 'var(--text-muted)',
};

export const UPTIME_TONE_PILL: Record<UptimeTone, StatusTone> = {
  good: 'success',
  warn: 'warning',
  bad: 'danger',
  none: 'neutral',
};

// ── Check buckets ────────────────────────────────────────────────────────

/**
 * Tone of a bucket of checks (a trace bar covering an hour or a day). A
 * bucket's ratio is a check success ratio, not availability: a monitor
 * failing from one of three locations, or one check in sixty, is still
 * available. So it is never toned against the availability target: no
 * failed check = good, some = warn, half or more = bad, no checks = none.
 * Single-check traces (CheckTrace) agree: one failed check is 1 of 1.
 */
export function failedChecksTone(failed: number, total: number): UptimeTone {
  if (!Number.isFinite(total) || total <= 0) return 'none';
  if (!Number.isFinite(failed) || failed <= 0) return 'good';
  if (failed * 2 >= total) return 'bad';
  return 'warn';
}

// ── Host utilization ─────────────────────────────────────────────────────

/** Alert level assumed for a utilization metric without a metric rule (ratio). */
export const DEFAULT_UTILIZATION_ALERT = 0.9;

/** How close to an alert threshold a value turns amber (ratio, 10 points). */
export const UTILIZATION_WARN_BAND = 0.1;

/**
 * The rules that cover one series: same metric, and no attribute filter the
 * series contradicts (a filter key the series does not carry still matches,
 * as the query may have aggregated it away).
 */
export function matchingMetricRules(
  metricName: string,
  attributes: Record<string, string> | undefined,
  rules: MetricRule[] | null | undefined
): MetricRule[] {
  return (rules ?? []).filter(
    (rule) =>
      rule.metric_name === metricName &&
      Object.entries(rule.attribute_filters ?? {}).every(
        ([key, value]) => attributes?.[key] === undefined || attributes[key] === value
      )
  );
}

/**
 * Tone of an agent utilization reading (native ratio 0-1), from the
 * monitor's own metric_rules: the rules for `metricName` whose attribute
 * filters do not contradict the series' attributes. bad = the rule would
 * fire (at or past its threshold), warn = within UTILIZATION_WARN_BAND of
 * it, good = below. Without a matching rule, `>= DEFAULT_UTILIZATION_ALERT`
 * stands in. Several matching rules: the worst tone wins.
 */
export function utilizationTone(
  ratio: number | null | undefined,
  metricName: string,
  attributes: Record<string, string> | undefined,
  rules: MetricRule[] | null | undefined
): UptimeTone {
  if (ratio === null || ratio === undefined || !Number.isFinite(ratio)) return 'none';
  const matching = matchingMetricRules(metricName, attributes, rules);
  const thresholds: Pick<MetricRule, 'operator' | 'threshold'>[] =
    matching.length > 0 ? matching : [{ operator: '>=', threshold: DEFAULT_UTILIZATION_ALERT }];
  const rank: Record<UptimeTone, number> = { none: 0, good: 1, warn: 2, bad: 3 };
  let worst: UptimeTone = 'good';
  for (const { operator, threshold } of thresholds) {
    // Distance to the firing side: <= 0 means the rule fires.
    const distance = operator === '<=' ? ratio - threshold : threshold - ratio;
    const tone: UptimeTone = distance <= 1e-9 ? 'bad' : distance <= UTILIZATION_WARN_BAND + 1e-9 ? 'warn' : 'good';
    if (rank[tone] > rank[worst]) worst = tone;
  }
  return worst;
}

// ── Monitor status ───────────────────────────────────────────────────────

export const MONITOR_STATUS_LABEL: Record<MonitorDisplayStatus, string> = {
  up: 'Up',
  down: 'Down',
  degraded: 'Degraded',
  paused: 'Paused',
  maintenance: 'Maintenance',
  unknown: 'No data',
};

export const MONITOR_STATUS_TONE: Record<MonitorDisplayStatus, StatusTone> = {
  up: 'success',
  down: 'danger',
  degraded: 'warning',
  paused: 'neutral',
  maintenance: 'info',
  unknown: 'neutral',
};

/** Status dot fill. */
export const MONITOR_STATUS_DOT: Record<MonitorDisplayStatus, string> = {
  up: 'bg-emerald-500',
  down: 'bg-rose-500',
  degraded: 'bg-amber-500',
  paused: 'bg-slate-500',
  maintenance: 'bg-sky-500',
  unknown: 'bg-slate-500',
};

// ── Check results ────────────────────────────────────────────────────────

export type CheckResultStatus = 'success' | 'failure' | 'error' | 'degraded';

/** One label map for a single check's outcome (overview table, history tab). */
export const CHECK_RESULT_LABEL: Record<CheckResultStatus, string> = {
  success: 'OK',
  degraded: 'Degraded',
  failure: 'Failed',
  error: 'Error',
};

export const CHECK_RESULT_TONE: Record<CheckResultStatus, StatusTone> = {
  success: 'success',
  degraded: 'warning',
  failure: 'danger',
  error: 'danger',
};

/** Bar fill for one check in a trace. */
export const CHECK_RESULT_BAR: Record<CheckResultStatus, string> = {
  success: 'bg-emerald-500',
  degraded: 'bg-amber-500',
  failure: 'bg-rose-500',
  error: 'bg-rose-500',
};

export function checkResultTone(status: string | null | undefined): StatusTone {
  return CHECK_RESULT_TONE[status as CheckResultStatus] ?? 'neutral';
}

export function checkResultLabel(status: string | null | undefined): string {
  return CHECK_RESULT_LABEL[status as CheckResultStatus] ?? (status || 'Unknown');
}

// ── Alerts and incidents ─────────────────────────────────────────────────

export const ALERT_STATUS_LABEL: Record<AlertStatus, string> = {
  active: 'Active',
  acknowledged: 'Acknowledged',
  resolved: 'Resolved',
};

export function alertStatusTone(status: AlertStatus | string | null | undefined): StatusTone {
  switch (status) {
    case 'active':
      return 'danger';
    case 'acknowledged':
      return 'warning';
    case 'resolved':
      return 'success';
    default:
      return 'neutral';
  }
}

export const INCIDENT_STATE_LABEL: Record<IncidentState, string> = {
  investigating: 'Investigating',
  identified: 'Identified',
  monitoring: 'Monitoring',
  resolved: 'Resolved',
};

export function incidentStateTone(state: IncidentState | string | null | undefined): StatusTone {
  switch (state) {
    case 'resolved':
      return 'success';
    case 'monitoring':
      return 'warning';
    case 'identified':
    case 'investigating':
      return 'danger';
    default:
      return 'neutral';
  }
}

export const INCIDENT_SEVERITY_LABEL: Record<IncidentSeverity, string> = {
  critical: 'Critical',
  high: 'High',
  medium: 'Medium',
  low: 'Low',
};

/** Triage order: the worst first. */
export const INCIDENT_SEVERITY_RANK: Record<IncidentSeverity, number> = {
  critical: 0,
  high: 1,
  medium: 2,
  low: 3,
};

export function incidentSeverityTone(severity: IncidentSeverity | string | null | undefined): StatusTone {
  switch (severity) {
    case 'critical':
    case 'high':
      return 'danger';
    case 'medium':
      return 'warning';
    default:
      return 'neutral';
  }
}

export function incidentSeverityLabel(severity: IncidentSeverity | string | null | undefined): string {
  return INCIDENT_SEVERITY_LABEL[severity as IncidentSeverity] ?? (severity || 'Unknown');
}

// ── AI ───────────────────────────────────────────────────────────────────

export type AIConfidence = 'low' | 'medium' | 'high';

/**
 * Tone of an AI confidence level (incident analysis, alert triage,
 * dependency suggestions). Never
 * danger or warning: a confident suspected cause is not a worse state, and
 * low confidence is a hint, not an error.
 */
export const AI_CONFIDENCE_TONE: Record<AIConfidence, StatusTone> = {
  high: 'success',
  medium: 'info',
  low: 'neutral',
};

export function aiConfidenceTone(confidence: AIConfidence | string | null | undefined): StatusTone {
  return AI_CONFIDENCE_TONE[confidence as AIConfidence] ?? 'neutral';
}

// ── Tone → classes ───────────────────────────────────────────────────────

/** Text colour per tone, for figures that carry a state without a pill. */
export const TONE_TEXT: Record<StatusTone, string> = {
  success: 'text-emerald-300',
  danger: 'text-rose-300',
  warning: 'text-amber-300',
  info: 'text-sky-300',
  neutral: 'text-slate-300',
};

/** Dot fill per tone. */
export const TONE_DOT: Record<StatusTone, string> = {
  success: 'bg-emerald-500',
  danger: 'bg-rose-500',
  warning: 'bg-amber-500',
  info: 'bg-sky-500',
  neutral: 'bg-slate-500',
};

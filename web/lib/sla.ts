// SLA definitions and reports — the wire types of /v1/slas and /v1/sla-reports
// plus the pure helpers the pages share (formatting, tone, target lookup).
// Numbers are computed server-side (shared/analytics IntegrateTimeline); the UI
// never re-derives availability.

export type SlaAggregation = 'serial' | 'mean';
export type SlaPeriod = 'weekly' | 'monthly' | 'quarterly';
export type SlaExportFormat = 'json' | 'csv' | 'pdf';

export interface SlaStatus {
  period_key: string;
  period_start: string;
  period_end: string;
  monitor_count: number;
  has_data: boolean;
  availability_pct: number | null;
  coverage_pct: number;
  met: boolean | null;
  budget_remaining_pct: number | null;
}

export interface Sla {
  id: string;
  tenant_id: string;
  name: string;
  description: string;
  target_pct: number;
  aggregation: SlaAggregation;
  period: SlaPeriod;
  timezone: string;
  degraded_counts_as_down: boolean;
  tags: string[];
  monitor_ids: string[];
  created_at: string;
  updated_at: string;
  current?: SlaStatus;
}

export interface SlaInput {
  name: string;
  description: string;
  target_pct: number;
  aggregation: SlaAggregation;
  period: SlaPeriod;
  timezone: string;
  degraded_counts_as_down: boolean;
  tags: string[];
  monitor_ids: string[];
}

export interface SlaReportOutage {
  monitor_id: string | null;
  monitor_name: string;
  start: string;
  end: string;
  duration_seconds: number;
  planned_seconds: number;
  unplanned_seconds: number;
  started_before: boolean;
  ongoing: boolean;
  /** The outage's suspected-cause bucket; only on per-monitor outages with unplanned time, on reports computed since causes exist. */
  cause_kind?: SlaCauseKind;
  /** Catalogue key for suspected causes, else the kind. */
  cause_key?: string;
  cause_title?: string;
}

/** Suspected-cause buckets (api models.SLACause*), in display order after the named causes. */
export type SlaCauseKind = 'suspected' | 'inconclusive' | 'pending' | 'untriaged' | 'no_alert';

export interface SlaReportCauseBucket {
  kind: SlaCauseKind;
  /** Catalogue key for suspected causes, else the kind. */
  key: string;
  /** Frozen when the report is computed, so an issued report keeps its wording. */
  title: string;
  outage_count: number;
  alert_count: number;
  monitor_count: number;
  unplanned_seconds: number;
  confirmed_outage_count: number;
  corrected_outage_count: number;
}

/**
 * Monitor outages rolled up by the triage of their overlapping availability
 * alerts. Counts are monitor outages, not alerts; unplanned_seconds is summed
 * per monitor, so it can exceed the service downtime.
 */
export interface SlaReportSuspectedCauses {
  outage_count: number;
  triaged_outage_count: number;
  pending_outage_count: number;
  unplanned_seconds: number;
  /** Already ordered by the API: named causes by downtime, then the fixed buckets. */
  buckets: SlaReportCauseBucket[];
}

export interface SlaReportMonitor {
  id: string;
  name: string;
  type: string;
  has_data: boolean;
  availability_pct: number | null;
  coverage_pct: number;
  available_seconds: number;
  unplanned_down_seconds: number;
  planned_down_seconds: number;
  paused_seconds: number;
  unknown_seconds: number;
  untracked_seconds: number;
  timeline_start: string | null;
  outage_count: number;
}

export interface SlaReportDay {
  date: string;
  start: string;
  end: string;
  has_data: boolean;
  availability_pct: number | null;
  coverage_pct: number;
  down_seconds: number;
}

export interface SlaReport {
  sla_id: string;
  definition: {
    name: string;
    description: string;
    target_pct: number;
    aggregation: SlaAggregation;
    period: SlaPeriod;
    timezone: string;
    degraded_counts_as_down: boolean;
    tags: string[];
  };
  period: {
    key: string;
    start: string;
    end: string;
    effective_end: string;
    is_closed: boolean;
    is_custom: boolean;
    previous_key?: string;
    next_key?: string;
  };
  generated_at: string;
  issued?: { id: string; issued_at: string; issued_by: string };
  summary: {
    has_data: boolean;
    availability_pct: number | null;
    coverage_pct: number;
    met: boolean | null;
    window_seconds: number;
    available_seconds: number;
    down_seconds: number;
    excluded_seconds: number;
    eligible_seconds: number;
  };
  budget: {
    allowed_seconds: number;
    consumed_seconds: number;
    remaining_seconds: number;
    remaining_pct: number | null;
    allowed_full_period_seconds: number;
  };
  daily: SlaReportDay[];
  monitors: SlaReportMonitor[];
  service_outages: SlaReportOutage[];
  outages: SlaReportOutage[];
  outages_truncated: boolean;
  response: {
    outage_count: number;
    mttr_seconds: number | null;
    alert_count: number;
    acknowledged_count: number;
    mtta_seconds: number | null;
  };
  notes: string[];
  /** Absent on reports issued before causes existed: render nothing for those. */
  suspected_causes?: SlaReportSuspectedCauses;
}

export interface SlaReportRef {
  id: string;
  sla_id: string;
  period_key: string;
  period_start: string;
  period_end: string;
  availability_pct: number | null;
  target_pct: number;
  met: boolean | null;
  issued_at: string;
  issued_by: string;
}

/** Selects a report: a period key, a custom local-date range, or neither (running period). */
export interface SlaReportQuery {
  period?: string;
  from?: string;
  to?: string;
}

export const SLA_TARGET_PRESETS = [99, 99.5, 99.9, 99.95, 99.99] as const;

/** Used where no SLA covers a monitor. */
export const DEFAULT_SLA_TARGET = 99.9;

export const SLA_PERIOD_LABELS: Record<SlaPeriod, string> = {
  weekly: 'Weekly',
  monthly: 'Monthly',
  quarterly: 'Quarterly',
};

export const SLA_AGGREGATION_LABELS: Record<SlaAggregation, string> = {
  serial: 'Serial (all must be up)',
  mean: 'Mean of monitors',
};

/** The aggregation as a plain rule, for compact sublines. */
export const SLA_AGGREGATION_SHORT: Record<SlaAggregation, string> = {
  serial: 'All must be up',
  mean: 'Mean of monitors',
};

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

interface DayParts {
  y: number;
  m: number; // 0-based
  d: number;
}

// Calendar day of an instant in the SLA's own timezone: periods are cut at
// local midnight there, so the browser's zone could shift a boundary by a day.
function dayIn(date: Date, timeZone: string): DayParts {
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone,
      year: 'numeric',
      month: 'numeric',
      day: 'numeric',
    }).formatToParts(date);
    const get = (type: string) => Number(parts.find((p) => p.type === type)?.value);
    return { y: get('year'), m: get('month') - 1, d: get('day') };
  } catch {
    return { y: date.getFullYear(), m: date.getMonth(), d: date.getDate() };
  }
}

/**
 * An instant in the SLA's own timezone, spelled like lib/format (`28 Sep 2026`,
 * `2 Oct 2026, 15:19`): built from parts and the shared month table, because
 * Intl's en-GB short month reads "Sept".
 */
export function formatSlaDate(iso: string, timeZone: string, withTime = true): string | null {
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone,
      year: 'numeric',
      month: 'numeric',
      day: 'numeric',
      ...(withTime ? { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' } : {}),
    } as Intl.DateTimeFormatOptions).formatToParts(new Date(iso));
    const get = (type: string) => parts.find((p) => p.type === type)?.value ?? '';
    const day = `${Number(get('day'))} ${MONTHS[Number(get('month')) - 1]} ${get('year')}`;
    return withTime ? `${day}, ${get('hour')}:${get('minute')}` : day;
  } catch {
    return null;
  }
}

/**
 * A period as readable dates instead of its key: `5–11 Oct`, `29 Sep – 5 Oct`,
 * `1 Oct – 31 Dec`; the year only when it is not the current one. `end` is
 * exclusive (the API's half-open span), so the last day shown is the one
 * before it, in the SLA's timezone.
 */
export function formatSlaPeriodRange(start: string, end: string, timeZone: string, now: Date = new Date()): string {
  const startDate = new Date(start);
  const endDate = new Date(end);
  if (Number.isNaN(startDate.getTime()) || Number.isNaN(endDate.getTime())) return '—';
  const s = dayIn(startDate, timeZone);
  const e = dayIn(new Date(endDate.getTime() - 1), timeZone);
  const currentYear = dayIn(now, timeZone).y;
  if (s.y !== e.y) {
    return `${s.d} ${MONTHS[s.m]} ${s.y} – ${e.d} ${MONTHS[e.m]} ${e.y}`;
  }
  const year = e.y === currentYear ? '' : ` ${e.y}`;
  if (s.m === e.m) {
    return s.d === e.d ? `${s.d} ${MONTHS[s.m]}${year}` : `${s.d}–${e.d} ${MONTHS[e.m]}${year}`;
  }
  return `${s.d} ${MONTHS[s.m]} – ${e.d} ${MONTHS[e.m]}${year}`;
}

export function slaReportQueryString(query: SlaReportQuery, format?: SlaExportFormat): string {
  const params = new URLSearchParams();
  if (query.period) params.set('period', query.period);
  if (query.from) params.set('from', query.from);
  if (query.to) params.set('to', query.to);
  if (format) params.set('format', format);
  const s = params.toString();
  return s ? `?${s}` : '';
}

/** Availability with enough precision to tell 99.9 from 99.95; "—" without data. */
export function formatSlaPct(value: number | null | undefined, digits = 3): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return '—';
  return `${value.toFixed(digits)}%`;
}

/** Target as entered: 99.9 stays "99.9%", 99.95 stays "99.95%". */
export function formatTarget(value: number): string {
  return `${Number(value.toFixed(4))}%`;
}

/** "2h 05m", "43m 49s", "12s"; negative durations keep their sign. */
export function formatSlaDuration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds)) return '—';
  const sign = seconds < 0 ? '-' : '';
  const s = Math.round(Math.abs(seconds));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const pad = (n: number) => String(n).padStart(2, '0');
  if (d > 0) return `${sign}${d}d ${pad(h)}h ${pad(m)}m`;
  if (h > 0) return `${sign}${h}h ${pad(m)}m`;
  if (m > 0) return `${sign}${m}m ${pad(sec)}s`;
  return `${sign}${sec}s`;
}

export type SlaTone = 'good' | 'warn' | 'bad' | 'none';

/**
 * Traffic-light for an availability against a target: at/above target is good,
 * below it bad, and "warn" for a running period whose budget is nearly spent.
 */
export function slaTone(met: boolean | null | undefined, budgetRemainingPct?: number | null): SlaTone {
  if (met === null || met === undefined) return 'none';
  if (!met) return 'bad';
  if (budgetRemainingPct !== null && budgetRemainingPct !== undefined && budgetRemainingPct < 25) return 'warn';
  return 'good';
}

/** Day colour for the daily strip, mirroring the PDF: target, ≥99%, below, no data. */
export function dayTone(pct: number | null, target: number): SlaTone {
  if (pct === null) return 'none';
  if (pct + 1e-9 >= target) return 'good';
  if (pct >= 99) return 'warn';
  return 'bad';
}

/** The strictest target among the SLAs covering a monitor, else the default. */
export function strictestTarget(slas: Pick<Sla, 'target_pct'>[]): number {
  if (slas.length === 0) return DEFAULT_SLA_TARGET;
  return Math.max(...slas.map((s) => s.target_pct));
}

/** Validates the form before it reaches the API; returns a message or null. */
export function slaInputError(input: SlaInput): string | null {
  if (!input.name.trim()) return 'Name is required.';
  if (!Number.isFinite(input.target_pct) || input.target_pct <= 0 || input.target_pct >= 100) {
    return 'Target must be between 0 and 100 (exclusive).';
  }
  if (input.monitor_ids.length === 0 && input.tags.length === 0) return 'Select at least one monitor or tag.';
  return null;
}

/** The outage table grows a Cause column only when a row carries one, so older issued reports look unchanged. */
export function hasOutageCauses(outages: Pick<SlaReportOutage, 'cause_title'>[]): boolean {
  return outages.some((o) => Boolean(o.cause_title));
}

/** A bucket's share of the attributed downtime, as the PDF prints it; "—" when nothing was attributed. */
export function causeShare(bucketSeconds: number, totalSeconds: number): string {
  if (!(totalSeconds > 0)) return '—';
  return `${((bucketSeconds / totalSeconds) * 100).toFixed(1)}%`;
}

/**
 * The note under the suspected-cause breakdown. Same wording as the PDF's
 * causesCaveat (api/internal/services/slas/export.go); change them together.
 * The triage lookback is left unnumbered here: it lives in shared/ai
 * (TriageLookback) and the docs, not in the UI.
 */
export function slaCausesCaveat(c: Pick<SlaReportSuspectedCauses, 'outage_count' | 'triaged_outage_count'>): string {
  return (
    "Each monitor outage takes the suspected cause of its monitor's availability alerts that overlapped it, " +
    "from Probara's local AI triage, or an operator's label on it (* marks causes that include operator-labelled outages). " +
    'Suspected causes are observed symptoms, not confirmed root causes. ' +
    'Not triaged: the alert was never triaged, for example automatic triage was off at the time, the alert resolved within the settle wait, ' +
    "the monitor was in its triage cooldown, the workspace's hourly triage limit or the platform backlog was reached, " +
    'the alert was older than the triage lookback, triage was switched off, or the local AI was unavailable. ' +
    'Triage pending: triage was still running or the alert still open when the report was computed. ' +
    'No alert: no availability alert overlapped the outage. ' +
    'Counts are monitor outages, not alerts, and downtime is summed per monitor, so it can exceed the service downtime when monitors fail together. ' +
    `${c.triaged_outage_count} of ${c.outage_count} monitor outages had a triaged alert.`
  );
}

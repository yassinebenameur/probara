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
    /** S-U8: (1 − availability) ÷ (1 − target); 1 spends the budget at the allowed pace. Absent without data and on older issued reports. */
    burn_rate?: number;
    /** Running periods only: when the full-period budget runs out at the pace so far, if before the period ends. */
    projected_exhaustion_at?: string;
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
  /** S-U8 burn-down, one point per day; absent on reports issued before it existed. */
  burn_down?: SlaBurnPoint[];
}

/** Error budget at the end of one day: consumed since the period start, and what is left of the full period's allowance. */
export interface SlaBurnPoint {
  date: string;
  consumed_seconds: number;
  /** Negative once overspent; null while nothing was observed yet. */
  remaining_pct: number | null;
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

/**
 * Whether the report shows its suspected-cause panel and the monitor-outage
 * Cause column: the PDF's rule (api/internal/services/slas/export.go), so a
 * workspace without auto-triage never sees a table of "Not triaged", and a
 * report issued before causes existed looks unchanged. Only the CSV carries
 * the causes regardless.
 */
export function showSuspectedCauses(r: Pick<SlaReport, 'suspected_causes'>): boolean {
  return !!r.suspected_causes && r.suspected_causes.triaged_outage_count > 0;
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
    "The AI's hypothesis counts as a suspected cause only at medium or high confidence; an operator's label counts whatever the confidence. " +
    'Suspected causes are observed symptoms, not confirmed root causes. ' +
    'Inconclusive: the triage found no supported cause, named one only at low confidence, or an operator labelled it insufficient evidence. ' +
    'Not triaged: the alert was never triaged, for example automatic triage was off at the time, the alert resolved within the settle wait, ' +
    "the monitor was in its triage cooldown, the workspace's hourly triage limit or the platform backlog was reached, " +
    'the alert was older than the triage lookback, triage was switched off while the alert was queued, or the local AI was unavailable. ' +
    'Triage pending: triage was still running or the alert still open when the report was computed. ' +
    'No alert: no availability alert overlapped the outage. ' +
    'Counts are monitor outages, not alerts, and downtime is summed per monitor, so it can exceed the service downtime when monitors fail together. ' +
    `${c.triaged_outage_count} of ${c.outage_count} monitor outages had a triaged alert.`
  );
}

/** A burn rate as "1.39×"; "—" without data. */
export function formatBurnRate(rate: number | null | undefined): string {
  if (rate === null || rate === undefined || !Number.isFinite(rate)) return '—';
  return `${rate.toFixed(2)}×`;
}

/**
 * Tone of a burn rate (S-U8): at or under the allowed pace is good, up to
 * twice it warns, faster is bad. A closed period's verdict is the met pill;
 * this colours the pace.
 */
export function burnRateTone(rate: number | null | undefined): SlaTone {
  if (rate === null || rate === undefined || !Number.isFinite(rate)) return 'none';
  if (rate <= 1 + 1e-9) return 'good';
  if (rate <= 2) return 'warn';
  return 'bad';
}

// ── Report schedules (/v1/report-schedules) ─────────────────────────────

export type SlaScheduleFormat = 'pdf' | 'csv';

export interface SlaReportSchedule {
  id: string;
  tenant_id: string;
  sla_id: string;
  recipients: string[];
  /** Recipients who followed their unsubscribe link: they get nothing more from this schedule. */
  unsubscribed: string[];
  formats: SlaScheduleFormat[];
  auto_issue: boolean;
  enabled: boolean;
  /** The running period's close plus an hour; meaningful only while enabled. */
  next_run_at: string;
  last_run_at: string | null;
  last_period_key: string;
  last_sent_count: number;
  last_error: string;
  created_at: string;
  updated_at: string;
}

export interface SlaReportScheduleList {
  items: SlaReportSchedule[];
  /** False when the API has no SMTP or no APP_BASE_URL: schedules cannot be enabled. */
  mail_ready: boolean;
  mail_problem?: string;
}

export interface SlaReportScheduleInput {
  recipients: string[];
  formats: SlaScheduleFormat[];
  auto_issue: boolean;
  enabled: boolean;
}

export interface SlaScheduleSendOutcome {
  period_key: string;
  issued: boolean;
  sent: number;
  skipped: number;
  failed: number;
  error?: string;
}

export const SLA_SCHEDULE_FORMAT_LABELS: Record<SlaScheduleFormat, string> = { pdf: 'PDF', csv: 'CSV' };

/** Splits a typed recipient list on commas, semicolons, whitespace and newlines; lower-cased, de-duplicated. */
export function parseRecipients(text: string): string[] {
  const seen = new Set<string>();
  for (const part of text.split(/[\s,;]+/)) {
    const value = part.trim().toLowerCase();
    if (value) seen.add(value);
  }
  return [...seen];
}

/** Validates the schedule form before it reaches the API; returns a message or null. */
export function scheduleInputError(input: SlaReportScheduleInput): string | null {
  if (input.recipients.length === 0) return 'Add at least one recipient.';
  if (input.recipients.length > 50) return 'A schedule has at most 50 recipients.';
  const bad = input.recipients.find((r) => !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(r));
  if (bad) return `${bad} is not an email address.`;
  if (input.formats.length === 0) return 'Attach at least one format.';
  return null;
}

/** A send's outcome as a toast: what went out, who was skipped, what failed. */
export function describeSendOutcome(o: SlaScheduleSendOutcome): { tone: 'success' | 'error'; text: string } {
  const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;
  const copy = o.issued ? 'issued' : 'live';
  const parts = [`${o.period_key} (${copy}) sent to ${plural(o.sent, 'recipient')}`];
  if (o.skipped > 0) parts.push(`${o.skipped} unsubscribed`);
  if (o.error) return { tone: 'error', text: `${parts.join(', ')}. ${o.error}` };
  return { tone: 'success', text: `${parts.join(', ')}.` };
}

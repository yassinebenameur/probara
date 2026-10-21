// Display formatting for the operator UI: the one place dates, recency,
// durations, uptime percentages, latency, call quality (MOS, loss, jitter)
// and time ranges are turned into text.
// Pages call these rather than toLocaleString()/toFixed() so the same value
// reads the same on every screen. Tones (what colour a value gets) live in
// lib/status-tones.ts.

type DateInput = string | number | Date | null | undefined;

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

function toDate(value: DateInput): Date | null {
  if (value === null || value === undefined || value === '') return null;
  const date = value instanceof Date ? value : new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

const pad2 = (n: number) => String(n).padStart(2, '0');

/**
 * Absolute local date and time, `2 Oct, 15:19`; the year is added only when it
 * is not the current one (`2 Oct 2025, 15:19`). Month names keep it
 * unambiguous for a mixed US/EU team, where `02/10/2026` is not.
 */
export function formatDateTime(value?: DateInput, fallback = '—', now: Date = new Date()): string {
  const date = toDate(value);
  if (!date) return value ? String(value) : fallback;
  return `${formatDate(date, fallback, now)}, ${pad2(date.getHours())}:${pad2(date.getMinutes())}`;
}

/** Local date without time, `2 Oct` (`2 Oct 2025` outside the current year). */
export function formatDate(value?: DateInput, fallback = '—', now: Date = new Date()): string {
  const date = toDate(value);
  if (!date) return value ? String(value) : fallback;
  const year = date.getFullYear() === now.getFullYear() ? '' : ` ${date.getFullYear()}`;
  return `${date.getDate()} ${MONTHS[date.getMonth()]}${year}`;
}

export type TimestampZone = 'local' | 'utc';

/**
 * Table timestamp, `2026-10-02 15:19:53` (mono, sortable). One zone per
 * table: state it once in the column header with `timezoneLabel(tz)`.
 */
export function formatTimestamp(
  value?: DateInput,
  opts: { tz?: TimestampZone; seconds?: boolean } = {},
  fallback = '—'
): string {
  const date = toDate(value);
  if (!date) return value ? String(value) : fallback;
  const utc = opts.tz === 'utc';
  const y = utc ? date.getUTCFullYear() : date.getFullYear();
  const mo = utc ? date.getUTCMonth() : date.getMonth();
  const d = utc ? date.getUTCDate() : date.getDate();
  const h = utc ? date.getUTCHours() : date.getHours();
  const mi = utc ? date.getUTCMinutes() : date.getMinutes();
  const s = utc ? date.getUTCSeconds() : date.getSeconds();
  const time = `${pad2(h)}:${pad2(mi)}${opts.seconds === false ? '' : `:${pad2(s)}`}`;
  return `${y}-${pad2(mo + 1)}-${pad2(d)} ${time}`;
}

/**
 * Short zone name for a column header: `UTC`, `UTC+2`, `UTC−5`, `UTC+5:30`.
 * Local uses the offset in effect at `at` (now by default).
 */
export function timezoneLabel(tz: TimestampZone = 'local', at: Date = new Date()): string {
  if (tz === 'utc') return 'UTC';
  const offset = -at.getTimezoneOffset();
  if (offset === 0) return 'UTC';
  const sign = offset > 0 ? '+' : '−';
  const abs = Math.abs(offset);
  const minutes = abs % 60;
  return `UTC${sign}${Math.floor(abs / 60)}${minutes ? `:${pad2(minutes)}` : ''}`;
}

/** Full timestamp with seconds and zone, for `title=` tooltips. */
export function formatDateTimeFull(value?: DateInput, fallback = '—'): string {
  const date = toDate(value);
  if (!date) return value ? String(value) : fallback;
  return `${formatTimestamp(date)} ${timezoneLabel('local', date)}`;
}

/**
 * Recency: `just now`, `12s ago`, `3 min ago`, `4 hr ago`, `2 days ago`;
 * future times read `in 3 min`. Past 30 days it falls back to the date
 * (`2 Jul`), which says more than "98 days ago". Clock skew under a minute
 * in the future still reads `just now`.
 */
export function formatRelativeTime(value?: DateInput, now: Date = new Date(), fallback = '—'): string {
  const date = toDate(value);
  if (!date) return fallback;
  const diff = Math.floor((now.getTime() - date.getTime()) / 1000);
  if (diff < 0 && diff > -60) return 'just now';
  const future = diff < 0;
  const seconds = Math.abs(diff);
  if (!future && seconds < 10) return 'just now';
  if (seconds >= 30 * 86400) return formatDate(date, fallback, now);
  let text: string;
  if (seconds < 60) text = `${seconds}s`;
  else if (seconds < 3600) text = `${Math.floor(seconds / 60)} min`;
  else if (seconds < 86400) text = `${Math.floor(seconds / 3600)} hr`;
  else {
    const days = Math.floor(seconds / 86400);
    text = `${days} day${days === 1 ? '' : 's'}`;
  }
  return future ? `in ${text}` : `${text} ago`;
}

/**
 * Elapsed time in seconds as its two most significant units: `45s`,
 * `3m 20s`, `3h 12m`, `2d 4h`. Negative or missing input gives `—`.
 */
export function formatDuration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds) || seconds < 0) return '—';
  const s = Math.floor(seconds);
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (d > 0) return h ? `${d}d ${h}h` : `${d}d`;
  if (h > 0) return m ? `${h}h ${m}m` : `${h}h`;
  if (m > 0) return sec ? `${m}m ${sec}s` : `${m}m`;
  return `${sec}s`;
}

/** Duration between two instants; an open-ended span runs to `now`. */
export function formatDurationBetween(start: DateInput, end?: DateInput, now: Date = new Date()): string {
  const from = toDate(start);
  if (!from) return '—';
  const to = toDate(end) ?? now;
  return formatDuration((to.getTime() - from.getTime()) / 1000);
}

/**
 * Uptime percentage. 100 → `100%`; from 99 up, two decimals; below, one.
 * Always floored, so 99.996 reads `99.99%` and never a false `100.00%`.
 * Missing data is `—`, never `0%`.
 */
export function formatUptime(pct: number | null | undefined): string {
  if (pct === null || pct === undefined || !Number.isFinite(pct)) return '—';
  if (pct >= 100) return '100%';
  if (pct <= 0) return pct === 0 ? '0.0%' : '—';
  const decimals = pct >= 99 ? 2 : 1;
  const factor = 10 ** decimals;
  // Rounding to 6 places first keeps 99.99 (stored as 99.98999…) from
  // flooring to 99.98; the clamp keeps anything short of 100 from reaching
  // the next bucket and reading as a false 100.00%.
  const floored = Math.floor(Math.round(pct * factor * 1e6) / 1e6) / factor;
  return `${Math.min(floored, 100 - 1 / factor).toFixed(decimals)}%`;
}

/**
 * Latency in milliseconds: `67 ms`, `1.2 s`, `12 s`. Missing or negative is
 * `—`. Feed it successful checks only (calculateLatencyStats does): a failed
 * check's latency is usually its timeout.
 */
export function formatLatency(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms) || ms < 0) return '—';
  // Pick the unit after rounding, so 999.6 ms is "1.0 s" (not "1000 ms")
  // and 9960 ms is "10 s" (not "10.0 s").
  if (Math.round(ms) < 1000) return `${Math.round(ms)} ms`;
  const tenths = Math.round(ms / 100) / 10;
  return tenths < 10 ? `${tenths.toFixed(1)} s` : `${Math.round(ms / 1000)} s`;
}

/**
 * A MOS (mean opinion score, 1-5): two decimals, `4.31`, as the worker
 * rounds and its threshold messages print it. Missing is `—`.
 */
export function formatMOS(mos: number | null | undefined): string {
  if (mos === null || mos === undefined || !Number.isFinite(mos)) return '—';
  return mos.toFixed(2);
}

/**
 * Packet loss in percent: `0%` for none, else two decimals (`0.44%`,
 * `5.00%`), as the worker's threshold messages print it, since one packet is
 * a fraction of a percent of a short call. Missing or negative is `—`.
 */
export function formatLossPct(pct: number | null | undefined): string {
  if (pct === null || pct === undefined || !Number.isFinite(pct) || pct < 0) return '—';
  return pct === 0 ? '0%' : `${pct.toFixed(2)}%`;
}

/**
 * Audio jitter in milliseconds: one decimal below 10 ms (`2.1 ms`), where a
 * millisecond matters, else as formatLatency (`34 ms`). Missing or negative
 * is `—`.
 */
export function formatJitter(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms) || ms < 0) return '—';
  const tenths = Math.round(ms * 10) / 10;
  return tenths < 10 ? `${tenths.toFixed(1)} ms` : formatLatency(ms);
}

export function pluralize(count: number, singular: string, plural = `${singular}s`): string {
  return `${count} ${count === 1 ? singular : plural}`;
}

// ── Time ranges ──────────────────────────────────────────────────────────
// One vocabulary for every range control (components/ui/RangePicker.tsx).
// Pages pass the subset they support; the short label is shown, the long
// one is the accessible name.

export type RangeId = '1h' | '6h' | '24h' | '7d' | '30d' | '90d' | '365d' | 'all';

export interface RangeDef {
  id: RangeId;
  short: string;
  long: string;
  /** Span in milliseconds; null for "all time". */
  ms: number | null;
}

const HOUR = 3_600_000;
const DAY = 24 * HOUR;

export const RANGES: Record<RangeId, RangeDef> = {
  '1h': { id: '1h', short: '1h', long: 'Last hour', ms: HOUR },
  '6h': { id: '6h', short: '6h', long: 'Last 6 hours', ms: 6 * HOUR },
  '24h': { id: '24h', short: '24h', long: 'Last 24 hours', ms: DAY },
  '7d': { id: '7d', short: '7d', long: 'Last 7 days', ms: 7 * DAY },
  '30d': { id: '30d', short: '30d', long: 'Last 30 days', ms: 30 * DAY },
  '90d': { id: '90d', short: '90d', long: 'Last 90 days', ms: 90 * DAY },
  '365d': { id: '365d', short: '1y', long: 'Last year', ms: 365 * DAY },
  all: { id: 'all', short: 'All', long: 'All time', ms: null },
};

export function isRangeId(value: unknown): value is RangeId {
  return typeof value === 'string' && Object.prototype.hasOwnProperty.call(RANGES, value);
}

/** Start of a range ending at `now`; null for "all time". */
export function rangeStart(id: RangeId, now: Date = new Date()): Date | null {
  const ms = RANGES[id].ms;
  return ms === null ? null : new Date(now.getTime() - ms);
}

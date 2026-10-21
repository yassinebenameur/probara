'use client';

import { formatDateTimeFull, formatDurationBetween, formatRelativeTime } from '@/lib/format';
import { useNow, type NowCadence } from '@/lib/useNow';

type DateInput = string | number | Date | null | undefined;

function toDate(value: DateInput): Date | null {
  if (value === null || value === undefined || value === '') return null;
  const date = value instanceof Date ? value : new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

// Second-resolution text ("12s ago", "45s") ticks every second while it is
// young; past a minute the shared 15s clock is plenty.
function cadenceFor(ageMs: number): NowCadence {
  return ageMs < 60_000 ? 'second' : 'minute';
}

interface RelativeTimeProps {
  value: DateInput;
  /** Shown when `value` is missing or invalid. */
  fallback?: string;
  /** Text before the relative time ("updated "). */
  prefix?: string;
  className?: string;
}

/**
 * "25s ago" that keeps moving: a <time> with the full timestamp as its
 * dateTime and tooltip, re-rendered by the shared clock (lib/useNow), so a
 * screen that does not refetch still ages correctly. Wording is
 * formatRelativeTime from lib/format.ts.
 */
export function RelativeTime({ value, fallback = '—', prefix, className }: RelativeTimeProps) {
  const date = toDate(value);
  // Hook order is fixed; the cadence only picks which shared clock to follow.
  const now = useNow(date ? cadenceFor(Date.now() - date.getTime()) : 'minute');
  if (!date) return <span className={className}>{fallback}</span>;
  return (
    <time
      dateTime={date.toISOString()}
      title={formatDateTimeFull(date)}
      className={className}
      suppressHydrationWarning
    >
      {prefix}
      {formatRelativeTime(date, new Date(now), fallback)}
    </time>
  );
}

interface ElapsedTimeProps {
  start: DateInput;
  /** When set the span is closed and the text is static. */
  end?: DateInput;
  className?: string;
}

/**
 * Duration from `start` to `end`, or to now while it is still running (an
 * ongoing alert or outage): "3m 20s", "2h 5m". Wording is
 * formatDurationBetween from lib/format.ts.
 */
export function ElapsedTime({ start, end, className }: ElapsedTimeProps) {
  const from = toDate(start);
  const to = toDate(end);
  const running = Boolean(from) && !to;
  const age = from ? Date.now() - from.getTime() : Infinity;
  // A closed span never needs the clock; an open one under an hour shows
  // seconds or minutes that change often enough to want the 1s clock.
  const now = useNow(running && age < 3_600_000 ? 'second' : 'minute');
  if (!from) return <span className={className}>—</span>;
  const text = formatDurationBetween(from, to ?? undefined, new Date(now));
  return (
    <span
      className={className}
      title={`${running ? 'Since' : 'From'} ${formatDateTimeFull(from)}${to ? ` to ${formatDateTimeFull(to)}` : ''}`}
      suppressHydrationWarning
    >
      {text}
    </span>
  );
}

export default RelativeTime;

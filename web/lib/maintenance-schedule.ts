// Recurring maintenance in the operator UI: converting the schedule dialog's
// wall-clock inputs to and from instants in the rule's IANA zone, and
// describing a rule in a few words. The rule itself is expanded only by the
// API (shared/maintenance, POST /maintenance-windows/preview); nothing here
// computes occurrences.

import { zonedParts } from './format';
import type { MaintenanceFrequency, MaintenanceRecurrence } from './types';

const pad2 = (n: number) => String(n).padStart(2, '0');

export const WEEKDAY_SHORT = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
/** Monday-first display order for weekday chips; values stay 0 = Sunday. */
export const WEEKDAY_ORDER = [1, 2, 3, 4, 5, 6, 0];

/** The browser's IANA zone, or UTC when it cannot say. */
export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

/** Zones for the timezone select: the runtime's list, always with UTC and `current`. */
export function timeZoneOptions(current?: string): string[] {
  const supported = (Intl as unknown as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf;
  let zones: string[] = [];
  try {
    zones = supported ? supported('timeZone') : [];
  } catch {
    zones = [];
  }
  const all = new Set(['UTC', ...zones]);
  if (current) all.add(current);
  return Array.from(all);
}

function offsetMs(instant: number, timeZone: string): number {
  const p = zonedParts(new Date(instant), timeZone);
  const asUTC = Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second);
  return asUTC - Math.floor(instant / 1000) * 1000;
}

/**
 * The instant a `YYYY-MM-DDTHH:mm` wall time names in timeZone, as ISO.
 * A wall time a DST gap skips resolves forward, like the server does.
 */
export function zonedWallToISO(wall: string, timeZone: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(wall);
  if (!m) return '';
  const naive = Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]), Number(m[5]));
  // The offset before the wall time first; re-check against the result so a
  // time just past a transition picks the offset in force there.
  const before = offsetMs(naive - 24 * 3600 * 1000, timeZone);
  let instant = naive - before;
  const at = offsetMs(instant, timeZone);
  if (at !== before) {
    const candidate = naive - at;
    if (offsetMs(candidate, timeZone) === at) instant = candidate;
  }
  return new Date(instant).toISOString();
}

/** An instant as the `YYYY-MM-DDTHH:mm` wall time a datetime-local input shows, in timeZone. */
export function isoToZonedWall(iso: string, timeZone: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  const p = zonedParts(date, timeZone);
  return `${p.year}-${pad2(p.month)}-${pad2(p.day)}T${pad2(p.hour)}:${pad2(p.minute)}`;
}

export const FREQUENCY_LABELS: Record<MaintenanceFrequency, string> = {
  none: 'Once',
  daily: 'Daily',
  weekly: 'Weekly',
  monthly: 'Monthly',
  cron: 'Cron',
};

function ordinal(n: number): string {
  const rem100 = n % 100;
  if (rem100 >= 11 && rem100 <= 13) return `${n}th`;
  return `${n}${['th', 'st', 'nd', 'rd'][n % 10] ?? 'th'}`;
}

/**
 * A rule in a few words: `Weekly on Mon, Thu`, `Every 2 days`,
 * `Monthly on the 31st (last day in shorter months)`, `Cron 0 2 * * 0`.
 * Empty for a one-off.
 */
export function describeRecurrence(rec?: MaintenanceRecurrence | null): string {
  if (!rec || rec.frequency === 'none') return '';
  const n = rec.interval && rec.interval > 1 ? rec.interval : 1;
  switch (rec.frequency) {
    case 'daily':
      return n > 1 ? `Every ${n} days` : 'Daily';
    case 'weekly': {
      const days = WEEKDAY_ORDER.filter((d) => (rec.weekdays ?? []).includes(d)).map((d) => WEEKDAY_SHORT[d]);
      const on = days.length ? ` on ${days.join(', ')}` : '';
      return (n > 1 ? `Every ${n} weeks` : 'Weekly') + on;
    }
    case 'monthly': {
      const day = rec.month_day ? ` on the ${ordinal(rec.month_day)}` : '';
      const clamp = rec.month_day && rec.month_day > 28 ? ' (last day in shorter months)' : '';
      return (n > 1 ? `Every ${n} months` : 'Monthly') + day + clamp;
    }
    case 'cron':
      return `Cron ${rec.cron ?? ''}`.trim();
    default:
      return '';
  }
}

/** Whether a window repeats (has a rule other than a one-off). */
export function isRepeating(rec?: MaintenanceRecurrence | null): boolean {
  return !!rec && rec.frequency !== 'none';
}

// Repeating maintenance windows in the operator UI: the weekday picker's
// order, a rule in words, the form's client-side checks and the timezone
// choices. The API (shared/maintenance) is the authority on when a rule
// runs; nothing here expands occurrences.

import { formatDate, formatDuration } from './format';
import type { MaintenanceRecurrence } from './types/maintenance';

/** Longest occurrence of a repeating window, as the API enforces (7 days). */
export const MAX_RECURRING_DURATION_MINUTES = 7 * 24 * 60;

const DAY_NAMES = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const DAY_LONG = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

/** Weekday chips in display order (Monday first); values are the API's 0 = Sunday. */
export const WEEKDAY_OPTIONS = [1, 2, 3, 4, 5, 6, 0].map((value) => ({
  value,
  label: DAY_NAMES[value],
  title: DAY_LONG[value],
}));

/** Weekdays sorted Monday first, the order every label uses. */
function mondayFirst(days: number[]): number[] {
  return [...new Set(days)].sort((a, b) => ((a + 6) % 7) - ((b + 6) % 7));
}

function joinWords(items: string[]): string {
  if (items.length <= 1) return items.join('');
  return `${items.slice(0, -1).join(', ')} and ${items[items.length - 1]}`;
}

/** Which days, in words: `every day`, `weekdays`, `weekends`, `Tue and Thu`. */
export function describeWeekdays(days: number[]): string {
  const sorted = mondayFirst(days);
  const key = sorted.join(',');
  if (sorted.length === 7) return 'every day';
  if (key === '1,2,3,4,5') return 'weekdays';
  if (key === '6,0') return 'weekends';
  return joinWords(sorted.map((d) => DAY_NAMES[d]));
}

/** A YYYY-MM-DD date as the UI shows dates (`31 Dec`, `31 Dec 2027`). */
export function formatCalendarDate(value: string, now: Date = new Date()): string {
  const [y, m, d] = value.split('-').map(Number);
  if (!y || !m || !d) return value;
  return formatDate(new Date(y, m - 1, d), value, now);
}

/**
 * A repeating rule in words, for the list and the form:
 * `Tue and Thu at 02:00 Europe/Paris, for 1h 30m, until 31 Dec`.
 */
export function describeRecurrence(rec: MaintenanceRecurrence, now: Date = new Date()): string {
  if (rec.frequency !== 'weekly' || !rec.weekdays?.length) return 'Once';
  const days = describeWeekdays(rec.weekdays);
  const lead = days === 'every day' ? 'Every day' : days === 'weekdays' || days === 'weekends' ? `On ${days}` : `Every ${days}`;
  const parts = [`${lead} at ${rec.start_time ?? '—'} ${rec.timezone ?? ''}`.trim()];
  if (rec.duration_minutes) parts.push(`for ${formatDuration(rec.duration_minutes * 60)}`);
  if (rec.until) parts.push(`until ${formatCalendarDate(rec.until, now)}`);
  return parts.join(', ');
}

/** The first problem with a repeating rule the form can see, or null. */
export function recurrenceInputError(rec: MaintenanceRecurrence): string | null {
  if (!rec.weekdays?.length) return 'Pick at least one day.';
  if (!rec.start_time || !/^([01]\d|2[0-3]):[0-5]\d$/.test(rec.start_time)) return 'Enter a start time.';
  const minutes = rec.duration_minutes ?? 0;
  if (!Number.isInteger(minutes) || minutes < 1) return 'The duration must be at least a minute.';
  if (minutes > MAX_RECURRING_DURATION_MINUTES) return 'A repeating window lasts at most 7 days.';
  if (!rec.timezone?.trim()) return 'Pick a timezone.';
  return null;
}

/** Whether two rules describe the same schedule (so saved occurrences still apply). */
export function sameRecurrence(a?: MaintenanceRecurrence, b?: MaintenanceRecurrence): boolean {
  if (!a || !b) return a === b;
  return (
    a.frequency === b.frequency &&
    mondayFirst(a.weekdays ?? []).join(',') === mondayFirst(b.weekdays ?? []).join(',') &&
    a.start_time === b.start_time &&
    a.duration_minutes === b.duration_minutes &&
    (a.timezone ?? '') === (b.timezone ?? '') &&
    (a.until ?? '') === (b.until ?? '')
  );
}

/** Minutes as the form's hours and minutes fields. */
export function splitMinutes(total: number): { hours: number; minutes: number } {
  return { hours: Math.floor(total / 60), minutes: total % 60 };
}

/** The browser's IANA zone, the default for a new repeating window. */
export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

/** IANA zones the browser knows, UTC first, for a datalist. */
export function timezoneOptions(): string[] {
  const supported = (Intl as unknown as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf;
  try {
    return supported ? ['UTC', ...supported('timeZone').filter((tz) => tz !== 'UTC')] : ['UTC'];
  } catch {
    return ['UTC'];
  }
}

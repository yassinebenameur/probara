import type { Alert } from './types';

/**
 * The acknowledge mute, as the Alerts page shows and drives it. An
 * acknowledged or snoozed open alert sends nothing but its resolve notice;
 * unacknowledging or ending the snooze resumes reminders and escalation tiers
 * not fired yet, counted from then. The rule itself is the server's
 * (shared/alertrouting); these only read the alert and shape requests.
 */

/** The snooze presets offered beside a custom end. */
export const SNOOZE_PRESETS = [
  { value: '1h', label: '1h', title: '1 hour', minutes: 60 },
  { value: '4h', label: '4h', title: '4 hours', minutes: 240 },
  { value: '24h', label: '24h', title: '24 hours', minutes: 1440 },
] as const;

export type SnoozeChoice = (typeof SNOOZE_PRESETS)[number]['value'] | 'custom';

/** The server's bounds on a snooze end (services/alerts MinSnooze/MaxSnooze). */
export const SNOOZE_MIN_MS = 60_000;
export const SNOOZE_MAX_MS = 7 * 24 * 3_600_000;

/** Whether the open alert is snoozed at `now` (a row read before its snooze ran out says so too). */
export function isSnoozed(alert: Alert, now: number = Date.now()): boolean {
  if (alert.status === 'resolved' || !alert.snoozed_until) return false;
  const until = Date.parse(alert.snoozed_until);
  return Number.isFinite(until) && until > now;
}

/**
 * What an open alert's notifications are doing: stopped by an
 * acknowledgement, held by a snooze, or flowing. Null once resolved.
 * Acknowledgement wins: a snooze on an acknowledged alert changes nothing
 * until it is unacknowledged.
 */
export function notificationState(alert: Alert, now: number = Date.now()): 'acknowledged' | 'snoozed' | 'notifying' | null {
  if (alert.status === 'resolved') return null;
  if (alert.status === 'acknowledged') return 'acknowledged';
  return isSnoozed(alert, now) ? 'snoozed' : 'notifying';
}

/** A `datetime-local` value (the browser's zone) as an epoch, or NaN. */
export function localInputTime(value: string): number {
  return value ? new Date(value).getTime() : Number.NaN;
}

/** `at` as a `datetime-local` value in the browser's zone, to the minute. */
export function toLocalInput(at: number): string {
  const d = new Date(at);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/**
 * The request body and end time for a snooze choice, or the reason it cannot
 * be sent. A preset sends its length (the server's clock starts it); a custom
 * end sends the time, which must be 1 minute to 7 days ahead.
 */
export function snoozeRequest(
  choice: SnoozeChoice,
  customValue: string,
  now: number = Date.now()
): { body: { duration_minutes: number } | { until: string }; end: number } | { error: string } {
  const preset = SNOOZE_PRESETS.find((p) => p.value === choice);
  if (preset) return { body: { duration_minutes: preset.minutes }, end: now + preset.minutes * 60_000 };
  const end = localInputTime(customValue);
  if (!Number.isFinite(end)) return { error: 'Choose when the snooze ends.' };
  if (end - now < SNOOZE_MIN_MS) return { error: 'The end must be at least a minute from now.' };
  if (end - now > SNOOZE_MAX_MS) return { error: 'A snooze can last at most 7 days.' };
  return { body: { until: new Date(end).toISOString() }, end };
}

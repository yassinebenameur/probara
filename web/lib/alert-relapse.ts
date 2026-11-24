import { formatDuration, pluralize } from './format';
import type { Alert, AlertRelapse } from './types';

/**
 * Relapse reopen, as the operator UI shows and sets it. When a workspace sets
 * a reopen cooldown, an alert that recovered on its own and fails again
 * within it reopens (its relapse count goes up) instead of a new alert
 * opening. The rule is the server's (shared/alertrouting relapse.go); these
 * only read the alert and shape the setting.
 */

/** The cooldown presets, in minutes. 0 is off. */
export const REOPEN_COOLDOWN_PRESETS = [
  { label: 'Off', minutes: 0 },
  { label: '5 min', minutes: 5 },
  { label: '15 min', minutes: 15 },
  { label: '30 min', minutes: 30 },
  { label: '1 hour', minutes: 60 },
  { label: '4 hours', minutes: 240 },
] as const;

/** The server's bound (alertrouting.MaxReopenCooldownMinutes). */
export const REOPEN_COOLDOWN_MAX_MINUTES = 1440;

/**
 * The select's options: the presets, plus the stored value when it is not
 * one of them (set through the API), so it shows and saves unchanged.
 */
export function reopenCooldownOptions(current: number): { label: string; minutes: number }[] {
  const options: { label: string; minutes: number }[] = [...REOPEN_COOLDOWN_PRESETS];
  if (current > 0 && !options.some((o) => o.minutes === current)) {
    options.push({ label: formatDuration(current * 60), minutes: current });
    options.sort((a, b) => a.minutes - b.minutes);
  }
  return options;
}

/** How many times the alert relapsed (0 when never). */
export function relapseCount(alert: Pick<Alert, 'relapse_count'>): number {
  return alert.relapse_count && alert.relapse_count > 0 ? alert.relapse_count : 0;
}

/** The chip text of a relapsed alert, e.g. "Relapsed ×3", or null. */
export function relapseChip(alert: Pick<Alert, 'relapse_count'>): string | null {
  const n = relapseCount(alert);
  return n > 0 ? `Relapsed ×${n}` : null;
}

/** The chip's tooltip: what a relapse is, how often, the last time. */
export function relapseTitle(alert: Pick<Alert, 'relapse_count'>, lastReopen: string | null): string {
  const n = relapseCount(alert);
  const last = lastReopen ? ` Last reopened ${lastReopen}.` : '';
  return `Failed again soon after recovering, so this alert reopened ${pluralize(n, 'time')} instead of a new one opening.${last}`;
}

/** One history line: "#2 · down again 4m after recovering". */
export function relapseLine(r: AlertRelapse): string {
  const gap = (Date.parse(r.reopened_at) - Date.parse(r.recovered_at)) / 1000;
  return `#${r.number} · down again ${formatDuration(gap)} after recovering`;
}

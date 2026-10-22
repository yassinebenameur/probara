import type { SortState } from './sort';
import type { AlertSortColumn, AlertStatus } from './types';

/**
 * The /alerts list's view rules, kept pure so node tests cover them: which
 * sort each view shows and sends, where a live alert may be inserted, how a
 * row selection grows and shrinks, and when the row shortcuts (A / R and the
 * arrow keys) may fire.
 */

/** The status chips: 'open' (active + acknowledged) is the operator's queue. */
export type AlertStatusFilter = 'open' | AlertStatus | 'all';

export const ALERT_SORT_KEYS = ['status', 'monitor', 'started', 'duration'] as const;
export type AlertSortKey = (typeof ALERT_SORT_KEYS)[number];

/**
 * The Open view's default, sorted here over the whole queue: unhandled
 * (active) first, then acknowledged; newest first within each.
 */
export const ALERT_SORT_DEFAULT: SortState<AlertSortKey> = { key: 'status', dir: 'asc' };
/** The server-paged views' default, the API's own (triggered_at index + LIMIT). */
export const ALERT_SORT_PAGED_DEFAULT: SortState<AlertSortKey> = { key: 'started', dir: 'desc' };
/** Newest and longest first; Status and Monitor start ascending. */
export const ALERT_SORT_DEFAULT_DIR = { started: 'desc', duration: 'desc' } as const;

/** The API's `sort` column for each header (server-paged views sort there). */
export const ALERT_SORT_COLUMN: Record<AlertSortKey, AlertSortColumn> = {
  status: 'status',
  monitor: 'monitor',
  started: 'triggered_at',
  duration: 'duration',
};

/** Only the Open view holds its whole list and sorts it here; the rest page and sort on the server. */
export function isClientSorted(view: AlertStatusFilter): boolean {
  return view === 'open';
}

/** The sort a view starts in (and leaves out of the URL). */
export function alertSortFallback(view: AlertStatusFilter): SortState<AlertSortKey> {
  return isClientSorted(view) ? ALERT_SORT_DEFAULT : ALERT_SORT_PAGED_DEFAULT;
}

/** Status orders nothing under a one-status filter (every row ranks the same). */
export function statusSortApplies(view: AlertStatusFilter): boolean {
  return view === 'open' || view === 'all';
}

/**
 * The sort a view actually applies, and so the one its header shows. A
 * status sort under a one-status filter is the API's newest-first order
 * (services/alerts ListAlerts collapses it), so it reads as Started.
 */
export function effectiveAlertSort(sort: SortState<AlertSortKey>, view: AlertStatusFilter): SortState<AlertSortKey> {
  return sort.key === 'status' && !statusSortApplies(view) ? ALERT_SORT_PAGED_DEFAULT : sort;
}

/**
 * Whether a live alert not yet listed belongs at the top of page 1 of a
 * server-paged view under `sort` (an effective sort): always when newest
 * first, only an active one under the status order (active ranks first,
 * newest first within). Any other order waits for the poll, which places it.
 */
export function newAlertLeads(sort: SortState<AlertSortKey>, status: AlertStatus): boolean {
  if (sort.key === 'started') return sort.dir === 'desc';
  if (sort.key === 'status') return sort.dir === 'asc' && status === 'active';
  return false;
}

/**
 * The selection after toggling `id`. With `shift` and an anchor still shown,
 * the whole range between them (over `order`, the selectable rows in shown
 * order) takes the toggled row's new state.
 */
export function toggleSelection(
  selected: ReadonlySet<string>,
  order: readonly string[],
  anchor: string | null,
  id: string,
  shift: boolean
): Set<string> {
  const from = anchor ? order.indexOf(anchor) : -1;
  const to = order.indexOf(id);
  const range = shift && from >= 0 && to >= 0 && from !== to;
  const select = !selected.has(id);
  const ids = range ? order.slice(Math.min(from, to), Math.max(from, to) + 1) : [id];
  const next = new Set(selected);
  ids.forEach((rowId) => (select ? next.add(rowId) : next.delete(rowId)));
  return next;
}

/**
 * `selected` limited to the rows still selectable (`order`), so an alert that
 * left the page or was resolved meanwhile never reaches a bulk action. Returns
 * `selected` itself when nothing dropped out, so callers can compare by
 * identity.
 */
export function pruneSelection(selected: ReadonlySet<string>, order: readonly string[]): ReadonlySet<string> {
  if (selected.size === 0) return selected;
  const shown = new Set(order);
  const next = new Set([...selected].filter((id) => shown.has(id)));
  return next.size === selected.size ? selected : next;
}

/**
 * Where the row shortcuts never fire: text entry, and the row's own menu and
 * dialogs (portaled, but React events still bubble to the row). A checkbox or
 * radio is not text entry, so the row's selection box keeps them.
 */
export const SHORTCUT_EXEMPT =
  'textarea, select, [contenteditable="true"], input:not([type="checkbox"]):not([type="radio"]), [role="menu"], [role="dialog"]';

export type RowShortcut = 'acknowledge' | 'resolve' | 'next' | 'previous';

interface ShortcutKey {
  key: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  defaultPrevented: boolean;
}

/**
 * The shortcut a key press on an alert row means, or null. The keys are
 * bound to the row (WCAG 2.1.4: active only while a row has focus), so this
 * refuses everything else: a press already handled, any Ctrl/Cmd/Alt chord
 * (browser and OS shortcuts), and a press whose target is `exempt`
 * (inside SHORTCUT_EXEMPT, or editable). Shift is allowed: "A" is "a".
 */
export function rowShortcut(event: ShortcutKey, exempt: boolean): RowShortcut | null {
  if (event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey || exempt) return null;
  switch (event.key) {
    case 'ArrowDown':
      return 'next';
    case 'ArrowUp':
      return 'previous';
    case 'a':
    case 'A':
      return 'acknowledge';
    case 'r':
    case 'R':
      return 'resolve';
    default:
      return null;
  }
}

/**
 * What a triage shortcut does to an alert in `status`: apply, or nothing
 * because the alert is already past it (said so, not silently ignored).
 */
export function shortcutEffect(
  action: 'acknowledge' | 'resolve',
  status: AlertStatus
): 'apply' | 'already_acknowledged' | 'already_resolved' {
  if (status === 'resolved') return 'already_resolved';
  if (action === 'acknowledge' && status === 'acknowledged') return 'already_acknowledged';
  return 'apply';
}

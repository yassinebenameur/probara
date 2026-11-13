import { mergeAlertUpdate } from './ai-triage';
import { currentTone, foldSuppressed, isOpenAlert, type TimelineTone } from './alert-timeline';
import type { Alert, AlertStatus } from './types';

/**
 * The /alerts list's view rules, kept pure so node tests cover them: which
 * alerts a status view lists, how they lay out on the range's calendar, which
 * fold under a root cause, what a live event may overwrite, how a row selection grows and
 * shrinks, and when the row shortcuts (A / R and the arrow keys) may fire.
 */

/**
 * The status chips. 'all' (the default) is every alert of the range; 'open'
 * is active + acknowledged, the queue.
 */
export type AlertStatusFilter = 'all' | 'open' | AlertStatus;

/** Rows the feed shows before it loads more, and each step after. */
export const FEED_PAGE = 25;

/** Whether a status view lists this alert. */
export function alertInView(alert: Alert, view: AlertStatusFilter): boolean {
  if (view === 'all') return true;
  if (view === 'open') return isOpenAlert(alert);
  return alert.status === view;
}

export interface Feed {
  /** Top-level alerts, newest start first. */
  rows: Alert[];
  /** Suppressed downstream alerts folded under a row, by that row's id, oldest first. */
  children: Map<string, Alert[]>;
}

/**
 * The feed for a view: every alert that was open at some point since
 * `start` (so every unresolved alert, however old), newest start first.
 *
 * In the views that show the whole queue ('all', 'open') an alert the
 * alerter is suppressing for a dependency folds under its root cause's alert
 * (foldSuppressed), so an outage and its blast radius are one entry. A
 * one-status view lists exactly the alerts in that status, unfolded: a parent
 * in the other status would otherwise hide them.
 */
export function feedRows(alerts: readonly Alert[], view: AlertStatusFilter, start: number): Feed {
  const inRange = alerts.filter((a) => isOpenAlert(a) || Date.parse(a.resolved_at ?? a.triggered_at) >= start);
  const folds = view === 'all' || view === 'open';
  const parentOf = folds ? foldSuppressed(inRange) : new Map<string, string>();
  const newestFirst = (a: Alert, b: Alert) => Date.parse(b.triggered_at) - Date.parse(a.triggered_at) || a.id.localeCompare(b.id);

  const children = new Map<string, Alert[]>();
  for (const alert of inRange) {
    const parent = parentOf.get(alert.id);
    if (!parent) continue;
    const list = children.get(parent);
    if (list) list.push(alert);
    else children.set(parent, [alert]);
  }
  for (const list of children.values()) list.sort((a, b) => -newestFirst(a, b));

  const rows = inRange.filter((alert) => !parentOf.has(alert.id) && alertInView(alert, view)).sort(newestFirst);
  return { rows, children };
}

/** The feed's time headings: one per hour up to a day's range, one per date beyond. */
export type FeedGrain = 'hour' | 'day';

export function feedGrain(rangeMs: number): FeedGrain {
  return rangeMs <= 24 * 3_600_000 ? 'hour' : 'day';
}

/** The start of the local hour or day `at` falls in. */
export function bucketStart(at: number, grain: FeedGrain): number {
  const d = new Date(at);
  if (grain === 'hour') d.setMinutes(0, 0, 0);
  else d.setHours(0, 0, 0, 0);
  return d.getTime();
}

/** The bucket `steps` after (or, negative, before) the one starting at `start`, on the local calendar. */
export function bucketShift(start: number, grain: FeedGrain, steps: number): number {
  const d = new Date(start);
  if (grain === 'hour') d.setHours(d.getHours() + steps);
  else d.setDate(d.getDate() + steps);
  return d.getTime();
}

export type FeedItem =
  /** A bucket that has alerts; they follow it. */
  | { kind: 'heading'; at: number }
  /** A run of buckets with no alert, `from` (oldest) through `to` (newest), both bucket starts. */
  | { kind: 'gap'; from: number; to: number }
  /** Alerts that started before the range and reach into it follow this. */
  | { kind: 'earlier' }
  | { kind: 'alert'; alert: Alert };

/**
 * `rows` (newest start first) laid out on the range's calendar, newest
 * bucket first: every hour or date from now back to the range start appears,
 * as a heading over its alerts or folded into a gap that says nothing
 * happened, so the list reads as the history it is. Alerts that started
 * before the range (still open, or resolved inside it) close the list under
 * 'earlier'.
 *
 * `complete` is whether `rows` is everything the view has: when more rows
 * are still to come the layout stops at the last one, since the buckets
 * below it are not known to be empty.
 */
export function feedItems(
  rows: readonly Alert[],
  window: { start: number; end: number },
  grain: FeedGrain,
  complete: boolean
): FeedItem[] {
  const items: FeedItem[] = [];
  const oldest = bucketStart(window.start, grain);
  let gap: { from: number; to: number } | null = null;
  const flushGap = () => {
    if (gap) items.push({ kind: 'gap', ...gap });
    gap = null;
  };
  let index = 0;
  for (let bucket = bucketStart(window.end, grain); bucket >= oldest; bucket = bucketShift(bucket, grain, -1)) {
    const from = index;
    // A start past the window's end (clock skew) belongs to the newest bucket.
    while (index < rows.length && Date.parse(rows[index].triggered_at) >= bucket) index++;
    if (index > from) {
      flushGap();
      items.push({ kind: 'heading', at: bucket });
      for (let i = from; i < index; i++) items.push({ kind: 'alert', alert: rows[i] });
    } else if (!complete && index >= rows.length) {
      break;
    } else {
      gap = { from: bucket, to: gap ? gap.to : bucket };
    }
  }
  if (complete || index < rows.length) flushGap();
  if (index < rows.length) {
    items.push({ kind: 'earlier' });
    for (let i = index; i < rows.length; i++) items.push({ kind: 'alert', alert: rows[i] });
  }
  return items;
}

/** The tone of the alert right now: what its dot on the rail shows. */
export function alertTone(alert: Alert): TimelineTone {
  return currentTone(alert, alert.suppression_reason === 'dependency');
}

/**
 * Applies a live stream event to a listed alert. Events are built from core
 * NATS and carry only the alert's identity, status, times and root cause, so
 * everything else a row shows is kept from `prev`: triage (mergeAlertUpdate),
 * the kind and its values, the mesh subject, the acknowledgement time, and,
 * while the alert stays open, its suppression and blast radius. An
 * acknowledgement with no time takes the event's. The user's own
 * acknowledge/resolve returns the whole alert: apply that with
 * mergeAlertUpdate, never this.
 */
export function mergeLiveAlert(prev: Alert | undefined, incoming: Alert): Alert {
  const next: Alert = { ...mergeAlertUpdate(prev, incoming) };
  if (prev) {
    if (!next.kind) next.kind = prev.kind;
    next.acknowledged_at ??= prev.acknowledged_at;
    next.baseline_latency_ms ??= prev.baseline_latency_ms;
    next.observed_latency_ms ??= prev.observed_latency_ms;
    next.anomaly_score ??= prev.anomaly_score;
    next.metric_name ??= prev.metric_name;
    next.metric_value ??= prev.metric_value;
    next.threshold_value ??= prev.threshold_value;
    next.source_location_id ??= prev.source_location_id;
    next.target_location_id ??= prev.target_location_id;
    next.source_location_name ??= prev.source_location_name;
    next.target_location_name ??= prev.target_location_name;
    next.monitor_name ??= prev.monitor_name;
    if (isOpenAlert(next)) {
      next.suppression_reason ??= prev.suppression_reason;
      next.impacted_count ??= prev.impacted_count;
    }
  }
  if (next.status === 'acknowledged' && !next.acknowledged_at) next.acknowledged_at = incoming.updated_at;
  return next;
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

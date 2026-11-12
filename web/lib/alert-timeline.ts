import type { Alert } from './types';

/**
 * The /alerts timeline's model, kept pure so node tests cover it: which open
 * alerts fold under a root cause, how one alert becomes line segments inside
 * a window, how alerts gather into lanes and groups, and where the axis
 * ticks. components/alerts/AlertTimeline.tsx only lays the result out.
 */

/** What a line segment says about the alert during that span. */
export type TimelineTone = 'active' | 'acknowledged' | 'suppressed' | 'resolved';

/** Draw order where spans overlap on a lane, and which open alert a lane speaks for. */
const TONE_RANK: Record<TimelineTone, number> = { resolved: 0, suppressed: 1, acknowledged: 2, active: 3 };

export interface TimelineWindow {
  /** Epoch ms. */
  start: number;
  end: number;
}

export interface TimelineSegment {
  alertId: string;
  /** Epoch ms, clamped to the window. */
  from: number;
  to: number;
  tone: TimelineTone;
}

/** Where an alert began, so a span too short to draw still shows. */
export interface TimelineMarker {
  alertId: string;
  at: number;
  tone: TimelineTone;
}

export interface TimelineLane {
  /** The subject: a monitor id, or a mesh location pair. */
  key: string;
  label: string;
  monitorId?: string;
  /** Sorted so the tone that matters most draws last. */
  segments: TimelineSegment[];
  markers: TimelineMarker[];
  /** The lane's leading open alert: its start and tone; null when nothing is open. */
  openSince: number | null;
  openTone: TimelineTone | null;
  /** The alert a click on the lane selects: the leading open one, else the newest. */
  focusAlertId: string;
  alertCount: number;
  /** Lanes of the suppressed downstream alerts folded under this lane's alert. */
  children: TimelineLane[];
}

export interface TimelineGroup {
  key: string;
  label: string;
  lanes: TimelineLane[];
  alertCount: number;
}

export interface Timeline {
  /** Subjects with an open alert, unacknowledged first then newest: the queue. */
  open: TimelineLane[];
  /** Everything else that alerted in the window, busiest group first. */
  groups: TimelineGroup[];
  /** Subjects that alerted in the window, folded ones included. */
  subjectCount: number;
}

const time = (value?: string): number | null => {
  if (!value) return null;
  const ms = Date.parse(value);
  return Number.isNaN(ms) ? null : ms;
};

export const isOpenAlert = (alert: Alert): boolean => alert.status !== 'resolved';

/** The alert's subject as the list names it. */
export function alertSubject(alert: Alert): string {
  return alert.kind === 'mesh_edge'
    ? `${alert.source_location_name || 'unknown'} → ${alert.target_location_name || 'unknown'}`
    : alert.monitor_name || alert.monitor_id || '';
}

/** One lane per subject: a monitor, or a directed mesh edge. */
export function alertSubjectKey(alert: Alert): string {
  if (alert.monitor_id) return alert.monitor_id;
  if (alert.kind === 'mesh_edge') return `mesh:${alert.source_location_id ?? ''}>${alert.target_location_id ?? ''}`;
  return `alert:${alert.id}`;
}

/**
 * Which open alerts fold under another: child alert id → parent alert id.
 *
 * A child is an open alert the alerter is suppressing for a dependency
 * (`suppression_reason`), whose root cause is another monitor that has an
 * open availability alert in `alerts`: the alert whose notification stands in
 * for it (`impacted_count`). Everything else stays top-level, because it
 * pages or will: a "likely caused by" alert that is not suppressed, one
 * whose upstream just recovered (no root named), and one whose parent is not
 * loaded.
 */
export function foldSuppressed(alerts: readonly Alert[]): Map<string, string> {
  const parentByMonitor = new Map<string, Alert>();
  for (const alert of alerts) {
    if (!isOpenAlert(alert) || alert.kind !== 'availability' || !alert.monitor_id) continue;
    if (alert.suppression_reason === 'dependency') continue;
    const current = parentByMonitor.get(alert.monitor_id);
    if (!current || Date.parse(alert.triggered_at) < Date.parse(current.triggered_at)) {
      parentByMonitor.set(alert.monitor_id, alert);
    }
  }
  const parentOf = new Map<string, string>();
  for (const alert of alerts) {
    if (!isOpenAlert(alert) || alert.suppression_reason !== 'dependency') continue;
    const root = alert.root_cause_monitor_id;
    if (!root || root === alert.monitor_id) continue;
    const parent = parentByMonitor.get(root);
    if (parent && parent.id !== alert.id) parentOf.set(alert.id, parent.id);
  }
  return parentOf;
}

/**
 * The alert as line segments inside `window`, oldest first; empty when the
 * alert never overlaps it. An acknowledged alert is active until its
 * acknowledgement and acknowledged after; a resolved one is one resolved
 * span whatever happened on the way; a suppressed one is suppressed
 * throughout (`suppressed` comes from the API's own predicate).
 */
export function alertSegments(alert: Alert, window: TimelineWindow, suppressed: boolean): TimelineSegment[] {
  const triggered = time(alert.triggered_at);
  if (triggered === null) return [];
  const resolved = time(alert.resolved_at);
  // A trigger a few seconds past the window's end (server clock ahead of
  // the browser's) still draws, at the edge, rather than hiding a new alert.
  const from = Math.min(Math.max(triggered, window.start), window.end);
  const to = Math.max(from, Math.min(resolved ?? window.end, window.end));
  if ((resolved ?? window.end) < window.start) return [];
  const span = (a: number, b: number, tone: TimelineTone): TimelineSegment => ({ alertId: alert.id, from: a, to: b, tone });

  if (alert.status === 'resolved') return [span(from, to, 'resolved')];
  if (suppressed) return [span(from, to, 'suppressed')];
  if (alert.status !== 'acknowledged') return [span(from, to, 'active')];
  const acked = time(alert.acknowledged_at);
  if (acked === null || acked <= from) return [span(from, to, 'acknowledged')];
  if (acked >= to) return [span(from, to, 'active')];
  return [span(from, acked, 'active'), span(acked, to, 'acknowledged')];
}

/** The tone an alert has now (its last segment's). */
export function currentTone(alert: Alert, suppressed: boolean): TimelineTone {
  if (alert.status === 'resolved') return 'resolved';
  if (suppressed) return 'suppressed';
  return alert.status === 'acknowledged' ? 'acknowledged' : 'active';
}

function buildLane(
  key: string,
  alerts: readonly Alert[],
  window: TimelineWindow,
  suppressedIds: ReadonlySet<string>
): TimelineLane | null {
  const segments: TimelineSegment[] = [];
  const markers: TimelineMarker[] = [];
  let lead: { alert: Alert; tone: TimelineTone } | null = null;
  let newest: Alert | null = null;
  let count = 0;

  for (const alert of alerts) {
    const suppressed = suppressedIds.has(alert.id);
    const spans = alertSegments(alert, window, suppressed);
    if (spans.length === 0) continue;
    count++;
    segments.push(...spans);
    const triggered = Date.parse(alert.triggered_at);
    if (triggered >= window.start) markers.push({ alertId: alert.id, at: triggered, tone: spans[0].tone });
    if (!newest || triggered > Date.parse(newest.triggered_at)) newest = alert;
    if (isOpenAlert(alert)) {
      const tone = currentTone(alert, suppressed);
      // The open alert that matters most; the newest among equals, as the queue orders them.
      if (
        !lead ||
        TONE_RANK[tone] > TONE_RANK[lead.tone] ||
        (TONE_RANK[tone] === TONE_RANK[lead.tone] && triggered > Date.parse(lead.alert.triggered_at))
      ) {
        lead = { alert, tone };
      }
    }
  }
  if (count === 0 || !newest) return null;
  segments.sort((a, b) => TONE_RANK[a.tone] - TONE_RANK[b.tone] || a.from - b.from);
  return {
    key,
    label: alertSubject(newest),
    monitorId: newest.monitor_id,
    segments,
    markers,
    openSince: lead ? Date.parse(lead.alert.triggered_at) : null,
    openTone: lead ? lead.tone : null,
    focusAlertId: (lead?.alert ?? newest).id,
    alertCount: count,
    children: [],
  };
}

export interface TimelineGroupRef {
  key: string;
  label: string;
}

/**
 * Lanes and groups for every alert overlapping `window`.
 *
 * A subject with an open top-level alert goes to `open`, and the subjects
 * folded under that alert (`foldSuppressed`) nest as its `children`: an
 * outage and its blast radius read as one entry. A folded subject whose
 * parent has no lane (it cannot happen while both are loaded) stays
 * top-level. Everything else lands in `groupOf`'s group.
 */
export function buildTimeline(
  alerts: readonly Alert[],
  window: TimelineWindow,
  groupOf: (alert: Alert) => TimelineGroupRef
): Timeline {
  const parentOf = foldSuppressed(alerts);
  const byId = new Map(alerts.map((alert) => [alert.id, alert]));
  const suppressedIds = new Set(
    alerts.filter((a) => isOpenAlert(a) && a.suppression_reason === 'dependency').map((a) => a.id)
  );

  const bySubject = new Map<string, Alert[]>();
  for (const alert of alerts) {
    const key = alertSubjectKey(alert);
    const list = bySubject.get(key);
    if (list) list.push(alert);
    else bySubject.set(key, [alert]);
  }

  // A subject folds under the parent of its open suppressed alert, unless it
  // also holds an open alert of its own that pages.
  const foldUnder = new Map<string, string>();
  for (const [key, list] of bySubject) {
    const open = list.filter(isOpenAlert);
    if (open.length === 0 || !open.every((a) => parentOf.has(a.id))) continue;
    const parent = byId.get(parentOf.get(open[0].id)!);
    if (parent) foldUnder.set(key, alertSubjectKey(parent));
  }

  const lanes = new Map<string, TimelineLane>();
  for (const [key, list] of bySubject) {
    const lane = buildLane(key, list, window, suppressedIds);
    if (lane) lanes.set(key, lane);
  }

  const byNewest = (a: TimelineLane, b: TimelineLane) =>
    Math.max(...b.markers.map((m) => m.at), b.openSince ?? 0, 0) - Math.max(...a.markers.map((m) => m.at), a.openSince ?? 0, 0) ||
    a.label.localeCompare(b.label);

  const open: TimelineLane[] = [];
  const grouped = new Map<string, TimelineGroup>();
  for (const [key, lane] of lanes) {
    const parentKey = foldUnder.get(key);
    const parent = parentKey ? lanes.get(parentKey) : undefined;
    if (parent && parent !== lane) {
      parent.children.push(lane);
      continue;
    }
    if (lane.openSince !== null) {
      open.push(lane);
      continue;
    }
    const ref = groupOf(byId.get(lane.focusAlertId)!);
    const group = grouped.get(ref.key) ?? { key: ref.key, label: ref.label, lanes: [], alertCount: 0 };
    group.lanes.push(lane);
    group.alertCount += lane.alertCount;
    grouped.set(ref.key, group);
  }

  open.sort(
    (a, b) =>
      TONE_RANK[b.openTone ?? 'resolved'] - TONE_RANK[a.openTone ?? 'resolved'] ||
      (b.openSince ?? 0) - (a.openSince ?? 0) ||
      a.label.localeCompare(b.label)
  );
  for (const lane of open) lane.children.sort((a, b) => (a.openSince ?? 0) - (b.openSince ?? 0) || a.label.localeCompare(b.label));
  const groups = [...grouped.values()].sort((a, b) => b.alertCount - a.alertCount || a.label.localeCompare(b.label));
  for (const group of groups) group.lanes.sort(byNewest);

  return { open, groups, subjectCount: lanes.size };
}

/** Every segment of a group's lanes, for the one line a collapsed group draws. */
export function groupSegments(lanes: readonly TimelineLane[]): TimelineSegment[] {
  return lanes
    .flatMap((lane) => [...lane.segments, ...groupSegments(lane.children)])
    .sort((a, b) => TONE_RANK[a.tone] - TONE_RANK[b.tone] || a.from - b.from);
}

/** Every start marker of a group's lanes, for the same collapsed row. */
export function groupMarkers(lanes: readonly TimelineLane[]): TimelineMarker[] {
  return lanes.flatMap((lane) => [...lane.markers, ...groupMarkers(lane.children)]);
}

/** A segment's place on the track, as fractions of the window. */
export function segmentBox(segment: { from: number; to: number }, window: TimelineWindow): { left: number; width: number } {
  const span = window.end - window.start;
  if (span <= 0) return { left: 0, width: 0 };
  const left = Math.min(1, Math.max(0, (segment.from - window.start) / span));
  const right = Math.min(1, Math.max(0, (segment.to - window.start) / span));
  return { left, width: Math.max(0, right - left) };
}

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const TICK_STEPS = [
  5 * MINUTE, 10 * MINUTE, 15 * MINUTE, 30 * MINUTE, HOUR, 2 * HOUR, 3 * HOUR, 6 * HOUR, 12 * HOUR,
  DAY, 2 * DAY, 7 * DAY, 14 * DAY, 30 * DAY, 60 * DAY,
];

export interface TimelineTick {
  at: number;
  /** A day boundary reads as a date, anything else as a clock time. */
  kind: 'time' | 'day';
}

/**
 * Axis ticks for `window`: the finest step from TICK_STEPS that fits in
 * `maxTicks`, on local clock boundaries (10:00, 12:00, midnight), so labels
 * are round and stay put while the window slides.
 */
export function timelineTicks(window: TimelineWindow, maxTicks = 8): TimelineTick[] {
  const span = window.end - window.start;
  if (span <= 0) return [];
  const step = TICK_STEPS.find((s) => span / s <= maxTicks) ?? TICK_STEPS[TICK_STEPS.length - 1];
  const ticks: TimelineTick[] = [];
  const cursor = new Date(window.start);
  cursor.setHours(0, 0, 0, 0);
  if (step >= DAY) {
    const days = step / DAY;
    for (; cursor.getTime() <= window.end; cursor.setDate(cursor.getDate() + days)) {
      if (cursor.getTime() >= window.start) ticks.push({ at: cursor.getTime(), kind: 'day' });
    }
    return ticks;
  }
  // Walk each local day from its own midnight, so a DST change never shifts
  // the ticks off the hour.
  for (; cursor.getTime() <= window.end; cursor.setDate(cursor.getDate() + 1)) {
    const next = new Date(cursor);
    next.setDate(next.getDate() + 1);
    for (let at = cursor.getTime(); at < next.getTime(); at += step) {
      if (at < window.start || at > window.end) continue;
      ticks.push({ at, kind: at === cursor.getTime() ? 'day' : 'time' });
    }
  }
  return ticks;
}

/**
 * The window's end for `now`: `now` raised to the next multiple of what one
 * pixel spans on a track about `pixels` wide, so a ticking clock re-lays the
 * timeline only when a line would actually move. Raised, never floored: an
 * alert that just fired must still fall inside the window.
 */
export function timelineNow(now: number, rangeMs: number, pixels = 1200): number {
  const grain = Math.max(1_000, Math.round(rangeMs / pixels));
  return Math.ceil(now / grain) * grain;
}

/**
 * How many times each subject alerted in the window, and each alert's place
 * in that run (1 = first), by alert id. Folded alerts count for their own
 * subject. Callers must not show this over a truncated set.
 */
export function occurrences(alerts: readonly Alert[], window: TimelineWindow): Map<string, { nth: number; total: number }> {
  const bySubject = new Map<string, Alert[]>();
  for (const alert of alerts) {
    const triggered = Date.parse(alert.triggered_at);
    if (Number.isNaN(triggered) || triggered < window.start || triggered > window.end) continue;
    const key = alertSubjectKey(alert);
    const list = bySubject.get(key);
    if (list) list.push(alert);
    else bySubject.set(key, [alert]);
  }
  const result = new Map<string, { nth: number; total: number }>();
  for (const list of bySubject.values()) {
    list.sort((a, b) => Date.parse(a.triggered_at) - Date.parse(b.triggered_at));
    list.forEach((alert, index) => result.set(alert.id, { nth: index + 1, total: list.length }));
  }
  return result;
}

/** What the timeline needs to know about an alert's monitor (from the monitors list). */
export interface TimelineMonitor {
  type: string;
  tags?: readonly string[];
}

/**
 * The group a quiet lane sits in. With dashboard group tags configured
 * (tenant `dashboard_group_tags`) it is the first of them the monitor
 * carries, so the timeline groups like the dashboard's service groups;
 * without any, monitors group by type. Mesh edges have a group of their own,
 * and a monitor the list does not know (deleted, or past the lookup cap)
 * falls into "Other".
 */
export function timelineGroupOf(
  alert: Alert,
  monitor: TimelineMonitor | undefined,
  groupTags: readonly string[],
  typeLabel: (type: string) => string
): TimelineGroupRef {
  if (alert.kind === 'mesh_edge') return { key: 'mesh', label: 'Mesh' };
  if (!monitor) return { key: 'other', label: 'Other' };
  if (groupTags.length === 0) return { key: `type:${monitor.type}`, label: typeLabel(monitor.type) };
  const tag = groupTags.find((candidate) => monitor.tags?.includes(candidate));
  return tag ? { key: `tag:${tag}`, label: tag } : { key: 'ungrouped', label: 'Ungrouped' };
}

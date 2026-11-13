import assert from 'node:assert/strict';
import test from 'node:test';
import {
  alertSegments,
  buildTimeline,
  foldSuppressed,
  groupSegments,
  occurrences,
  segmentBox,
  timelineGroupOf,
  timelineNow,
  timelineTicks,
} from './alert-timeline';
import type { Alert } from './types';

const T0 = Date.parse('2026-11-12T10:00:00Z');
const MIN = 60_000;
const at = (minutes: number) => new Date(T0 + minutes * MIN).toISOString();
const WINDOW = { start: T0, end: T0 + 120 * MIN };

function alert(id: string, monitor: string, over: Partial<Alert> = {}): Alert {
  return {
    id,
    tenant_id: 't',
    monitor_id: monitor,
    monitor_name: monitor,
    status: 'active',
    triggered_at: at(10),
    failure_count: 3,
    kind: 'availability',
    created_at: at(10),
    updated_at: at(10),
    ...over,
  };
}

const group = (a: Alert) => ({ key: a.monitor_id?.[0] ?? '?', label: `Group ${a.monitor_id?.[0] ?? '?'}` });

test('alertSegments: an acknowledged alert is active until its acknowledgement', () => {
  const a = alert('a', 'm', { status: 'acknowledged', triggered_at: at(10), acknowledged_at: at(30) });
  assert.deepEqual(
    alertSegments(a, WINDOW, false).map((s) => [s.tone, s.from - T0, s.to - T0]),
    [
      ['active', 10 * MIN, 30 * MIN],
      ['acknowledged', 30 * MIN, 120 * MIN],
    ]
  );
  // Acknowledged before the window opens: acknowledged throughout.
  const early = alert('b', 'm', { status: 'acknowledged', triggered_at: at(-60), acknowledged_at: at(-30) });
  assert.deepEqual(alertSegments(early, WINDOW, false).map((s) => s.tone), ['acknowledged']);
  // No acknowledgement time (a live event): still one acknowledged span.
  const untimed = alert('c', 'm', { status: 'acknowledged' });
  assert.deepEqual(alertSegments(untimed, WINDOW, false).map((s) => s.tone), ['acknowledged']);
});

test('alertSegments: resolved, suppressed and out-of-window alerts', () => {
  const resolved = alert('a', 'm', { status: 'resolved', triggered_at: at(-20), acknowledged_at: at(5), resolved_at: at(40) });
  assert.deepEqual(
    alertSegments(resolved, WINDOW, false).map((s) => [s.tone, s.from - T0, s.to - T0]),
    [['resolved', 0, 40 * MIN]]
  );
  assert.deepEqual(alertSegments(alert('b', 'm'), WINDOW, true).map((s) => s.tone), ['suppressed']);
  const before = alert('c', 'm', { status: 'resolved', triggered_at: at(-90), resolved_at: at(-30) });
  assert.deepEqual(alertSegments(before, WINDOW, false), []);
  // A trigger just past the window's end (clock skew) draws at the edge.
  const ahead = alertSegments(alert('d', 'm', { triggered_at: at(121) }), WINDOW, false);
  assert.deepEqual(ahead.map((s) => [s.tone, s.from - T0, s.to - T0]), [['active', 120 * MIN, 120 * MIN]]);
});

test('foldSuppressed folds only suppressed alerts whose root has an open availability alert', () => {
  const root = alert('root', 'db');
  const child = alert('child', 'api', { suppression_reason: 'dependency', root_cause_monitor_id: 'db' });
  const likely = alert('likely', 'web', { root_cause_monitor_id: 'db' });
  const grace = alert('grace', 'cron', { suppression_reason: 'dependency' });
  const self = alert('self', 'db2', { suppression_reason: 'dependency', root_cause_monitor_id: 'db2' });
  const orphan = alert('orphan', 'queue', { suppression_reason: 'dependency', root_cause_monitor_id: 'gone' });
  const latencyRoot = alert('lat', 'cache', { kind: 'latency_anomaly' });
  const underLatency = alert('ul', 'shop', { suppression_reason: 'dependency', root_cause_monitor_id: 'cache' });
  const folded = foldSuppressed([root, child, likely, grace, self, orphan, latencyRoot, underLatency]);
  assert.deepEqual([...folded], [['child', 'root']]);

  // A resolved root is no parent.
  const recovered = foldSuppressed([alert('root', 'db', { status: 'resolved', resolved_at: at(20) }), child]);
  assert.equal(recovered.size, 0);
});

test('buildTimeline: open subjects lead, their suppressed dependents nest, the rest group', () => {
  const alerts = [
    alert('root', 'db', { triggered_at: at(60) }),
    alert('k1', 'api', { triggered_at: at(61), suppression_reason: 'dependency', root_cause_monitor_id: 'db' }),
    alert('k2', 'auth', { triggered_at: at(62), suppression_reason: 'dependency', root_cause_monitor_id: 'db' }),
    alert('acked', 'sip', { status: 'acknowledged', triggered_at: at(5), acknowledged_at: at(15) }),
    alert('newer', 'edge', { triggered_at: at(90) }),
    alert('r1', 'mail', { status: 'resolved', triggered_at: at(20), resolved_at: at(25) }),
    alert('r2', 'mail', { status: 'resolved', triggered_at: at(50), resolved_at: at(58) }),
    alert('r3', 'mx', { status: 'resolved', triggered_at: at(30), resolved_at: at(31) }),
    alert('old', 'zzz', { status: 'resolved', triggered_at: at(-300), resolved_at: at(-200) }),
  ];
  const timeline = buildTimeline(alerts, WINDOW, group);

  // Unacknowledged first (newest first), then acknowledged.
  assert.deepEqual(timeline.open.map((l) => l.key), ['edge', 'db', 'sip']);
  const db = timeline.open[1];
  assert.deepEqual(db.children.map((l) => l.key), ['api', 'auth']);
  assert.equal(db.children[0].openTone, 'suppressed');
  assert.equal(db.focusAlertId, 'root');

  assert.deepEqual(timeline.groups.map((g) => [g.key, g.alertCount, g.lanes.map((l) => l.key)]), [['m', 3, ['mail', 'mx']]]);
  assert.equal(timeline.groups[0].lanes[0].focusAlertId, 'r2', 'a quiet lane selects its newest alert');
  assert.equal(timeline.subjectCount, 7, 'the alert outside the window has no lane');
});

test('buildTimeline: a lane with two open alerts speaks for the one that pages', () => {
  const alerts = [
    alert('down', 'm', { status: 'acknowledged', triggered_at: at(10), acknowledged_at: at(20) }),
    alert('slow', 'm', { kind: 'latency_anomaly', triggered_at: at(40) }),
  ];
  const lane = buildTimeline(alerts, WINDOW, group).open[0];
  assert.equal(lane.alertCount, 2);
  assert.equal(lane.openTone, 'active');
  assert.equal(lane.focusAlertId, 'slow');
  // Active draws last, over the acknowledged span it overlaps.
  assert.equal(lane.segments[lane.segments.length - 1].tone, 'active');
});

test('buildTimeline: a subject with its own paging alert never folds', () => {
  const alerts = [
    alert('root', 'db'),
    alert('sup', 'api', { suppression_reason: 'dependency', root_cause_monitor_id: 'db' }),
    alert('own', 'api', { kind: 'latency_anomaly' }),
  ];
  const timeline = buildTimeline(alerts, WINDOW, group);
  assert.deepEqual(timeline.open.map((l) => l.key).sort(), ['api', 'db']);
  assert.equal(timeline.open.every((l) => l.children.length === 0), true);
});

test('mesh alerts get one lane per directed edge', () => {
  const mesh = (id: string, src: string, dst: string) =>
    alert(id, '', {
      monitor_id: undefined,
      monitor_name: undefined,
      kind: 'mesh_edge',
      source_location_id: src,
      target_location_id: dst,
      source_location_name: src,
      target_location_name: dst,
    });
  const timeline = buildTimeline([mesh('a', 'paris', 'fra'), mesh('b', 'fra', 'paris')], WINDOW, () => ({ key: 'mesh', label: 'Mesh' }));
  assert.deepEqual(timeline.open.map((l) => l.label).sort(), ['fra → paris', 'paris → fra']);
});

test('groupSegments flattens lanes and their children, most important tone last', () => {
  const alerts = [
    alert('root', 'db', { triggered_at: at(60) }),
    alert('k1', 'api', { triggered_at: at(61), suppression_reason: 'dependency', root_cause_monitor_id: 'db' }),
  ];
  const tones = groupSegments(buildTimeline(alerts, WINDOW, group).open).map((s) => s.tone);
  assert.deepEqual(tones, ['suppressed', 'active']);
});

test('segmentBox places a span as fractions of the window', () => {
  assert.deepEqual(segmentBox({ from: T0 + 30 * MIN, to: T0 + 60 * MIN }, WINDOW), { left: 0.25, width: 0.25 });
  assert.deepEqual(segmentBox({ from: T0 - 30 * MIN, to: T0 + 300 * MIN }, WINDOW), { left: 0, width: 1 });
});

test('timelineTicks sit on round local clock times and fit the budget', () => {
  const day = { start: new Date(2026, 10, 11, 14, 20).getTime(), end: new Date(2026, 10, 12, 14, 20).getTime() };
  const ticks = timelineTicks(day);
  assert.equal(ticks.length <= 8, true);
  assert.equal(ticks.every((t) => new Date(t.at).getMinutes() === 0 && new Date(t.at).getHours() % 3 === 0), true);
  assert.deepEqual(ticks.filter((t) => t.kind === 'day').map((t) => new Date(t.at).getDate()), [12]);

  const hour = { start: new Date(2026, 10, 12, 13, 22).getTime(), end: new Date(2026, 10, 12, 14, 22).getTime() };
  assert.deepEqual(timelineTicks(hour).map((t) => new Date(t.at).getMinutes()), [30, 40, 50, 0, 10, 20]);

  const month = { start: new Date(2026, 9, 13, 9).getTime(), end: new Date(2026, 10, 12, 9).getTime() };
  const days = timelineTicks(month);
  assert.equal(days.length <= 8 && days.every((t) => t.kind === 'day' && new Date(t.at).getHours() === 0), true);

  const year = { start: new Date(2025, 10, 12, 9).getTime(), end: new Date(2026, 10, 12, 9).getTime() };
  assert.equal(timelineTicks(year).length <= 8, true);
});

test('timelineNow moves only when a line would', () => {
  const dayMs = 24 * 60 * MIN;
  const now = T0 + 12_345;
  assert.equal(timelineNow(now, dayMs), timelineNow(now + 30_000, dayMs));
  assert.notEqual(timelineNow(now, 60 * MIN), timelineNow(now + 30_000, 60 * MIN));
  assert.equal(timelineNow(now, dayMs) >= now, true, 'an alert that just fired is inside the window');
});

test('occurrences number a subject\'s alerts inside the window', () => {
  const alerts = [
    alert('r1', 'mail', { status: 'resolved', triggered_at: at(20), resolved_at: at(25) }),
    alert('r2', 'mail', { status: 'resolved', triggered_at: at(50), resolved_at: at(58) }),
    alert('before', 'mail', { status: 'resolved', triggered_at: at(-50), resolved_at: at(5) }),
    alert('solo', 'db'),
  ];
  const counts = occurrences(alerts, WINDOW);
  assert.deepEqual(counts.get('r1'), { nth: 1, total: 2 });
  assert.deepEqual(counts.get('r2'), { nth: 2, total: 2 });
  assert.deepEqual(counts.get('solo'), { nth: 1, total: 1 });
  assert.equal(counts.has('before'), false);
});

test('timelineGroupOf: dashboard group tags first, else the monitor type', () => {
  const label = (type: string) => type.toUpperCase();
  const a = alert('a', 'm');
  assert.deepEqual(timelineGroupOf(a, { type: 'http', tags: ['eu', 'payments'] }, ['payments', 'eu'], label), { key: 'tag:payments', label: 'payments' });
  assert.deepEqual(timelineGroupOf(a, { type: 'http', tags: ['misc'] }, ['payments'], label), { key: 'ungrouped', label: 'Ungrouped' });
  assert.deepEqual(timelineGroupOf(a, { type: 'http' }, [], label), { key: 'type:http', label: 'HTTP' });
  assert.deepEqual(timelineGroupOf(a, undefined, ['payments'], label), { key: 'other', label: 'Other' });
  assert.deepEqual(timelineGroupOf(alert('e', '', { kind: 'mesh_edge' }), undefined, [], label), { key: 'mesh', label: 'Mesh' });
});

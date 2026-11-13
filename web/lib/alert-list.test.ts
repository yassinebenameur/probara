import assert from 'node:assert/strict';
import test from 'node:test';
import {
  alertTone,
  bucketShift,
  bucketStart,
  feedGrain,
  feedItems,
  feedRows,
  mergeLiveAlert,
  type FeedItem,
  pruneSelection,
  rowShortcut,
  shortcutEffect,
  toggleSelection,
} from './alert-list';
import type { Alert } from './types';

const T0 = Date.parse('2026-11-12T10:00:00Z');
const at = (minutes: number) => new Date(T0 + minutes * 60_000).toISOString();

function alert(id: string, monitor: string, over: Partial<Alert> = {}): Alert {
  return {
    id,
    tenant_id: 't',
    monitor_id: monitor,
    monitor_name: monitor,
    status: 'active',
    triggered_at: at(0),
    failure_count: 3,
    kind: 'availability',
    created_at: at(0),
    updated_at: at(0),
    ...over,
  };
}

test('feedRows: newest start first, suppressed dependents folded under their root, per view', () => {
  const alerts = [
    alert('old', 'a', { triggered_at: at(1) }),
    alert('acked', 'b', { status: 'acknowledged', triggered_at: at(50) }),
    alert('root', 'db', { triggered_at: at(20) }),
    alert('k2', 'api', { triggered_at: at(22), suppression_reason: 'dependency', root_cause_monitor_id: 'db' }),
    alert('k1', 'auth', { triggered_at: at(21), suppression_reason: 'dependency', root_cause_monitor_id: 'db' }),
    alert('done', 'c', { status: 'resolved', triggered_at: at(30), resolved_at: at(35) }),
    alert('long', 'd', { status: 'resolved', triggered_at: at(-500), resolved_at: at(2) }),
    alert('before', 'e', { status: 'resolved', triggered_at: at(-90), resolved_at: at(-60) }),
    alert('ancient', 'f', { triggered_at: at(-9000) }),
  ];
  const all = feedRows(alerts, 'all', T0);
  assert.deepEqual(all.rows.map((a) => a.id), ['acked', 'done', 'root', 'old', 'long', 'ancient']);
  assert.deepEqual(all.children.get('root')?.map((a) => a.id), ['k1', 'k2']);
  assert.deepEqual(feedRows(alerts, 'open', T0).rows.map((a) => a.id), ['acked', 'root', 'old', 'ancient']);
  assert.deepEqual(feedRows(alerts, 'resolved', T0).rows.map((a) => a.id), ['done', 'long']);
});

test('feedRows: a one-status view lists every alert in that status, unfolded', () => {
  const alerts = [
    alert('root', 'db', { status: 'acknowledged' }),
    alert('kid', 'api', { triggered_at: at(1), suppression_reason: 'dependency', root_cause_monitor_id: 'db' }),
  ];
  const active = feedRows(alerts, 'active', T0);
  assert.deepEqual(active.rows.map((a) => a.id), ['kid']);
  assert.equal(active.children.size, 0);
  assert.deepEqual(feedRows(alerts, 'acknowledged', T0).rows.map((a) => a.id), ['root']);
});

const DAY = (d: number, h = 12) => new Date(2026, 10, d, h).getTime();
const on = (id: string, d: number, h = 12, over: Partial<Alert> = {}) =>
  alert(id, id, { triggered_at: new Date(DAY(d, h)).toISOString(), ...over });
const shape = (items: FeedItem[]) =>
  items.map((item) =>
    item.kind === 'alert'
      ? item.alert.id
      : item.kind === 'heading'
        ? `#${new Date(item.at).getDate()}`
        : item.kind === 'gap'
          ? `gap ${new Date(item.from).getDate()}-${new Date(item.to).getDate()}`
          : 'earlier'
  );

test('feedItems: every date of the range appears, empty ones as gaps, older alerts last', () => {
  const window = { start: DAY(5), end: DAY(12) };
  const rows = [on('a', 12, 9), on('b', 12, 8), on('c', 9), on('d', 5, 18), on('open', 1)];
  assert.deepEqual(shape(feedItems(rows, window, 'day', true)), [
    '#12', 'a', 'b', 'gap 10-11', '#9', 'c', 'gap 6-8', '#5', 'd', 'earlier', 'open',
  ]);
  // Nothing at all: the whole range is one gap.
  assert.deepEqual(shape(feedItems([], window, 'day', true)), ['gap 5-12']);
});

test('feedItems: with more rows still to load, the layout stops at the last one', () => {
  const window = { start: DAY(5), end: DAY(12) };
  const rows = [on('a', 12), on('c', 9)];
  assert.deepEqual(shape(feedItems(rows, window, 'day', false)), ['#12', 'a', 'gap 10-11', '#9', 'c']);
  assert.deepEqual(shape(feedItems(rows, window, 'day', true)), ['#12', 'a', 'gap 10-11', '#9', 'c', 'gap 5-8']);
});

test('feedItems: by the hour up to a day, on local clock hours', () => {
  assert.equal(feedGrain(24 * 3_600_000), 'hour');
  assert.equal(feedGrain(7 * 24 * 3_600_000), 'day');
  const window = { start: DAY(12, 9) + 20 * 60_000, end: DAY(12, 12) + 20 * 60_000 };
  const rows = [on('late', 12, 12), on('early', 12, 10)];
  const items = feedItems(rows, window, 'hour', true);
  assert.deepEqual(
    items.map((item) => (item.kind === 'alert' ? item.alert.id : item.kind === 'heading' ? new Date(item.at).getHours() : item.kind === 'gap' ? [new Date(item.from).getHours(), new Date(item.to).getHours()] : 'earlier')),
    [12, 'late', [11, 11], 10, 'early', [9, 9]]
  );
  assert.equal(bucketStart(DAY(12, 9) + 59 * 60_000, 'hour'), DAY(12, 9));
  assert.equal(bucketShift(DAY(12, 0), 'day', -1), DAY(11, 0));
});

test('alertTone: what the alert is now', () => {
  assert.equal(alertTone(alert('a', 'm')), 'active');
  assert.equal(alertTone(alert('a', 'm', { status: 'acknowledged', acknowledged_at: at(5) })), 'acknowledged');
  assert.equal(alertTone(alert('a', 'm', { suppression_reason: 'dependency' })), 'suppressed');
  assert.equal(alertTone(alert('a', 'm', { status: 'resolved', resolved_at: at(9), suppression_reason: 'dependency' })), 'resolved');
});

test('mergeLiveAlert keeps what a stream event never carries', () => {
  const prev = alert('a', 'm', {
    kind: 'latency_anomaly',
    observed_latency_ms: 800,
    baseline_latency_ms: 200,
    suppression_reason: 'dependency',
    impacted_count: 0,
    root_cause_monitor_id: 'db',
  });
  // As the API builds it from core NATS: no kind, no values, no suppression.
  const event = { ...alert('a', 'm', { status: 'acknowledged', updated_at: at(7) }), kind: '' as Alert['kind'] };
  const acked = mergeLiveAlert(prev, event);
  assert.equal(acked.kind, 'latency_anomaly');
  assert.equal(acked.observed_latency_ms, 800);
  assert.equal(acked.suppression_reason, 'dependency', 'still open: still folded');
  assert.equal(acked.acknowledged_at, at(7), 'the event time stands in for the acknowledgement');

  const resolved = mergeLiveAlert(acked, { ...event, status: 'resolved', resolved_at: at(9), updated_at: at(9) });
  assert.equal(resolved.suppression_reason, undefined, 'a resolved alert is never suppressed');
  assert.equal(resolved.acknowledged_at, at(7));

  // A brand new alert passes through.
  assert.equal(mergeLiveAlert(undefined, alert('n', 'x')).id, 'n');
});

test('toggleSelection toggles one row, or a Shift range from the anchor', () => {
  const order = ['a', 'b', 'c', 'd', 'e'];
  assert.deepEqual([...toggleSelection(new Set(), order, null, 'b', false)], ['b']);
  assert.deepEqual([...toggleSelection(new Set(['b']), order, 'b', 'b', false)], []);
  // Shift from b to d selects b..d, in either direction.
  assert.deepEqual([...toggleSelection(new Set(['b']), order, 'b', 'd', true)].sort(), ['b', 'c', 'd']);
  assert.deepEqual([...toggleSelection(new Set(), order, 'e', 'c', true)].sort(), ['c', 'd', 'e']);
  // A Shift range takes the clicked row's new state: deselecting clears it.
  assert.deepEqual([...toggleSelection(new Set(order), order, 'a', 'c', true)].sort(), ['d', 'e']);
  // No anchor, or an anchor no longer shown: a plain toggle.
  assert.deepEqual([...toggleSelection(new Set(), order, null, 'c', true)], ['c']);
  assert.deepEqual([...toggleSelection(new Set(), order, 'gone', 'c', true)], ['c']);
});

test('pruneSelection drops rows that are no longer selectable, keeping identity otherwise', () => {
  const selected = new Set(['a', 'b']);
  assert.equal(pruneSelection(selected, ['a', 'b', 'c']), selected);
  assert.deepEqual([...pruneSelection(selected, ['a', 'c'])], ['a']);
  assert.deepEqual([...pruneSelection(selected, [])], []);
});

const press = (key: string, mods: Partial<{ ctrlKey: boolean; metaKey: boolean; altKey: boolean; defaultPrevented: boolean }> = {}) => ({
  key,
  ctrlKey: false,
  metaKey: false,
  altKey: false,
  defaultPrevented: false,
  ...mods,
});

test('rowShortcut maps A / R / arrows, either case', () => {
  assert.equal(rowShortcut(press('a'), false), 'acknowledge');
  assert.equal(rowShortcut(press('A'), false), 'acknowledge');
  assert.equal(rowShortcut(press('r'), false), 'resolve');
  assert.equal(rowShortcut(press('R'), false), 'resolve');
  assert.equal(rowShortcut(press('ArrowDown'), false), 'next');
  assert.equal(rowShortcut(press('ArrowUp'), false), 'previous');
  assert.equal(rowShortcut(press('x'), false), null);
  assert.equal(rowShortcut(press('Enter'), false), null);
});

test('rowShortcut never fires in text entry, menus or dialogs, under a modifier, or when handled (WCAG 2.1.4)', () => {
  for (const key of ['a', 'r', 'ArrowDown']) {
    assert.equal(rowShortcut(press(key), true), null, `${key} in an exempt target`);
    assert.equal(rowShortcut(press(key, { ctrlKey: true }), false), null, `Ctrl+${key}`);
    assert.equal(rowShortcut(press(key, { metaKey: true }), false), null, `Cmd+${key}`);
    assert.equal(rowShortcut(press(key, { altKey: true }), false), null, `Alt+${key}`);
    assert.equal(rowShortcut(press(key, { defaultPrevented: true }), false), null, `${key} already handled`);
  }
});

test('shortcutEffect applies only to an alert the action can still move', () => {
  assert.equal(shortcutEffect('acknowledge', 'active'), 'apply');
  assert.equal(shortcutEffect('acknowledge', 'acknowledged'), 'already_acknowledged');
  assert.equal(shortcutEffect('acknowledge', 'resolved'), 'already_resolved');
  assert.equal(shortcutEffect('resolve', 'active'), 'apply');
  assert.equal(shortcutEffect('resolve', 'acknowledged'), 'apply');
  assert.equal(shortcutEffect('resolve', 'resolved'), 'already_resolved');
});

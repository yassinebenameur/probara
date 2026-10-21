import assert from 'node:assert/strict';
import test from 'node:test';
import {
  ALERT_SORT_DEFAULT,
  ALERT_SORT_PAGED_DEFAULT,
  alertSortFallback,
  effectiveAlertSort,
  newAlertLeads,
  pruneSelection,
  rowShortcut,
  shortcutEffect,
  toggleSelection,
} from './alert-list';

test('each view starts in its own sort: the queue order for Open, newest first for paged views', () => {
  assert.deepEqual(alertSortFallback('open'), ALERT_SORT_DEFAULT);
  for (const view of ['active', 'acknowledged', 'resolved', 'all'] as const) {
    assert.deepEqual(alertSortFallback(view), ALERT_SORT_PAGED_DEFAULT);
  }
});

test('a status sort under a one-status filter shows as the newest-first order the API applies', () => {
  for (const view of ['active', 'acknowledged', 'resolved'] as const) {
    assert.deepEqual(effectiveAlertSort({ key: 'status', dir: 'asc' }, view), { key: 'started', dir: 'desc' });
    assert.deepEqual(effectiveAlertSort({ key: 'status', dir: 'desc' }, view), { key: 'started', dir: 'desc' });
    assert.deepEqual(effectiveAlertSort({ key: 'monitor', dir: 'asc' }, view), { key: 'monitor', dir: 'asc' });
  }
  assert.deepEqual(effectiveAlertSort({ key: 'status', dir: 'asc' }, 'all'), { key: 'status', dir: 'asc' });
  assert.deepEqual(effectiveAlertSort({ key: 'status', dir: 'desc' }, 'open'), { key: 'status', dir: 'desc' });
});

test('a live alert leads page 1 only where the sort puts it there', () => {
  assert.equal(newAlertLeads({ key: 'started', dir: 'desc' }, 'resolved'), true);
  assert.equal(newAlertLeads({ key: 'started', dir: 'asc' }, 'active'), false);
  // Status order: active first, so only an active alert goes on top.
  assert.equal(newAlertLeads({ key: 'status', dir: 'asc' }, 'active'), true);
  assert.equal(newAlertLeads({ key: 'status', dir: 'asc' }, 'acknowledged'), false);
  assert.equal(newAlertLeads({ key: 'status', dir: 'asc' }, 'resolved'), false);
  assert.equal(newAlertLeads({ key: 'status', dir: 'desc' }, 'active'), false);
  assert.equal(newAlertLeads({ key: 'monitor', dir: 'asc' }, 'active'), false);
  assert.equal(newAlertLeads({ key: 'duration', dir: 'desc' }, 'active'), false);
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

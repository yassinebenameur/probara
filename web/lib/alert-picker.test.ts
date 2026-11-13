import assert from 'node:assert/strict';
import test from 'node:test';
import { ALERT_QUERY_MAX_LENGTH, mergeAlertPage, normalizeAlertQuery } from './alert-picker';
import type { Alert, AlertListResponse } from './types';

const alert = (id: string) => ({ id, monitor_name: `monitor ${id}` }) as Alert;
const page = (ids: string[], total: number, n = 1): AlertListResponse => ({
  items: ids.map(alert),
  page: n,
  page_size: 25,
  total,
});

test('normalizeAlertQuery trims and caps the term at the API limit', () => {
  assert.equal(normalizeAlertQuery('  checkout api  '), 'checkout api');
  assert.equal(normalizeAlertQuery('   '), '');
  assert.equal(Array.from(normalizeAlertQuery('é'.repeat(ALERT_QUERY_MAX_LENGTH + 5))).length, ALERT_QUERY_MAX_LENGTH);
});

test('alerts past the first page stay reachable: later pages append until the total is read', () => {
  const first = mergeAlertPage(null, 1, page(['a', 'b'], 3));
  assert.deepEqual(first.items.map((a) => a.id), ['a', 'b']);
  assert.equal(first.hasMore, true);

  const second = mergeAlertPage(first, 2, page(['c'], 3, 2));
  assert.deepEqual(second.items.map((a) => a.id), ['a', 'b', 'c']);
  assert.equal(second.page, 2);
  assert.equal(second.hasMore, false);
});

test('a row repeated by a shifted offset page is kept once, and the offset still advances', () => {
  const first = mergeAlertPage(null, 1, page(['a', 'b'], 5));
  // A new alert arrived: page 2 starts with b again.
  const second = mergeAlertPage(first, 2, page(['b', 'c'], 5, 2));
  assert.deepEqual(second.items.map((a) => a.id), ['a', 'b', 'c']);
  assert.equal(second.fetched, 4);
  assert.equal(second.hasMore, true);
});

test('page 1 replaces the results (a new search), and an empty page ends paging', () => {
  const earlier = mergeAlertPage(null, 1, page(['a', 'b'], 9));
  const fresh = mergeAlertPage(earlier, 1, page(['z'], 1));
  assert.deepEqual(fresh.items.map((a) => a.id), ['z']);
  assert.equal(fresh.hasMore, false);

  const shrunk = mergeAlertPage(earlier, 2, page([], 9, 2));
  assert.equal(shrunk.hasMore, false, 'a short read never loops on an empty page');
});

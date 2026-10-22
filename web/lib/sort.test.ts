import assert from 'node:assert/strict';
import test from 'node:test';
import { compareValues, nextSort, readSortParams, sortRows, writeSortParams, type SortState } from './sort';

test('nulls, undefined, NaN and invalid dates sort last in both directions', () => {
  const values = [3, null, 1, undefined, Number.NaN, 2];
  assert.deepEqual(sortRows(values, (v) => v, 'asc').slice(0, 3), [1, 2, 3]);
  assert.deepEqual(sortRows(values, (v) => v, 'desc').slice(0, 3), [3, 2, 1]);
  assert.equal(compareValues(null, 0, 'desc'), 1);
  assert.equal(compareValues(new Date('nope'), new Date(0), 'asc'), 1);
});

test('strings compare naturally and case-insensitively', () => {
  assert.deepEqual(sortRows(['web-10', 'web-9', 'Web-1'], (v) => v), ['Web-1', 'web-9', 'web-10']);
  assert.deepEqual(sortRows(['beta', 'Zeta', 'API', 'edi'], (v) => v), ['API', 'beta', 'edi', 'Zeta']);
  assert.equal(compareValues('9', '10'), -1);
});

test('numbers, dates and booleans', () => {
  assert.equal(compareValues(2, 10), -1);
  assert.equal(compareValues(new Date('2026-01-02'), new Date('2026-01-01')), 1);
  assert.equal(compareValues(false, true), -1);
  assert.equal(compareValues(false, true, 'desc'), 1);
});

test('sortRows is stable and applies the tie-break ascending', () => {
  const rows = [
    { id: 'a', rank: 1, name: 'zulu' },
    { id: 'b', rank: 0, name: 'mike' },
    { id: 'c', rank: 1, name: 'alpha' },
    { id: 'd', rank: 1, name: 'alpha' },
  ];
  assert.deepEqual(
    sortRows(rows, (r) => r.rank, 'asc').map((r) => r.id),
    ['b', 'a', 'c', 'd']
  );
  assert.deepEqual(
    sortRows(rows, (r) => r.rank, 'desc', (r) => r.name).map((r) => r.id),
    ['c', 'd', 'a', 'b']
  );
  // Input is not mutated.
  assert.equal(rows[0].id, 'a');
});

test('nextSort flips the active key and starts a new key at its default', () => {
  const defaults = { name: 'asc', latency: 'desc' } as const;
  const start: SortState<'name' | 'latency'> = { key: 'name', dir: 'asc' };
  assert.deepEqual(nextSort(start, 'name', defaults), { key: 'name', dir: 'desc' });
  assert.deepEqual(nextSort(start, 'latency', defaults), { key: 'latency', dir: 'desc' });
  assert.deepEqual(nextSort(start, 'latency', () => 'asc'), { key: 'latency', dir: 'asc' });
  assert.deepEqual(nextSort(start, 'latency'), { key: 'latency', dir: 'asc' });
});

test('URL params round-trip and omit defaults', () => {
  type K = 'status' | 'name' | 'latency';
  const keys: K[] = ['status', 'name', 'latency'];
  const fallback: SortState<K> = { key: 'status', dir: 'asc' };
  const defaults = { latency: 'desc' } as const;

  assert.equal(writeSortParams(new URLSearchParams('q=x'), fallback, fallback, defaults).toString(), 'q=x');
  assert.equal(
    writeSortParams(new URLSearchParams(), { key: 'latency', dir: 'desc' }, fallback, defaults).toString(),
    'sort=latency'
  );
  assert.equal(
    writeSortParams(new URLSearchParams(), { key: 'latency', dir: 'asc' }, fallback, defaults).toString(),
    'sort=latency&dir=asc'
  );

  assert.deepEqual(readSortParams(new URLSearchParams('sort=latency'), keys, fallback, defaults), {
    key: 'latency',
    dir: 'desc',
  });
  assert.deepEqual(readSortParams(new URLSearchParams('sort=name&dir=desc'), keys, fallback, defaults), {
    key: 'name',
    dir: 'desc',
  });
  assert.deepEqual(readSortParams(new URLSearchParams('sort=bogus&dir=up'), keys, fallback, defaults), fallback);
});

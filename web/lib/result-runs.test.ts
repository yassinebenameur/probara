import assert from 'node:assert/strict';
import test from 'node:test';
import { collapseResultRuns, describeRun } from './result-runs';
import type { CheckResult } from './types';

function result(id: string, status: CheckResult['status'], location_name?: string): CheckResult {
  return {
    id,
    status,
    location_name,
    result_source: 'monitor',
    created_at: `2026-09-28T10:0${id}:00Z`,
  } as CheckResult;
}

test('collapseResultRuns folds consecutive identical results, newest first', () => {
  const runs = collapseResultRuns([
    result('9', 'error'),
    result('8', 'error'),
    result('7', 'error'),
    result('6', 'success'),
    result('5', 'error'),
  ]);

  assert.deepEqual(
    runs.map((run) => [run.latest.id, run.earliest.id, run.count]),
    [
      ['9', '7', 3],
      ['6', '6', 1],
      ['5', '5', 1],
    ]
  );
});

test('collapseResultRuns keeps locations apart', () => {
  const runs = collapseResultRuns([
    result('3', 'failure', 'paris'),
    result('2', 'failure', 'london'),
    result('1', 'failure', 'paris'),
  ]);

  assert.equal(runs.length, 3);
});

test('collapseResultRuns returns nothing for no results', () => {
  assert.deepEqual(collapseResultRuns([]), []);
});

function at(id: string, status: CheckResult['status'], local: Date): CheckResult {
  return { id, status, result_source: 'monitor', created_at: local.toISOString() } as CheckResult;
}

test('describeRun keeps the end date when a run spans days', () => {
  const [run] = collapseResultRuns([
    at('2', 'failure', new Date(2026, 9, 9, 10, 51, 53)),
    at('1', 'failure', new Date(2026, 9, 6, 9, 25, 27)),
  ]);
  const span = describeRun(run);
  assert.equal(span.start, '2026-10-06 09:25:27');
  assert.equal(span.end, '2026-10-09 10:51:53');
  assert.equal(span.count, '×2');
  assert.equal(span.duration, '3d 1h');
});

test('describeRun prints only the time when a run stays within one day', () => {
  const [run] = collapseResultRuns([
    at('2', 'failure', new Date(2026, 9, 6, 10, 51, 53)),
    at('1', 'failure', new Date(2026, 9, 6, 9, 25, 27)),
  ]);
  const span = describeRun(run);
  assert.equal(span.end, '10:51:53');
  assert.equal(span.duration, '1h 26m');
});

test('describeRun marks a run cut off by the fetch limit as a lower bound', () => {
  const [run] = collapseResultRuns([
    at('2', 'failure', new Date(2026, 9, 6, 10, 0, 0)),
    at('1', 'failure', new Date(2026, 9, 6, 9, 0, 0)),
  ]);
  const span = describeRun(run, { truncated: true });
  assert.equal(span.count, '×2+');
  assert.equal(span.duration, '1h+');
});

test('describeRun leaves a single check without an end or count', () => {
  const [run] = collapseResultRuns([at('1', 'success', new Date(2026, 9, 6, 9, 0, 0))]);
  const span = describeRun(run);
  assert.equal(span.end, null);
  assert.equal(span.count, '');
  assert.equal(span.duration, null);
});

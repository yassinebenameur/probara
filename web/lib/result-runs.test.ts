import assert from 'node:assert/strict';
import test from 'node:test';
import { collapseResultRuns } from './result-runs';
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

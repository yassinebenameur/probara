import assert from 'node:assert/strict';
import test from 'node:test';
import {
  collapseResultRuns,
  describeRun,
  foldFailureRuns,
  foldRunsByLane,
  laneReachesCut,
  runStartLabel,
} from './result-runs';
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

// Checks on a 60s cadence: `tick(n)` is n minutes after 10:00 UTC.
function tick(n: number, status: CheckResult['status'], location_name?: string, error_message?: string): CheckResult {
  return {
    id: `${location_name ?? 'x'}-${n}`,
    status,
    location_name,
    error_message,
    result_source: 'monitor',
    created_at: new Date(Date.UTC(2026, 9, 6, 10, n, 0)).toISOString(),
  } as CheckResult;
}

const byLocation = (r: CheckResult) => r.location_name ?? '';
const never = () => false;

test('foldRunsByLane folds interleaved locations into one run per location', () => {
  const results = [
    tick(3, 'success', 'paris'),
    tick(3, 'failure', 'london'),
    tick(2, 'success', 'paris'),
    tick(2, 'failure', 'london'),
    tick(1, 'success', 'paris'),
    tick(1, 'failure', 'london'),
  ];
  const folded = foldRunsByLane(results, byLocation, never);
  assert.deepEqual(
    folded.map(({ run }) => [run.latest.location_name, run.latest.status, run.count]),
    [
      ['paris', 'success', 3],
      ['london', 'failure', 3],
    ]
  );
  // One lane: the same checks do not fold at all.
  assert.equal(foldRunsByLane(results, () => '', never).length, 6);
});

test('foldRunsByLane marks only lanes that reach back to the oldest fetched result as truncated', () => {
  const results = [
    tick(5, 'success', 'paris'),
    tick(5, 'success', 'tokyo'),
    tick(4, 'success', 'paris'),
    tick(4, 'success', 'tokyo'), // tokyo was added at minute 4
    tick(3, 'failure', 'paris'),
    tick(2, 'failure', 'paris'),
    tick(1, 'success', 'paris'),
  ];
  const oldest = results[results.length - 1];
  const folded = foldRunsByLane(results, byLocation, (laneOldest) => laneReachesCut(laneOldest, oldest, 60));
  const marks = folded.map(({ run, truncated }) => [run.latest.id, truncated]);
  assert.deepEqual(marks, [
    ['paris-5', false],
    ['tokyo-5', false],
    ['paris-3', false],
    ['paris-1', true],
  ]);
});

test('laneReachesCut allows one interval of slack and nothing more', () => {
  const oldest = tick(0, 'success', 'paris');
  assert.equal(laneReachesCut(tick(1, 'success', 'tokyo'), oldest, 60), true);
  assert.equal(laneReachesCut(tick(2, 'success', 'tokyo'), oldest, 60), false);
  assert.equal(laneReachesCut(oldest, undefined, 60), false);
});

const reason = (r: CheckResult) => r.error_message ?? null;

test('foldFailureRuns ends a run at a success and splits it on a reason change', () => {
  const results = [
    tick(6, 'failure', 'paris', 'timeout'),
    tick(5, 'failure', 'paris', 'timeout'),
    tick(4, 'failure', 'paris', 'HTTP 502'),
    tick(3, 'success', 'paris'),
    tick(2, 'failure', 'paris', 'HTTP 502'),
    tick(1, 'failure', 'paris', 'HTTP 502'),
  ];
  const folded = foldFailureRuns(results, byLocation, reason, never);
  assert.deepEqual(
    folded.map(({ run }) => [run.latest.id, run.earliest.id, run.count, run.reason]),
    [
      ['paris-6', 'paris-5', 2, 'timeout'],
      ['paris-4', 'paris-4', 1, 'HTTP 502'],
      ['paris-2', 'paris-1', 2, 'HTTP 502'],
    ]
  );
});

test('foldFailureRuns keeps locations apart and marks only a run still open at the cut', () => {
  const results = [
    tick(3, 'failure', 'paris', 'timeout'),
    tick(3, 'failure', 'london', 'timeout'),
    tick(2, 'failure', 'paris', 'timeout'),
    tick(2, 'success', 'london'),
    tick(1, 'failure', 'paris', 'timeout'),
    tick(1, 'failure', 'london', 'timeout'),
    tick(0, 'success', 'london'),
  ];
  const oldest = results[results.length - 1];
  const folded = foldFailureRuns(results, byLocation, reason, (laneOldest) => laneReachesCut(laneOldest, oldest, 60));
  assert.deepEqual(
    folded.map(({ run, truncated }) => [run.latest.id, run.count, truncated]),
    [
      ['paris-3', 3, true], // paris failed through its oldest fetched check
      ['london-3', 1, false],
      ['london-1', 1, false], // a success before it closes the run
    ]
  );
});

test('runStartLabel drops the date only when the whole run is today', () => {
  const now = new Date(2026, 9, 6, 12, 0, 0);
  const run = (from: Date, to: Date) =>
    collapseResultRuns([at('2', 'failure', to), at('1', 'failure', from)])[0];
  assert.equal(runStartLabel(run(new Date(2026, 9, 6, 9, 25, 27), new Date(2026, 9, 6, 10, 0, 0)), now), '09:25:27');
  // Crossing midnight into today keeps the date on the start.
  assert.equal(
    runStartLabel(run(new Date(2026, 9, 5, 23, 59, 0), new Date(2026, 9, 6, 0, 1, 0)), now),
    '2026-10-05 23:59:00'
  );
  // Entirely yesterday.
  assert.equal(
    runStartLabel(run(new Date(2026, 9, 5, 9, 0, 0), new Date(2026, 9, 5, 10, 0, 0)), now),
    '2026-10-05 09:00:00'
  );
});

import { formatDuration, formatTimestamp } from './format';
import type { CheckResult } from './types';

export type ResultRun = {
  /** Newest result of the run (results arrive newest first). */
  latest: CheckResult;
  /** Oldest result of the run. */
  earliest: CheckResult;
  count: number;
};

// Folds consecutive results that share status, location and source into one
// run, so a monitor failing the same way for an hour reads as one row instead
// of a page of identical ones. Input and output are newest first.
export function collapseResultRuns(results: CheckResult[]): ResultRun[] {
  const runs: ResultRun[] = [];
  for (const result of results) {
    const run = runs[runs.length - 1];
    if (
      run &&
      run.latest.status === result.status &&
      run.latest.location_name === result.location_name &&
      run.latest.result_source === result.result_source
    ) {
      run.earliest = result;
      run.count += 1;
    } else {
      runs.push({ latest: result, earliest: result, count: 1 });
    }
  }
  return runs;
}

export type RunSpan = {
  /** Oldest check, `2026-10-06 09:25:27` (local). */
  start: string;
  /**
   * Newest check: time only when it falls on the start's local day, the full
   * timestamp otherwise (a run spanning days must not read as minutes).
   * Null for a single check.
   */
  end: string | null;
  /** `×12`, or `×50+` when the run was cut off by the fetch limit. */
  count: string;
  /** `3d 1h` between the oldest and newest check; `+` when cut off; null for a single check. */
  duration: string | null;
};

/**
 * Display text for one run. `truncated` marks the oldest run of a page that
 * hit its fetch limit: it continues past the oldest result we have, so its
 * count and duration are lower bounds.
 */
export function describeRun(run: ResultRun, opts: { truncated?: boolean } = {}): RunSpan {
  const plus = opts.truncated ? '+' : '';
  const start = formatTimestamp(run.earliest.created_at);
  if (run.count <= 1 && !opts.truncated) {
    return { start, end: null, count: '', duration: null };
  }
  const end = formatTimestamp(run.latest.created_at);
  const sameDay = start.slice(0, 10) === end.slice(0, 10);
  const seconds = (Date.parse(run.latest.created_at) - Date.parse(run.earliest.created_at)) / 1000;
  return {
    start,
    end: run.count > 1 ? (sameDay ? end.slice(11) : end) : null,
    count: `×${run.count}${plus}`,
    duration: run.count > 1 ? `${formatDuration(seconds)}${plus}` : null,
  };
}

/** A run plus whether it may continue past the oldest result fetched. */
export type FoldedRun<R extends ResultRun = ResultRun> = { run: R; truncated: boolean };

/**
 * Whether a lane may continue before its oldest fetched result: the fetch
 * was cut off and the lane reaches back to (within one check interval of)
 * the oldest result fetched overall. A lane that starts later than that —
 * a location added recently, a worker that was offline — had everything
 * newer fetched, so it cannot have been cut.
 */
export function laneReachesCut(
  laneOldest: CheckResult,
  oldest: CheckResult | undefined,
  intervalSeconds: number
): boolean {
  if (!oldest) return false;
  const gapMs = Date.parse(laneOldest.created_at) - Date.parse(oldest.created_at);
  return gapMs <= Math.max(intervalSeconds, 1) * 1000;
}

function groupByLane(results: CheckResult[], laneOf: (result: CheckResult) => string): CheckResult[][] {
  const lanes = new Map<string, CheckResult[]>();
  for (const result of results) {
    const key = laneOf(result);
    const lane = lanes.get(key);
    if (lane) lane.push(result);
    else lanes.set(key, [result]);
  }
  return Array.from(lanes.values());
}

const newestFirst = (a: FoldedRun, b: FoldedRun) =>
  Date.parse(b.run.latest.created_at) - Date.parse(a.run.latest.created_at);

/**
 * Results folded into runs per lane, newest first. Interleaved checks (one
 * per location, or per group member, each tick) never fold across lanes, so
 * three locations passing for an hour read as three rows, not one row per
 * check. The oldest run of a lane is `truncated` when `laneCut(laneOldest)`
 * says the lane may go back further than what was fetched.
 */
export function foldRunsByLane(
  results: CheckResult[],
  laneOf: (result: CheckResult) => string,
  laneCut: (laneOldest: CheckResult) => boolean
): FoldedRun[] {
  return groupByLane(results, laneOf)
    .flatMap((lane) => {
      const runs = collapseResultRuns(lane);
      const cut = laneCut(lane[lane.length - 1]);
      return runs.map((run, index) => ({ run, truncated: cut && index === runs.length - 1 }));
    })
    .sort(newestFirst);
}

export type FailureRun = ResultRun & { reason: string | null };

const isFailure = (result: CheckResult) => result.status === 'failure' || result.status === 'error';

/**
 * Failed checks folded into runs, newest first: consecutive failures of one
 * lane with the same status and reason are one run, a success (or degraded
 * check) in that lane ends it, and a reason change splits it. A run that
 * holds its lane's oldest fetched result is `truncated` when
 * `laneCut(laneOldest)` says the lane may go back further.
 */
export function foldFailureRuns(
  results: CheckResult[],
  laneOf: (result: CheckResult) => string,
  reasonOf: (result: CheckResult) => string | null,
  laneCut: (laneOldest: CheckResult) => boolean
): FoldedRun<FailureRun>[] {
  const folded: FoldedRun<FailureRun>[] = [];
  for (const lane of groupByLane(results, laneOf)) {
    let open: FoldedRun<FailureRun> | null = null;
    for (const result of lane) {
      if (!isFailure(result)) {
        open = null;
        continue;
      }
      const reason = reasonOf(result);
      if (open && open.run.latest.status === result.status && open.run.reason === reason) {
        open.run.earliest = result;
        open.run.count += 1;
      } else {
        open = { run: { latest: result, earliest: result, count: 1, reason }, truncated: false };
        folded.push(open);
      }
    }
    // Still open at the lane's oldest result: the run may start earlier.
    if (open && laneCut(lane[lane.length - 1])) open.truncated = true;
  }
  return folded.sort(newestFirst);
}

const sameLocalDay = (a: Date, b: Date) =>
  a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();

/**
 * A run's start for a list where today is implied: the time alone when the
 * whole run falls on `now`'s local day (`09:25:27`), the full timestamp
 * otherwise, so a run from yesterday never reads as today's.
 */
export function runStartLabel(run: ResultRun, now: Date = new Date()): string {
  const start = formatTimestamp(run.earliest.created_at);
  const today =
    sameLocalDay(new Date(run.earliest.created_at), now) && sameLocalDay(new Date(run.latest.created_at), now);
  return today ? start.slice(11) : start;
}

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

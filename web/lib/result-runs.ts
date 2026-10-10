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

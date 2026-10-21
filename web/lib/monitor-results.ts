import { getMonitorResultsBatch, MONITOR_RESULTS_BATCH_MAX_IDS } from './api';
import type { CheckResult, MonitorResultsBatchResponse } from './types';

// Recent results for many monitors through GET /monitors/results/batch, the
// monitors list's one round trip (first load and live ticks).

export type FetchResultsChunk = (ids: string[], params: { limit: number }) => Promise<MonitorResultsBatchResponse>;

export type ResultsBatch = {
  results: Record<string, CheckResult[]>;
  /** Direct member ids of each group among the requested ids. */
  members: Record<string, string[]>;
  /** Ids whose chunk failed: callers keep what is on screen for them. */
  failed: Set<string>;
  /** The first chunk failure, for the message; set whenever `failed` is not empty. */
  error?: unknown;
};

/**
 * Results (and group member ids) for `ids`, one request per chunk of the
 * server's cap, all in flight at once. A failed chunk marks its ids in
 * `failed` and leaves them out of `results`/`members`; an id a successful
 * chunk did not return is no longer a live monitor and gets `[]`.
 */
export async function fetchResultsBatches(
  ids: string[],
  limit: number,
  fetchChunk: FetchResultsChunk = getMonitorResultsBatch,
  chunkSize: number = MONITOR_RESULTS_BATCH_MAX_IDS
): Promise<ResultsBatch> {
  const unique = Array.from(new Set(ids));
  const chunks: string[][] = [];
  for (let i = 0; i < unique.length; i += chunkSize) {
    chunks.push(unique.slice(i, i + chunkSize));
  }
  const settled = await Promise.allSettled(chunks.map((chunk) => fetchChunk(chunk, { limit })));
  const batch: ResultsBatch = { results: {}, members: {}, failed: new Set() };
  settled.forEach((outcome, index) => {
    if (outcome.status === 'rejected') {
      chunks[index].forEach((id) => batch.failed.add(id));
      if (batch.error === undefined) batch.error = outcome.reason ?? new Error('Request failed');
      return;
    }
    chunks[index].forEach((id) => {
      batch.results[id] = outcome.value.results?.[id] ?? [];
    });
    Object.assign(batch.members, outcome.value.members ?? {});
  });
  return batch;
}

/**
 * The error a caller throws when some chunks failed, so the list's error
 * and live-status path reports it (the rows already on screen stay). Null
 * when nothing failed.
 */
export function resultsBatchError(batch: ResultsBatch, requested: number): Error | null {
  if (batch.failed.size === 0) return null;
  const reason = errorMessage(batch.error);
  const scope =
    batch.failed.size >= requested ? 'Recent checks failed to load' : `Recent checks failed to load for ${batch.failed.size} of ${requested} monitors`;
  return new Error(reason ? `${scope}: ${reason}` : scope);
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string') {
    return error.message;
  }
  return '';
}

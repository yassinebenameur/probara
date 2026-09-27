import type { CheckResult, MonitorResultsResponse, RunMonitorNowResponse } from '../types';

export type SyntheticTestPhase = 'idle' | 'queueing' | 'polling' | 'success' | 'failure' | 'error' | 'timeout';

export type SyntheticTestState = {
  phase: SyntheticTestPhase;
  message?: string;
  queuedAt?: string;
  jobId?: string;
  result?: CheckResult;
};

export type SyntheticTestUpdate = SyntheticTestState | ((prev: SyntheticTestState) => SyntheticTestState);

export const IDLE_SYNTHETIC_TEST: SyntheticTestState = { phase: 'idle' };

export const isSyntheticTestRunning = (state: SyntheticTestState): boolean =>
  state.phase === 'queueing' || state.phase === 'polling';

/** The two API calls a live test makes; `lib/api` in the app, fakes in tests. */
export interface SyntheticLiveTestClient {
  runNow(monitorId: string, opts?: { location_id?: string }): Promise<Pick<RunMonitorNowResponse, 'job_id' | 'queued_at'>>;
  getResults(monitorId: string, params: { limit: number; since: string }): Promise<Pick<MonitorResultsResponse, 'results'>>;
}

export const SYNTHETIC_TEST_MAX_ATTEMPTS = 30;
export const SYNTHETIC_TEST_POLL_INTERVAL_MS = 2000;
/** A result stamped slightly before the queue time still belongs to this run (clock skew). */
const QUEUE_CLOCK_SKEW_MS = 1500;

/** Resolves after `ms`, or immediately once `signal` aborts (clearing the timer). */
export const abortableSleep = (ms: number, signal: AbortSignal): Promise<void> =>
  new Promise((resolve) => {
    if (signal.aborted) {
      resolve();
      return;
    }
    const onAbort = () => {
      clearTimeout(timer);
      resolve();
    };
    const timer = setTimeout(() => {
      signal.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    signal.addEventListener('abort', onAbort, { once: true });
  });

const errorMessage = (err: unknown): string | undefined => {
  if (typeof err === 'object' && err !== null && 'message' in err) {
    const { message } = err as { message?: unknown };
    if (typeof message === 'string' && message) return message;
  }
  return undefined;
};

export interface RunSyntheticLiveTestOptions {
  monitorId: string;
  /** '' fans out to all the monitor's locations (or the default fleet). */
  locationId: string;
  client: SyntheticLiveTestClient;
  /** Aborting stops polling and suppresses every later update. */
  signal: AbortSignal;
  onUpdate: (update: SyntheticTestUpdate) => void;
  sleep?: (ms: number, signal: AbortSignal) => Promise<void>;
  now?: () => number;
  maxAttempts?: number;
  intervalMs?: number;
}

/**
 * Queues an on-demand run of the saved monitor, then polls its results until a
 * result from this run appears, the attempts run out, or `signal` aborts.
 * No update is delivered after an abort, so a superseded or unmounted test
 * can never overwrite newer state.
 */
export async function runSyntheticLiveTest({
  monitorId,
  locationId,
  client,
  signal,
  onUpdate,
  sleep = abortableSleep,
  now = Date.now,
  maxAttempts = SYNTHETIC_TEST_MAX_ATTEMPTS,
  intervalMs = SYNTHETIC_TEST_POLL_INTERVAL_MS,
}: RunSyntheticLiveTestOptions): Promise<void> {
  const update = (next: SyntheticTestUpdate) => {
    if (!signal.aborted) onUpdate(next);
  };

  update({ phase: 'queueing', message: 'Queuing on-demand run...' });

  try {
    const run = await client.runNow(monitorId, locationId ? { location_id: locationId } : undefined);
    if (signal.aborted) return;

    const queuedAt = run.queued_at || new Date(now()).toISOString();
    const queuedAtMs = new Date(queuedAt).getTime();
    update({
      phase: 'polling',
      message: 'Run queued. Waiting for worker result...',
      queuedAt,
      jobId: run.job_id,
    });

    let matched: CheckResult | undefined;
    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
      if (signal.aborted) return;

      const response = await client.getResults(monitorId, { limit: 10, since: queuedAt });
      if (signal.aborted) return;
      matched = (response.results || []).find((result) => {
        if (locationId && result.location_id !== locationId) return false;
        return new Date(result.created_at).getTime() >= queuedAtMs - QUEUE_CLOCK_SKEW_MS;
      });
      if (matched) break;

      update((prev) => ({
        ...prev,
        phase: 'polling',
        message: `Waiting for worker result... (${attempt}/${maxAttempts})`,
      }));
      await sleep(intervalMs, signal);
    }

    if (signal.aborted) return;

    if (!matched) {
      update((prev) => ({
        ...prev,
        phase: 'timeout',
        message: 'No fresh result found yet. Check worker/scheduler health and try again.',
      }));
      return;
    }

    const phase: SyntheticTestPhase = matched.status === 'success' ? 'success' : 'failure';
    update({
      phase,
      queuedAt,
      jobId: run.job_id,
      result: matched,
      message: matched.error_message || (phase === 'success' ? 'Run completed successfully.' : 'Run failed.'),
    });
  } catch (err) {
    update({ phase: 'error', message: errorMessage(err) || 'Failed to run monitor on demand.' });
  }
}

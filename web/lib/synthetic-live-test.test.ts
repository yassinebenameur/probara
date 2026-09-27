import assert from 'node:assert/strict';
import test from 'node:test';
import {
  type SyntheticLiveTestClient,
  type SyntheticTestState,
  type SyntheticTestUpdate,
  IDLE_SYNTHETIC_TEST,
  abortableSleep,
  isSyntheticTestRunning,
  runSyntheticLiveTest,
} from './monitor-form/synthetic-live-test';
import type { CheckResult } from './types';

const QUEUED_AT = '2026-09-01T10:00:00.000Z';
const queuedMs = Date.parse(QUEUED_AT);

const result = (patch: Partial<CheckResult>): CheckResult => ({
  id: 'r', status: 'success', created_at: new Date(queuedMs + 500).toISOString(), ...patch,
});

/** Applies updates the way React's functional setState would, keeping every intermediate state. */
function recorder() {
  let state: SyntheticTestState = IDLE_SYNTHETIC_TEST;
  const history: SyntheticTestState[] = [];
  const onUpdate = (update: SyntheticTestUpdate) => {
    state = typeof update === 'function' ? update(state) : update;
    history.push(state);
  };
  return { onUpdate, history, current: () => state };
}

type Calls = { run: Array<[string, unknown]>; results: Array<[string, unknown]>; sleeps: number[] };

function fakeClient(pages: Array<CheckResult[] | Error>, run: Partial<{ job_id: string; queued_at: string }> | Error = {}) {
  const calls: Calls = { run: [], results: [], sleeps: [] };
  const client: SyntheticLiveTestClient = {
    async runNow(id, opts) {
      calls.run.push([id, opts]);
      if (run instanceof Error) throw run;
      return { job_id: 'job-1', queued_at: QUEUED_AT, ...run };
    },
    async getResults(id, params) {
      calls.results.push([id, params]);
      const page = pages.length > 1 ? pages.shift() : pages[0];
      if (page instanceof Error) throw page;
      return { results: page ?? [] };
    },
  };
  const sleep = async (ms: number) => {
    calls.sleeps.push(ms);
  };
  return { client, calls, sleep };
}

test('live test: success after polling, restricted to the chosen location', async () => {
  const { client, calls, sleep } = fakeClient([
    [],
    [],
    [
      result({ id: 'other-location', status: 'failure', location_id: 'loc-a' }),
      result({ id: 'match', status: 'success', location_id: 'loc-b', latency_ms: 42 }),
    ],
  ]);
  const rec = recorder();
  await runSyntheticLiveTest({
    monitorId: 'mon-1', locationId: 'loc-b', client, sleep, signal: new AbortController().signal, onUpdate: rec.onUpdate,
  });

  assert.deepEqual(calls.run, [['mon-1', { location_id: 'loc-b' }]]);
  assert.deepEqual(calls.results.map(([, params]) => params), Array(3).fill({ limit: 10, since: QUEUED_AT }));
  assert.deepEqual(calls.sleeps, [2000, 2000]);
  assert.deepEqual(rec.history.map((s) => [s.phase, s.message]), [
    ['queueing', 'Queuing on-demand run...'],
    ['polling', 'Run queued. Waiting for worker result...'],
    ['polling', 'Waiting for worker result... (1/30)'],
    ['polling', 'Waiting for worker result... (2/30)'],
    ['success', 'Run completed successfully.'],
  ]);
  const final = rec.current();
  assert.equal(final.result?.id, 'match');
  assert.equal(final.jobId, 'job-1');
  assert.equal(final.queuedAt, QUEUED_AT);
  assert.equal(isSyntheticTestRunning(final), false);
});

test('live test: failure uses the result error message, all locations by default', async () => {
  const { client, calls, sleep } = fakeClient([[result({ status: 'failure', error_message: 'assertion failed' })]]);
  const rec = recorder();
  await runSyntheticLiveTest({ monitorId: 'mon-1', locationId: '', client, sleep, signal: new AbortController().signal, onUpdate: rec.onUpdate });
  assert.deepEqual(calls.run, [['mon-1', undefined]]);
  assert.equal(rec.current().phase, 'failure');
  assert.equal(rec.current().message, 'assertion failed');

  const degraded = fakeClient([[result({ status: 'degraded' })]]);
  const rec2 = recorder();
  await runSyntheticLiveTest({ monitorId: 'm', locationId: '', client: degraded.client, sleep: degraded.sleep, signal: new AbortController().signal, onUpdate: rec2.onUpdate });
  assert.deepEqual([rec2.current().phase, rec2.current().message], ['failure', 'Run failed.']);
});

test('live test: results older than the queue time (beyond clock skew) are ignored', async () => {
  const { client, sleep } = fakeClient([
    [result({ id: 'stale', created_at: new Date(queuedMs - 1501).toISOString() })],
    [result({ id: 'skewed', created_at: new Date(queuedMs - 1500).toISOString() })],
  ]);
  const rec = recorder();
  await runSyntheticLiveTest({ monitorId: 'm', locationId: '', client, sleep, signal: new AbortController().signal, onUpdate: rec.onUpdate });
  assert.equal(rec.current().result?.id, 'skewed');
});

test('live test: times out after the attempt budget, keeping the queued job', async () => {
  const { client, calls, sleep } = fakeClient([[]]);
  const rec = recorder();
  await runSyntheticLiveTest({ monitorId: 'm', locationId: '', client, sleep, signal: new AbortController().signal, onUpdate: rec.onUpdate });
  assert.equal(calls.results.length, 30);
  assert.equal(calls.sleeps.length, 30);
  assert.deepEqual(rec.current(), {
    phase: 'timeout',
    message: 'No fresh result found yet. Check worker/scheduler health and try again.',
    queuedAt: QUEUED_AT,
    jobId: 'job-1',
  });
});

test('live test: API errors surface their message, with a fallback', async () => {
  const queueDown = fakeClient([[]], Object.assign(new Error('queue unavailable')));
  const rec = recorder();
  await runSyntheticLiveTest({ monitorId: 'm', locationId: '', client: queueDown.client, sleep: queueDown.sleep, signal: new AbortController().signal, onUpdate: rec.onUpdate });
  assert.deepEqual(rec.current(), { phase: 'error', message: 'queue unavailable' });

  // lib/api throws plain `{ error, message }` objects, not Error instances.
  const pollFails = fakeClient([[]]);
  pollFails.client.getResults = async () => {
    throw { error: 'boom', message: '' };
  };
  const rec2 = recorder();
  await runSyntheticLiveTest({ monitorId: 'm', locationId: '', client: pollFails.client, sleep: pollFails.sleep, signal: new AbortController().signal, onUpdate: rec2.onUpdate });
  assert.deepEqual(rec2.current(), { phase: 'error', message: 'Failed to run monitor on demand.' });
});

test('live test: falls back to the local clock when the API omits queued_at', async () => {
  const { client, calls, sleep } = fakeClient([[result({ created_at: new Date(queuedMs).toISOString() })]], { queued_at: '' });
  const rec = recorder();
  await runSyntheticLiveTest({ monitorId: 'm', locationId: '', client, sleep, now: () => queuedMs, signal: new AbortController().signal, onUpdate: rec.onUpdate });
  assert.deepEqual(calls.results[0][1], { limit: 10, since: QUEUED_AT });
  assert.equal(rec.current().phase, 'success');
});

test('live test: abort (unmount) mid-poll stops polling and drops the late response', async () => {
  const controller = new AbortController();
  let releaseResults: (value: { results: CheckResult[] }) => void = () => {};
  let resultCalls = 0;
  const client: SyntheticLiveTestClient = {
    runNow: async () => ({ job_id: 'job-1', queued_at: QUEUED_AT }),
    getResults: () => {
      resultCalls += 1;
      return new Promise((resolve) => {
        releaseResults = resolve;
      });
    },
  };
  const rec = recorder();
  const done = runSyntheticLiveTest({ monitorId: 'm', locationId: '', client, signal: controller.signal, onUpdate: rec.onUpdate });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(rec.current().phase, 'polling');

  controller.abort();
  releaseResults({ results: [result({})] });
  await done;

  assert.equal(resultCalls, 1);
  assert.equal(rec.current().message, 'Run queued. Waiting for worker result...');
  assert.equal(rec.history.length, 2);
});

test('live test: abort while waiting between polls cancels the timer', async () => {
  const controller = new AbortController();
  let resultCalls = 0;
  const client: SyntheticLiveTestClient = {
    runNow: async () => ({ job_id: 'job-1', queued_at: QUEUED_AT }),
    getResults: async () => {
      resultCalls += 1;
      return { results: [] };
    },
  };
  const rec = recorder();
  // Real (abortable) sleep with a long interval: only the abort can end it promptly.
  const done = runSyntheticLiveTest({ monitorId: 'm', locationId: '', client, signal: controller.signal, onUpdate: rec.onUpdate, intervalMs: 60_000 });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(rec.current().message, 'Waiting for worker result... (1/30)');

  const started = Date.now();
  controller.abort();
  await done;
  assert.ok(Date.now() - started < 1000, 'abort should end the wait immediately');
  assert.equal(resultCalls, 1);
  assert.equal(rec.history.length, 3);
});

test('live test: a superseded run never overwrites the newer run', async () => {
  let releaseFirst: (value: { job_id: string; queued_at: string }) => void = () => {};
  const client: SyntheticLiveTestClient = {
    runNow: (_id, opts) =>
      opts?.location_id === 'slow'
        ? new Promise((resolve) => {
            releaseFirst = resolve;
          })
        : Promise.resolve({ job_id: 'job-2', queued_at: QUEUED_AT }),
    getResults: async () => ({ results: [result({ id: 'second' })] }),
  };
  const rec = recorder();
  const first = new AbortController();
  const firstRun = runSyntheticLiveTest({ monitorId: 'm', locationId: 'slow', client, signal: first.signal, onUpdate: rec.onUpdate });

  // Starting a new run aborts the previous one (what useSyntheticLiveTest does).
  first.abort();
  await runSyntheticLiveTest({ monitorId: 'm', locationId: '', client, signal: new AbortController().signal, onUpdate: rec.onUpdate });
  releaseFirst({ job_id: 'job-1', queued_at: QUEUED_AT });
  await firstRun;

  assert.equal(rec.current().phase, 'success');
  assert.equal(rec.current().jobId, 'job-2');
  assert.equal(rec.current().result?.id, 'second');
});

test('abortableSleep resolves on timeout and immediately when already aborted', async () => {
  const controller = new AbortController();
  await abortableSleep(1, controller.signal);
  controller.abort();
  const started = Date.now();
  await abortableSleep(60_000, controller.signal);
  assert.ok(Date.now() - started < 1000);
});

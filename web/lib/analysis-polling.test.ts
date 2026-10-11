import assert from 'node:assert/strict';
import { test } from 'node:test';
import { pollAnalysis } from './analysis-polling';

function clock() {
  let next = 0;
  const callbacks = new Map<number, () => void>();
  return {
    callbacks,
    schedule: (callback: () => void) => { const id = ++next; callbacks.set(id, callback); return id as unknown as ReturnType<typeof setTimeout>; },
    unschedule: (id: ReturnType<typeof setTimeout>) => { callbacks.delete(id as unknown as number); },
    tick: async () => {
      const [id, callback] = [...callbacks][0]; callbacks.delete(id); callback();
      await new Promise<void>((resolve) => setImmediate(resolve));
    },
  };
}

test('transient errors, empty replies and unchanged pending rows continue until completion', async () => {
  const timer = clock(); let failures = 0; const results: string[] = []; let calls = 0;
  const stop = pollAnalysis(async () => {
    calls++;
    if (calls === 1) throw new Error('transient');
    if (calls === 2) return null;
    return { status: calls === 3 ? 'pending' : 'ready' };
  }, (value) => results.push(value.status), () => { failures++; }, 2500, timer.schedule, timer.unschedule);
  for (let i = 0; i < 4; i++) await timer.tick();
  assert.equal(failures, 1); assert.deepEqual(results, ['pending', 'ready']);
  assert.equal(timer.callbacks.size, 0); stop();
});

test('unmount cancels the timer and ignores an in-flight response', async () => {
  const timer = clock(); let resolve!: (value: { status: string }) => void; let results = 0;
  const stop = pollAnalysis(() => new Promise<{ status: string }>((done) => { resolve = done; }), () => { results++; }, () => {}, 2500, timer.schedule, timer.unschedule);
  await timer.tick(); stop(); resolve({ status: 'pending' });
  await new Promise<void>((done) => setImmediate(done));
  assert.equal(results, 0); assert.equal(timer.callbacks.size, 0);
});

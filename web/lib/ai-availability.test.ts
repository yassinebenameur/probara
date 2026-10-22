import assert from 'node:assert/strict';
import { test } from 'node:test';
import { watchAIAvailability } from './ai-availability';

type Availability = { enabled: boolean; available: boolean };

function clock() {
  let next = 0;
  const callbacks = new Map<number, { callback: () => void; delay: number }>();
  return {
    callbacks,
    schedule: (callback: () => void, delay: number) => { const id = ++next; callbacks.set(id, { callback, delay }); return id as unknown as ReturnType<typeof setTimeout>; },
    unschedule: (id: ReturnType<typeof setTimeout>) => { callbacks.delete(id as unknown as number); },
    tick: async () => {
      const [id, { callback }] = [...callbacks][0]; callbacks.delete(id); callback();
      await settle();
    },
  };
}

const settle = () => new Promise<void>((resolve) => setImmediate(resolve));
const down: Availability = { enabled: true, available: false };
const ready: Availability = { enabled: true, available: true };

test('rechecks at the slow interval while not ready and stops once ready', async () => {
  const timer = clock(); const replies = [down, { enabled: false, available: true }, ready]; const seen: Availability[] = [];
  const watch = watchAIAvailability(async () => replies.shift()!, (v) => seen.push(v), { interval: 30_000, schedule: timer.schedule, unschedule: timer.unschedule });
  await settle();
  assert.deepEqual([...timer.callbacks.values()].map((c) => c.delay), [30_000]);
  await timer.tick(); await timer.tick();
  assert.deepEqual(seen, [down, { enabled: false, available: true }, ready]);
  assert.equal(timer.callbacks.size, 0, 'no polling once ready');
  watch.stop();
});

test('ready on the first read never schedules, and recheck is then a no-op', async () => {
  const timer = clock(); let reads = 0;
  const watch = watchAIAvailability(async () => { reads++; return ready; }, () => {}, { schedule: timer.schedule, unschedule: timer.unschedule });
  await settle(); watch.recheck(); await settle();
  assert.equal(reads, 1); assert.equal(timer.callbacks.size, 0);
  watch.stop();
});

test('a failed first read stops; a failed later read keeps the last value and keeps polling', async () => {
  const timer = clock(); const seen: Availability[] = [];
  const failing = watchAIAvailability<Availability>(async () => { throw new Error('offline'); }, (v) => seen.push(v), { schedule: timer.schedule, unschedule: timer.unschedule });
  await settle();
  assert.equal(timer.callbacks.size, 0); assert.equal(seen.length, 0);
  failing.stop();

  let calls = 0;
  const watch = watchAIAvailability(async () => { calls++; if (calls === 2) throw new Error('blip'); return calls === 1 ? down : ready; }, (v) => seen.push(v), { schedule: timer.schedule, unschedule: timer.unschedule });
  await settle(); await timer.tick();
  assert.deepEqual(seen, [down]); assert.equal(timer.callbacks.size, 1);
  await timer.tick();
  assert.deepEqual(seen, [down, ready]); assert.equal(timer.callbacks.size, 0);
  watch.stop();
});

test('hidden ticks skip the request; recheck reads now and replaces the pending tick', async () => {
  const timer = clock(); let hidden = true; let reads = 0;
  const watch = watchAIAvailability(async () => { reads++; return reads < 3 ? down : ready; }, () => {}, { isHidden: () => hidden, schedule: timer.schedule, unschedule: timer.unschedule });
  await settle(); await timer.tick();
  assert.equal(reads, 1, 'hidden tick does not read'); assert.equal(timer.callbacks.size, 1);
  hidden = false; watch.recheck(); await settle();
  assert.equal(reads, 2); assert.equal(timer.callbacks.size, 1, 'one pending tick, not two');
  await timer.tick();
  assert.equal(reads, 3); assert.equal(timer.callbacks.size, 0);
  watch.stop();
});

test('stop cancels the timer and ignores an in-flight reply', async () => {
  const timer = clock(); let resolve!: (v: Availability) => void; let results = 0;
  const watch = watchAIAvailability(() => new Promise<Availability>((done) => { resolve = done; }), () => { results++; }, { schedule: timer.schedule, unschedule: timer.unschedule });
  watch.stop(); resolve(down); await settle();
  assert.equal(results, 0); assert.equal(timer.callbacks.size, 0);
});

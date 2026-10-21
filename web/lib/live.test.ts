// Pure parts of the liveness primitives: the shared clock (useNow), the
// minimum busy time (useMinDuration) and the row-flash diff (useRowFlash).
import assert from 'node:assert/strict';
import test from 'node:test';
import { createClockStore, type ClockDeps } from './useNow';
import { minDurationRemaining } from './useMinDuration';
import { changedIds } from './useRowFlash';
import { documentTitle } from './useDocumentTitle';

function fakeDeps() {
  let now = 1_000;
  let visible = true;
  const intervals = new Map<number, () => void>();
  let nextHandle = 1;
  const deps: ClockDeps = {
    now: () => now,
    setInterval: (fn) => {
      const handle = nextHandle++;
      intervals.set(handle, fn);
      return handle;
    },
    clearInterval: (handle) => {
      intervals.delete(handle as number);
    },
    isVisible: () => visible,
  };
  return {
    deps,
    intervals,
    advance(ms: number) {
      now += ms;
      intervals.forEach((fn) => fn());
    },
    setVisible(v: boolean) {
      visible = v;
    },
    setNow(v: number) {
      now = v;
    },
  };
}

test('clock runs one interval only while subscribed and visible', () => {
  const fake = fakeDeps();
  const store = createClockStore(1_000, fake.deps);
  assert.equal(fake.intervals.size, 0);

  let calls = 0;
  const offA = store.subscribe(() => calls++);
  const offB = store.subscribe(() => calls++);
  assert.equal(fake.intervals.size, 1, 'one shared interval for all subscribers');

  fake.advance(1_000);
  assert.equal(store.getSnapshot(), 2_000);
  assert.equal(calls, 2);

  fake.setVisible(false);
  store.sync();
  assert.equal(fake.intervals.size, 0, 'hidden tab stops the clock');
  fake.setVisible(true);
  store.sync();
  assert.equal(fake.intervals.size, 1);

  offA();
  offB();
  assert.equal(fake.intervals.size, 0, 'no subscribers, no interval');
});

test('an idle clock catches up on the first subscribe', () => {
  const fake = fakeDeps();
  const store = createClockStore(15_000, fake.deps);
  fake.setNow(90_000);
  const off = store.subscribe(() => {});
  assert.equal(store.getSnapshot(), 90_000);
  off();
});

test('minDurationRemaining holds an indicator for the minimum time', () => {
  assert.equal(minDurationRemaining(null, 5_000, 400), 0);
  assert.equal(minDurationRemaining(1_000, 1_100, 400), 300);
  assert.equal(minDurationRemaining(1_000, 1_400, 400), 0);
  assert.equal(minDurationRemaining(1_000, 9_000, 400), 0);
});

test('changedIds flashes status changes, not the first load', () => {
  const first = new Map([
    ['a', 'up'],
    ['b', 'down'],
  ]);
  assert.deepEqual(changedIds(null, first), []);
  const second = new Map([
    ['a', 'down'],
    ['b', 'down'],
    ['c', 'up'],
  ]);
  assert.deepEqual(changedIds(first, second), ['a']);
  assert.deepEqual(changedIds(first, second, true), ['a', 'c']);
});

test('documentTitle', () => {
  assert.equal(documentTitle('Orders API'), 'Orders API · Probara');
  assert.equal(documentTitle('  '), 'Probara');
  assert.equal(documentTitle(undefined), 'Probara');
});

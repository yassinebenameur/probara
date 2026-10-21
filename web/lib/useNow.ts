'use client';

import { useSyncExternalStore } from 'react';

/**
 * One shared clock for every live-updating time on screen ("25s ago", an
 * ongoing alert's duration). Each cadence is a single module-level interval,
 * running only while something subscribes and the tab is visible, and it
 * ticks once more when the tab comes back. Leaf components read it through
 * useNow (or RelativeTime / ElapsedTime), so only those leaves re-render.
 *
 * Cadences: 'second' (1s) for second-resolution text and short durations,
 * 'minute' (15s) for "N min ago" style text — the default.
 */
export type NowCadence = 'second' | 'minute';

export const CADENCE_MS: Record<NowCadence, number> = { second: 1_000, minute: 15_000 };

export interface ClockDeps {
  now: () => number;
  setInterval: (fn: () => void, ms: number) => unknown;
  clearInterval: (handle: unknown) => void;
  /** False while the document is hidden: the clock stops ticking. */
  isVisible: () => boolean;
}

export interface ClockStore {
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => number;
  /** Re-read the time and notify (visibility return). */
  tick: () => void;
  /** Start or stop the interval to match visibility. */
  sync: () => void;
}

/**
 * A ticking clock store for useSyncExternalStore. Exported for tests; the
 * app uses the shared instances behind useNow.
 */
export function createClockStore(intervalMs: number, deps: ClockDeps): ClockStore {
  const listeners = new Set<() => void>();
  let snapshot = deps.now();
  let handle: unknown = null;

  const tick = () => {
    snapshot = deps.now();
    listeners.forEach((listener) => listener());
  };

  const sync = () => {
    const shouldRun = listeners.size > 0 && deps.isVisible();
    if (shouldRun && handle === null) {
      handle = deps.setInterval(tick, intervalMs);
    } else if (!shouldRun && handle !== null) {
      deps.clearInterval(handle);
      handle = null;
    }
  };

  return {
    subscribe(listener) {
      listeners.add(listener);
      // A clock that sat idle is stale: catch up before the first read.
      if (listeners.size === 1) snapshot = deps.now();
      sync();
      return () => {
        listeners.delete(listener);
        sync();
      };
    },
    getSnapshot: () => snapshot,
    tick,
    sync,
  };
}

const browserDeps: ClockDeps = {
  now: () => Date.now(),
  setInterval: (fn, ms) => window.setInterval(fn, ms),
  clearInterval: (handle) => window.clearInterval(handle as number),
  isVisible: () => typeof document === 'undefined' || document.visibilityState !== 'hidden',
};

const stores: Partial<Record<NowCadence, ClockStore>> = {};
let visibilityBound = false;

function storeFor(cadence: NowCadence): ClockStore {
  let store = stores[cadence];
  if (!store) {
    store = createClockStore(CADENCE_MS[cadence], browserDeps);
    stores[cadence] = store;
  }
  if (!visibilityBound && typeof document !== 'undefined') {
    visibilityBound = true;
    document.addEventListener('visibilitychange', () => {
      (Object.values(stores) as ClockStore[]).forEach((s) => {
        s.sync();
        if (document.visibilityState === 'visible') s.tick();
      });
    });
  }
  return store;
}

// The server render (and hydration) uses a fixed 0 so markup matches; the
// first client tick replaces it. Consumers treat 0 as "now".
const getServerSnapshot = () => 0;

/**
 * The current time in epoch ms, re-rendering on the shared clock's cadence.
 * Returns `Date.now()` semantics; during SSR/hydration it is read fresh.
 */
export function useNow(cadence: NowCadence = 'minute'): number {
  const store = storeFor(cadence);
  const value = useSyncExternalStore(store.subscribe, store.getSnapshot, getServerSnapshot);
  return value === 0 ? Date.now() : value;
}

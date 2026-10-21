'use client';

import { CSSProperties, useCallback, useEffect, useRef, useState } from 'react';

/** How long a row stays marked: the length of the animate-row-flash decay. */
export const ROW_FLASH_MS = 1_600;

export type FlashTone = 'danger' | 'warning' | 'success' | 'info' | 'neutral';

// The flash background per tone, from the status ramps (no new colours).
const FLASH_COLOR: Record<FlashTone, string> = {
  danger: 'rgb(var(--tw-rose-500) / 0.16)',
  warning: 'rgb(var(--tw-amber-500) / 0.18)',
  success: 'rgb(var(--tw-emerald-500) / 0.16)',
  info: 'rgb(var(--tw-sky-500) / 0.14)',
  neutral: 'rgb(var(--ink-rgb) / 0.08)',
};

/** Inline style that colours a row's animate-row-flash (sets --flash). */
export function flashStyle(tone: FlashTone): CSSProperties {
  return { ['--flash' as string]: FLASH_COLOR[tone] } as CSSProperties;
}

/**
 * Ids whose status differs between the previous snapshot and `next`. The
 * first snapshot (`previous` null) flashes nothing: a page load is not a
 * change. New ids flash only with `includeNew`.
 */
export function changedIds(
  previous: ReadonlyMap<string, string> | null,
  next: ReadonlyMap<string, string>,
  includeNew = false
): string[] {
  if (previous === null) return [];
  const changed: string[] = [];
  next.forEach((status, id) => {
    const before = previous.get(id);
    if (before === undefined) {
      if (includeNew) changed.push(id);
    } else if (before !== status) {
      changed.push(id);
    }
  });
  return changed;
}

export interface RowFlash {
  /** Ids currently flashing. */
  flashing: ReadonlySet<string>;
  /** `animate-row-flash` for a flashing id, '' otherwise. */
  flashClass: (id: string) => string;
  /** Flash a row by hand (a live insert, a row the user just changed). */
  flash: (id: string) => void;
}

/**
 * Marks rows whose status changed since the previous data, so a live list
 * can say "this one just changed" with animate-row-flash. Give the row
 * `className={flashClass(id)}` and `style={flashStyle(tone)}` (its new
 * status tone). Pair it with a polite announcement for screen readers.
 *
 * `rows` null/undefined means "no data yet" and leaves the baseline alone.
 */
export function useRowFlash<T>(
  rows: readonly T[] | null | undefined,
  getId: (row: T) => string,
  getStatus: (row: T) => string | null | undefined,
  options: { durationMs?: number; includeNew?: boolean } = {}
): RowFlash {
  const { durationMs = ROW_FLASH_MS, includeNew = false } = options;
  const getters = useRef({ getId, getStatus });
  useEffect(() => {
    getters.current = { getId, getStatus };
  });

  const baseline = useRef<Map<string, string> | null>(null);
  const timers = useRef(new Map<string, number>());
  const frames = useRef(new Set<number>());
  const current = useRef(new Set<string>());
  const [flashing, setFlashing] = useState<ReadonlySet<string>>(() => new Set());
  const publish = useCallback(() => setFlashing(new Set(current.current)), []);

  const flash = useCallback(
    (id: string) => {
      const existing = timers.current.get(id);
      if (existing !== undefined) window.clearTimeout(existing);
      if (current.current.has(id)) {
        // Already flashing: drop the class for a frame so the animation restarts.
        current.current.delete(id);
        publish();
        const frame = window.requestAnimationFrame(() => {
          frames.current.delete(frame);
          current.current.add(id);
          publish();
        });
        frames.current.add(frame);
      } else {
        current.current.add(id);
        publish();
      }
      timers.current.set(
        id,
        window.setTimeout(() => {
          timers.current.delete(id);
          if (current.current.delete(id)) publish();
        }, durationMs)
      );
    },
    [durationMs, publish]
  );

  useEffect(() => {
    if (!rows) return;
    const { getId: idOf, getStatus: statusOf } = getters.current;
    const next = new Map<string, string>();
    rows.forEach((row) => next.set(idOf(row), statusOf(row) ?? ''));
    const changed = changedIds(baseline.current, next, includeNew);
    baseline.current = next;
    changed.forEach(flash);
  }, [rows, includeNew, flash]);

  useEffect(() => {
    const pending = timers.current;
    const pendingFrames = frames.current;
    return () => {
      pending.forEach((t) => window.clearTimeout(t));
      pendingFrames.forEach((f) => window.cancelAnimationFrame(f));
    };
  }, []);

  const flashClass = useCallback((id: string) => (flashing.has(id) ? 'animate-row-flash' : ''), [flashing]);

  return { flashing, flashClass, flash };
}

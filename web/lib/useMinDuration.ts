'use client';

import { useEffect, useRef, useState } from 'react';

/** Default minimum visible time for a busy indicator. */
export const MIN_BUSY_MS = 400;

/**
 * How much longer an indicator that turned on at `startedAt` must stay
 * visible at `now`, given a minimum of `minMs`. Zero when it may go.
 */
export function minDurationRemaining(startedAt: number | null, now: number, minMs: number): number {
  if (startedAt === null) return 0;
  return Math.max(0, startedAt + minMs - now);
}

/**
 * `active`, held true for at least `minMs` once it turns true. A refresh that
 * finishes in 80ms would otherwise flash its spinner for one frame (or not
 * at all), which reads as "nothing happened". Turns on immediately; turns
 * off no earlier than `minMs` after it turned on.
 */
export function useMinDuration(active: boolean, minMs: number = MIN_BUSY_MS): boolean {
  const [held, setHeld] = useState(active);
  const startedAt = useRef<number | null>(active ? Date.now() : null);

  // Render-time sync for the rising edge, so the indicator shows on the same
  // frame as the change that started the work.
  if (active && !held) {
    setHeld(true);
  }

  useEffect(() => {
    if (active) {
      startedAt.current = Date.now();
      return;
    }
    if (startedAt.current === null) return;
    const remaining = minDurationRemaining(startedAt.current, Date.now(), minMs);
    const release = () => {
      startedAt.current = null;
      setHeld(false);
    };
    if (remaining === 0) {
      release();
      return;
    }
    const timer = window.setTimeout(release, remaining);
    return () => window.clearTimeout(timer);
  }, [active, minMs]);

  return active || held;
}

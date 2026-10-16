import type { AISettings } from './types';

/**
 * How often a mounted AI control re-reads /v1/ai-settings while local AI is
 * not ready. Slow on purpose: the check only re-enables a disabled trigger,
 * and window focus rechecks immediately.
 */
export const AI_AVAILABILITY_RECHECK_MS = 30_000;

type Availability = Pick<AISettings, 'enabled' | 'available'>;

export function isAIReady(s: Availability): boolean {
  return s.enabled && s.available;
}

export interface AIAvailabilityWatch {
  /** Re-read now unless the last read said ready (e.g. on window focus). */
  recheck: () => void;
  stop: () => void;
}

/**
 * Reads AI availability once, then re-reads every `interval` while the last
 * read said not ready; it stops once ready. A failed read keeps the last
 * value (a first failure leaves the caller's trigger enabled, so there is
 * nothing to re-enable), and ticks while `isHidden()` skip the request.
 */
export function watchAIAvailability<T extends Availability>(
  read: () => Promise<T>,
  onResult: (value: T) => void,
  {
    interval = AI_AVAILABILITY_RECHECK_MS,
    isHidden = () => false,
    schedule = setTimeout,
    unschedule = clearTimeout,
  }: {
    interval?: number;
    isHidden?: () => boolean;
    schedule?: (callback: () => void, delay: number) => ReturnType<typeof setTimeout>;
    unschedule?: (timer: ReturnType<typeof setTimeout>) => void;
  } = {},
): AIAvailabilityWatch {
  let stopped = false;
  let inFlight = false;
  let latest: T | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;

  const clear = () => {
    if (timer !== undefined) unschedule(timer);
    timer = undefined;
  };
  const tick = () => {
    timer = undefined;
    if (isHidden()) timer = schedule(tick, interval);
    else void check();
  };
  const check = async () => {
    if (stopped || inFlight) return;
    clear();
    inFlight = true;
    try {
      const value = await read();
      if (!stopped) { latest = value; onResult(value); }
    } catch {
      // Keep the last value; the action's own error stays authoritative.
    } finally {
      inFlight = false;
      if (!stopped && latest !== null && !isAIReady(latest)) timer = schedule(tick, interval);
    }
  };

  void check();
  return {
    recheck: () => { if (latest === null || !isAIReady(latest)) void check(); },
    stop: () => { stopped = true; clear(); },
  };
}

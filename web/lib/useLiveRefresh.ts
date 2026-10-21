'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { useOptionalAlertEvents } from '@/components/alerts/AlertStreamProvider';
import type { AlertStreamEvent } from './types';

export interface LiveLoadOptions {
  /**
   * True for background refreshes (poll, stream event, tab return): keep
   * what is on screen, don't flash errors or dim panels, only swap data in
   * when the load succeeds.
   */
  silent: boolean;
}

/**
 * Return `false` (or throw) to mark a load as failed; anything else counts
 * as success and stamps `updatedAt`.
 */
export type LiveLoad = (options: LiveLoadOptions) => unknown | Promise<unknown>;

export interface UseLiveRefreshOptions {
  /** Poll period while the tab is visible. Default 30s. */
  intervalMs?: number;
  /** Debounce for reloads triggered by alert-stream events. Default 1.5s; 0 disables. */
  streamDebounceMs?: number;
  /** Which stream events should reload (default: all). */
  streamFilter?: (event: AlertStreamEvent) => boolean;
  /** Run a (non-silent) load on mount and whenever `load` changes. Default true. */
  immediate?: boolean;
  enabled?: boolean;
}

export interface LiveRefreshState {
  /** Epoch ms of the last successful load; null before the first. */
  updatedAt: number | null;
  /** Any load in flight, silent or not. */
  refreshing: boolean;
  /** A non-silent load in flight (first load, manual refresh, `load` change). */
  loading: boolean;
  /** The most recent load failed (cleared by the next success). */
  failed: boolean;
  /** Manual refresh (the Refresh button): non-silent. */
  refresh: () => Promise<void>;
}

/** True when a poll is due: visible, nothing in flight, interval elapsed. */
export function isPollDue(input: {
  visible: boolean;
  inFlight: boolean;
  lastStartedAt: number | null;
  now: number;
  intervalMs: number;
}): boolean {
  if (!input.visible || input.inFlight) return false;
  if (input.lastStartedAt === null) return true;
  return input.now - input.lastStartedAt >= input.intervalMs;
}

/**
 * Keeps a live page fresh: replaces the bare `useEffect(() => { load() })`.
 *
 * - loads on mount (and when `load` changes, e.g. a new range) — non-silent;
 * - polls every `intervalMs`, only while the tab is visible, and catches up
 *   at once when the tab comes back after a due poll;
 * - reloads (debounced) on alert-stream events;
 * - reloads once when the stream reconnects after a drop (missed events);
 * - never overlaps loads.
 *
 * Pair it with <LiveStatus updatedAt refreshing onRefresh /> in the header.
 */
export function useLiveRefresh(load: LiveLoad, options: UseLiveRefreshOptions = {}): LiveRefreshState {
  const {
    intervalMs = 30_000,
    streamDebounceMs = 1_500,
    streamFilter,
    immediate = true,
    enabled = true,
  } = options;
  const stream = useOptionalAlertEvents();
  const subscribe = stream?.subscribe;
  const subscribeResync = stream?.subscribeResync;

  const loadRef = useRef(load);
  const filterRef = useRef(streamFilter);
  useEffect(() => {
    loadRef.current = load;
    filterRef.current = streamFilter;
  });

  const inFlight = useRef(0);
  const lastStartedAt = useRef<number | null>(null);
  const [updatedAt, setUpdatedAt] = useState<number | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const run = useCallback(async (silent: boolean) => {
    // Background refreshes never stack; a manual one still goes through.
    if (silent && inFlight.current > 0) return;
    inFlight.current += 1;
    lastStartedAt.current = Date.now();
    setRefreshing(true);
    if (!silent) setLoading(true);
    let ok = true;
    try {
      ok = (await loadRef.current({ silent })) !== false;
    } catch {
      ok = false;
    } finally {
      inFlight.current -= 1;
      if (mounted.current) {
        if (inFlight.current === 0) setRefreshing(false);
        if (!silent) setLoading(false);
        setFailed(!ok);
        if (ok) setUpdatedAt(Date.now());
      }
    }
  }, []);

  // Mount and `load` change.
  useEffect(() => {
    if (!enabled || !immediate) return;
    void run(false);
  }, [enabled, immediate, load, run]);

  // Visibility-gated poll.
  useEffect(() => {
    if (!enabled || intervalMs <= 0) return;
    const tick = () => {
      const due = isPollDue({
        visible: document.visibilityState === 'visible',
        inFlight: inFlight.current > 0,
        lastStartedAt: lastStartedAt.current,
        now: Date.now(),
        intervalMs,
      });
      if (due) void run(true);
    };
    // Check often enough that a returning tab catches up promptly, but only
    // load when the interval has really elapsed.
    const timer = window.setInterval(tick, Math.min(intervalMs, 5_000));
    const onVisibility = () => {
      if (document.visibilityState === 'visible') tick();
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisibility);
    };
  }, [enabled, intervalMs, run]);

  // Stream events, debounced so an alert storm costs one reload.
  useEffect(() => {
    if (!enabled || !subscribe || streamDebounceMs <= 0) return;
    let timer: number | undefined;
    const unsubscribe = subscribe((event) => {
      if (filterRef.current && !filterRef.current(event)) return;
      window.clearTimeout(timer);
      timer = window.setTimeout(() => void run(true), streamDebounceMs);
    });
    return () => {
      window.clearTimeout(timer);
      unsubscribe();
    };
  }, [enabled, subscribe, streamDebounceMs, run]);

  // Reconnect after a drop: events in the gap are gone, refetch once.
  useEffect(() => {
    if (!enabled || !subscribeResync) return;
    return subscribeResync(() => void run(true));
  }, [enabled, subscribeResync, run]);

  const refresh = useCallback(() => run(false), [run]);

  return { updatedAt, refreshing, loading, failed, refresh };
}

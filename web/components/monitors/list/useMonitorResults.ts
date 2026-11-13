'use client';

import { useCallback, useState, useEffect, useMemo, useRef } from 'react';
import { useOptionalAlertEvents } from '@/components/alerts/AlertStreamProvider';
import { type LiveLoadOptions, useLiveRefresh } from '@/lib/useLiveRefresh';
import { Monitor, CheckResult } from '@/lib/types';
import { MONITOR_RESULTS_BATCH_MAX_IDS } from '@/lib/api';
import { getAllMonitors, sortMonitorsByName } from '@/lib/monitor-list';
import { fetchResultsBatches, resultsBatchError } from '@/lib/monitor-results';
import { RECENT_CHECKS } from '@/components/monitors/list/constants';


// Live refresh: the monitor list is one request and polls every 30s. Results
// come from GET /monitors/results/batch, MONITOR_RESULTS_BATCH_MAX_IDS
// monitors per request with every chunk in flight at once, so the first load
// ranks the list in one round trip. A background tick refetches only the
// monitors that changed state, that an alert event named, or whose next
// check is due, at most this many per tick (stalest first): one request.
const RESULTS_PER_TICK = MONITOR_RESULTS_BATCH_MAX_IDS;

/**
 * The monitors list's data: the list itself, each monitor's recent results
 * and each group's members, loaded once and then kept live.
 */
export function useMonitorResults() {
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [checkResultsMap, setCheckResultsMap] = useState<Record<string, CheckResult[]>>({});
  // False until the first full results pass lands. Until then a monitor
  // without results reads as "unknown" and would sort above "up", so rows
  // keep name order and re-rank once instead of jumping with every chunk.
  const [resultsReady, setResultsReady] = useState(false);
  // Group membership as ids (monitor_groups, from the results batch); the
  // member rows themselves are the list's own monitors, so they stay as
  // fresh as the list and a monitor in two groups is one object.
  const [memberIdsByGroup, setMemberIdsByGroup] = useState<Record<string, string[]>>({});
  const groupMembersMap = useMemo(() => {
    const byId = new Map(monitors.map((monitor) => [monitor.id, monitor]));
    const map: Record<string, Monitor[]> = {};
    Object.entries(memberIdsByGroup).forEach(([groupId, ids]) => {
      if (!byId.has(groupId)) return;
      map[groupId] = sortMonitorsByName(
        ids.map((id) => byId.get(id)).filter((member): member is Monitor => Boolean(member))
      );
    });
    return map;
  }, [monitors, memberIdsByGroup]);

  // ── Loading and live refresh ──
  //
  // The first load (and a manual refresh) fetches the list, then every
  // monitor's recent results and every group's member ids in one batched
  // round trip (fetchResultsBatches). Background ticks (useLiveRefresh: 30s
  // while visible, alert events, stream reconnects) refetch the list and
  // only the results that can have changed. A failed refresh keeps the rows
  // on screen.
  const monitorsRef = useRef<Monitor[]>([]);
  const fetchedAtRef = useRef(new Map<string, number>());
  const streamIdsRef = useRef(new Set<string>());
  const fullPassDone = useRef(false);

  const loadAllResults = useCallback(async (list: Monitor[]) => {
    const batch = await fetchResultsBatches(list.map((monitor) => monitor.id), RECENT_CHECKS);
    const now = Date.now();
    Object.keys(batch.results).forEach((id) => fetchedAtRef.current.set(id, now));
    const listed = new Set(list.map((monitor) => monitor.id));
    const keepFailed = <T,>(prev: Record<string, T>, next: Record<string, T>) => {
      batch.failed.forEach((id) => {
        if (id in prev) next[id] = prev[id];
      });
      return next;
    };
    // The exact maps replace the old ones (monitors that left the list drop
    // out); ids whose request failed keep what is on screen.
    setCheckResultsMap((prev) => keepFailed(prev, { ...batch.results }));
    setMemberIdsByGroup((prev) => {
      const next = keepFailed(prev, { ...batch.members });
      list.forEach((monitor) => {
        if (monitor.type === 'group' && !(monitor.id in next)) next[monitor.id] = [];
      });
      Object.keys(next).forEach((id) => {
        if (!listed.has(id)) delete next[id];
      });
      return next;
    });
    // Then say so: loadMonitors reports it (error callout or live status)
    // and, with the full pass not done, the next tick retries all of it.
    const failure = resultsBatchError(batch, list.length);
    if (failure) throw failure;
  }, []);

  // A background tick: results for monitors that changed state, that an
  // alert event named, or new to the list first, then those whose next
  // check is due since they were last fetched (stalest first), capped. The
  // batch returns member ids for any group among them, so a group that just
  // appeared (or changed state) gets its members too.
  const refreshChangedResults = useCallback(async (previous: Monitor[], list: Monitor[]) => {
    const before = new Map(previous.map((m) => [m.id, m]));
    const now = Date.now();
    const priority = new Set<string>(streamIdsRef.current);
    streamIdsRef.current.clear();
    list.forEach((m) => {
      const old = before.get(m.id);
      if (
        !old ||
        old.current_state !== m.current_state ||
        old.enabled !== m.enabled ||
        old.in_maintenance !== m.in_maintenance
      ) {
        priority.add(m.id);
      }
    });
    const due = list
      .filter((m) => m.enabled && !priority.has(m.id))
      .map((m) => {
        const intervalMs = Math.max(m.interval_seconds || 60, 10) * 1000;
        const since = now - (fetchedAtRef.current.get(m.id) ?? 0);
        return { id: m.id, overdue: since / intervalMs };
      })
      .filter((m) => m.overdue >= 1)
      .sort((a, b) => b.overdue - a.overdue)
      .map((m) => m.id);
    const ids = [...Array.from(priority).filter((id) => list.some((m) => m.id === id)), ...due].slice(
      0,
      RESULTS_PER_TICK
    );
    if (ids.length === 0) return;

    // Failed ids keep what is on screen (the live status reports the
    // failure) and stay due, so the next tick tries them again.
    const batch = await fetchResultsBatches(ids, RECENT_CHECKS);
    const fetchedAt = Date.now();
    Object.keys(batch.results).forEach((id) => fetchedAtRef.current.set(id, fetchedAt));
    if (Object.keys(batch.results).length > 0) setCheckResultsMap((prev) => ({ ...prev, ...batch.results }));
    if (Object.keys(batch.members).length > 0) setMemberIdsByGroup((prev) => ({ ...prev, ...batch.members }));
    const failure = resultsBatchError(batch, ids.length);
    if (failure) {
      batch.failed.forEach((id) => {
        if (priority.has(id)) streamIdsRef.current.add(id);
      });
      throw failure;
    }
  }, []);

  const loadMonitors = useCallback(
    async ({ silent }: LiveLoadOptions) => {
      try {
        if (!silent) setError('');
        const list = await getAllMonitors();
        const previous = monitorsRef.current;
        monitorsRef.current = list;
        setMonitors(list);
        // The list is on screen; results fill in behind it.
        setLoading(false);
        if (!silent || !fullPassDone.current) {
          await loadAllResults(list);
          fullPassDone.current = true;
        } else {
          await refreshChangedResults(previous, list);
        }
        setError('');
        setResultsReady(true);
        return true;
      } catch (err) {
        // Keep the rows already on screen. A background failure only shows
        // in the live status; a requested load also says why.
        if (!silent || monitorsRef.current.length === 0) {
          setError(err instanceof Error && err.message ? err.message : 'Failed to load monitors');
        }
        setLoading(false);
        setResultsReady(true);
        return false;
      }
    },
    [loadAllResults, refreshChangedResults]
  );

  // Alert events name the monitor whose results just moved; the stream
  // reload that useLiveRefresh debounces then refetches exactly those.
  const alertStream = useOptionalAlertEvents();
  const subscribeAlerts = alertStream?.subscribe;
  useEffect(() => {
    if (!subscribeAlerts) return;
    return subscribeAlerts((event) => {
      if (event.alert.monitor_id) streamIdsRef.current.add(event.alert.monitor_id);
    });
  }, [subscribeAlerts]);

  const live = useLiveRefresh(loadMonitors, {
    streamFilter: (event) => Boolean(event.alert.monitor_id),
  });
  const reloadAll = live.refresh;

  return { monitors, setMonitors, loading, error, checkResultsMap, resultsReady, groupMembersMap, live, reloadAll };
}

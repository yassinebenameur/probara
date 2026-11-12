'use client';

import { useState, useEffect, useRef } from 'react';
import { getMonitorResults } from '@/lib/api';
import { CheckResult, DashboardProblemMonitor } from '@/lib/types';
import { TRACE_SLOTS } from '@/components/dashboard/layout';
import { useVisibleRows } from '@/components/dashboard/useVisibleRows';

/**
 * Recent checks for the Needs attention rows on screen. `observe` registers a
 * row with the visibility observer.
 */
export function useProblemTraces(
  sortedProblems: DashboardProblemMonitor[],
  problemMonitorsData: DashboardProblemMonitor[] | null
) {
  const [traces, setTraces] = useState<Record<string, CheckResult[]>>({});
  // Each Needs attention row draws its recent checks, fetched for the rows on
  // screen only: again whenever the list reloads, and when a row scrolls
  // into view with traces older than the current list.
  const { visible, observe, supported: observing } = useVisibleRows();
  // Which list load each row's trace was fetched for: a row is fetched once
  // per load, and a batch in flight is never cancelled (scrolling would
  // otherwise refetch the same rows over and over).
  const traceGeneration = useRef(new Map<string, DashboardProblemMonitor[]>());
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (!problemMonitorsData || sortedProblems.length === 0) return;
    const due = sortedProblems.filter(
      (m) => (!observing || visible.has(m.monitor_id)) && traceGeneration.current.get(m.monitor_id) !== problemMonitorsData,
    );
    if (due.length === 0) return;
    due.forEach((m) => traceGeneration.current.set(m.monitor_id, problemMonitorsData));
    void Promise.all(
      due.map(async (monitor) => {
        try {
          const response = await getMonitorResults(monitor.monitor_id, { limit: TRACE_SLOTS });
          return [monitor.monitor_id, response.results] as const;
        } catch {
          return [monitor.monitor_id, [] as CheckResult[]] as const;
        }
      }),
    ).then((entries) => {
      if (mounted.current) setTraces((current) => ({ ...current, ...Object.fromEntries(entries) }));
    });
  }, [sortedProblems, visible, observing, problemMonitorsData]);

  return { traces, observe };
}

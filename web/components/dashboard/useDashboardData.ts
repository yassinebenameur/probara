'use client';

import { useState, useEffect, useCallback, useRef } from 'react';
import { getDashboardProblemMonitors, getDashboardRecentAlerts, getDashboardRecentFailures, getDashboardSummary, getTenantSettings } from '@/lib/api';
import { Alert, DashboardFailureEvent, DashboardSummaryResponse, DashboardProblemMonitor } from '@/lib/types';
import { useLiveRefresh, type LiveLoadOptions } from '@/lib/useLiveRefresh';
import { type DashboardPageRange, DASHBOARD_LIST_LIMIT } from '@/components/dashboard/layout';

/**
 * The dashboard's data for one range and tag scope: the summary first, then
 * the problem, failure and alert lists, kept live; plus tenant retention.
 */
export function useDashboardData(timeRange: DashboardPageRange, selectedTags: string[]) {
  const [dashboard, setDashboard] = useState<DashboardSummaryResponse | null>(null);
  const [problemMonitorsData, setProblemMonitorsData] = useState<DashboardProblemMonitor[] | null>(null);
  const [recentFailuresData, setRecentFailuresData] = useState<DashboardFailureEvent[] | null>(null);
  const [recentAlertsData, setRecentAlertsData] = useState<Alert[] | null>(null);
  const [tenantRetentionDays, setTenantRetentionDays] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Set when the panels' data failed to load; a section that has no data then
  // renders the failure instead of an empty (all-clear) state.
  const [sectionsError, setSectionsError] = useState(false);
  const requestSequenceRef = useRef(0);
  const hasDataRef = useRef(false);

  // Retention is a tenant setting, not live data: read it once.
  useEffect(() => {
    let cancelled = false;
    getTenantSettings()
      .then((settings) => {
        if (!cancelled) setTenantRetentionDays(settings.data_retention_days);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  /** False when the sections failed; stale responses are dropped. */
  const loadSecondarySections = useCallback(async (
    requestID: number,
    range: DashboardPageRange,
    tags: string[]
  ): Promise<boolean> => {
    try {
      const [problemResponse, failuresResponse, alertsResponse] = await Promise.all([
        getDashboardProblemMonitors({ range, limit: DASHBOARD_LIST_LIMIT, tags }),
        getDashboardRecentFailures({ range, limit: DASHBOARD_LIST_LIMIT, tags }),
        getDashboardRecentAlerts({ range, limit: DASHBOARD_LIST_LIMIT, tags }),
      ]);
      if (requestSequenceRef.current !== requestID) return true;

      setProblemMonitorsData(problemResponse.problem_monitors);
      setRecentFailuresData(failuresResponse.recent_failures);
      setRecentAlertsData(alertsResponse.recent_alerts);
      setSectionsError(false);
      return true;
    } catch (err) {
      console.error('Failed to load dashboard secondary sections:', err);
      if (requestSequenceRef.current !== requestID) return true;
      // Keep whatever was on screen; sections with nothing show the failure.
      setSectionsError(true);
      return false;
    }
  }, []);

  // Every load keeps what is on screen until new data lands; only the first
  // load shows skeletons. A silent (background) refresh that fails leaves the
  // page as it was and LiveStatus says "Update failed"; a visible one (first
  // load, range or tag change, Refresh) raises the error callout.
  const loadData = useCallback(async ({ silent }: LiveLoadOptions): Promise<boolean> => {
    const requestID = requestSequenceRef.current + 1;
    requestSequenceRef.current = requestID;
    const range = timeRange;
    const tags = selectedTags;

    try {
      const response = await getDashboardSummary({ range, tags });
      if (requestSequenceRef.current !== requestID) return true;

      setDashboard(response);
      setError(null);
      hasDataRef.current = true;
      // The summary paints first; the lists follow.
      return await loadSecondarySections(requestID, range, tags);
    } catch (err) {
      console.error('Failed to load dashboard data:', err);
      if (requestSequenceRef.current === requestID && (!silent || !hasDataRef.current)) {
        setError('Failed to load dashboard data');
        setSectionsError(true);
      }
      return false;
    }
  }, [loadSecondarySections, selectedTags, timeRange]);

  // Loads on mount and on range/tag change, then polls every 30s while the
  // tab is visible and reloads (debounced) on alert-stream events.
  const live = useLiveRefresh(loadData);
  const { refresh } = live;
  const retry = useCallback(() => void refresh(), [refresh]);

  return {
    dashboard,
    problemMonitorsData,
    recentFailuresData,
    recentAlertsData,
    tenantRetentionDays,
    error,
    sectionsError,
    live,
    retry,
  };
}

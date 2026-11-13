'use client';

import { useRouter } from 'next/navigation';
import { useState, useEffect, useCallback, useRef } from 'react';
import { Monitor, UpdateMonitorRequest, MonitorResultsResponse, CheckResult, MonitorAnalyticsResponse, MonitorAnalyticsRange } from '@/lib/types';
import { getMonitor, updateMonitor, getMonitorResults, getMonitorAnalytics, deleteMonitor, deleteMonitorHistory, getTenantSettings, getSlas, runMonitorNow, toggleMonitorEnabled } from '@/lib/api';
import { useToast } from '@/components/ui/ToastProvider';
import useDocumentTitle from '@/lib/useDocumentTitle';
import { shortFailureReason } from '@/lib/monitor-utils';
import { checkResultLabel } from '@/lib/status-tones';
import { strictestTarget, type Sla } from '@/lib/sla';
import { errorMessage } from '@/lib/errors';

export const MONITOR_DETAIL_TABS = ['overview', 'history', 'settings', 'json'] as const;
export type MonitorDetailTab = (typeof MONITOR_DETAIL_TABS)[number];

export const HISTORY_WINDOW_MS = 7 * 24 * 60 * 60 * 1000;
export const OVERVIEW_RESULTS_LIMIT = 50;
// Run check polls for the run's result this long before handing over to the
// regular 15 s refresh.
const RUN_RESULT_WAIT_MS = 60_000;

function mergeAndSortResults(newResults: CheckResult[], existingResults: CheckResult[]): CheckResult[] {
  const byID = new Map<string, CheckResult>();

  for (const result of existingResults) {
    byID.set(result.id, result);
  }
  for (const result of newResults) {
    byID.set(result.id, result);
  }

  return Array.from(byID.values()).sort(
    (a, b) => Date.parse(b.created_at) - Date.parse(a.created_at)
  );
}

/**
 * The monitor page's data and actions: the monitor, its recent and 7-day
 * results, analytics for the Overview range and the History tab, covering
 * SLAs, tenant retention, and run-now / pause / save / delete / history
 * reset with the dialog state they drive. The page owns the tab and the
 * range pickers and passes them in.
 */
export function useMonitorDetail(id: string, activeTab: MonitorDetailTab, overviewRange: MonitorAnalyticsRange) {
  const router = useRouter();

  const [monitor, setMonitor] = useState<Monitor | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>('');
  const { showToast, updateToast } = useToast();
  useDocumentTitle(monitor?.name);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [results, setResults] = useState<MonitorResultsResponse | null>(null);
  const [historyResults, setHistoryResults] = useState<MonitorResultsResponse | null>(null);
  const [historyAnalytics, setHistoryAnalytics] = useState<MonitorAnalyticsResponse | null>(null);
  const [analytics, setAnalytics] = useState<MonitorAnalyticsResponse | null>(null);
  const [coveringSlas, setCoveringSlas] = useState<Sla[]>([]);
  const [tenantRetentionDays, setTenantRetentionDays] = useState<number | null>(null);
  const [resultsLoading, setResultsLoading] = useState(false);
  const [analyticsLoading, setAnalyticsLoading] = useState(false);
  const [clearingHistory, setClearingHistory] = useState(false);
  const [isHistoryResetModalOpen, setIsHistoryResetModalOpen] = useState(false);
  const [historyResetConfirmation, setHistoryResetConfirmation] = useState('');
  const [runningCheck, setRunningCheck] = useState(false);
  const [confirmRunCall, setConfirmRunCall] = useState(false);
  const [togglingEnabled, setTogglingEnabled] = useState(false);
  const isPollingRef = useRef(false);
  const resultsRef = useRef<MonitorResultsResponse | null>(null);
  const mountedRef = useRef(true);
  // Latest analytics request per panel: a slow earlier range (1y) must not
  // overwrite the range the picker now shows.
  const analyticsRequestRef = useRef(0);
  const historyAnalyticsRequestRef = useRef(0);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    resultsRef.current = results;
  }, [results]);

  useEffect(() => {
    setHistoryResults(null);
    setHistoryAnalytics(null);
  }, [id]);

  const loadMonitor = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      const data = await getMonitor(id);
      setMonitor(data);
    } catch (err) {
      setError(errorMessage(err, 'Failed to load monitor'));
    } finally {
      setLoading(false);
    }
  }, [id]);

  // Recent results power the status badge and the History tab. Agent monitor
  // results are heartbeats now — metrics render from the metric store query
  // API (fetched inside AgentMetricsView), never from these results.
  const loadResults = useCallback(async (opts?: { silent?: boolean }) => {
    try {
      if (!opts?.silent) {
        setResultsLoading(true);
      }

      const currentResults = resultsRef.current;
      const latestKnownCreatedAt = currentResults?.results?.[0]?.created_at;
      const requestParams: { limit?: number; since?: string } =
        opts?.silent && latestKnownCreatedAt
          ? { since: latestKnownCreatedAt }
          : { limit: OVERVIEW_RESULTS_LIMIT };

      const data = await getMonitorResults(id, requestParams);
      // A monitor with no results yet (just created, paused) returns null.
      const fetched = data.results ?? [];

      if (opts?.silent && currentResults?.results?.length) {
        const mergedResults = mergeAndSortResults(fetched, currentResults.results)
          .slice(0, OVERVIEW_RESULTS_LIMIT);

        const mergedPayload: MonitorResultsResponse = {
          monitor_id: currentResults.monitor_id || data.monitor_id,
          results: mergedResults,
        };
        resultsRef.current = mergedPayload;
        setResults(mergedPayload);
      } else {
        const nextPayload: MonitorResultsResponse = {
          monitor_id: data.monitor_id,
          results: fetched.slice(0, OVERVIEW_RESULTS_LIMIT),
        };
        resultsRef.current = nextPayload;
        setResults(nextPayload);
      }
    } catch (err) {
      console.error('Failed to load monitor results:', err);
    } finally {
      if (!opts?.silent) {
        setResultsLoading(false);
      }
    }
  }, [id]);

  const loadHistoryResults = useCallback(async () => {
    try {
      setResultsLoading(true);
      const cutoffISO = new Date(Date.now() - HISTORY_WINDOW_MS).toISOString();
      const data = await getMonitorResults(id, { since: cutoffISO });
      setHistoryResults(data);
    } catch (err) {
      console.error('Failed to load monitor history results:', err);
      setHistoryResults(null);
    } finally {
      setResultsLoading(false);
    }
  }, [id]);

  // The History tab's availability comes from the same interval analytics
  // as the Overview at 7d, so the two tabs never disagree.
  // Both analytics loaders wait for the monitor's type (agents have none) and
  // drop any response a newer request has superseded.
  const monitorType = monitor?.type;
  const loadHistoryAnalytics = useCallback(async () => {
    const requestID = ++historyAnalyticsRequestRef.current;
    if (!monitorType) return;
    if (monitorType === 'agent') {
      setHistoryAnalytics(null);
      return;
    }
    try {
      const data = await getMonitorAnalytics(id, { range: '7d' });
      if (historyAnalyticsRequestRef.current === requestID) setHistoryAnalytics(data);
    } catch (err) {
      console.error('Failed to load 7-day monitor analytics:', err);
      if (historyAnalyticsRequestRef.current === requestID) setHistoryAnalytics(null);
    }
  }, [id, monitorType]);

  const loadAnalytics = useCallback(async () => {
    const requestID = ++analyticsRequestRef.current;
    if (!monitorType) return;
    if (monitorType === 'agent') {
      setAnalytics(null);
      setAnalyticsLoading(false);
      return;
    }
    try {
      setAnalyticsLoading(true);
      const data = await getMonitorAnalytics(id, { range: overviewRange });
      if (analyticsRequestRef.current === requestID) setAnalytics(data);
    } catch (err) {
      console.error('Failed to load monitor analytics:', err);
      if (analyticsRequestRef.current === requestID) setAnalytics(null);
    } finally {
      if (analyticsRequestRef.current === requestID) setAnalyticsLoading(false);
    }
  }, [id, monitorType, overviewRange]);

  useEffect(() => {
    void loadMonitor();
  }, [loadMonitor]);

  useEffect(() => {
    let cancelled = false;
    const loadTenantSettings = async () => {
      try {
        const settings = await getTenantSettings();
        if (!cancelled) {
          setTenantRetentionDays(settings.data_retention_days);
        }
      } catch {
        if (!cancelled) {
          setTenantRetentionDays(null);
        }
      }
    };
    void loadTenantSettings();
    return () => {
      cancelled = true;
    };
  }, []);

  // The availability target is the strictest SLA covering this monitor;
  // with none, lib/sla's default stands in.
  useEffect(() => {
    let cancelled = false;
    getSlas({ monitor_id: id })
      .then((slas) => {
        if (!cancelled) setCoveringSlas(slas);
      })
      .catch(() => {
        if (!cancelled) setCoveringSlas([]);
      });
    return () => {
      cancelled = true;
    };
  }, [id]);
  const slaTarget = strictestTarget(coveringSlas);
  const targetSla = coveringSlas.find((s) => s.target_pct === slaTarget);

  useEffect(() => {
    void loadResults();
  }, [loadResults]);

  useEffect(() => {
    void loadAnalytics();
  }, [loadAnalytics]);

  useEffect(() => {
    if (activeTab !== 'overview') {
      return;
    }

    // Agent metric charts poll themselves inside AgentMetricsView; this only
    // keeps the status badge and recent results fresh.
    const intervalMs = 15000;

    const poll = async () => {
      if (document.visibilityState !== 'visible') {
        return;
      }
      if (isPollingRef.current) {
        return;
      }
      isPollingRef.current = true;
      try {
        await loadResults({ silent: true });
      } finally {
        isPollingRef.current = false;
      }
    };

    const intervalId = window.setInterval(() => {
      void poll();
    }, intervalMs);

    const onVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        void poll();
      }
    };

    document.addEventListener('visibilitychange', onVisibilityChange);

    return () => {
      window.clearInterval(intervalId);
      document.removeEventListener('visibilitychange', onVisibilityChange);
    };
  }, [activeTab, loadResults, monitor?.type]);

  useEffect(() => {
    if (activeTab !== 'history') {
      return;
    }
    void loadHistoryResults();
    void loadHistoryAnalytics();
  }, [activeTab, loadHistoryResults, loadHistoryAnalytics]);

  const handleSubmit = async (data: UpdateMonitorRequest) => {
    try {
      setSaving(true);
      const updated = await updateMonitor(id, data);
      setMonitor(updated);
      showToast('Monitor updated successfully', 'success');
      loadResults();
    } catch (err) {
      showToast(errorMessage(err, 'Failed to update monitor'), 'error');
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async () => {
    setDeleting(true);
    try {
      await deleteMonitor(id);
      showToast('Monitor deleted', 'success');
      setTimeout(() => router.push('/monitors'), 1000);
    } catch (err) {
      showToast(errorMessage(err, 'Failed to delete'), 'error');
      setDeleting(false);
    }
  };

  // Queues an on-demand run (all of the monitor's locations), then waits for
  // its first result. One toast carries the whole run: "Running check" stays
  // up while it waits and becomes the result (or "no result yet").
  const handleRunCheck = async () => {
    if (!monitor || runningCheck) return;
    setConfirmRunCall(false);
    setRunningCheck(true);
    let toastId: string | null = null;
    // Leaving the page mid-wait must not strand a toast that never ends.
    const settle = (patch: Parameters<typeof updateToast>[1]) => {
      if (toastId) updateToast(toastId, patch);
      else showToast(patch);
    };
    try {
      const run = await runMonitorNow(monitor.id);
      toastId = showToast({
        title: 'Running check',
        message: `${monitor.name}: queued, waiting for the result…`,
        type: 'info',
        duration: Infinity,
      });
      const queuedMs = Date.parse(run.queued_at) || Date.now();
      const sinceISO = new Date(queuedMs - 1500).toISOString();
      const deadline = Date.now() + RUN_RESULT_WAIT_MS;
      while (mountedRef.current && Date.now() < deadline) {
        await new Promise((resolve) => window.setTimeout(resolve, 2000));
        if (!mountedRef.current) break;
        const data = await getMonitorResults(monitor.id, { limit: 10, since: sinceISO });
        const result = (data.results || []).find((r) => Date.parse(r.created_at) >= queuedMs - 1500);
        if (result) {
          await loadResults({ silent: true });
          void loadAnalytics();
          const reason = shortFailureReason(result.error_message, {
            httpStatus: result.http_status,
            timeoutSeconds: monitor.timeout_seconds,
          });
          settle({
            title: `Check ${checkResultLabel(result.status).toLowerCase()}`,
            message: reason ? `${monitor.name}: ${reason}` : monitor.name,
            type: result.status === 'success' ? 'success' : result.status === 'degraded' ? 'warning' : 'error',
          });
          return;
        }
      }
      settle({
        title: 'No result yet',
        message: 'It will appear under Latest checks when the worker reports.',
        type: 'info',
      });
    } catch (err) {
      settle({ title: 'Check not run', message: errorMessage(err, 'Failed to run the check'), type: 'error' });
    } finally {
      if (mountedRef.current) setRunningCheck(false);
    }
  };

  const handleToggleEnabled = async () => {
    if (!monitor || togglingEnabled) return;
    const next = !monitor.enabled;
    setTogglingEnabled(true);
    try {
      const updated = await toggleMonitorEnabled(monitor.id, next);
      // Keep the detail-only fields (locations, routing) the update
      // response may not embed.
      setMonitor((prev) => (prev ? { ...prev, enabled: updated.enabled ?? next } : updated));
      showToast(next ? 'Monitor resumed' : 'Monitor paused', 'success');
    } catch (err) {
      showToast(errorMessage(err, `Failed to ${next ? 'resume' : 'pause'} the monitor`), 'error');
    } finally {
      setTogglingEnabled(false);
    }
  };

  const openHistoryResetModal = () => {
    setHistoryResetConfirmation('');
    setIsHistoryResetModalOpen(true);
  };

  const closeHistoryResetModal = () => {
    if (clearingHistory) return;
    setHistoryResetConfirmation('');
    setIsHistoryResetModalOpen(false);
  };

  const handleClearHistory = async () => {
    if (!monitor) return;

    try {
      setClearingHistory(true);
      await deleteMonitorHistory(id);
      setResults((current) => current ? { ...current, results: [] } : current);
      setHistoryResults((current) => current ? { ...current, results: [] } : current);
      setAnalytics(null);
      setHistoryAnalytics(null);
      await Promise.all([
        loadMonitor(),
        loadResults(),
        loadAnalytics(),
        activeTab === 'history' ? loadHistoryResults() : Promise.resolve(),
        activeTab === 'history' ? loadHistoryAnalytics() : Promise.resolve(),
      ]);
      showToast(monitor.type === 'group'
          ? 'Group history cleared for all member monitors'
          : 'Monitor history cleared', 'success');
      setIsHistoryResetModalOpen(false);
      setHistoryResetConfirmation('');
    } catch (err) {
      showToast(errorMessage(err, 'Failed to clear monitor history'), 'error');
    } finally {
      setClearingHistory(false);
    }
  };

  return {
    monitor,
    loading,
    saving,
    error,
    results,
    historyResults,
    historyAnalytics,
    analytics,
    resultsLoading,
    analyticsLoading,
    slaTarget,
    targetSla,
    tenantRetentionDays,
    runningCheck,
    confirmRunCall,
    setConfirmRunCall,
    togglingEnabled,
    confirmDelete,
    setConfirmDelete,
    deleting,
    clearingHistory,
    isHistoryResetModalOpen,
    historyResetConfirmation,
    setHistoryResetConfirmation,
    handleSubmit,
    handleDelete,
    handleRunCheck,
    handleToggleEnabled,
    openHistoryResetModal,
    closeHistoryResetModal,
    handleClearHistory,
  };
}

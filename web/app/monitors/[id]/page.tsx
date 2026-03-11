'use client';

import { useRouter, useParams } from 'next/navigation';
import { useState, useEffect, useCallback, useRef } from 'react';
import Link from 'next/link';
import Image from 'next/image';
import { Monitor, UpdateMonitorRequest, MonitorResultsResponse, CheckResult, MonitorAnalyticsResponse, MonitorAnalyticsRange } from '@/lib/types';
import { getMonitor, updateMonitor, getMonitorResults, getMonitorAnalytics, deleteMonitor, deleteMonitorHistory, getSyntheticBrowserScreenshotUrl, getTenantSettings } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import MonitorForm from '@/components/monitors/MonitorForm';
import MonitorDetailOverview from '@/components/monitors/MonitorDetailOverview';
import MonitorDetailHistory from '@/components/monitors/MonitorDetailHistory';
import MonitorDetailJson from '@/components/monitors/MonitorDetailJson';
import { getEffectiveMonitorStatus, MonitorDisplayStatus } from '@/lib/monitor-utils';

type TabType = 'overview' | 'history' | 'settings' | 'json';
type AgentTimeRange = '1h' | '6h' | '24h' | '7d';
type OverviewTimeRange = MonitorAnalyticsRange;

const AGENT_RANGE_MS: Record<AgentTimeRange, number> = {
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
  '7d': 7 * 24 * 60 * 60 * 1000,
};

const OVERVIEW_RANGE_MS: Record<OverviewTimeRange, number> = {
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
  '7d': 7 * 24 * 60 * 60 * 1000,
  '30d': 30 * 24 * 60 * 60 * 1000,
  '90d': 90 * 24 * 60 * 60 * 1000,
  '365d': 365 * 24 * 60 * 60 * 1000,
};

const NON_AGENT_HISTORY_WINDOW_MS = 7 * 24 * 60 * 60 * 1000;
const MIN_CLIENT_RESULTS = 500;
const MAX_CLIENT_RESULTS = 100000;
const LIMIT_PADDING = 120;
const LIMIT_HEADROOM_NUM = 115;
const LIMIT_HEADROOM_DEN = 100;

function estimateResultsLimit(
  windowMs: number,
  intervalSeconds?: number,
  fallbackIntervalSeconds = 60
): number {
  const safeIntervalSeconds = intervalSeconds && intervalSeconds > 0
    ? intervalSeconds
    : fallbackIntervalSeconds;

  const expectedPoints = Math.ceil(windowMs / (safeIntervalSeconds * 1000));
  const buffered = Math.ceil((expectedPoints * LIMIT_HEADROOM_NUM) / LIMIT_HEADROOM_DEN) + LIMIT_PADDING;

  return Math.max(MIN_CLIENT_RESULTS, Math.min(MAX_CLIENT_RESULTS, buffered));
}

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

// Status badge component
function StatusBadge({ status }: { status: MonitorDisplayStatus }) {
  const config = {
    up: { label: 'Operational', bg: 'bg-emerald-500/10', text: 'text-emerald-400', dot: 'bg-emerald-500' },
    down: { label: 'Down', bg: 'bg-rose-500/10', text: 'text-rose-400', dot: 'bg-rose-500' },
    degraded: { label: 'Degraded', bg: 'bg-amber-500/10', text: 'text-amber-400', dot: 'bg-amber-500' },
    paused: { label: 'Paused', bg: 'bg-slate-500/10', text: 'text-slate-300', dot: 'bg-slate-500' },
    unknown: { label: 'Unknown', bg: 'bg-slate-500/10', text: 'text-slate-300', dot: 'bg-slate-500' },
  };
  const { label, bg, text, dot } = config[status];

  return (
    <span className={`inline-flex items-center gap-1.5 rounded-full ${bg} px-2.5 py-1 text-xs font-medium ${text}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${dot}`} />
      {label}
    </span>
  );
}

function ConfirmHistoryResetModal({
  monitorName,
  isGroup,
  confirmationText,
  onConfirmationTextChange,
  onClose,
  onConfirm,
  loading,
}: {
  monitorName: string;
  isGroup: boolean;
  confirmationText: string;
  onConfirmationTextChange: (value: string) => void;
  onClose: () => void;
  onConfirm: () => void;
  loading: boolean;
}) {
  const expectedText = 'CLEAR';
  const canConfirm = confirmationText.trim().toUpperCase() === expectedText && !loading;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/80 px-4 backdrop-blur-sm">
      <div className="w-full max-w-lg rounded-2xl border border-rose-500/20 bg-slate-950 shadow-2xl">
        <div className="border-b border-white/[0.06] px-6 py-5">
          <div className="flex items-start justify-between gap-4">
            <div>
              <p className="text-[11px] font-semibold uppercase tracking-[0.22em] text-rose-400">Danger Zone</p>
              <h2 className="mt-2 text-lg font-semibold text-white">Clear monitor history</h2>
              <p className="mt-2 text-sm text-slate-400">
                {isGroup
                  ? `This will permanently remove checks, alerts, and SLA history for ${monitorName} and every monitor inside that group.`
                  : `This will permanently remove checks, alerts, and SLA history for ${monitorName}, but keep the monitor itself.`}
              </p>
            </div>
            <button
              type="button"
              onClick={onClose}
              className="rounded-lg border border-white/[0.08] px-2.5 py-1.5 text-xs text-slate-400 transition-colors hover:border-white/[0.16] hover:text-white"
              disabled={loading}
            >
              Close
            </button>
          </div>
        </div>

        <div className="space-y-4 px-6 py-5">
          <div className="rounded-xl border border-rose-500/15 bg-rose-500/5 px-4 py-3 text-sm text-slate-300">
            <p className="font-medium text-rose-300">This cannot be undone.</p>
            <p className="mt-1 text-slate-400">
              Dashboard failures, monitor analytics, and public status page history will rebuild only from future checks.
            </p>
          </div>

          <div>
            <label className="block text-xs font-medium uppercase tracking-[0.18em] text-slate-500">
              Type {expectedText} to confirm
            </label>
            <input
              type="text"
              value={confirmationText}
              onChange={(event) => onConfirmationTextChange(event.target.value)}
              className="mt-2 w-full rounded-xl border border-white/[0.08] bg-slate-900 px-4 py-3 text-sm text-white outline-none transition-colors focus:border-rose-400/60"
              placeholder={expectedText}
              autoFocus
              disabled={loading}
            />
          </div>
        </div>

        <div className="flex items-center justify-end gap-3 border-t border-white/[0.06] px-6 py-4">
          <button
            type="button"
            onClick={onClose}
            className="rounded-xl border border-white/[0.08] px-4 py-2 text-sm text-slate-300 transition-colors hover:border-white/[0.16] hover:text-white"
            disabled={loading}
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={!canConfirm}
            className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-4 py-2 text-sm font-medium text-rose-300 transition-colors hover:bg-rose-500/20 disabled:cursor-not-allowed disabled:border-white/[0.08] disabled:bg-slate-900 disabled:text-slate-500"
          >
            {loading ? 'Clearing…' : isGroup ? 'Clear Group History' : 'Clear History'}
          </button>
        </div>
      </div>
    </div>
  );
}

export default function EditMonitorPage() {
  const router = useRouter();
  const params = useParams();
  const id = params.id as string;

  const [monitor, setMonitor] = useState<Monitor | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>('');
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);
  const [results, setResults] = useState<MonitorResultsResponse | null>(null);
  const [analytics, setAnalytics] = useState<MonitorAnalyticsResponse | null>(null);
  const [tenantRetentionDays, setTenantRetentionDays] = useState<number | null>(null);
  const [resultsLoading, setResultsLoading] = useState(false);
  const [analyticsLoading, setAnalyticsLoading] = useState(false);
  const [clearingHistory, setClearingHistory] = useState(false);
  const [isHistoryResetModalOpen, setIsHistoryResetModalOpen] = useState(false);
  const [historyResetConfirmation, setHistoryResetConfirmation] = useState('');
  const [activeTab, setActiveTab] = useState<TabType>('overview');
  const [agentTimeRange, setAgentTimeRange] = useState<AgentTimeRange>('24h');
  const [overviewRange, setOverviewRange] = useState<OverviewTimeRange>('24h');
  const [screenshotBlobURL, setScreenshotBlobURL] = useState<string | null>(null);
  const [screenshotLoading, setScreenshotLoading] = useState(false);
  const isPollingRef = useRef(false);
  const resultsRef = useRef<MonitorResultsResponse | null>(null);

  useEffect(() => {
    resultsRef.current = results;
  }, [results]);

  const loadMonitor = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      const data = await getMonitor(id);
      setMonitor(data);
    } catch (err: any) {
      setError(err.message || 'Failed to load monitor');
    } finally {
      setLoading(false);
    }
  }, [id]);

  const loadResults = useCallback(async (opts?: { silent?: boolean; range?: AgentTimeRange }) => {
    try {
      if (!opts?.silent) {
        setResultsLoading(true);
      }

      const isAgentMonitor = monitor?.type === 'agent';
      const range = opts?.range ?? agentTimeRange;
      const nowMs = Date.now();
      const currentResults = resultsRef.current;
      const latestKnownCreatedAt = currentResults?.results?.[0]?.created_at;

      const selectedWindowMs = isAgentMonitor
        ? AGENT_RANGE_MS[range]
        : NON_AGENT_HISTORY_WINDOW_MS;
      const cutoffMs = nowMs - selectedWindowMs;
      const cutoffISO = new Date(cutoffMs).toISOString();
      const maxResults = estimateResultsLimit(
        selectedWindowMs,
        monitor?.interval_seconds,
        isAgentMonitor ? 30 : 60
      );

      const requestParams =
        opts?.silent && latestKnownCreatedAt
          ? { since: latestKnownCreatedAt }
          : { since: cutoffISO };

      const data = await getMonitorResults(id, requestParams);

      if (opts?.silent && currentResults?.results?.length) {
        const mergedResults = mergeAndSortResults(data.results, currentResults.results)
          .filter((result) => {
            const ts = Date.parse(result.created_at);
            return Number.isFinite(ts) && ts >= cutoffMs;
          })
          .slice(0, maxResults);

        const mergedPayload: MonitorResultsResponse = {
          monitor_id: currentResults.monitor_id || data.monitor_id,
          results: mergedResults,
        };
        resultsRef.current = mergedPayload;
        setResults(mergedPayload);
      } else {
        resultsRef.current = data;
        setResults(data);
      }
    } catch (err: any) {
      console.error('Failed to load monitor results:', err);
    } finally {
      if (!opts?.silent) {
        setResultsLoading(false);
      }
    }
  }, [id, monitor?.type, monitor?.interval_seconds, agentTimeRange]);

  const loadAnalytics = useCallback(async (range?: OverviewTimeRange) => {
    if (monitor?.type === 'agent') {
      setAnalytics(null);
      return;
    }
    try {
      setAnalyticsLoading(true);
      const data = await getMonitorAnalytics(id, { range: range ?? overviewRange });
      setAnalytics(data);
    } catch (err) {
      console.error('Failed to load monitor analytics:', err);
      setAnalytics(null);
    } finally {
      setAnalyticsLoading(false);
    }
  }, [id, monitor?.type, overviewRange]);

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

  useEffect(() => {
    void loadResults();
  }, [loadResults]);

  useEffect(() => {
    void loadAnalytics();
  }, [loadAnalytics]);

  useEffect(() => {
    if (activeTab !== 'overview' && activeTab !== 'history') {
      return;
    }

    const intervalMs = monitor?.type === 'agent' ? 10000 : 15000;

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

  const handleSubmit = async (data: UpdateMonitorRequest) => {
    try {
      setSaving(true);
      const updated = await updateMonitor(id, data);
      setMonitor(updated);
      setToast({ message: 'Monitor updated successfully', type: 'success' });
      loadResults();
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to update monitor', type: 'error' });
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async () => {
    if (!confirm('Delete this monitor? All check history will be removed.')) return;

    try {
      await deleteMonitor(id);
      setToast({ message: 'Monitor deleted', type: 'success' });
      setTimeout(() => router.push('/monitors'), 1000);
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to delete', type: 'error' });
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
      setAnalytics(null);
      await Promise.all([loadMonitor(), loadResults(), loadAnalytics()]);
      setToast({
        message: monitor.type === 'group'
          ? 'Group history cleared for all member monitors'
          : 'Monitor history cleared',
        type: 'success',
      });
      setIsHistoryResetModalOpen(false);
      setHistoryResetConfirmation('');
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to clear monitor history', type: 'error' });
    } finally {
      setClearingHistory(false);
    }
  };

  // Get URL from monitor config
  const getUrl = () => {
    if (!monitor) return null;
    if (monitor.config && 'url' in monitor.config) return monitor.config.url;
    if (monitor.url) return monitor.url;
    if (monitor.config && 'host' in monitor.config) return monitor.config.host;
    return null;
  };

  const status = monitor ? getEffectiveMonitorStatus(monitor, results?.results || []) : 'unknown';
  const getSyntheticBrowserScreenshotPath = (result?: CheckResult): string | null => {
    const metrics = result?.metrics_data as { synthetic_browser?: { artifacts?: { screenshot_path?: string } } } | undefined;
    return metrics?.synthetic_browser?.artifacts?.screenshot_path || null;
  };

  const latestSyntheticBrowserResult = monitor?.type === 'synthetic_browser'
    ? (results?.results || []).find((result) => Boolean(getSyntheticBrowserScreenshotPath(result)))
    : undefined;
  const latestSyntheticBrowserScreenshotPath = getSyntheticBrowserScreenshotPath(latestSyntheticBrowserResult);
  const syntheticBrowserScreenshotURL = monitor && latestSyntheticBrowserScreenshotPath
    ? getSyntheticBrowserScreenshotUrl(monitor.id, monitor.tenant_id, latestSyntheticBrowserScreenshotPath)
    : null;

  useEffect(() => {
    let cancelled = false;
    let objectURL: string | null = null;

    if (!syntheticBrowserScreenshotURL) {
      setScreenshotBlobURL((prev) => {
        if (prev) URL.revokeObjectURL(prev);
        return null;
      });
      setScreenshotLoading(false);
      return;
    }

    const loadScreenshot = async () => {
      setScreenshotLoading(true);
      try {
        const headers: HeadersInit = {};
        const apiKey = getApiKey();
        if (apiKey) {
          headers.Authorization = `Bearer ${apiKey}`;
        }

        const response = await fetch(syntheticBrowserScreenshotURL, {
          method: 'GET',
          headers,
          credentials: 'include',
        });
        if (!response.ok) {
          throw new Error(`screenshot request failed: ${response.status}`);
        }

        const blob = await response.blob();
        objectURL = URL.createObjectURL(blob);
        if (!cancelled) {
          setScreenshotBlobURL((prev) => {
            if (prev) URL.revokeObjectURL(prev);
            return objectURL;
          });
        }
      } catch (err) {
        console.error('Failed to load synthetic browser screenshot:', err);
        if (!cancelled) {
          setScreenshotBlobURL((prev) => {
            if (prev) URL.revokeObjectURL(prev);
            return null;
          });
        }
      } finally {
        if (!cancelled) {
          setScreenshotLoading(false);
        }
      }
    };

    loadScreenshot();

    return () => {
      cancelled = true;
      if (objectURL) {
        URL.revokeObjectURL(objectURL);
      }
    };
  }, [syntheticBrowserScreenshotURL]);

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-slate-500">Loading monitor...</div>
      </div>
    );
  }

  if (error || !monitor) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-center">
        <p className="text-rose-400">{error || 'Monitor not found'}</p>
        <Link href="/monitors">
          <button className="mt-4 text-sm text-slate-400 hover:text-white">
            ← Back to Monitors
          </button>
        </Link>
      </div>
    );
  }

  const tabs = [
    { id: 'overview', label: 'Overview' },
    { id: 'history', label: 'History' },
    { id: 'settings', label: 'Settings' },
    { id: 'json', label: 'JSON' },
  ] as const;
  const selectedWindowMs =
    monitor.type === 'agent'
      ? AGENT_RANGE_MS[agentTimeRange]
      : OVERVIEW_RANGE_MS[overviewRange];
  const boundedRetentionDays =
    tenantRetentionDays && tenantRetentionDays > 0 ? tenantRetentionDays : null;
  const retentionWindowMs =
    boundedRetentionDays !== null
      ? boundedRetentionDays * 24 * 60 * 60 * 1000
      : null;
  const showRetentionWarning =
    (activeTab === 'overview' || activeTab === 'history') &&
    retentionWindowMs !== null &&
    selectedWindowMs > retentionWindowMs;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div>
          {/* Breadcrumb */}
          <div className="flex items-center gap-2 text-xs text-slate-500 mb-3">
            <Link href="/monitors" className="hover:text-slate-400">Monitors</Link>
            <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
            </svg>
            <span className="text-slate-400">{monitor.name}</span>
          </div>

          {/* Title & Status */}
          <div className="flex items-center gap-3">
            <h1 className="text-xl font-semibold text-white">{monitor.name}</h1>
            <StatusBadge status={status} />
          </div>

          {/* Subtitle */}
          <p className="mt-1 text-sm text-slate-500">
            <span className="uppercase">{monitor.type}</span>
            {getUrl() && <span> · {getUrl()}</span>}
          </p>
        </div>

        {/* Actions */}
        <div className="flex items-center gap-2">
          <button
            onClick={handleDelete}
            className="rounded-lg border border-rose-500/30 px-3 py-1.5 text-xs text-rose-400 transition-colors hover:bg-rose-500/10"
          >
            Delete
          </button>
        </div>
      </div>

      {/* Tabs */}
      <div className="flex items-center gap-1 rounded-lg border border-white/[0.06] bg-slate-900/50 p-1 w-fit">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={`rounded-md px-4 py-1.5 text-xs font-medium transition-colors ${
              activeTab === tab.id
                ? 'bg-white/[0.08] text-white'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {showRetentionWarning && (
        <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3">
          <p className="text-sm text-amber-300">
            Data retention is set to {boundedRetentionDays} day{boundedRetentionDays === 1 ? '' : 's'}.
            Older history is deleted, so this view may be partial.
          </p>
        </div>
      )}

      {/* Content */}
      <div className="grid gap-6 lg:grid-cols-[1fr_400px]">
        {/* Main Content */}
        <div>
          {activeTab === 'overview' && (
              <MonitorDetailOverview
                monitor={monitor}
                results={results?.results || []}
                analytics={analytics}
                loading={resultsLoading || analyticsLoading}
              agentTimeRange={agentTimeRange}
              onAgentTimeRangeChange={(range) => {
                setAgentTimeRange(range);
                void loadResults({ range });
              }}
              timeRange={overviewRange}
              onTimeRangeChange={(range) => {
                setOverviewRange(range);
                void loadAnalytics(range);
              }}
            />
          )}
          {activeTab === 'history' && (
            <MonitorDetailHistory
              results={results?.results || []}
              loading={resultsLoading}
            />
          )}
          {activeTab === 'settings' && (
            <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
              <MonitorForm
                monitor={monitor}
                onSubmit={handleSubmit}
                onCancel={() => router.push('/monitors')}
                loading={saving}
              />
            </div>
          )}
          {activeTab === 'json' && (
            <MonitorDetailJson monitor={monitor} />
          )}
        </div>

        {/* Sidebar - Quick Stats (visible on overview) */}
        {activeTab === 'overview' && (
          <div className="space-y-4">
            {/* Quick Actions */}
            <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
              <h3 className="text-xs font-medium uppercase tracking-wider text-slate-500 mb-3">Quick Actions</h3>
              <div className="space-y-2">
                <button
                  onClick={() => setActiveTab('settings')}
                  className="w-full flex items-center gap-3 rounded-lg bg-slate-800/50 px-3 py-2.5 text-left text-sm text-slate-300 transition-colors hover:bg-slate-800"
                >
                  <svg className="h-4 w-4 text-slate-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                  </svg>
                  Edit Settings
                </button>
                <button
                  onClick={() => setActiveTab('history')}
                  className="w-full flex items-center gap-3 rounded-lg bg-slate-800/50 px-3 py-2.5 text-left text-sm text-slate-300 transition-colors hover:bg-slate-800"
                >
                  <svg className="h-4 w-4 text-slate-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                  View History
                </button>
                <button
                  onClick={openHistoryResetModal}
                  className="w-full flex items-center gap-3 rounded-lg border border-rose-500/20 bg-rose-500/10 px-3 py-2.5 text-left text-sm text-rose-300 transition-colors hover:bg-rose-500/15"
                >
                  <svg className="h-4 w-4 text-rose-300" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6M9 7V4a1 1 0 011-1h4a1 1 0 011 1v3m-7 0h8" />
                  </svg>
                  {monitor.type === 'group' ? 'Clear Group History' : 'Clear History'}
                </button>
              </div>
            </div>

            {/* Config Summary */}
            <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
              <h3 className="text-xs font-medium uppercase tracking-wider text-slate-500 mb-3">Configuration</h3>
              <div className="space-y-2 text-sm">
                <div className="flex justify-between">
                  <span className="text-slate-500">Type</span>
                  <span className="text-slate-300 uppercase">{monitor.type}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-slate-500">Interval</span>
                  <span className="text-slate-300">{monitor.interval_seconds}s</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-slate-500">Timeout</span>
                  <span className="text-slate-300">{monitor.timeout_seconds}s</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-slate-500">Status</span>
                  <span className={monitor.enabled ? 'text-emerald-400' : 'text-slate-500'}>
                    {monitor.enabled ? 'Enabled' : 'Paused'}
                  </span>
                </div>
              </div>
            </div>

            {monitor.type === 'synthetic_browser' && (
              <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
                <h3 className="text-xs font-medium uppercase tracking-wider text-slate-500 mb-3">Latest failure screenshot</h3>
                {screenshotLoading ? (
                  <p className="text-xs text-slate-500">Loading screenshot...</p>
                ) : screenshotBlobURL ? (
                  <div className="overflow-hidden rounded-lg border border-white/[0.08] bg-slate-800/40 p-2">
                    <Image
                      src={screenshotBlobURL}
                      alt="Latest synthetic browser failure screenshot"
                      width={1200}
                      height={675}
                      className="h-auto max-h-[220px] w-full rounded-md object-contain"
                      unoptimized
                      loading="lazy"
                    />
                    <p className="mt-2 text-[11px] text-slate-500">
                      {latestSyntheticBrowserResult
                        ? `Captured ${new Date(latestSyntheticBrowserResult.created_at).toLocaleString()}`
                        : 'Latest run'}
                    </p>
                  </div>
                ) : (
                  <p className="text-xs text-slate-500">
                    No screenshot available yet. A failed run with screenshot capture enabled is required.
                  </p>
                )}
              </div>
            )}
          </div>
        )}
      </div>

      {/* Toast */}
      {toast && (
        <div className={`fixed bottom-4 right-4 rounded-lg px-4 py-3 shadow-lg ${
          toast.type === 'success' ? 'bg-emerald-500' : 'bg-rose-500'
        }`}>
          <div className="flex items-center gap-3">
            <p className="text-sm text-white">{toast.message}</p>
            <button onClick={() => setToast(null)} className="text-white/80 hover:text-white">
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        </div>
      )}

      {monitor && isHistoryResetModalOpen && (
        <ConfirmHistoryResetModal
          monitorName={monitor.name}
          isGroup={monitor.type === 'group'}
          confirmationText={historyResetConfirmation}
          onConfirmationTextChange={setHistoryResetConfirmation}
          onClose={closeHistoryResetModal}
          onConfirm={handleClearHistory}
          loading={clearingHistory}
        />
      )}
    </div>
  );
}

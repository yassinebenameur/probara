'use client';

import { useRouter, useParams } from 'next/navigation';
import { useState, useEffect, useCallback, useRef } from 'react';
import Image from 'next/image';
import { Clock, Settings as SettingsIcon, Trash2 } from 'lucide-react';
import { Monitor, UpdateMonitorRequest, MonitorResultsResponse, CheckResult, MonitorAnalyticsResponse, MonitorAnalyticsRange, DBMetricsEnvelope, TCPMonitorConfig, TCPMetricsEnvelope } from '@/lib/types';
import { getMonitor, updateMonitor, getMonitorResults, getMonitorAnalytics, deleteMonitor, deleteMonitorHistory, getSyntheticBrowserScreenshotUrl, getTenantSettings } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import MonitorForm from '@/components/monitors/MonitorForm';
import MonitorDetailOverview from '@/components/monitors/MonitorDetailOverview';
import MonitorDetailHistory from '@/components/monitors/MonitorDetailHistory';
import MonitorDetailJson from '@/components/monitors/MonitorDetailJson';
import { MonitorDependenciesCard } from '@/components/monitors/MonitorDependenciesCard';
import PageHeader from '@/components/ui/PageHeader';
import FormCard from '@/components/ui/FormCard';
import Button from '@/components/ui/Button';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { getEffectiveMonitorStatus, MonitorDisplayStatus } from '@/lib/monitor-utils';

type TabType = 'overview' | 'history' | 'settings' | 'json';

const isDatabaseMonitorType = (t: string): boolean =>
  t === 'redis' || t === 'postgres' || t === 'mongodb' || t === 'rabbitmq';
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
const NON_AGENT_OVERVIEW_RESULTS_LIMIT = 50;
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
    maintenance: { label: 'Maintenance', bg: 'bg-sky-500/10', text: 'text-sky-300', dot: 'bg-sky-500' },
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
  const { showToast } = useToast();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [results, setResults] = useState<MonitorResultsResponse | null>(null);
  const [historyResults, setHistoryResults] = useState<MonitorResultsResponse | null>(null);
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

  useEffect(() => {
    setHistoryResults(null);
  }, [id]);

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
      let maxResults = NON_AGENT_OVERVIEW_RESULTS_LIMIT;
      let cutoffMs: number | null = null;
      let requestParams: { limit?: number; since?: string };

      if (isAgentMonitor) {
        const selectedWindowMs = AGENT_RANGE_MS[range];
        cutoffMs = nowMs - selectedWindowMs;
        const cutoffISO = new Date(cutoffMs).toISOString();
        maxResults = estimateResultsLimit(
          selectedWindowMs,
          monitor?.interval_seconds,
          30
        );
        requestParams =
          opts?.silent && latestKnownCreatedAt
            ? { since: latestKnownCreatedAt }
            : { since: cutoffISO };
      } else {
        requestParams =
          opts?.silent && latestKnownCreatedAt
            ? { since: latestKnownCreatedAt }
            : { limit: NON_AGENT_OVERVIEW_RESULTS_LIMIT };
      }

      const data = await getMonitorResults(id, requestParams);

      if (opts?.silent && currentResults?.results?.length) {
        let mergedResults = mergeAndSortResults(data.results, currentResults.results);
        if (cutoffMs !== null) {
          mergedResults = mergedResults.filter((result) => {
            const ts = Date.parse(result.created_at);
            return Number.isFinite(ts) && ts >= cutoffMs;
          });
        }
        mergedResults = mergedResults.slice(0, maxResults);

        const mergedPayload: MonitorResultsResponse = {
          monitor_id: currentResults.monitor_id || data.monitor_id,
          results: mergedResults,
        };
        resultsRef.current = mergedPayload;
        setResults(mergedPayload);
      } else {
        const nextPayload: MonitorResultsResponse = {
          monitor_id: data.monitor_id,
          results: isAgentMonitor ? data.results : data.results.slice(0, NON_AGENT_OVERVIEW_RESULTS_LIMIT),
        };
        resultsRef.current = nextPayload;
        setResults(nextPayload);
      }
    } catch (err: any) {
      console.error('Failed to load monitor results:', err);
    } finally {
      if (!opts?.silent) {
        setResultsLoading(false);
      }
    }
  }, [id, monitor?.type, monitor?.interval_seconds, agentTimeRange]);

  const loadHistoryResults = useCallback(async () => {
    if (monitor?.type === 'agent') {
      await loadResults();
      return;
    }

    try {
      setResultsLoading(true);
      const cutoffISO = new Date(Date.now() - NON_AGENT_HISTORY_WINDOW_MS).toISOString();
      const data = await getMonitorResults(id, { since: cutoffISO });
      setHistoryResults(data);
    } catch (err: any) {
      console.error('Failed to load monitor history results:', err);
      setHistoryResults(null);
    } finally {
      setResultsLoading(false);
    }
  }, [id, loadResults, monitor?.type]);

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
    if (activeTab !== 'overview') {
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

  useEffect(() => {
    if (activeTab !== 'history') {
      return;
    }
    void loadHistoryResults();
  }, [activeTab, loadHistoryResults]);

  const handleSubmit = async (data: UpdateMonitorRequest) => {
    try {
      setSaving(true);
      const updated = await updateMonitor(id, data);
      setMonitor(updated);
      showToast('Monitor updated successfully', 'success');
      loadResults();
    } catch (err: any) {
      showToast(err.message || 'Failed to update monitor', 'error');
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
    } catch (err: any) {
      showToast(err.message || 'Failed to delete', 'error');
      setDeleting(false);
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
      await Promise.all([
        loadMonitor(),
        loadResults(),
        loadAnalytics(),
        activeTab === 'history' ? loadHistoryResults() : Promise.resolve(),
      ]);
      showToast(monitor.type === 'group'
          ? 'Group history cleared for all member monitors'
          : 'Monitor history cleared', 'success');
      setIsHistoryResetModalOpen(false);
      setHistoryResetConfirmation('');
    } catch (err: any) {
      showToast(err.message || 'Failed to clear monitor history', 'error');
    } finally {
      setClearingHistory(false);
    }
  };

  // Get URL from monitor config
  const getUrl = () => {
    if (!monitor) return null;
    if (monitor.config && 'url' in monitor.config) return monitor.config.url;
    if (monitor.url) return monitor.url;
    if ((monitor.type === 'tcp' || monitor.type === 'grpc') && monitor.config && 'host' in monitor.config) {
      const cfg = monitor.config as { host?: string; port?: number };
      if (cfg.host) return cfg.port ? `${cfg.host}:${cfg.port}` : cfg.host;
    }
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
      <div className="flex flex-col items-center justify-center h-64 gap-3 text-center">
        <p className="text-rose-400">{error || 'Monitor not found'}</p>
        <Button variant="ghost" size="sm" onClick={() => router.push('/monitors')}>
          ← Back to monitors
        </Button>
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
      : activeTab === 'history'
        ? NON_AGENT_HISTORY_WINDOW_MS
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
      <PageHeader
        breadcrumb={[{ label: 'Monitors', href: '/monitors' }, { label: monitor.name }]}
        title={
          <span className="flex items-center gap-3">
            <span>{monitor.name}</span>
            <StatusBadge status={status} />
          </span>
        }
        subtitle={
          <span>
            <span className="uppercase">{monitor.type}</span>
            {getUrl() && <span> · {getUrl()}</span>}
          </span>
        }
        action={
          <Button
            variant="danger"
            size="sm"
            icon={<Trash2 strokeWidth={1.75} />}
            onClick={() => setConfirmDelete(true)}
          >
            Delete
          </Button>
        }
      />

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

      {monitor.in_maintenance && (
        <div className="rounded-lg border border-sky-500/30 bg-sky-500/10 px-4 py-3">
          <p className="text-sm text-sky-300">
            Under maintenance
            {monitor.maintenance_until
              ? ` until ${new Date(monitor.maintenance_until).toLocaleString()}`
              : ''}{' '}
            — alerts are suppressed; checks keep running.
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
              results={monitor.type === 'agent' ? (results?.results || []) : (historyResults?.results || [])}
              loading={resultsLoading}
            />
          )}
          {activeTab === 'settings' && (
            <FormCard>
              <MonitorForm
                monitor={monitor}
                onSubmit={handleSubmit}
                onCancel={() => router.push('/monitors')}
                loading={saving}
              />
            </FormCard>
          )}
          {activeTab === 'json' && (
            <MonitorDetailJson monitor={monitor} />
          )}
        </div>

        {/* Sidebar - Quick Stats (visible on overview) */}
        {activeTab === 'overview' && (
          <div className="space-y-4">
            <FormCard className="p-4">
              <h3 className="mb-3 text-xs font-medium uppercase tracking-wider text-slate-500">Quick actions</h3>
              <div className="space-y-2">
                <Button
                  variant="ghost"
                  size="sm"
                  icon={<SettingsIcon strokeWidth={1.75} />}
                  className="w-full justify-start"
                  onClick={() => setActiveTab('settings')}
                >
                  Edit settings
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  icon={<Clock strokeWidth={1.75} />}
                  className="w-full justify-start"
                  onClick={() => setActiveTab('history')}
                >
                  View history
                </Button>
                <Button
                  variant="danger"
                  size="sm"
                  icon={<Trash2 strokeWidth={1.75} />}
                  className="w-full justify-start"
                  onClick={openHistoryResetModal}
                >
                  {monitor.type === 'group' ? 'Clear group history' : 'Clear history'}
                </Button>
              </div>
            </FormCard>

            <FormCard className="p-4">
              <h3 className="mb-3 text-xs font-medium uppercase tracking-wider text-slate-500">Configuration</h3>
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
                {(() => {
                  // Server facts reported by the database/broker checkers
                  // (version, role, …) live in the latest result's metrics.
                  if (!isDatabaseMonitorType(monitor.type)) return null;
                  const latest = (results?.results || []).find((r) => {
                    const md = r.metrics_data as DBMetricsEnvelope | undefined;
                    return Boolean(md && md[monitor.type as keyof DBMetricsEnvelope]);
                  });
                  const dbMetrics = latest
                    ? (latest.metrics_data as DBMetricsEnvelope)[monitor.type as keyof DBMetricsEnvelope]
                    : null;
                  if (!dbMetrics) return null;
                  const server = [
                    [dbMetrics.product, dbMetrics.server_version].filter(Boolean).join(' '),
                    dbMetrics.role,
                    dbMetrics.replica_set ? `set ${dbMetrics.replica_set}` : '',
                  ]
                    .filter(Boolean)
                    .join(' · ');
                  return (
                    <>
                      {server && (
                        <div className="flex justify-between">
                          <span className="text-slate-500">Server</span>
                          <span className="truncate text-right text-slate-300">{server}</span>
                        </div>
                      )}
                      {dbMetrics.latency_warn_ms ? (
                        <div className="flex justify-between">
                          <span className="text-amber-400">Warning</span>
                          <span className="text-right text-amber-300">
                            latency over {dbMetrics.latency_warn_ms}ms threshold
                          </span>
                        </div>
                      ) : null}
                    </>
                  );
                })()}
                {monitor.type === 'tcp' && (() => {
                  const cfg = monitor.config as TCPMonitorConfig | undefined;
                  const latest = (results?.results || []).find((r) => {
                    const md = r.metrics_data as TCPMetricsEnvelope | undefined;
                    return Boolean(md && md.tcp);
                  });
                  const tcpMetrics = latest ? (latest.metrics_data as TCPMetricsEnvelope).tcp : null;
                  const target = cfg?.host ? (cfg.port ? `${cfg.host}:${cfg.port}` : cfg.host) : null;
                  return (
                    <>
                      {target && (
                        <div className="flex justify-between">
                          <span className="text-slate-500">Target</span>
                          <span className="truncate text-right text-slate-300">{target}</span>
                        </div>
                      )}
                      {cfg?.use_tls && (
                        <div className="flex justify-between">
                          <span className="text-slate-500">TLS</span>
                          <span className="text-right text-slate-300">
                            {tcpMetrics?.tls_version || 'Enabled'}
                          </span>
                        </div>
                      )}
                    </>
                  );
                })()}
              </div>
            </FormCard>

            <MonitorDependenciesCard monitorId={monitor.id} monitorType={monitor.type} />

            {monitor.type === 'synthetic_browser' && (
              <FormCard className="p-4">
                <h3 className="mb-3 text-xs font-medium uppercase tracking-wider text-slate-500">Latest failure screenshot</h3>
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
              </FormCard>
            )}
          </div>
        )}
      </div>

      <ConfirmDialog
        open={confirmDelete}
        title={monitor?.type === 'group' ? 'Delete group' : 'Delete monitor'}
        description={
          monitor
            ? `“${monitor.name}” will be removed along with all of its check history. This cannot be undone.`
            : 'This monitor will be removed along with all of its check history. This cannot be undone.'
        }
        confirmLabel="Delete"
        loading={deleting}
        onConfirm={handleDelete}
        onCancel={() => !deleting && setConfirmDelete(false)}
      />

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

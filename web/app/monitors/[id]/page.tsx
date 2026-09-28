'use client';

import PrometheusDetails from '@/components/monitors/PrometheusDetails';
import { useRouter, useParams } from 'next/navigation';
import { useState, useEffect, useCallback, useRef, ReactNode } from 'react';
import Image from 'next/image';
import { AlertTriangle, Trash2 } from 'lucide-react';
import { Monitor, UpdateMonitorRequest, MonitorResultsResponse, CheckResult, MonitorAnalyticsResponse, MonitorAnalyticsRange, DBMetricsEnvelope, MongoDBMetrics, TCPMonitorConfig, TCPMetricsEnvelope } from '@/lib/types';
import { getMonitor, updateMonitor, getMonitorResults, getMonitorAnalytics, deleteMonitor, deleteMonitorHistory, getSyntheticBrowserScreenshotUrl, getTenantSettings } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import MonitorForm from '@/components/monitors/MonitorForm';
import MonitorDetailOverview from '@/components/monitors/MonitorDetailOverview';
import { AlertRoutingNotice } from '@/components/monitors/AlertRoutingBadge';
import MonitorDetailHistory from '@/components/monitors/MonitorDetailHistory';
import MonitorDetailJson from '@/components/monitors/MonitorDetailJson';
import { GroupMembersCard } from '@/components/monitors/GroupMembersCard';
import { MonitorDependenciesCard } from '@/components/monitors/MonitorDependenciesCard';
import { MonitorLocationStrip } from '@/components/monitors/MonitorLocationStrip';
import PageHeader from '@/components/ui/PageHeader';
import Panel from '@/components/ui/Panel';
import Pill, { PillTone } from '@/components/ui/Pill';
import Tabs from '@/components/ui/Tabs';
import Callout from '@/components/ui/Callout';
import Input from '@/components/ui/Input';
import Button from '@/components/ui/Button';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import ModalPortal from '@/components/ui/ModalPortal';
import CopyableTarget from '@/components/ui/CopyableTarget';
import { useToast } from '@/components/ui/ToastProvider';
import { getEffectiveMonitorStatus, monitorTargetLabel, MonitorDisplayStatus } from '@/lib/monitor-utils';
import { RANGE_MS as AGENT_RANGE_MS, type TimeRange as AgentTimeRange } from '@/components/monitors/metric-chart';
import { formatBytes } from '@/lib/metrics';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';

type TabType = 'overview' | 'history' | 'settings' | 'json';

const isDatabaseMonitorType = (t: string): boolean =>
  t === 'redis' || t === 'postgres' || t === 'mongodb' || t === 'rabbitmq' || t === 'mysql';
type OverviewTimeRange = MonitorAnalyticsRange;

const OVERVIEW_RANGE_MS: Record<OverviewTimeRange, number> = {
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
  '7d': 7 * 24 * 60 * 60 * 1000,
  '30d': 30 * 24 * 60 * 60 * 1000,
  '90d': 90 * 24 * 60 * 60 * 1000,
  '365d': 365 * 24 * 60 * 60 * 1000,
};

const HISTORY_WINDOW_MS = 7 * 24 * 60 * 60 * 1000;
const OVERVIEW_RESULTS_LIMIT = 50;

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

const STATUS_PILL: Record<MonitorDisplayStatus, { label: string; tone: PillTone }> = {
  up: { label: 'Operational', tone: 'success' },
  down: { label: 'Down', tone: 'danger' },
  degraded: { label: 'Degraded', tone: 'warning' },
  paused: { label: 'Paused', tone: 'neutral' },
  maintenance: { label: 'Maintenance', tone: 'info' },
  unknown: { label: 'Unknown', tone: 'neutral' },
};

function StatusBadge({ status }: { status: MonitorDisplayStatus }) {
  const { label, tone } = STATUS_PILL[status];
  return (
    <Pill tone={tone} dot>
      {label}
    </Pill>
  );
}

// Label/value line in the overview sidebar. Warnings and notes wrap instead
// of truncating so the whole message stays readable.
function DetailRow({
  label,
  tone = 'default',
  children,
}: {
  label: string;
  tone?: 'default' | 'warning' | 'note';
  children: ReactNode;
}) {
  const labelClass = tone === 'warning' ? 'text-amber-300' : 'text-slate-400';
  const valueClass =
    tone === 'warning'
      ? 'text-right text-amber-300'
      : tone === 'note'
        ? 'text-right text-slate-400'
        : 'min-w-0 truncate text-right text-slate-200';
  return (
    <div className="flex justify-between gap-3">
      <span className={`shrink-0 ${labelClass}`}>{label}</span>
      <span className={valueClass}>{children}</span>
    </div>
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
    <ModalPortal>
      <div className="glass-scrim fixed inset-0 z-50 flex items-center justify-center p-4">
        <div className="glass-overlay w-full max-w-lg">
          <div className="flex items-start gap-4 px-6 pt-5">
            <div className="mt-0.5 flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-full bg-rose-500/[0.13] text-rose-400">
              <AlertTriangle className="h-[18px] w-[18px]" strokeWidth={1.75} aria-hidden="true" />
            </div>
            <div className="min-w-0">
              <h2 className="text-base font-semibold text-white">Clear monitor history</h2>
              <p className="mt-1.5 text-sm leading-relaxed text-slate-400">
                {isGroup
                  ? `This will permanently remove checks, alerts, and SLA history for ${monitorName} and every monitor inside that group.`
                  : `This will permanently remove checks, alerts, and SLA history for ${monitorName}, but keep the monitor itself.`}
              </p>
            </div>
          </div>

          <div className="space-y-4 px-6 py-4">
            <Callout tone="danger" title="This cannot be undone.">
              Dashboard failures, monitor analytics, and public status page history will rebuild only from future checks.
            </Callout>

            <Input
              label={`Type ${expectedText} to confirm`}
              type="text"
              value={confirmationText}
              onChange={(event) => onConfirmationTextChange(event.target.value)}
              placeholder={expectedText}
              autoFocus
              disabled={loading}
            />
          </div>

          <div className="flex items-center justify-end gap-2 px-6 pb-5">
            <Button type="button" variant="ghost" onClick={onClose} disabled={loading}>
              Cancel
            </Button>
            <Button type="button" variant="danger" onClick={onConfirm} disabled={!canConfirm} loading={loading}>
              {loading ? 'Clearing…' : isGroup ? 'Clear group history' : 'Clear history'}
            </Button>
          </div>
        </div>
      </div>
    </ModalPortal>
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

      if (opts?.silent && currentResults?.results?.length) {
        const mergedResults = mergeAndSortResults(data.results, currentResults.results)
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
          results: data.results.slice(0, OVERVIEW_RESULTS_LIMIT),
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
  }, [id]);

  const loadHistoryResults = useCallback(async () => {
    try {
      setResultsLoading(true);
      const cutoffISO = new Date(Date.now() - HISTORY_WINDOW_MS).toISOString();
      const data = await getMonitorResults(id, { since: cutoffISO });
      setHistoryResults(data);
    } catch (err: any) {
      console.error('Failed to load monitor history results:', err);
      setHistoryResults(null);
    } finally {
      setResultsLoading(false);
    }
  }, [id]);

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

  // Get URL from monitor config — mirrors the monitor list so synthetic and
  // gRPC monitors expose the same copyable target on both screens.
  const getUrl = () => {
    if (!monitor) return null;
    if (monitor.config && 'url' in monitor.config) return monitor.config.url;
    if (monitor.config && 'base_url' in monitor.config) return monitor.config.base_url || null;
    if (monitor.config && 'start_url' in monitor.config) return monitor.config.start_url;
    if (monitor.type === 'grpc' && monitor.config && 'host' in monitor.config) {
      const cfg = monitor.config as { host?: string; port?: number; use_tls?: boolean };
      if (cfg.host) {
        const port = cfg.port || (cfg.use_tls === false ? 80 : 443);
        return `${cfg.host}:${port}`;
      }
    }
    if (monitor.type === 'tcp' && monitor.config && 'host' in monitor.config) {
      const cfg = monitor.config as { host?: string; port?: number };
      if (cfg.host) return cfg.port ? `${cfg.host}:${cfg.port}` : cfg.host;
    }
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
      <div className="flex h-64 items-center justify-center text-sm text-slate-500">
        Loading monitor...
      </div>
    );
  }

  if (error || !monitor) {
    return (
      <div className="flex h-64 flex-col items-center justify-center gap-3 text-center">
        <p className="text-sm text-rose-300">{error || 'Monitor not found'}</p>
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
    activeTab === 'history'
      ? HISTORY_WINDOW_MS
      : monitor.type === 'agent'
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
      <PageHeader
        breadcrumb={[{ label: 'Monitors', href: '/monitors' }, { label: monitor.name }]}
        title={
          <span className="flex items-center gap-3">
            <span>{monitor.name}</span>
            <StatusBadge status={status} />
          </span>
        }
        subtitle={
          <span className="inline-flex max-w-full flex-wrap items-center gap-x-2">
            <span>{monitorTypeLabel(monitor.type)}</span>
            {getUrl() && (
              <>
                <span aria-hidden="true">·</span>
                <CopyableTarget
                  value={getUrl() as string}
                  label={monitorTargetLabel(monitor.type)}
                  size="sm"
                  textClassName="text-slate-400"
                />
              </>
            )}
          </span>
        }
        action={
          <>
            <Button variant="ghost" size="sm" onClick={openHistoryResetModal}>
              {monitor.type === 'group' ? 'Clear group history' : 'Clear history'}
            </Button>
            <Button
              variant="danger"
              size="sm"
              icon={<Trash2 className="h-4 w-4" strokeWidth={1.75} />}
              onClick={() => setConfirmDelete(true)}
            >
              Delete
            </Button>
          </>
        }
      />

      <AlertRoutingNotice routing={monitor.alert_routing} enabled={monitor.enabled} />

      <Tabs tabs={tabs} active={activeTab} onChange={setActiveTab} />

      {monitor.locations && monitor.locations.length > 0 && (
        <MonitorLocationStrip
          locations={monitor.locations}
          quorum={monitor.location_quorum ?? 1}
        />
      )}

      {showRetentionWarning && (
        <Callout tone="warning">
          Data retention is set to {boundedRetentionDays} day{boundedRetentionDays === 1 ? '' : 's'}.
          Older history is deleted, so this view may be partial.
        </Callout>
      )}

      {monitor.in_maintenance && (
        <Callout tone="info">
          Under maintenance
          {monitor.maintenance_until
            ? ` until ${new Date(monitor.maintenance_until).toLocaleString()}`
            : ''}{' '}
          — alerts are suppressed; checks keep running.
        </Callout>
      )}

      <div
        className={
          activeTab === 'overview'
            ? 'grid grid-cols-1 gap-6 min-[1360px]:grid-cols-[minmax(0,1fr)_360px]'
            : undefined
        }
      >
        <div className="min-w-0">
          {activeTab === 'overview' && (
            <MonitorDetailOverview
              monitor={monitor}
              results={results?.results || []}
              analytics={analytics}
              loading={resultsLoading || analyticsLoading}
              agentTimeRange={agentTimeRange}
              onAgentTimeRangeChange={setAgentTimeRange}
              timeRange={overviewRange}
              onTimeRangeChange={(range) => {
                setOverviewRange(range);
                void loadAnalytics(range);
              }}
            />
          )}
          {activeTab === 'history' && (
            <MonitorDetailHistory
              results={historyResults?.results || []}
              loading={resultsLoading}
            />
          )}
          {activeTab === 'settings' && (
            <MonitorForm
              monitor={monitor}
              onSubmit={handleSubmit}
              onCancel={() => router.push('/monitors')}
              loading={saving}
            />
          )}
          {activeTab === 'json' && (
            <MonitorDetailJson monitor={monitor} />
          )}
        </div>

        {/* Sidebar (overview only) */}
        {activeTab === 'overview' && (
          <div className="grid grid-cols-1 content-start items-start gap-4 md:grid-cols-2 min-[1360px]:grid-cols-1">
            {monitor.type === 'group' && <GroupMembersCard groupId={monitor.id} />}

            <Panel title="Configuration">
              <div className="space-y-2.5 text-sm">
                <DetailRow label="Interval">{monitor.interval_seconds}s</DetailRow>
                {monitor.type !== 'group' && monitor.timeout_seconds > 0 && (
                  <DetailRow label="Timeout">{monitor.timeout_seconds}s</DetailRow>
                )}
                {monitor.type === 'prometheus' && (
                  <div className="border-t border-divider pt-3">
                    <PrometheusDetails monitor={monitor} result={results?.results?.[0]} />
                  </div>
                )}
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
                  const mongo = monitor.type === 'mongodb' ? (dbMetrics as MongoDBMetrics) : null;
                  const repl = mongo?.replication;
                  const mongoRows = mongo
                    ? [
                        mongo.connected_clients != null && mongo.connections_available != null
                          ? { label: 'Connections', value: `${mongo.connected_clients} current · ${mongo.connections_available} available` }
                          : null,
                        mongo.used_memory_bytes != null || mongo.mem_virtual_bytes != null
                          ? {
                              label: 'Memory',
                              value: [
                                mongo.used_memory_bytes != null ? `${formatBytes(mongo.used_memory_bytes)} resident` : '',
                                mongo.mem_virtual_bytes != null ? `${formatBytes(mongo.mem_virtual_bytes)} virtual` : '',
                              ].filter(Boolean).join(' · '),
                            }
                          : null,
                        mongo.cache_used_bytes != null && mongo.cache_max_bytes != null
                          ? {
                              label: 'Cache',
                              value: `${formatBytes(mongo.cache_used_bytes)} / ${formatBytes(mongo.cache_max_bytes)}${
                                mongo.cache_dirty_bytes != null ? ` · ${formatBytes(mongo.cache_dirty_bytes)} dirty` : ''
                              }`,
                            }
                          : null,
                        mongo.network_bytes_in != null && mongo.network_bytes_out != null
                          ? { label: 'Network', value: `${formatBytes(mongo.network_bytes_in)} in · ${formatBytes(mongo.network_bytes_out)} out` }
                          : null,
                        repl
                          ? {
                              label: 'Replication',
                              value: `${repl.members_healthy}/${repl.members_total} healthy${
                                repl.max_lag_seconds != null ? ` · lag ${repl.max_lag_seconds.toFixed(1)}s` : ''
                              }${repl.primary ? ` · primary ${repl.primary}` : ''}`,
                            }
                          : null,
                      ].filter((row): row is { label: string; value: string } => row !== null)
                    : [];
                  const mongoWarnings = mongo
                    ? [
                        mongo.replication_lag_warn_seconds != null
                          ? `replication lag over ${mongo.replication_lag_warn_seconds}s threshold`
                          : null,
                        repl && !repl.primary ? 'replica set has no primary' : null,
                        repl && repl.members_healthy < repl.members_total ? 'unhealthy replica set member(s)' : null,
                      ].filter((w): w is string => w !== null)
                    : [];
                  const mongoHints = (mongo?.unavailable ?? []).map((u) =>
                    u.reason === 'unauthorized'
                      ? 'Cluster checks skipped — grant clusterMonitor'
                      : u.reason === 'not_replica_set'
                        ? 'Replication: not a replica set'
                        : 'Cluster checks temporarily unavailable'
                  );
                  return (
                    <>
                      {server && <DetailRow label="Server">{server}</DetailRow>}
                      {mongoRows.map((row) => (
                        <DetailRow key={row.label} label={row.label}>
                          {row.value}
                        </DetailRow>
                      ))}
                      {dbMetrics.latency_warn_ms ? (
                        <DetailRow label="Warning" tone="warning">
                          latency over {dbMetrics.latency_warn_ms}ms threshold
                        </DetailRow>
                      ) : null}
                      {mongoWarnings.map((warning) => (
                        <DetailRow key={warning} label="Warning" tone="warning">
                          {warning}
                        </DetailRow>
                      ))}
                      {mongoHints.map((hint) => (
                        <DetailRow key={hint} label="Note" tone="note">
                          {hint}
                        </DetailRow>
                      ))}
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
                      {target && <DetailRow label="Target">{target}</DetailRow>}
                      {cfg?.use_tls && (
                        <DetailRow label="TLS">{tcpMetrics?.tls_version || 'Enabled'}</DetailRow>
                      )}
                    </>
                  );
                })()}
              </div>
            </Panel>

            <MonitorDependenciesCard monitorId={monitor.id} monitorType={monitor.type} />

            {monitor.type === 'synthetic_browser' && (
              <Panel title="Latest failure screenshot">
                {screenshotLoading ? (
                  <p className="text-[13px] text-slate-400">Loading screenshot...</p>
                ) : screenshotBlobURL ? (
                  <>
                    <div className="glass-well overflow-hidden p-2">
                      <Image
                        src={screenshotBlobURL}
                        alt="Latest synthetic browser failure screenshot"
                        width={1200}
                        height={675}
                        className="h-auto max-h-[220px] w-full rounded-lg object-contain"
                        unoptimized
                        loading="lazy"
                      />
                    </div>
                    <p className="mt-2 text-xs text-slate-500">
                      {latestSyntheticBrowserResult
                        ? `Captured ${new Date(latestSyntheticBrowserResult.created_at).toLocaleString()}`
                        : 'Latest run'}
                    </p>
                  </>
                ) : (
                  <p className="text-[13px] text-slate-400">
                    No screenshot available yet. A failed run with screenshot capture enabled is required.
                  </p>
                )}
              </Panel>
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

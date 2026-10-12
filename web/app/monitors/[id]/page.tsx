'use client';

import PrometheusDetails from '@/components/monitors/PrometheusDetails';
import VaultDetails from '@/components/monitors/VaultDetails';
import { useRouter, useParams } from 'next/navigation';
import { useState, useEffect, useCallback, useRef, ReactNode } from 'react';
import Image from 'next/image';
import { AlertTriangle, Eraser, Pause, Pencil, Play, RefreshCw, Trash2 } from 'lucide-react';
import { Monitor, UpdateMonitorRequest, MonitorResultsResponse, CheckResult, MonitorAnalyticsResponse, MonitorAnalyticsRange, DBMetricsEnvelope, MongoDBMetrics, TCPMonitorConfig, TCPMetricsEnvelope } from '@/lib/types';
import { getMonitor, updateMonitor, getMonitorResults, getMonitorAnalytics, deleteMonitor, deleteMonitorHistory, getSyntheticBrowserScreenshotUrl, getTenantSettings, getSlas, runMonitorNow, toggleMonitorEnabled } from '@/lib/api';
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
import Pill from '@/components/ui/Pill';
import Tabs, { useUrlTab } from '@/components/ui/Tabs';
import RangePicker from '@/components/ui/RangePicker';
import RowMenu, { RowMenuItem, RowMenuSeparator } from '@/components/ui/RowMenu';
import TypeBadge from '@/components/ui/TypeBadge';
import Callout from '@/components/ui/Callout';
import Input from '@/components/ui/Input';
import Button from '@/components/ui/Button';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import ModalPortal from '@/components/ui/ModalPortal';
import CopyableTarget from '@/components/ui/CopyableTarget';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { getEffectiveMonitorStatus, monitorTarget, monitorTargetLabel, shortFailureReason } from '@/lib/monitor-utils';
import { checkResultLabel, MONITOR_STATUS_LABEL, MONITOR_STATUS_TONE } from '@/lib/status-tones';
import { formatDateTime, formatDateTimeFull, formatDuration, formatRelativeTime, RANGES } from '@/lib/format';
import { strictestTarget, type Sla } from '@/lib/sla';
import { RANGE_MS as AGENT_RANGE_MS, type TimeRange as AgentTimeRange } from '@/components/monitors/metric-chart';
import { formatBytes } from '@/lib/metrics';

const TAB_IDS = ['overview', 'history', 'settings', 'json'] as const;

const isDatabaseMonitorType = (t: string): boolean =>
  t === 'redis' || t === 'postgres' || t === 'mongodb' || t === 'rabbitmq' || t === 'mysql';
type OverviewTimeRange = MonitorAnalyticsRange;

const OVERVIEW_RANGES: readonly OverviewTimeRange[] = ['1h', '6h', '24h', '7d', '30d', '90d', '365d'];
const AGENT_RANGES: readonly AgentTimeRange[] = ['1h', '6h', '24h', '7d'];

// Monitors that never execute a check of their own: a group rolls up its
// members (the API refuses run-now for it), agent and push monitors are
// pushed to. Run check and the timeout fact are hidden for them.
const PASSIVE_TYPES = new Set(['group', 'agent', 'push']);

const HISTORY_WINDOW_MS = 7 * 24 * 60 * 60 * 1000;
const OVERVIEW_RESULTS_LIMIT = 50;
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

// Type-specific facts for the sidebar's Server panel: what the database or
// broker reported about itself, the TCP TLS version. Interval and timeout
// live in the page header. Empty when the type has nothing to add, so the
// panel is not rendered at all.
function serverFactRows(monitor: Monitor, results: CheckResult[]): ReactNode[] {
  const rows: ReactNode[] = [];
  if (isDatabaseMonitorType(monitor.type)) {
    const latest = results.find((r) => {
      const md = r.metrics_data as DBMetricsEnvelope | undefined;
      return Boolean(md && md[monitor.type as keyof DBMetricsEnvelope]);
    });
    const dbMetrics = latest
      ? (latest.metrics_data as DBMetricsEnvelope)[monitor.type as keyof DBMetricsEnvelope]
      : null;
    if (dbMetrics) {
      const server = [
        [dbMetrics.product, dbMetrics.server_version].filter(Boolean).join(' '),
        dbMetrics.role,
        dbMetrics.replica_set ? `set ${dbMetrics.replica_set}` : '',
      ]
        .filter(Boolean)
        .join(', ');
      const mongo = monitor.type === 'mongodb' ? (dbMetrics as MongoDBMetrics) : null;
      const repl = mongo?.replication;
      const mongoRows = mongo
        ? [
            mongo.connected_clients != null && mongo.connections_available != null
              ? { label: 'Connections', value: `${mongo.connected_clients} current, ${mongo.connections_available} available` }
              : null,
            mongo.used_memory_bytes != null || mongo.mem_virtual_bytes != null
              ? {
                  label: 'Memory',
                  value: [
                    mongo.used_memory_bytes != null ? `${formatBytes(mongo.used_memory_bytes)} resident` : '',
                    mongo.mem_virtual_bytes != null ? `${formatBytes(mongo.mem_virtual_bytes)} virtual` : '',
                  ].filter(Boolean).join(', '),
                }
              : null,
            mongo.cache_used_bytes != null && mongo.cache_max_bytes != null
              ? {
                  label: 'Cache',
                  value: `${formatBytes(mongo.cache_used_bytes)} / ${formatBytes(mongo.cache_max_bytes)}${
                    mongo.cache_dirty_bytes != null ? `, ${formatBytes(mongo.cache_dirty_bytes)} dirty` : ''
                  }`,
                }
              : null,
            mongo.network_bytes_in != null && mongo.network_bytes_out != null
              ? { label: 'Network', value: `${formatBytes(mongo.network_bytes_in)} in, ${formatBytes(mongo.network_bytes_out)} out` }
              : null,
            repl
              ? {
                  label: 'Replication',
                  value: `${repl.members_healthy}/${repl.members_total} healthy${
                    repl.max_lag_seconds != null ? `, lag ${repl.max_lag_seconds.toFixed(1)}s` : ''
                  }${repl.primary ? `, primary ${repl.primary}` : ''}`,
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
          ? 'Cluster checks skipped: grant clusterMonitor'
          : u.reason === 'not_replica_set'
            ? 'Replication: not a replica set'
            : 'Cluster checks temporarily unavailable'
      );
      // "Version", not "Server": the panel around it is already titled Server.
      if (server) rows.push(<DetailRow key="server" label="Version">{server}</DetailRow>);
      for (const row of mongoRows) {
        rows.push(
          <DetailRow key={row.label} label={row.label}>
            {row.value}
          </DetailRow>
        );
      }
      if (dbMetrics.latency_warn_ms) {
        rows.push(
          <DetailRow key="latency-warn" label="Warning" tone="warning">
            latency over {dbMetrics.latency_warn_ms}ms threshold
          </DetailRow>
        );
      }
      for (const warning of mongoWarnings) {
        rows.push(
          <DetailRow key={warning} label="Warning" tone="warning">
            {warning}
          </DetailRow>
        );
      }
      for (const hint of mongoHints) {
        rows.push(
          <DetailRow key={hint} label="Note" tone="note">
            {hint}
          </DetailRow>
        );
      }
    }
  }
  if (monitor.type === 'tcp') {
    const cfg = monitor.config as TCPMonitorConfig | undefined;
    const latest = results.find((r) => {
      const md = r.metrics_data as TCPMetricsEnvelope | undefined;
      return Boolean(md && md.tcp);
    });
    const tcpMetrics = latest ? (latest.metrics_data as TCPMetricsEnvelope).tcp : null;
    if (cfg?.use_tls) {
      rows.push(<DetailRow key="tls" label="TLS">{tcpMetrics?.tls_version || 'Enabled'}</DetailRow>);
    }
  }
  return rows;
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
  const canWrite = useCanWrite();

  const [monitor, setMonitor] = useState<Monitor | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>('');
  const { showToast } = useToast();
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
  const [activeTab, setActiveTab] = useUrlTab(TAB_IDS);
  const [agentTimeRange, setAgentTimeRange] = useState<AgentTimeRange>('24h');
  const [overviewRange, setOverviewRange] = useState<OverviewTimeRange>('24h');
  const [screenshotBlobURL, setScreenshotBlobURL] = useState<string | null>(null);
  const [screenshotLoading, setScreenshotLoading] = useState(false);
  const [runningCheck, setRunningCheck] = useState(false);
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

  // Queues an on-demand run (all of the monitor's locations), then waits for
  // its first result so the toast can say how it went.
  const handleRunCheck = async () => {
    if (!monitor || runningCheck) return;
    setRunningCheck(true);
    try {
      const run = await runMonitorNow(monitor.id);
      showToast('Check queued', 'info', 3000);
      const queuedMs = Date.parse(run.queued_at) || Date.now();
      const sinceISO = new Date(queuedMs - 1500).toISOString();
      const deadline = Date.now() + RUN_RESULT_WAIT_MS;
      while (mountedRef.current && Date.now() < deadline) {
        await new Promise((resolve) => window.setTimeout(resolve, 2000));
        if (!mountedRef.current) return;
        const data = await getMonitorResults(monitor.id, { limit: 10, since: sinceISO });
        const result = (data.results || []).find((r) => Date.parse(r.created_at) >= queuedMs - 1500);
        if (result) {
          await loadResults({ silent: true });
          void loadAnalytics();
          const reason = shortFailureReason(result.error_message, {
            httpStatus: result.http_status,
            timeoutSeconds: monitor.timeout_seconds,
          });
          showToast(
            `Check ${checkResultLabel(result.status).toLowerCase()}${reason ? `: ${reason}` : ''}`,
            result.status === 'success' ? 'success' : result.status === 'degraded' ? 'warning' : 'error'
          );
          return;
        }
      }
      if (mountedRef.current) {
        showToast('No result yet. It will appear under Latest checks when the worker reports.', 'info');
      }
    } catch (err: any) {
      showToast(err.message || 'Failed to run the check', 'error');
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
    } catch (err: any) {
      showToast(err.message || `Failed to ${next ? 'resume' : 'pause'} the monitor`, 'error');
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
    } catch (err: any) {
      showToast(err.message || 'Failed to clear monitor history', 'error');
    } finally {
      setClearingHistory(false);
    }
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
          Back to monitors
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
        : (RANGES[overviewRange].ms ?? 0);
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

  const isPassive = PASSIVE_TYPES.has(monitor.type);
  const target = monitorTarget(monitor);
  const lastCheckAt = results?.results?.[0]?.created_at ?? analytics?.summary?.latest_check_at;
  const serverRows = serverFactRows(monitor, results?.results || []);
  const isGroup = monitor.type === 'group';

  const aside = (
    <>
      {isGroup && <GroupMembersCard groupId={monitor.id} />}

      {(monitor.type === 'prometheus' || monitor.type === 'vault' || serverRows.length > 0) && (
        <Panel title={monitor.type === 'prometheus' ? 'Query' : monitor.type === 'vault' ? 'Vault' : 'Server'}>
          <div className="space-y-2.5 text-sm">
            {monitor.type === 'prometheus' && (
              <PrometheusDetails monitor={monitor} result={results?.results?.[0]} />
            )}
            {monitor.type === 'vault' && (
              <VaultDetails monitor={monitor} result={results?.results?.[0]} />
            )}
            {serverRows}
          </div>
        </Panel>
      )}

      <MonitorDependenciesCard monitorId={monitor.id} monitorType={monitor.type} />

      {monitor.type === 'synthetic_browser' && (
        <Panel title="Latest failure screenshot">
          {screenshotLoading ? (
            <p className="text-ui text-slate-400">Loading screenshot...</p>
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
                  ? <span title={formatDateTimeFull(latestSyntheticBrowserResult.created_at)}>Captured {formatDateTime(latestSyntheticBrowserResult.created_at)}</span>
                  : 'Latest run'}
              </p>
            </>
          ) : (
            <p className="text-ui text-slate-400">
              No screenshot available yet. A failed run with screenshot capture enabled is required.
            </p>
          )}
        </Panel>
      )}
    </>
  );

  return (
    <div className="space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Monitors', href: '/monitors' }, { label: monitor.name }]}
        title={monitor.name}
        titleAside={
          <Pill tone={MONITOR_STATUS_TONE[status]} dot>
            {MONITOR_STATUS_LABEL[status]}
          </Pill>
        }
        // One line of facts on desktop: the target truncates (full value in
        // its tooltip, copy/open stay visible) so "checked …" never wraps
        // alone. Phones put the target on its own line above the facts.
        subtitle={
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 tabular-nums sm:flex-nowrap">
            <span className="inline-flex min-w-0 items-center gap-2 max-sm:basis-full">
              <TypeBadge type={monitor.type} />
              {target && (
                <CopyableTarget
                  value={target}
                  label={monitorTargetLabel(monitor.type)}
                  size="sm"
                  className="min-w-0"
                  textClassName="text-slate-400"
                />
              )}
            </span>
            <span className="shrink-0 whitespace-nowrap">every {formatDuration(monitor.interval_seconds)}</span>
            {!isPassive && monitor.timeout_seconds > 0 && (
              <span className="shrink-0 whitespace-nowrap">timeout {formatDuration(monitor.timeout_seconds)}</span>
            )}
            {/* Agent results are scheduler-written freshness verdicts, not
                reports; the agent view shows the real last report itself. */}
            {lastCheckAt && monitor.type !== 'agent' && (
              <span className="shrink-0 whitespace-nowrap" title={formatDateTimeFull(lastCheckAt)}>
                checked {formatRelativeTime(lastCheckAt)}
              </span>
            )}
          </div>
        }
        action={
          canWrite ? (
            <>
              {!isPassive && (
                <Button
                  variant="ghost"
                  size="sm"
                  icon={<RefreshCw className={`h-4 w-4 ${runningCheck ? 'animate-spin' : ''}`} strokeWidth={1.75} />}
                  onClick={handleRunCheck}
                  disabled={runningCheck}
                  title="Run the check now on every location of this monitor"
                >
                  {runningCheck ? 'Running…' : 'Run check'}
                </Button>
              )}
              <Button
                variant="ghost"
                size="sm"
                icon={
                  monitor.enabled ? (
                    <Pause className="h-4 w-4" strokeWidth={1.75} />
                  ) : (
                    <Play className="h-4 w-4" strokeWidth={1.75} />
                  )
                }
                onClick={handleToggleEnabled}
                loading={togglingEnabled}
              >
                {monitor.enabled ? 'Pause' : 'Resume'}
              </Button>
              <RowMenu label="More actions">
                <RowMenuItem
                  icon={<Pencil className="h-3.5 w-3.5" strokeWidth={1.75} />}
                  onSelect={() => setActiveTab('settings')}
                >
                  Edit settings
                </RowMenuItem>
                <RowMenuItem
                  icon={<Eraser className="h-3.5 w-3.5" strokeWidth={1.75} />}
                  onSelect={openHistoryResetModal}
                >
                  {isGroup ? 'Clear group history…' : 'Clear history…'}
                </RowMenuItem>
                <RowMenuSeparator />
                <RowMenuItem
                  danger
                  icon={<Trash2 className="h-3.5 w-3.5" strokeWidth={1.75} />}
                  onSelect={() => setConfirmDelete(true)}
                >
                  Delete…
                </RowMenuItem>
              </RowMenu>
            </>
          ) : undefined
        }
      />

      <AlertRoutingNotice routing={monitor.alert_routing} enabled={monitor.enabled} />

      {!monitor.enabled && (
        <Callout tone="info">
          Paused. No checks run and no alerts are sent until it is resumed.
          {lastCheckAt && (
            <>
              {' '}Last check <span title={formatDateTimeFull(lastCheckAt)}>{formatDateTime(lastCheckAt)}</span>.
            </>
          )}
        </Callout>
      )}

      {monitor.in_maintenance && (
        <Callout tone="info">
          Under maintenance
          {monitor.maintenance_until ? (
            <>
              {' '}until <span title={formatDateTimeFull(monitor.maintenance_until)}>{formatDateTime(monitor.maintenance_until)}</span>
            </>
          ) : null}
          . Alerts are suppressed; checks keep running.
        </Callout>
      )}

      <Tabs
        tabs={tabs}
        active={activeTab}
        onChange={setActiveTab}
        label="Monitor sections"
        trailing={
          activeTab === 'overview' ? (
            monitor.type === 'agent' ? (
              <RangePicker ranges={AGENT_RANGES} value={agentTimeRange} onChange={setAgentTimeRange} />
            ) : (
              <RangePicker ranges={OVERVIEW_RANGES} value={overviewRange} onChange={setOverviewRange} />
            )
          ) : activeTab === 'history' ? (
            <span className="text-xs text-slate-500">{RANGES['7d'].long}</span>
          ) : undefined
        }
      />

      {monitor.locations && monitor.locations.length > 0 && (activeTab === 'overview' || activeTab === 'history') && (
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

      {activeTab === 'overview' && (
        <MonitorDetailOverview
          monitor={monitor}
          results={results?.results || []}
          analytics={analytics}
          loading={resultsLoading || analyticsLoading}
          agentTimeRange={agentTimeRange}
          timeRange={overviewRange}
          slaTarget={slaTarget}
          targetSla={targetSla}
          resultsLimit={OVERVIEW_RESULTS_LIMIT}
          aside={aside}
        />
      )}
      {activeTab === 'history' && (
        <MonitorDetailHistory
          results={historyResults?.results || []}
          analytics={historyAnalytics}
          slaTarget={slaTarget}
          timeoutSeconds={monitor.timeout_seconds}
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
        <MonitorDetailJson monitor={monitor} analytics={analytics} />
      )}

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

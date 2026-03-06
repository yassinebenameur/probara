'use client';

import { useState, useEffect, useMemo, useCallback } from 'react';
import { getDashboardOverview, getTenantSettings } from '@/lib/api';
import {
  Alert,
  DashboardFailureEvent,
  DashboardOverviewResponse,
  DashboardProblemMonitor,
} from '@/lib/types';
import Link from 'next/link';
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
} from 'recharts';

type TrendPoint = { date: string; uptime: number; responseTime: number; total: number };

const DASHBOARD_LIST_LIMIT: Record<'24h' | '7d' | '30d' | '90d' | '365d', number> = {
  '24h': 10,
  '7d': 25,
  '30d': 50,
  '90d': 50,
  '365d': 50,
};

function formatRelativeTime(dateString: string): string {
  const date = new Date(dateString);
  const now = new Date();
  const seconds = Math.floor((now.getTime() - date.getTime()) / 1000);

  if (seconds < 60) return 'just now';
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

// Stat Card Component
function StatCard({
  label,
  value,
  unit,
  change,
  changeLabel,
  positive = true,
  icon,
}: {
  label: string;
  value: string | number;
  unit?: string;
  change?: string;
  changeLabel?: string;
  positive?: boolean;
  icon: React.ReactNode;
}) {
  return (
    <div className="relative overflow-hidden rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
      <div className="flex items-start justify-between">
        <div>
          <p className="text-xs font-medium uppercase tracking-wider text-slate-500">{label}</p>
          <div className="mt-2 flex items-baseline gap-1">
            <span className="text-3xl font-semibold tracking-tight text-white">{value}</span>
            {unit && <span className="text-lg text-slate-500">{unit}</span>}
          </div>
          {change && (
            <div className="mt-2 flex items-center gap-2">
              <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${
                positive
                  ? 'bg-emerald-500/10 text-emerald-400'
                  : 'bg-rose-500/10 text-rose-400'
              }`}>
                {positive ? '↑' : '↓'} {change}
              </span>
              {changeLabel && <span className="text-xs text-slate-500">{changeLabel}</span>}
            </div>
          )}
        </div>
        <div className="rounded-lg bg-slate-800/50 p-2.5 text-slate-400">
          {icon}
        </div>
      </div>
    </div>
  );
}

function StatusPill({ status }: { status: string | null | undefined }) {
  const normalized = status || 'paused';
  const classes =
    normalized === 'success'
      ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-400'
      : normalized === 'error'
      ? 'border-amber-500/20 bg-amber-500/10 text-amber-400'
      : normalized === 'failure'
      ? 'border-rose-500/20 bg-rose-500/10 text-rose-400'
      : 'border-slate-500/20 bg-slate-500/10 text-slate-300';

  return (
    <span className={`rounded border px-2 py-0.5 text-[10px] font-medium uppercase ${classes}`}>
      {normalized}
    </span>
  );
}

function OpsSummaryMetric({
  label,
  value,
  tone,
}: {
  label: string;
  value: number;
  tone: 'rose' | 'amber' | 'cyan' | 'slate';
}) {
  const tones = {
    rose: 'border-rose-500/20 bg-rose-500/10 text-rose-300',
    amber: 'border-amber-500/20 bg-amber-500/10 text-amber-300',
    cyan: 'border-cyan-500/20 bg-cyan-500/10 text-cyan-300',
    slate: 'border-white/[0.08] bg-slate-800/50 text-slate-200',
  };

  return (
    <div className={`flex min-h-[132px] flex-col rounded-lg border p-4 ${tones[tone]}`}>
      <p className="min-h-[40px] text-[11px] font-medium uppercase tracking-[0.18em] leading-6 text-slate-500">{label}</p>
      <p className="mt-auto text-3xl font-semibold tracking-tight text-white">{value}</p>
    </div>
  );
}

function ProblemMonitorItem({ monitor }: { monitor: DashboardProblemMonitor }) {
  const issueCount = monitor.failure_count + monitor.error_count;

  return (
    <div className="flex items-start justify-between gap-3 py-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium text-white">{monitor.monitor_name}</p>
          <p className="mt-1 text-xs text-slate-500">
            {issueCount} issue{issueCount !== 1 ? 's' : ''} in range
            {monitor.latest_failure_at ? ` · latest ${formatRelativeTime(monitor.latest_failure_at)}` : ''}
          </p>
          <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
            <span className="text-slate-500">
              Failures <span className="ml-1 font-medium text-rose-300">{monitor.failure_count}</span>
            </span>
            <span className="text-slate-500">
              Errors <span className="ml-1 font-medium text-amber-300">{monitor.error_count}</span>
            </span>
            <span className="text-slate-500">
              Uptime <span className="ml-1 font-medium text-white">{monitor.uptime.toFixed(1)}%</span>
            </span>
          </div>
        </div>
      </div>
      <StatusPill status={monitor.current_status} />
    </div>
  );
}

function FailureItem({ event }: { event: DashboardFailureEvent }) {
  const isPlatformEvent = event.result_source === 'platform';
  const statusColor = isPlatformEvent ? 'bg-slate-400' : event.status === 'error' ? 'bg-amber-500' : 'bg-rose-500';
  const stateClass =
    event.state === 'resolved'
      ? 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20'
      : 'bg-rose-500/10 text-rose-400 border-rose-500/20';

  return (
    <div className="flex items-start gap-3 py-2">
      <div className={`mt-1.5 h-2 w-2 rounded-full ${statusColor}`} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between gap-2">
          <p className="truncate text-sm text-white">{event.monitor_name}</p>
          <div className="flex items-center gap-1.5">
            {isPlatformEvent && (
              <span className="rounded border border-slate-500/30 bg-slate-500/10 px-2 py-0.5 text-[10px] font-medium uppercase text-slate-300">
                Platform
              </span>
            )}
            <span className={`rounded border px-2 py-0.5 text-[10px] font-medium uppercase ${stateClass}`}>
              {event.state}
            </span>
          </div>
        </div>
        <p className="text-xs text-slate-500">
          {formatRelativeTime(event.occurred_at)} · {event.status}
          {typeof event.latency_ms === 'number' ? ` · ${event.latency_ms}ms` : ''}
        </p>
        {event.error_message && (
          <p className="mt-0.5 truncate text-xs text-slate-500" title={event.error_message}>
            {event.error_message}
          </p>
        )}
      </div>
    </div>
  );
}

function AlertItem({ alert }: { alert: Alert }) {
  const statusClass =
    alert.status === 'active'
      ? 'bg-rose-500/10 text-rose-400 border-rose-500/20'
      : alert.status === 'acknowledged'
      ? 'bg-amber-500/10 text-amber-400 border-amber-500/20'
      : 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20';

  return (
    <div className="flex items-start gap-3 py-2">
      <div className={`mt-1.5 h-2 w-2 rounded-full ${alert.status === 'resolved' ? 'bg-emerald-500' : alert.status === 'acknowledged' ? 'bg-amber-500' : 'bg-rose-500'}`} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between gap-2">
          <p className="truncate text-sm text-white">{alert.monitor_name || 'Unknown monitor'}</p>
          <span className={`rounded border px-2 py-0.5 text-[10px] font-medium uppercase ${statusClass}`}>
            {alert.status}
          </span>
        </div>
        <p className="text-xs text-slate-500">
          {formatRelativeTime(alert.triggered_at)} · {alert.failure_count} failure{alert.failure_count !== 1 ? 's' : ''}
        </p>
        {alert.last_error && (
          <p className="mt-0.5 truncate text-xs text-slate-500" title={alert.last_error}>
            {alert.last_error}
          </p>
        )}
      </div>
    </div>
  );
}

// Custom Tooltip for charts
function CustomTooltip({ active, payload, label }: any) {
  if (!active || !payload?.length) return null;

  return (
    <div className="rounded-lg border border-white/10 bg-slate-900 px-3 py-2 shadow-xl">
      <p className="text-xs text-slate-400">{label}</p>
      {payload.map((entry: any, idx: number) => (
        <p key={idx} className="text-sm font-medium text-white">
          {entry.name}: {typeof entry.value === 'number' ? entry.value.toFixed(2) : entry.value}
          {entry.name === 'uptime' ? '%' : entry.name === 'responseTime' ? 'ms' : ''}
        </p>
      ))}
    </div>
  );
}

// Loading Skeleton
function LoadingSkeleton() {
  return (
    <div className="space-y-6 animate-pulse">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {[...Array(4)].map((_, i) => (
          <div key={i} className="h-32 rounded-xl bg-slate-800/50" />
        ))}
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <div className="h-72 rounded-xl bg-slate-800/50" />
        <div className="h-72 rounded-xl bg-slate-800/50" />
      </div>
    </div>
  );
}

function EmptyChart({ message }: { message: string }) {
  return (
    <div className="flex h-48 items-center justify-center rounded-lg border border-dashed border-white/[0.08] bg-slate-900/30">
      <p className="text-sm text-slate-500">{message}</p>
    </div>
  );
}

export default function DashboardPage() {
  const [dashboard, setDashboard] = useState<DashboardOverviewResponse | null>(null);
  const [tenantRetentionDays, setTenantRetentionDays] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [timeRange, setTimeRange] = useState<'24h' | '7d' | '30d' | '90d' | '365d'>('24h');

  const loadData = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const [response, settings] = await Promise.all([
        getDashboardOverview({
          range: timeRange,
          failures_limit: DASHBOARD_LIST_LIMIT[timeRange],
          alerts_limit: DASHBOARD_LIST_LIMIT[timeRange],
        }),
        getTenantSettings().catch(() => null),
      ]);
      setDashboard(response);
      if (settings) {
        setTenantRetentionDays(settings.data_retention_days);
      }
    } catch (err) {
      console.error('Failed to load dashboard data:', err);
      setError('Failed to load dashboard data');
    } finally {
      setLoading(false);
    }
  }, [timeRange]);

  useEffect(() => {
    loadData();
  }, [loadData]);

  const trendData = useMemo<TrendPoint[]>(() => {
    return (dashboard?.trend || []).map((point) => ({
      date: point.label,
      uptime: point.uptime,
      responseTime: point.response_time,
      total: point.total_checks,
    }));
  }, [dashboard]);

  const opsSummary = dashboard?.ops_summary;
  const problemMonitors = dashboard?.problem_monitors || [];
  const recentFailures = dashboard?.recent_failures || [];
  const recentAlerts = dashboard?.recent_alerts || [];

  const hasTrendData = useMemo(() => trendData.some((d) => d.total > 0), [trendData]);

  const stats = dashboard?.stats;
  const totalMonitors = stats?.total_monitors || 0;
  const activeMonitors = stats?.active_monitors || 0;
  const httpMonitors = stats?.http_monitors || 0;
  const agentMonitors = stats?.agent_monitors || 0;
  const avgUptime = (stats?.overall_uptime || 0).toFixed(2);
  const avgResponseTime = Math.round(stats?.avg_response_ms || 0);

  const minUptime = useMemo(() => {
    if (!hasTrendData) return 95;
    return Math.min(...trendData.map((d) => (d.total > 0 ? d.uptime : 100)));
  }, [trendData, hasTrendData]);
  const uptimeDomain: [number, number] = minUptime < 95 ? [0, 100] : [95, 100];

  const selectedRangeDays = useMemo(() => {
    if (timeRange === '24h') return 1;
    if (timeRange === '7d') return 7;
    if (timeRange === '30d') return 30;
    if (timeRange === '90d') return 90;
    return 365;
  }, [timeRange]);
  const showRetentionWarning = tenantRetentionDays !== null && tenantRetentionDays > 0 && selectedRangeDays > tenantRetentionDays;

  if (loading) {
    return <LoadingSkeleton />;
  }

  return (
    <div className="space-y-6">
      {/* Error Banner */}
      {error && (
        <div className="flex items-center justify-between rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3">
          <p className="text-sm text-rose-400">{error}</p>
          <button onClick={loadData} className="btn btn-danger btn-sm">
            Retry
          </button>
        </div>
      )}

      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-white">Dashboard</h1>
          <p className="mt-1 text-sm text-slate-500">
            Overview of your monitoring infrastructure
          </p>
        </div>
        <div className="flex items-center gap-2">
          {(['24h', '7d', '30d', '90d', '365d'] as const).map((range) => (
            <button
              key={range}
              onClick={() => setTimeRange(range)}
              className={`btn-filter ${timeRange === range ? 'is-active' : ''}`}
            >
              {range}
            </button>
          ))}
        </div>
      </div>

      {showRetentionWarning && (
        <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3">
          <p className="text-sm text-amber-300">
            Data retention is set to {tenantRetentionDays} day{tenantRetentionDays === 1 ? '' : 's'}.
            Older history is deleted, so this {timeRange} view may be partial.
          </p>
        </div>
      )}

      {/* Stats Grid */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label="Overall Uptime"
          value={avgUptime}
          unit="%"
          change={undefined}
          changeLabel={undefined}
          positive={true}
          icon={
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          }
        />
        <StatCard
          label="Avg Response"
          value={avgResponseTime}
          unit="ms"
          change={undefined}
          changeLabel={undefined}
          positive={true}
          icon={
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M13 10V3L4 14h7v7l9-11h-7z" />
            </svg>
          }
        />
        <StatCard
          label="Total Monitors"
          value={totalMonitors}
          change={`${httpMonitors} HTTP, ${agentMonitors} Agent`}
          positive={true}
          icon={
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
            </svg>
          }
        />
        <StatCard
          label="Active"
          value={activeMonitors}
          change={totalMonitors > 0 ? `${((activeMonitors / totalMonitors) * 100).toFixed(0)}% enabled` : 'No monitors'}
          positive={activeMonitors === totalMonitors}
          icon={
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M5.636 18.364a9 9 0 010-12.728m12.728 0a9 9 0 010 12.728m-9.9-2.829a5 5 0 010-7.07m7.072 0a5 5 0 010 7.07M13 12a1 1 0 11-2 0 1 1 0 012 0z" />
            </svg>
          }
        />
      </div>

      {/* Charts Row */}
      <div className="grid gap-4 lg:grid-cols-2">
        {/* Uptime Trend Chart */}
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <h3 className="font-medium text-white">Uptime Trend</h3>
              <p className="text-xs text-slate-500">Monitor-weighted uptime across enabled services</p>
            </div>
            <div className="flex items-center gap-2 text-xs">
              <span className="flex items-center gap-1 text-emerald-400">
                <span className="h-2 w-2 rounded-full bg-emerald-400" />
                Uptime
              </span>
            </div>
          </div>
          {hasTrendData ? (
            <div className="h-48">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={trendData}>
                  <defs>
                    <linearGradient id="uptimeGradient" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#10b981" stopOpacity={0.3} />
                      <stop offset="100%" stopColor="#10b981" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <XAxis
                    dataKey="date"
                    axisLine={false}
                    tickLine={false}
                    tick={{ fill: '#64748b', fontSize: 11 }}
                  />
                  <YAxis
                    domain={uptimeDomain}
                    axisLine={false}
                    tickLine={false}
                    tick={{ fill: '#64748b', fontSize: 11 }}
                    tickFormatter={(v) => `${v}%`}
                  />
                  <Tooltip content={<CustomTooltip />} />
                  <Area
                    type="monotone"
                    dataKey="uptime"
                    stroke="#10b981"
                    strokeWidth={2}
                    fill="url(#uptimeGradient)"
                    name="uptime"
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          ) : (
            <EmptyChart message="No uptime data yet" />
          )}
        </div>

        {/* Response Time Chart */}
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <h3 className="font-medium text-white">Response Time</h3>
              <p className="text-xs text-slate-500">Average response time trend</p>
            </div>
            <div className="flex items-center gap-2 text-xs">
              <span className="flex items-center gap-1 text-cyan-400">
                <span className="h-2 w-2 rounded-full bg-cyan-400" />
                Latency
              </span>
            </div>
          </div>
          {hasTrendData ? (
            <div className="h-48">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={trendData}>
                  <defs>
                    <linearGradient id="latencyGradient" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#06b6d4" stopOpacity={0.3} />
                      <stop offset="100%" stopColor="#06b6d4" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <XAxis
                    dataKey="date"
                    axisLine={false}
                    tickLine={false}
                    tick={{ fill: '#64748b', fontSize: 11 }}
                  />
                  <YAxis
                    axisLine={false}
                    tickLine={false}
                    tick={{ fill: '#64748b', fontSize: 11 }}
                    tickFormatter={(v) => `${v}ms`}
                  />
                  <Tooltip content={<CustomTooltip />} />
                  <Area
                    type="monotone"
                    dataKey="responseTime"
                    stroke="#06b6d4"
                    strokeWidth={2}
                    fill="url(#latencyGradient)"
                    name="responseTime"
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          ) : (
            <EmptyChart message="No response time data yet" />
          )}
        </div>
      </div>

      {/* Action Section */}
      <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(320px,1.15fr)]">
        <div className="min-w-0 self-start overflow-hidden rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
          <div className="mb-5">
            <h3 className="font-medium text-white">Operations Summary</h3>
            <p className="text-xs text-slate-500">Current monitor and alert posture</p>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <OpsSummaryMetric label="Down monitors" value={opsSummary?.down_monitors || 0} tone="rose" />
            <OpsSummaryMetric label="Paused monitors" value={opsSummary?.paused_monitors || 0} tone="slate" />
            <OpsSummaryMetric label="Active alerts" value={opsSummary?.active_alerts || 0} tone="amber" />
            <OpsSummaryMetric
              label="Ack alerts"
              value={opsSummary?.acknowledged_alerts || 0}
              tone="cyan"
            />
          </div>
        </div>

        <div className="min-w-0 self-start rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <h3 className="font-medium text-white">Problem Monitors</h3>
              <p className="text-xs text-slate-500">Top monitors with the most issues in range</p>
            </div>
            <Link href="/monitors" className="text-xs text-cyan-400 hover:text-cyan-300">
              View all →
            </Link>
          </div>
          {problemMonitors.length > 0 ? (
            <div className="dashboard-scroll max-h-[320px] divide-y divide-white/[0.04] overflow-x-hidden overflow-y-auto pb-1 pr-2">
              {problemMonitors.map((monitor) => (
                <ProblemMonitorItem key={monitor.monitor_id} monitor={monitor} />
              ))}
            </div>
          ) : (
            <div className="flex min-h-[220px] flex-col items-center justify-center text-center">
              <p className="text-sm text-slate-500">No noisy monitors in this range</p>
              <p className="mt-1 text-xs text-slate-600">Nothing is standing out from recent checks.</p>
            </div>
          )}
        </div>

          <div className="min-w-0 self-start grid gap-4 content-start">
          <div className="min-w-0 overflow-hidden rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
            <div className="mb-4 flex items-center justify-between">
              <div>
                <h3 className="font-medium text-white">Last Failures</h3>
                <p className="text-xs text-slate-500">
                  {recentFailures.length === 0 ? 'No recent failures' : `${recentFailures.length} recent failure${recentFailures.length !== 1 ? 's' : ''}`}
                </p>
              </div>
            </div>
            {recentFailures.length > 0 ? (
              <div className="dashboard-scroll max-h-[320px] divide-y divide-white/[0.04] overflow-x-hidden overflow-y-auto pb-1 pr-2">
                {recentFailures.map((event) => (
                  <FailureItem key={event.check_result_id} event={event} />
                ))}
              </div>
            ) : (
              <div className="flex min-h-[220px] flex-col items-center justify-center text-center">
                <p className="text-sm text-slate-500">No recent failures</p>
                <p className="mt-1 text-xs text-slate-600">Failure events will appear here when checks fail</p>
              </div>
            )}
          </div>

          <div className="min-w-0 overflow-hidden rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
            <div className="mb-4 flex items-center justify-between">
              <div>
                <h3 className="font-medium text-white">Last Alerts</h3>
                <p className="text-xs text-slate-500">
                  {recentAlerts.length === 0 ? 'No recent alerts' : `${recentAlerts.length} recent alert${recentAlerts.length !== 1 ? 's' : ''}`}
                </p>
              </div>
              <Link href="/alerts" className="text-xs text-cyan-400 hover:text-cyan-300">
                View all →
              </Link>
            </div>
            {recentAlerts.length > 0 ? (
              <div className="dashboard-scroll max-h-[220px] divide-y divide-white/[0.04] overflow-x-hidden overflow-y-auto pb-1 pr-2">
                {recentAlerts.map((alert) => (
                  <AlertItem key={alert.id} alert={alert} />
                ))}
              </div>
            ) : (
              <div className="flex min-h-[160px] flex-col items-center justify-center text-center">
                <p className="text-sm text-slate-500">No recent alerts</p>
                <p className="mt-1 text-xs text-slate-600">Alerts will appear when failures trigger policies</p>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

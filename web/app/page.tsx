'use client';

import { useState, useEffect, useMemo, useCallback, useRef } from 'react';
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

type TrendDelta = {
  value: number;       // absolute delta (e.g. +0.3 or -12)
  direction: 'up' | 'down' | 'neutral';
  label: string;       // e.g. "0.3% vs prior period"
};

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

/** Compute a trend delta by comparing the second half vs first half of the series. */
function computeUptimeTrend(data: TrendPoint[]): TrendDelta | null {
  const active = data.filter((d) => d.total > 0);
  if (active.length < 4) return null;
  const mid = Math.floor(active.length / 2);
  const first = active.slice(0, mid);
  const second = active.slice(mid);
  const avg = (arr: TrendPoint[]) => arr.reduce((s, d) => s + d.uptime, 0) / arr.length;
  const delta = avg(second) - avg(first);
  return {
    value: Math.abs(delta),
    direction: delta > 0.05 ? 'up' : delta < -0.05 ? 'down' : 'neutral',
    label: `${Math.abs(delta).toFixed(2)}% vs prior period`,
  };
}

function computeResponseTrend(data: TrendPoint[]): TrendDelta | null {
  const active = data.filter((d) => d.total > 0 && d.responseTime > 0);
  if (active.length < 4) return null;
  const mid = Math.floor(active.length / 2);
  const first = active.slice(0, mid);
  const second = active.slice(mid);
  const avg = (arr: TrendPoint[]) => arr.reduce((s, d) => s + d.responseTime, 0) / arr.length;
  const delta = avg(second) - avg(first);
  // For response time, going down is good
  return {
    value: Math.abs(delta),
    direction: delta < -5 ? 'up' : delta > 5 ? 'down' : 'neutral',
    label: `${Math.abs(Math.round(delta))}ms vs prior period`,
  };
}

// ─── Info Popover ──────────────────────────────────────────────────────────────

type PopoverEntry = { label: string; value: string | number };

function InfoPopover({ entries, title }: { entries: PopoverEntry[]; title?: string }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onClickOutside(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false);
    }
    document.addEventListener('mousedown', onClickOutside);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onClickOutside);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <div className="relative inline-flex" ref={ref}>
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex h-4 w-4 items-center justify-center rounded-full text-slate-500 transition-colors hover:text-slate-300 focus:outline-none"
        aria-label="Show details"
      >
        <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
      </button>
      {open && (
        <div className="absolute bottom-full left-1/2 z-50 mb-2 w-52 -translate-x-1/2 rounded-lg border border-white/10 bg-slate-800 shadow-xl">
          {title && (
            <div className="border-b border-white/[0.06] px-3 py-2">
              <p className="text-[11px] font-semibold uppercase tracking-wider text-slate-400">{title}</p>
            </div>
          )}
          <div className="divide-y divide-white/[0.04] px-3 py-1">
            {entries.map((e, i) => (
              <div key={i} className="flex items-center justify-between py-1.5">
                <span className="text-xs text-slate-500">{e.label}</span>
                <span className="text-xs font-medium text-slate-200">{e.value}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

// ─── Stat Card ─────────────────────────────────────────────────────────────────

function StatCard({
  label,
  value,
  unit,
  trend,
  higherIsBetter = true,
  icon,
  subtitle,
  infoEntries,
}: {
  label: string;
  value: string | number;
  unit?: string;
  trend?: TrendDelta | null;
  higherIsBetter?: boolean;
  icon: React.ReactNode;
  subtitle?: string;
  infoEntries?: PopoverEntry[];
}) {
  const trendPositive =
    trend && trend.direction !== 'neutral'
      ? (trend.direction === 'up') === higherIsBetter
      : null;

  return (
    <div className="relative overflow-hidden rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
      <div className="flex items-start justify-between">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <p className="text-xs font-medium uppercase tracking-wider text-slate-500">{label}</p>
            {infoEntries && <InfoPopover entries={infoEntries} title={label} />}
          </div>
          <div className="mt-2 flex items-baseline gap-1">
            <span className="text-3xl font-semibold tracking-tight text-white">{value}</span>
            {unit && <span className="text-lg text-slate-500">{unit}</span>}
          </div>
          <div className="mt-2 h-5">
            {trend && trend.direction !== 'neutral' ? (
              <span
                className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ${trendPositive
                    ? 'bg-emerald-500/10 text-emerald-400'
                    : 'bg-rose-500/10 text-rose-400'
                  }`}
              >
                {trend.direction === 'up' ? '↑' : '↓'} {trend.label}
              </span>
            ) : subtitle ? (
              <span className="text-xs text-slate-500">{subtitle}</span>
            ) : null}
          </div>
        </div>
        <div className="ml-3 shrink-0 rounded-lg bg-slate-800/60 p-2.5 text-slate-400">
          {icon}
        </div>
      </div>
    </div>
  );
}

// ─── Fleet Status Bar ──────────────────────────────────────────────────────────

function FleetStatusBar({
  up,
  down,
  paused,
  activeAlerts,
  acknowledgedAlerts,
}: {
  up: number;
  down: number;
  paused: number;
  activeAlerts: number;
  acknowledgedAlerts: number;
}) {
  const items = [
    { label: 'Up', value: up, dot: 'bg-emerald-400', text: 'text-emerald-400' },
    { label: 'Down', value: down, dot: 'bg-rose-400', text: 'text-rose-400' },
    { label: 'Paused', value: paused, dot: 'bg-slate-500', text: 'text-slate-400' },
    { label: 'Alerts', value: activeAlerts, dot: 'bg-amber-400', text: 'text-amber-400', info: acknowledgedAlerts > 0 ? `${acknowledgedAlerts} acknowledged` : undefined },
  ];

  return (
    <div className="flex items-center gap-5 rounded-xl border border-white/[0.06] bg-slate-900/50 px-5 py-3">
      <p className="text-xs font-medium uppercase tracking-wider text-slate-500 shrink-0">Fleet</p>
      <div className="h-4 w-px bg-white/[0.06]" />
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
        {items.map((item) => (
          <div key={item.label} className="flex items-center gap-2">
            <span className={`h-2 w-2 rounded-full ${item.dot} ${item.value === 0 ? 'opacity-30' : ''}`} />
            <span className={`text-sm font-semibold ${item.value > 0 ? item.text : 'text-slate-600'}`}>
              {item.value}
            </span>
            <span className="text-xs text-slate-500">{item.label}</span>
            {item.info && item.value > 0 && (
              <InfoPopover entries={[{ label: 'Acknowledged', value: acknowledgedAlerts }]} />
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

// ─── Status Pill ───────────────────────────────────────────────────────────────

function StatusPill({ status }: { status: string | null | undefined }) {
  const normalized = status || 'paused';
  const classes =
    normalized === 'success'
      ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-400'
      : normalized === 'error'
        ? 'border-amber-500/20 bg-amber-500/10 text-amber-400'
        : normalized === 'failure'
          ? 'border-rose-500/20 bg-rose-500/10 text-rose-400'
          : 'border-slate-500/20 bg-slate-500/10 text-slate-400';

  return (
    <span className={`rounded border px-2 py-0.5 text-[10px] font-medium uppercase ${classes}`}>
      {normalized}
    </span>
  );
}

// ─── Problem Monitor Item ──────────────────────────────────────────────────────

function ProblemMonitorItem({ monitor }: { monitor: DashboardProblemMonitor }) {
  const issueCount = monitor.failure_count + monitor.error_count;
  const uptimePct = Math.max(0, Math.min(100, monitor.uptime));
  const uptimeGood = uptimePct >= 99;
  const uptimeMid = uptimePct >= 95;
  const barColor = uptimeGood
    ? 'bg-emerald-400'
    : uptimeMid
      ? 'bg-amber-400'
      : 'bg-rose-400';
  const uptimeTextColor = uptimeGood
    ? 'text-emerald-400'
    : uptimeMid
      ? 'text-amber-400'
      : 'text-rose-400';

  const infoEntries: PopoverEntry[] = [
    { label: 'Failures', value: monitor.failure_count },
    { label: 'Errors', value: monitor.error_count },
    { label: 'Total issues', value: issueCount },
    { label: 'Latest issue', value: monitor.latest_failure_at ? formatRelativeTime(monitor.latest_failure_at) : '—' },
    { label: 'Uptime', value: `${uptimePct.toFixed(2)}%` },
  ];

  return (
    <div className="py-3">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <Link
              href={`/monitors/${monitor.monitor_id}`}
              className="truncate text-sm font-medium text-white hover:text-cyan-400 transition-colors"
            >
              {monitor.monitor_name}
            </Link>
            <InfoPopover entries={infoEntries} title={monitor.monitor_name} />
          </div>
          <div className="mt-2 flex items-center gap-3">
            {/* Uptime bar */}
            <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-slate-800">
              <div
                className={`h-full rounded-full ${barColor} transition-all`}
                style={{ width: `${uptimePct}%` }}
              />
            </div>
            <span className={`shrink-0 text-xs font-medium tabular-nums ${uptimeTextColor}`}>
              {uptimePct.toFixed(1)}%
            </span>
          </div>
          <p className="mt-1 text-[11px] text-slate-500">
            {issueCount} issue{issueCount !== 1 ? 's' : ''}
            {monitor.latest_failure_at ? ` · last ${formatRelativeTime(monitor.latest_failure_at)}` : ''}
          </p>
        </div>
        <StatusPill status={monitor.current_status} />
      </div>
    </div>
  );
}

// ─── Failure Item ──────────────────────────────────────────────────────────────

function FailureItem({ event }: { event: DashboardFailureEvent }) {
  const isPlatformEvent = event.result_source === 'platform';
  const dotColor = isPlatformEvent ? 'bg-slate-500' : event.status === 'error' ? 'bg-amber-400' : 'bg-rose-400';
  const resolved = event.state === 'resolved';

  const infoEntries: PopoverEntry[] = [
    { label: 'Status', value: event.status },
    { label: 'Source', value: event.result_source },
    ...(typeof event.latency_ms === 'number' ? [{ label: 'Latency', value: `${event.latency_ms}ms` }] : []),
    ...(event.error_message ? [{ label: 'Error', value: event.error_message }] : []),
  ];

  return (
    <div className="flex items-start gap-3 py-2.5">
      <div className={`mt-1.5 h-2 w-2 shrink-0 rounded-full ${dotColor} ${resolved ? 'opacity-40' : ''}`} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-1.5">
            <p className="truncate text-sm text-white">{event.monitor_name}</p>
            <InfoPopover entries={infoEntries} title="Check Details" />
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            {isPlatformEvent && (
              <span className="rounded border border-slate-500/30 bg-slate-500/10 px-1.5 py-0.5 text-[10px] font-medium uppercase text-slate-400">
                Platform
              </span>
            )}
            <span
              className={`rounded border px-1.5 py-0.5 text-[10px] font-medium uppercase ${resolved
                  ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-400'
                  : 'border-rose-500/20 bg-rose-500/10 text-rose-400'
                }`}
            >
              {event.state}
            </span>
          </div>
        </div>
        <p className="mt-0.5 text-xs text-slate-500">
          {formatRelativeTime(event.occurred_at)}
          {typeof event.latency_ms === 'number' ? ` · ${event.latency_ms}ms` : ''}
        </p>
      </div>
    </div>
  );
}

// ─── Alert Item ────────────────────────────────────────────────────────────────

function AlertItem({ alert }: { alert: Alert }) {
  const colors = {
    active: { dot: 'bg-rose-400', badge: 'border-rose-500/20 bg-rose-500/10 text-rose-400' },
    acknowledged: { dot: 'bg-amber-400', badge: 'border-amber-500/20 bg-amber-500/10 text-amber-400' },
    resolved: { dot: 'bg-emerald-400', badge: 'border-emerald-500/20 bg-emerald-500/10 text-emerald-400' },
  };
  const c = colors[alert.status] ?? colors.resolved;

  return (
    <div className="flex items-start gap-3 py-2.5">
      <div className={`mt-1.5 h-2 w-2 shrink-0 rounded-full ${c.dot} ${alert.status === 'resolved' ? 'opacity-40' : ''}`} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between gap-2">
          <p className="truncate text-sm text-white">{alert.monitor_name || 'Unknown monitor'}</p>
          <span className={`shrink-0 rounded border px-1.5 py-0.5 text-[10px] font-medium uppercase ${c.badge}`}>
            {alert.status}
          </span>
        </div>
        <p className="mt-0.5 text-xs text-slate-500">
          {formatRelativeTime(alert.triggered_at)} · {alert.failure_count} failure{alert.failure_count !== 1 ? 's' : ''}
        </p>
        {alert.last_error && (
          <p className="mt-0.5 truncate text-xs text-slate-600" title={alert.last_error}>
            {alert.last_error}
          </p>
        )}
      </div>
    </div>
  );
}

// ─── Chart Tooltip ─────────────────────────────────────────────────────────────

function CustomTooltip({ active, payload, label }: any) {
  if (!active || !payload?.length) return null;
  return (
    <div className="rounded-lg border border-white/10 bg-slate-900 px-3 py-2 shadow-xl">
      <p className="mb-1 text-xs text-slate-400">{label}</p>
      {payload.map((entry: any, idx: number) => (
        <p key={idx} className="text-sm font-medium text-white">
          {typeof entry.value === 'number' ? entry.value.toFixed(2) : entry.value}
          {entry.name === 'uptime' ? '%' : entry.name === 'responseTime' ? 'ms' : ''}
        </p>
      ))}
    </div>
  );
}

// ─── Loading Skeleton ──────────────────────────────────────────────────────────

function LoadingSkeleton() {
  return (
    <div className="animate-pulse space-y-5">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {[...Array(4)].map((_, i) => (
          <div key={i} className="h-28 rounded-xl bg-slate-800/50" />
        ))}
      </div>
      <div className="h-10 rounded-xl bg-slate-800/50" />
      <div className="grid gap-4 lg:grid-cols-2">
        <div className="h-64 rounded-xl bg-slate-800/50" />
        <div className="h-64 rounded-xl bg-slate-800/50" />
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <div className="h-72 rounded-xl bg-slate-800/50" />
        <div className="h-72 rounded-xl bg-slate-800/50" />
      </div>
    </div>
  );
}

function EmptyState({ message, sub }: { message: string; sub?: string }) {
  return (
    <div className="flex min-h-[160px] flex-col items-center justify-center text-center">
      <p className="text-sm text-slate-500">{message}</p>
      {sub && <p className="mt-1 text-xs text-slate-600">{sub}</p>}
    </div>
  );
}

// ─── Section Card ──────────────────────────────────────────────────────────────

function SectionCard({
  title,
  action,
  children,
  className,
}: {
  title: string;
  action?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={`rounded-xl border border-white/[0.06] bg-slate-900/50 ${className ?? ''}`}>
      <div className="flex items-center justify-between border-b border-white/[0.04] px-5 py-3.5">
        <h3 className="text-sm font-medium text-white">{title}</h3>
        {action && <div className="text-xs text-slate-500">{action}</div>}
      </div>
      <div className="px-5">{children}</div>
    </div>
  );
}

// ─── Dashboard Page ────────────────────────────────────────────────────────────

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
      if (settings) setTenantRetentionDays(settings.data_retention_days);
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

  const hasTrendData = useMemo(() => trendData.some((d) => d.total > 0), [trendData]);
  const uptimeTrend = useMemo(() => computeUptimeTrend(trendData), [trendData]);
  const responseTrend = useMemo(() => computeResponseTrend(trendData), [trendData]);

  const opsSummary = dashboard?.ops_summary;
  const problemMonitors = dashboard?.problem_monitors || [];
  const recentFailures = dashboard?.recent_failures || [];
  const recentAlerts = dashboard?.recent_alerts || [];

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
  const showRetentionWarning =
    tenantRetentionDays !== null && tenantRetentionDays > 0 && selectedRangeDays > tenantRetentionDays;

  if (loading) return <LoadingSkeleton />;

  return (
    <div className="space-y-5">
      {/* Error Banner */}
      {error && (
        <div className="flex items-center justify-between rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3">
          <p className="text-sm text-rose-400">{error}</p>
          <button onClick={loadData} className="btn btn-danger btn-sm">Retry</button>
        </div>
      )}

      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-white">Dashboard</h1>
          <p className="mt-0.5 text-sm text-slate-500">Overview of your monitoring infrastructure</p>
        </div>
        <div className="flex items-center gap-1.5">
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

      {/* Stat Cards */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label="Overall Uptime"
          value={avgUptime}
          unit="%"
          trend={uptimeTrend}
          higherIsBetter={true}
          infoEntries={[
            { label: 'Period', value: timeRange },
            { label: 'Monitors', value: `${activeMonitors} active` },
          ]}
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
          trend={responseTrend}
          higherIsBetter={false}
          infoEntries={[
            { label: 'Period', value: timeRange },
            { label: 'Scope', value: 'HTTP monitors' },
          ]}
          icon={
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M13 10V3L4 14h7v7l9-11h-7z" />
            </svg>
          }
        />
        <StatCard
          label="Total Monitors"
          value={totalMonitors}
          subtitle={`${httpMonitors} HTTP · ${agentMonitors} Agent`}
          icon={
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
            </svg>
          }
        />
        <StatCard
          label="Active"
          value={activeMonitors}
          subtitle={
            totalMonitors > 0
              ? `${((activeMonitors / totalMonitors) * 100).toFixed(0)}% enabled`
              : 'No monitors configured'
          }
          icon={
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M5.636 18.364a9 9 0 010-12.728m12.728 0a9 9 0 010 12.728m-9.9-2.829a5 5 0 010-7.07m7.072 0a5 5 0 010 7.07M13 12a1 1 0 11-2 0 1 1 0 012 0z" />
            </svg>
          }
        />
      </div>

      {/* Fleet Status Bar */}
      <FleetStatusBar
        up={opsSummary?.up_monitors ?? 0}
        down={opsSummary?.down_monitors ?? 0}
        paused={opsSummary?.paused_monitors ?? 0}
        activeAlerts={opsSummary?.active_alerts ?? 0}
        acknowledgedAlerts={opsSummary?.acknowledged_alerts ?? 0}
      />

      {/* Charts */}
      <div className="grid gap-4 lg:grid-cols-2">
        {/* Uptime Trend */}
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
          <div className="mb-4 flex items-center justify-between">
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-medium text-white">Uptime Trend</h3>
              <InfoPopover
                entries={[{ label: 'Metric', value: 'Monitor-weighted average uptime across all enabled services for the selected period' }]}
                title="Uptime Trend"
              />
            </div>
            <span className="flex items-center gap-1.5 text-xs text-emerald-400">
              <span className="h-2 w-2 rounded-full bg-emerald-400" /> Uptime
            </span>
          </div>
          {hasTrendData ? (
            <div className="h-44">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={trendData}>
                  <defs>
                    <linearGradient id="uptimeGradient" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#10b981" stopOpacity={0.25} />
                      <stop offset="100%" stopColor="#10b981" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <XAxis dataKey="date" axisLine={false} tickLine={false} tick={{ fill: '#64748b', fontSize: 11 }} />
                  <YAxis domain={uptimeDomain} axisLine={false} tickLine={false} tick={{ fill: '#64748b', fontSize: 11 }} tickFormatter={(v) => `${v}%`} />
                  <Tooltip content={<CustomTooltip />} />
                  <Area type="monotone" dataKey="uptime" stroke="#10b981" strokeWidth={2} fill="url(#uptimeGradient)" name="uptime" />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          ) : (
            <div className="flex h-44 items-center justify-center rounded-lg border border-dashed border-white/[0.06]">
              <p className="text-sm text-slate-500">No uptime data yet</p>
            </div>
          )}
        </div>

        {/* Response Time */}
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
          <div className="mb-4 flex items-center justify-between">
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-medium text-white">Response Time</h3>
              <InfoPopover
                entries={[{ label: 'Metric', value: 'Average HTTP response latency across all active monitors for the selected period' }]}
                title="Response Time"
              />
            </div>
            <span className="flex items-center gap-1.5 text-xs text-cyan-400">
              <span className="h-2 w-2 rounded-full bg-cyan-400" /> Latency
            </span>
          </div>
          {hasTrendData ? (
            <div className="h-44">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={trendData}>
                  <defs>
                    <linearGradient id="latencyGradient" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#06b6d4" stopOpacity={0.25} />
                      <stop offset="100%" stopColor="#06b6d4" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <XAxis dataKey="date" axisLine={false} tickLine={false} tick={{ fill: '#64748b', fontSize: 11 }} />
                  <YAxis axisLine={false} tickLine={false} tick={{ fill: '#64748b', fontSize: 11 }} tickFormatter={(v) => `${v}ms`} />
                  <Tooltip content={<CustomTooltip />} />
                  <Area type="monotone" dataKey="responseTime" stroke="#06b6d4" strokeWidth={2} fill="url(#latencyGradient)" name="responseTime" />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          ) : (
            <div className="flex h-44 items-center justify-center rounded-lg border border-dashed border-white/[0.06]">
              <p className="text-sm text-slate-500">No response time data yet</p>
            </div>
          )}
        </div>
      </div>

      {/* Bottom Grid: Problems | Failures + Alerts */}
      <div className="grid items-start gap-4 lg:grid-cols-2">
        {/* Problem Monitors */}
        <SectionCard
          title="Problem Monitors"
          action={
            <Link href="/monitors" className="text-cyan-400 hover:text-cyan-300 transition-colors">
              View all →
            </Link>
          }
        >
          {problemMonitors.length > 0 ? (
            <div className="dashboard-scroll max-h-80 divide-y divide-white/[0.04] overflow-y-auto pb-2 pr-1">
              {problemMonitors.map((monitor) => (
                <ProblemMonitorItem key={monitor.monitor_id} monitor={monitor} />
              ))}
            </div>
          ) : (
            <EmptyState
              message="No problem monitors"
              sub="All monitors are running cleanly in this range."
            />
          )}
        </SectionCard>

        {/* Right Column: Failures + Alerts */}
        <div className="grid gap-4">
          <SectionCard
            title="Recent Failures"
            action={
              recentFailures.length > 0 ? (
                <span className="text-slate-500">{recentFailures.length} event{recentFailures.length !== 1 ? 's' : ''}</span>
              ) : undefined
            }
          >
            {recentFailures.length > 0 ? (
              <div className="dashboard-scroll max-h-64 divide-y divide-white/[0.04] overflow-y-auto pb-2 pr-1">
                {recentFailures.map((event) => (
                  <FailureItem key={event.check_result_id} event={event} />
                ))}
              </div>
            ) : (
              <EmptyState message="No recent failures" sub="Failure events will appear here when checks fail." />
            )}
          </SectionCard>

          <SectionCard
            title="Recent Alerts"
            action={
              <Link href="/alerts" className="text-cyan-400 hover:text-cyan-300 transition-colors">
                View all →
              </Link>
            }
          >
            {recentAlerts.length > 0 ? (
              <div className="dashboard-scroll max-h-52 divide-y divide-white/[0.04] overflow-y-auto pb-2 pr-1">
                {recentAlerts.map((alert) => (
                  <AlertItem key={alert.id} alert={alert} />
                ))}
              </div>
            ) : (
              <EmptyState message="No recent alerts" sub="Alerts appear when failures trigger your policies." />
            )}
          </SectionCard>
        </div>
      </div>
    </div>
  );
}

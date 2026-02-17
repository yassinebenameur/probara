'use client';

import { useState, useEffect, useMemo, useCallback } from 'react';
import { getMonitors, getMonitorResults } from '@/lib/api';
import { Monitor, CheckResult } from '@/lib/types';
import Link from 'next/link';
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  BarChart,
  Bar,
  Cell,
} from 'recharts';

const HOUR_MS = 60 * 60 * 1000;
const DAY_MS = 24 * HOUR_MS;

type ResultItem = { monitorId: string; monitor: string; result: CheckResult };
type TrendPoint = { date: string; uptime: number; responseTime: number; total: number };
type ActivityPoint = { time: string; checks: number; failures: number };

function buildTrendData(results: ResultItem[], timeRange: '24h' | '7d' | '30d'): TrendPoint[] {
  const now = new Date();
  const isHourly = timeRange === '24h';
  const bucketCount = isHourly ? 24 : timeRange === '7d' ? 7 : 30;
  const intervalMs = isHourly ? HOUR_MS : DAY_MS;

  const end = new Date(now);
  if (isHourly) {
    end.setMinutes(0, 0, 0);
  } else {
    end.setHours(0, 0, 0, 0);
  }
  const start = new Date(end.getTime() - (bucketCount - 1) * intervalMs);

  const labelFor = (d: Date) =>
    isHourly
      ? d.toLocaleTimeString('en-US', { hour: '2-digit', hour12: false })
      : timeRange === '7d'
      ? d.toLocaleDateString('en-US', { weekday: 'short' })
      : d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });

  const buckets = Array.from({ length: bucketCount }, (_, i) => {
    const bucketStart = new Date(start.getTime() + i * intervalMs);
    return {
      date: labelFor(bucketStart),
      start: bucketStart.getTime(),
      success: 0,
      total: 0,
      latencySum: 0,
      latencyCount: 0,
    };
  });

  const rangeStart = start.getTime();
  const rangeEnd = start.getTime() + bucketCount * intervalMs;

  for (const { result } of results) {
    const t = new Date(result.created_at).getTime();
    if (Number.isNaN(t) || t < rangeStart || t >= rangeEnd) continue;
    const idx = Math.floor((t - rangeStart) / intervalMs);
    const bucket = buckets[idx];
    if (!bucket) continue;
    bucket.total += 1;
    if (result.status === 'success') bucket.success += 1;
    if (typeof result.latency_ms === 'number') {
      bucket.latencySum += result.latency_ms;
      bucket.latencyCount += 1;
    }
  }

  return buckets.map((b) => ({
    date: b.date,
    uptime: b.total > 0 ? (b.success / b.total) * 100 : 0,
    responseTime: b.latencyCount > 0 ? b.latencySum / b.latencyCount : 0,
    total: b.total,
  }));
}

function buildActivityData(results: ResultItem[]): ActivityPoint[] {
  const now = new Date();
  const end = new Date(now);
  end.setMinutes(0, 0, 0);
  const start = new Date(end.getTime() - 23 * HOUR_MS);

  const buckets = Array.from({ length: 24 }, (_, i) => {
    const bucketStart = new Date(start.getTime() + i * HOUR_MS);
    return {
      time: bucketStart.toLocaleTimeString('en-US', { hour: '2-digit', hour12: false }),
      start: bucketStart.getTime(),
      checks: 0,
      failures: 0,
    };
  });

  const rangeStart = start.getTime();
  const rangeEnd = start.getTime() + 24 * HOUR_MS;

  for (const { result } of results) {
    const t = new Date(result.created_at).getTime();
    if (Number.isNaN(t) || t < rangeStart || t >= rangeEnd) continue;
    const idx = Math.floor((t - rangeStart) / HOUR_MS);
    const bucket = buckets[idx];
    if (!bucket) continue;
    bucket.checks += 1;
    if (result.status !== 'success') bucket.failures += 1;
  }

  return buckets.map(({ time, checks, failures }) => ({ time, checks, failures }));
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

// Uptime Bar Component
function UptimeBar({
  value,
  label,
  dense = false,
}: {
  value: number;
  label: string;
  dense?: boolean;
}) {
  const getColor = (v: number) => {
    if (v >= 99.9) return 'bg-emerald-500';
    if (v >= 99) return 'bg-emerald-400';
    if (v >= 95) return 'bg-amber-400';
    return 'bg-rose-400';
  };

  const barWidthClass = dense ? 'w-full max-w-[10px]' : 'w-3';
  const labelClass = dense
    ? 'w-full text-center text-[9px] tracking-tight tabular-nums text-slate-600 group-hover:text-slate-400 truncate'
    : 'text-[10px] text-slate-600 group-hover:text-slate-400';
  const valueClass = dense
    ? 'w-full justify-center text-[10px] text-slate-400 tabular-nums'
    : 'text-xs text-slate-400';

  return (
    <div className={`group flex flex-col items-center ${dense ? 'flex-1 min-w-0 gap-0.5' : 'gap-1'}`}>
      <div
        className={`relative h-20 overflow-hidden rounded-full bg-slate-800 ${barWidthClass} ${
          dense ? 'mx-auto' : ''
        }`}
      >
        <div 
          className={`absolute bottom-0 left-0 right-0 rounded-full transition-all ${getColor(value)}`}
          style={{ height: `${value}%` }}
        />
      </div>
      <span className={labelClass}>{label}</span>
      <span
        className={`inline-flex h-4 items-center opacity-0 transition-opacity group-hover:opacity-100 ${valueClass}`}
      >
        {value.toFixed(1)}%
      </span>
    </div>
  );
}

// Monitor Health Dot
function HealthDot({ status, name }: { status: 'up' | 'down' | 'paused'; name: string }) {
  const colors = {
    up: 'bg-emerald-500 shadow-emerald-500/50',
    down: 'bg-rose-500 shadow-rose-500/50',
    paused: 'bg-slate-500',
  };

  return (
    <div className="group relative">
      <div 
        className={`h-3 w-3 rounded-sm ${colors[status]} ${status !== 'paused' ? 'shadow-[0_0_8px]' : ''} transition-transform hover:scale-150`}
        title={name}
      />
      <div className="pointer-events-none absolute bottom-full left-1/2 mb-2 -translate-x-1/2 whitespace-nowrap rounded bg-slate-800 px-2 py-1 text-xs text-white opacity-0 transition-opacity group-hover:opacity-100">
        {name}
      </div>
    </div>
  );
}

// Recent Activity Item
function ActivityItem({ 
  monitor, 
  status, 
  time, 
  latency 
}: { 
  monitor: string;
  status: CheckResult['status'];
  time: string;
  latency?: number;
}) {
  const statusColor =
    status === 'success'
      ? 'bg-emerald-500'
      : status === 'degraded'
      ? 'bg-amber-500'
      : 'bg-rose-500';

  return (
    <div className="flex items-center gap-3 py-2">
      <div className={`h-2 w-2 rounded-full ${statusColor}`} />
      <div className="flex-1 min-w-0">
        <p className="truncate text-sm text-white">{monitor}</p>
        <p className="text-xs text-slate-500">{time}</p>
      </div>
      {latency && (
        <span className="text-xs text-slate-400">{latency}ms</span>
      )}
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
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [allResults, setAllResults] = useState<ResultItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [timeRange, setTimeRange] = useState<'24h' | '7d' | '30d'>('7d');

  // Memoized chart data
  const uptimeData = useMemo(() => buildTrendData(allResults, timeRange), [allResults, timeRange]);
  const hourlyData = useMemo(() => buildActivityData(allResults), [allResults]);
  const hasTrendData = useMemo(() => uptimeData.some((d) => d.total > 0), [uptimeData]);
  const hasActivityData = useMemo(() => hourlyData.some((d) => d.checks > 0), [hourlyData]);

  const loadData = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const response = await getMonitors({ page_size: 50 });
      const monitorList = response.items || [];
      setMonitors(monitorList);

      if (monitorList.length === 0) {
        setAllResults([]);
        return;
      }

      const now = Date.now();
      const rangeMs =
        timeRange === '24h' ? 24 * HOUR_MS : timeRange === '7d' ? 7 * DAY_MS : 30 * DAY_MS;
      const since = new Date(now - rangeMs).toISOString();
      const limit = timeRange === '24h' ? 200 : timeRange === '7d' ? 1000 : 2000;

      const resultsPromises = monitorList.map(async (m) => {
        try {
          const res = await getMonitorResults(m.id, { limit, since });
          return res.results.map((r) => ({ monitorId: m.id, monitor: m.name, result: r }));
        } catch {
          return [];
        }
      });

      const results = (await Promise.all(resultsPromises)).flat();
      setAllResults(results);
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

  // Calculate statistics
  const totalMonitors = monitors.length;
  const activeMonitors = monitors.filter(m => m.enabled).length;
  const httpMonitors = monitors.filter(m => m.type === 'http').length;
  const agentMonitors = monitors.filter(m => m.type === 'agent').length;

  const totalChecks = allResults.length;
  const successChecks = allResults.filter((r) => r.result.status === 'success').length;
  const avgUptime = totalChecks > 0 ? ((successChecks / totalChecks) * 100).toFixed(2) : '0.00';
  const latencyValues = allResults
    .map((r) => r.result.latency_ms)
    .filter((v): v is number => typeof v === 'number');
  const avgResponseTime =
    latencyValues.length > 0
      ? Math.round(latencyValues.reduce((sum, v) => sum + v, 0) / latencyValues.length)
      : 0;

  const recentResults = useMemo(() => {
    return [...allResults]
      .sort((a, b) => new Date(b.result.created_at).getTime() - new Date(a.result.created_at).getTime())
      .slice(0, 10);
  }, [allResults]);

  const latestResultByMonitor = useMemo(() => {
    const map = new Map<string, CheckResult>();
    for (const item of allResults) {
      const existing = map.get(item.monitorId);
      if (!existing || new Date(item.result.created_at) > new Date(existing.created_at)) {
        map.set(item.monitorId, item.result);
      }
    }
    return map;
  }, [allResults]);

  const minUptime = useMemo(() => {
    if (!hasTrendData) return 95;
    return Math.min(...uptimeData.map((d) => (d.total > 0 ? d.uptime : 100)));
  }, [uptimeData, hasTrendData]);
  const uptimeDomain: [number, number] = minUptime < 95 ? [0, 100] : [95, 100];

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
          {(['24h', '7d', '30d'] as const).map((range) => (
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
              <p className="text-xs text-slate-500">Average uptime across all monitors</p>
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
                <AreaChart data={uptimeData}>
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
                <AreaChart data={uptimeData}>
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
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          ) : (
            <EmptyChart message="No response time data yet" />
          )}
        </div>
      </div>

      {/* Bottom Section */}
      <div className="grid gap-4 lg:grid-cols-3">
        {/* Daily Uptime Bars */}
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
          <div className="mb-4">
            <h3 className="font-medium text-white">
              {timeRange === '24h' ? 'Hourly Uptime' : 'Daily Uptime'}
            </h3>
            <p className="text-xs text-slate-500">
              {timeRange === '24h'
                ? 'Last 24 hours performance'
                : `Last ${timeRange === '30d' ? '30' : '7'} days performance`}
            </p>
          </div>
          {hasTrendData ? (
            <>
              <div
                className={`flex items-end ${
                  timeRange === '24h' || timeRange === '30d'
                    ? 'gap-0.5 px-1'
                    : 'justify-between gap-1 px-2'
                }`}
              >
                {uptimeData.map((d, i) => (
                  <UptimeBar
                    key={i}
                    value={d.uptime}
                    label={d.date}
                    dense={timeRange === '24h' || timeRange === '30d'}
                  />
                ))}
              </div>
              <div className="mt-4 flex items-center justify-between text-xs text-slate-500">
                <span>Hover for details</span>
                <span className="flex items-center gap-2">
                  <span className="flex items-center gap-1">
                    <span className="h-2 w-2 rounded-full bg-emerald-500" />
                    &gt;99%
                  </span>
                  <span className="flex items-center gap-1">
                    <span className="h-2 w-2 rounded-full bg-amber-400" />
                    95-99%
                  </span>
                </span>
              </div>
            </>
          ) : (
            <EmptyChart message="No uptime data yet" />
          )}
        </div>

        {/* Monitor Health Grid */}
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <h3 className="font-medium text-white">Monitor Health</h3>
              <p className="text-xs text-slate-500">{totalMonitors} monitors at a glance</p>
            </div>
            <Link href="/monitors" className="text-xs text-cyan-400 hover:text-cyan-300">
              View all →
            </Link>
          </div>
          {monitors.length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {monitors.slice(0, 30).map((m) => (
                <HealthDot 
                  key={m.id} 
                  status={
                    !m.enabled
                      ? 'paused'
                      : latestResultByMonitor.get(m.id)?.status === 'success'
                      ? 'up'
                      : latestResultByMonitor.has(m.id)
                      ? 'down'
                      : 'paused'
                  } 
                  name={m.name}
                />
              ))}
              {monitors.length > 30 && (
                <span className="flex h-3 items-center text-xs text-slate-500">
                  +{monitors.length - 30} more
                </span>
              )}
            </div>
          ) : (
            <div className="flex flex-col items-center justify-center py-8 text-center">
              <p className="text-sm text-slate-500">No monitors configured</p>
              <Link 
                href="/monitors/new"
                className="mt-2 text-xs text-cyan-400 hover:text-cyan-300"
              >
                Create your first monitor →
              </Link>
            </div>
          )}
        </div>

        {/* Recent Activity */}
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <h3 className="font-medium text-white">Recent Checks</h3>
              <p className="text-xs text-slate-500">Latest check results</p>
            </div>
          </div>
          {recentResults.length > 0 ? (
            <div className="divide-y divide-white/[0.04]">
              {recentResults.slice(0, 6).map((item, i) => (
                <ActivityItem
                  key={i}
                  monitor={item.monitor}
                  status={item.result.status}
                  time={new Date(item.result.created_at).toLocaleTimeString()}
                  latency={item.result.latency_ms}
                />
              ))}
            </div>
          ) : (
            <div className="flex flex-col items-center justify-center py-8 text-center">
              <p className="text-sm text-slate-500">No recent checks</p>
              <p className="mt-1 text-xs text-slate-600">Results will appear once monitors run</p>
            </div>
          )}
        </div>
      </div>

      {/* Hourly Activity Chart */}
      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
        <div className="mb-4 flex items-center justify-between">
          <div>
            <h3 className="font-medium text-white">24-Hour Activity</h3>
            <p className="text-xs text-slate-500">Checks performed per hour</p>
          </div>
          <div className="flex items-center gap-4 text-xs">
            <span className="flex items-center gap-1 text-cyan-400">
              <span className="h-2 w-2 rounded-full bg-cyan-400" />
              Checks
            </span>
            <span className="flex items-center gap-1 text-rose-400">
              <span className="h-2 w-2 rounded-full bg-rose-400" />
              Failures
            </span>
          </div>
        </div>
        {hasActivityData ? (
          <div className="h-32">
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={hourlyData} barGap={2}>
                <XAxis 
                  dataKey="time" 
                  axisLine={false} 
                  tickLine={false} 
                  tick={{ fill: '#64748b', fontSize: 10 }}
                  interval={3}
                />
                <Tooltip 
                  contentStyle={{ 
                    backgroundColor: '#0f172a', 
                    border: '1px solid rgba(255,255,255,0.1)',
                    borderRadius: '8px',
                    fontSize: '12px'
                  }}
                />
                <Bar dataKey="checks" fill="#06b6d4" radius={[2, 2, 0, 0]} />
                <Bar dataKey="failures" fill="#f43f5e" radius={[2, 2, 0, 0]} />
              </BarChart>
            </ResponsiveContainer>
          </div>
        ) : (
          <EmptyChart message="No activity yet" />
        )}
      </div>

      {/* Quick Actions */}
      <div className="grid gap-4 sm:grid-cols-3">
        <Link
          href="/monitors/new"
          className="group flex items-center gap-4 rounded-xl border border-white/[0.06] bg-slate-900/50 p-4 transition-all hover:border-cyan-500/30 hover:bg-slate-900/70"
        >
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-cyan-500/10 text-cyan-400 transition-colors group-hover:bg-cyan-500/20">
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M12 4v16m8-8H4" />
            </svg>
          </div>
          <div>
            <p className="font-medium text-white">Add Monitor</p>
            <p className="text-xs text-slate-500">HTTP, Ping, or Agent</p>
          </div>
        </Link>
        
        <Link
          href="/status-pages"
          className="group flex items-center gap-4 rounded-xl border border-white/[0.06] bg-slate-900/50 p-4 transition-all hover:border-emerald-500/30 hover:bg-slate-900/70"
        >
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-emerald-500/10 text-emerald-400 transition-colors group-hover:bg-emerald-500/20">
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
            </svg>
          </div>
          <div>
            <p className="font-medium text-white">Status Pages</p>
            <p className="text-xs text-slate-500">Manage public pages</p>
          </div>
        </Link>
        
        <Link
          href="/alert-policies"
          className="group flex items-center gap-4 rounded-xl border border-white/[0.06] bg-slate-900/50 p-4 transition-all hover:border-rose-500/30 hover:bg-slate-900/70"
        >
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-rose-500/10 text-rose-400 transition-colors group-hover:bg-rose-500/20">
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9" />
            </svg>
          </div>
          <div>
            <p className="font-medium text-white">Alert Policies</p>
            <p className="text-xs text-slate-500">Configure alerts</p>
          </div>
        </Link>
      </div>
    </div>
  );
}

'use client';

import { useMemo, useState, useRef, useEffect } from 'react';
import {
  Area,
  AreaChart,
  ReferenceArea,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { Info } from 'lucide-react';

import { CheckResult, Monitor, MonitorAnalyticsResponse, MonitorAnalyticsRange } from '@/lib/types';
import { getEffectiveMonitorStatus, MonitorDisplayStatus } from '@/lib/monitor-utils';
import { SLA_TARGET, UptimeHeroGauge } from './UptimeHeroGauge';

const OVERVIEW_RANGES: MonitorAnalyticsRange[] = ['1h', '6h', '24h', '7d', '30d', '90d', '365d'];

function formatLatency(value?: number): string {
  if (value === undefined || value === null) return 'N/A';
  if (value >= 1000) return `${(value / 1000).toFixed(2)}s`;
  return `${Math.round(value)}ms`;
}

function formatTime(value?: string): string {
  if (!value) return 'N/A';
  return new Date(value).toLocaleString();
}

function formatBucketLabel(value: number, range: MonitorAnalyticsRange): string {
  const date = new Date(value);
  if (range === '1h' || range === '6h' || range === '24h') {
    return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }
  return date.toLocaleDateString([], { month: 'short', day: 'numeric' });
}

function formatRangeWindowLabel(range: MonitorAnalyticsRange): string {
  switch (range) {
    case '1h':
      return '1 Hour Window';
    case '6h':
      return '6 Hour Window';
    case '24h':
      return '24 Hour Window';
    case '7d':
      return '7 Day Window';
    case '30d':
      return '30 Day Window';
    case '90d':
      return '90 Day Window';
    case '365d':
      return '365 Day Window';
    default:
      return range;
  }
}

function formatTooltipValue(value: unknown, kind: 'uptime' | 'latency'): string {
  if (typeof value !== 'number') return 'N/A';
  if (kind === 'uptime') return `${value.toFixed(2)}%`;
  return formatLatency(value);
}

function StatusPill({ status }: { status: string }) {
  const className =
    status === 'success' || status === 'up'
      ? 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20'
      : status === 'error' || status === 'degraded'
        ? 'bg-amber-500/10 text-amber-400 border-amber-500/20'
        : status === 'failure' || status === 'down'
          ? 'bg-rose-500/10 text-rose-400 border-rose-500/20'
          : status === 'paused'
            ? 'bg-slate-500/10 text-slate-300 border-slate-500/20'
          : status === 'maintenance'
            ? 'bg-sky-500/10 text-sky-300 border-sky-500/20'
          : 'bg-slate-500/10 text-slate-300 border-slate-500/20';

  const label = status === 'paused' ? 'paused' : status;

  return (
    <span className={`inline-flex rounded-full border px-2.5 py-1 text-xs font-medium uppercase ${className}`}>
      {label}
    </span>
  );
}

function MetadataPopover({
  source,
  coverage,
  coverageStart,
  generatedAt,
}: {
  source?: string;
  coverage?: string;
  coverageStart?: string;
  generatedAt?: string;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, [open]);

  const rows = [
    { label: 'Source', value: source || 'raw' },
    { label: 'Coverage', value: coverage || '—' },
    { label: 'Coverage Start', value: coverageStart ? formatTime(coverageStart) : '—' },
    { label: 'Generated At', value: generatedAt ? formatTime(generatedAt) : '—' },
  ];

  return (
    <div className="relative inline-flex" ref={ref}>
      <button
        onClick={() => setOpen((v) => !v)}
        className="inline-flex items-center justify-center rounded-full p-1 text-slate-500 transition-colors hover:bg-white/[0.06] hover:text-slate-300"
        aria-label="Show metadata"
      >
        <Info size={14} />
      </button>
      {open && (
        <div className="absolute left-0 top-full z-50 mt-2 w-56 rounded-lg border border-white/[0.08] bg-slate-900 p-3 shadow-xl animate-fade-in">
          {rows.map((row) => (
            <div key={row.label} className="flex items-baseline justify-between py-1.5">
              <span className="text-[10px] font-medium uppercase tracking-wider text-slate-500">{row.label}</span>
              <span className="text-xs text-slate-300">{row.value}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function ChartTooltip({
  active,
  payload,
  label,
  kind,
  range,
}: {
  active?: boolean;
  payload?: Array<{ color?: string; name?: string; value?: unknown }>;
  label?: number;
  kind: 'uptime' | 'latency';
  range: MonitorAnalyticsRange;
}) {
  if (!active || !payload?.length || typeof label !== 'number') return null;

  const point = payload.find((entry) => entry.value !== null && entry.value !== undefined);
  if (!point) return null;

  return (
    <div className="rounded-lg border border-white/10 bg-slate-900 px-3 py-2 shadow-xl">
      <p className="text-xs text-slate-400">{formatBucketLabel(label, range)}</p>
      <p className="mt-1 text-sm font-medium text-white">
        {kind === 'uptime' ? 'Uptime' : 'Latency'}: {formatTooltipValue(point.value, kind)}
      </p>
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

function RecentResultsTable({ results }: { results: CheckResult[] }) {
  const rows = results.slice(0, 10);

  return (
    <div className="overflow-hidden rounded-xl border border-white/[0.06] bg-slate-900/50">
      <div className="border-b border-white/[0.06] px-4 py-3">
        <h3 className="text-sm font-medium text-white">Recent Results</h3>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full">
          <thead>
            <tr className="border-b border-white/[0.06] bg-slate-800/30">
              <th className="px-5 py-3 text-left text-xs font-medium text-slate-500">Time (UTC)</th>
              <th className="px-5 py-3 text-left text-xs font-medium text-slate-500">Status</th>
              <th className="px-5 py-3 text-left text-xs font-medium text-slate-500">Latency</th>
              <th className="px-5 py-3 text-left text-xs font-medium text-slate-500">Location</th>
              <th className="px-5 py-3 text-left text-xs font-medium text-slate-500">Source</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((result, idx) => (
              <tr key={result.id} className={`border-b border-white/[0.03] ${idx % 2 === 1 ? 'bg-slate-800/20' : ''}`}>
                <td className="px-5 py-3 text-sm font-mono text-slate-400">
                  {new Date(result.created_at).toISOString().replace('T', ' ').slice(0, 19)}
                </td>
                <td className="px-5 py-3 text-sm text-slate-300">{result.status}</td>
                <td className="px-5 py-3 text-sm text-slate-400">{formatLatency(result.latency_ms)}</td>
                <td className="px-5 py-3 text-sm">
                  {result.location_name ? (
                    <span className="inline-flex items-center rounded-full border border-cyan-500/35 bg-cyan-500/12 px-2 py-0.5 text-[0.7rem] font-medium text-cyan-200">
                      {result.location_name}
                    </span>
                  ) : (
                    <span className="text-slate-500">Default</span>
                  )}
                </td>
                <td className="px-5 py-3 text-sm text-slate-500">{result.result_source}</td>
              </tr>
            ))}
            {rows.length === 0 && (
              <tr>
                <td colSpan={5} className="px-5 py-8 text-center text-sm text-slate-500">
                  No raw results available yet
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export default function MonitorAnalyticsOverview({
  monitor,
  analytics,
  results,
  loading = false,
  timeRange,
  onTimeRangeChange,
}: {
  monitor: Monitor;
  analytics: MonitorAnalyticsResponse | null;
  results: CheckResult[];
  loading?: boolean;
  timeRange: MonitorAnalyticsRange;
  onTimeRangeChange: (range: MonitorAnalyticsRange) => void;
}) {
  const chartData = useMemo(() => {
    return (analytics?.uptime_series || []).map((point, index) => ({
      id: `${point.bucket_start}-${index}`,
      ts: new Date(point.bucket_start).getTime(),
      uptime: point.has_data ? point.uptime_pct : null,
      latency: analytics?.latency_series?.[index]?.avg_latency_ms ?? null,
      hasData: point.has_data,
    }));
  }, [analytics]);

  const downtimeAreas = useMemo(
    () =>
      (analytics?.downtime_periods || []).map((period, index) => ({
        id: `${period.start_time}-${index}`,
        start: new Date(period.start_time).getTime(),
        end: new Date(period.end_time).getTime(),
      })),
    [analytics]
  );

  const summary = analytics?.summary;
  const latencyHeadlineLabel = summary?.p95_latency_ms !== undefined ? 'P95 Latency' : 'Avg Latency';
  const latencyHeadlineValue = summary?.p95_latency_ms ?? summary?.avg_latency_ms;
  const hasData = Boolean(chartData.some((point) => point.hasData));
  const hasLatencyData = Boolean(chartData.some((point) => typeof point.latency === 'number'));
  const isSlaCompliant = (summary?.sla_pct ?? 0) >= SLA_TARGET;
  const effectiveStatus = getEffectiveMonitorStatus(monitor, results);

  if (loading) {
    return <div className="py-12 text-center text-slate-500">Loading monitor analytics...</div>;
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
        <h2 className="text-lg font-semibold text-white">Overview</h2>
        <div className="flex flex-wrap items-center gap-1.5 xl:justify-end">
          {OVERVIEW_RANGES.map((range) => (
            <button
              key={range}
              onClick={() => onTimeRangeChange(range)}
              className={`rounded-full px-3.5 py-1.5 text-xs font-medium transition-colors ${timeRange === range ? 'bg-cyan-500 text-white' : 'bg-slate-800/60 text-slate-400 hover:text-white'
                }`}
            >
              {range}
            </button>
          ))}
        </div>
      </div>

      <div className="relative overflow-hidden rounded-2xl border border-white/[0.06] bg-slate-900/60 p-4 sm:p-5">
        <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(255,90,36,0.08),transparent_42%)]" />
        <div className="relative grid gap-4 lg:grid-cols-[minmax(0,280px)_minmax(0,1fr)] lg:items-center">
          <div className="flex justify-center lg:justify-start">
            <UptimeHeroGauge
              uptime={summary?.sla_pct || 0}
              hasData={hasData}
              rangeLabel={formatRangeWindowLabel(timeRange)}
            />
          </div>

          <div className="space-y-4">
            <span
              className={`inline-flex items-center gap-2 rounded-full border px-3 py-1 text-[11px] font-semibold uppercase tracking-[0.18em] ${isSlaCompliant
                  ? 'border-cyan-400/30 bg-cyan-400/10 text-cyan-300'
                  : 'border-rose-400/30 bg-rose-400/10 text-rose-300'
                }`}
            >
              <span className={`h-1.5 w-1.5 rounded-full ${isSlaCompliant ? 'bg-cyan-400' : 'bg-rose-400'}`} />
              {isSlaCompliant ? 'SLA Compliant' : 'SLA Breach'}
            </span>

            <div className="space-y-1">
              <div className="flex items-center gap-1.5">
                <p className="text-[11px] font-semibold uppercase tracking-[0.18em] text-slate-500">SLA / Uptime</p>
                <MetadataPopover
                  source={analytics?.source}
                  coverage={analytics?.is_partial ? 'Partial' : 'Complete'}
                  coverageStart={analytics?.coverage_start}
                  generatedAt={analytics?.generated_at}
                />
              </div>
              <p className="text-sm text-slate-400">
                target {SLA_TARGET.toFixed(1)}%
              </p>
              {summary?.downtime_pct !== undefined && summary.downtime_pct > 0 && (
                <p className="text-sm font-medium text-slate-300">
                  {summary.downtime_pct.toFixed(2)}% total downtime
                </p>
              )}
            </div>

            <div className="flex flex-wrap items-center gap-x-6 gap-y-2 border-t border-white/[0.06] pt-4">
              <div>
                <p className="text-[10px] font-medium uppercase tracking-wider text-slate-500">{latencyHeadlineLabel}</p>
                <p className="mt-0.5 text-sm font-semibold text-white">{formatLatency(latencyHeadlineValue)}</p>
              </div>
              <div>
                <p className="text-[10px] font-medium uppercase tracking-wider text-slate-500">Latest Status</p>
                <div className="mt-0.5 flex items-center gap-2">
                  <StatusPill status={monitor.enabled ? (summary?.latest_status || 'unknown') : effectiveStatus} />
                  <span className="text-xs text-slate-500">{formatTime(summary?.latest_check_at)}</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-sm font-medium text-white">Uptime Trend</h3>
            <span className="flex items-center gap-1.5 text-[11px] text-emerald-400">
              <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
              Uptime
            </span>
          </div>
          {hasData ? (
            <div className="h-48">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={chartData}>
                  <defs>
                    <linearGradient id="monitorUptimeGradient" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#46d17f" stopOpacity={0.3} />
                      <stop offset="100%" stopColor="#46d17f" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <XAxis
                    dataKey="ts"
                    type="number"
                    domain={['dataMin', 'dataMax']}
                    axisLine={false}
                    tickLine={false}
                    tick={{ fill: '#64748b', fontSize: 11 }}
                    tickFormatter={(value) => formatBucketLabel(value, timeRange)}
                  />
                  <YAxis
                    domain={[0, 100]}
                    axisLine={false}
                    tickLine={false}
                    tick={{ fill: '#64748b', fontSize: 11 }}
                    tickFormatter={(value) => `${value}%`}
                  />
                  <Tooltip content={<ChartTooltip kind="uptime" range={timeRange} />} />
                  <Area
                    type="monotone"
                    dataKey="uptime"
                    stroke="#46d17f"
                    strokeWidth={2}
                    fill="url(#monitorUptimeGradient)"
                    connectNulls={false}
                    dot={false}
                    name="Uptime"
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          ) : (
            <EmptyChart message="No uptime data yet" />
          )}
        </div>

        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-sm font-medium text-white">Latency & Downtime</h3>
            <span className="flex items-center gap-1.5 text-[11px] text-cyan-400">
              <span className="h-1.5 w-1.5 rounded-full bg-cyan-400" />
              Latency
            </span>
          </div>
          {hasLatencyData ? (
            <div className="h-48">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={chartData}>
                  <defs>
                    <linearGradient id="monitorLatencyGradient" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#ff5a24" stopOpacity={0.3} />
                      <stop offset="100%" stopColor="#ff5a24" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <XAxis
                    dataKey="ts"
                    type="number"
                    domain={['dataMin', 'dataMax']}
                    axisLine={false}
                    tickLine={false}
                    tick={{ fill: '#64748b', fontSize: 11 }}
                    tickFormatter={(value) => formatBucketLabel(value, timeRange)}
                  />
                  <YAxis
                    axisLine={false}
                    tickLine={false}
                    tick={{ fill: '#64748b', fontSize: 11 }}
                    tickFormatter={(value) => `${value}ms`}
                  />
                  <Tooltip content={<ChartTooltip kind="latency" range={timeRange} />} />
                  {downtimeAreas.map((area) => (
                    <ReferenceArea key={area.id} x1={area.start} x2={area.end} fill="rgba(240,74,90,0.08)" strokeOpacity={0} />
                  ))}
                  <Area
                    type="monotone"
                    dataKey="latency"
                    stroke="#ff5a24"
                    strokeWidth={2}
                    fill="url(#monitorLatencyGradient)"
                    connectNulls={false}
                    dot={false}
                    name="Latency"
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          ) : (
            <EmptyChart message="No latency data yet" />
          )}
        </div>
      </div>

      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-sm font-medium text-white">Downtime Periods</h3>
          <span className="text-xs text-slate-500">{analytics?.downtime_periods.length || 0} periods</span>
        </div>
        <div className="space-y-2">
          {(analytics?.downtime_periods || []).slice(0, 8).map((period) => (
            <div key={`${period.start_time}-${period.end_time}`} className="flex items-center justify-between rounded-lg border border-white/[0.06] bg-slate-950/40 px-4 py-3 text-sm">
              <span className="text-slate-300">{formatTime(period.start_time)}</span>
              <span className="text-slate-500">to</span>
              <span className="text-slate-300">{formatTime(period.end_time)}</span>
            </div>
          ))}
          {(!analytics?.downtime_periods || analytics.downtime_periods.length === 0) && (
            <div className="rounded-lg border border-white/[0.06] bg-slate-950/40 px-4 py-6 text-center text-sm text-slate-500">
              No downtime periods in the selected range.
            </div>
          )}
        </div>
      </div>

      <RecentResultsTable results={results} />
    </div>
  );
}

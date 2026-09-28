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
import { collapseResultRuns } from '@/lib/result-runs';
import Link from 'next/link';
import { getSlas } from '@/lib/api';
import { strictestTarget, formatTarget, type Sla } from '@/lib/sla';
import FilterChip from '@/components/ui/FilterChip';
import Pill from '@/components/ui/Pill';
import { CHART_AXIS_TICK, CHART_CURSOR, CHART_TOOLTIP_CLASS } from './metric-chart';
import { UptimeHeroGauge } from './UptimeHeroGauge';

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

function formatTooltipValue(value: unknown, kind: 'uptime' | 'latency'): string {
  if (typeof value !== 'number') return 'N/A';
  if (kind === 'uptime') return `${value.toFixed(2)}%`;
  return formatLatency(value);
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
    { label: 'Coverage start', value: coverageStart ? formatTime(coverageStart) : '—' },
    { label: 'Generated at', value: generatedAt ? formatTime(generatedAt) : '—' },
  ];

  return (
    <div className="relative inline-flex" ref={ref}>
      <button
        onClick={() => setOpen((v) => !v)}
        className="inline-flex h-6 w-6 items-center justify-center rounded-full text-slate-500 transition-colors hover:bg-well-hover hover:text-white"
        aria-label="Show metadata"
      >
        <Info className="h-3.5 w-3.5" strokeWidth={1.75} />
      </button>
      {open && (
        <div className="glass-overlay absolute left-0 top-full z-50 mt-2 w-64 !rounded-xl px-3 py-2 animate-fade-in">
          {rows.map((row) => (
            <div key={row.label} className="flex items-baseline justify-between gap-3 py-1.5">
              <span className="text-xs text-slate-500">{row.label}</span>
              <span className="text-right text-xs text-slate-300">{row.value}</span>
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
    <div className={CHART_TOOLTIP_CLASS}>
      <p className="text-xs text-slate-400">{formatBucketLabel(label, range)}</p>
      <p className="mt-1 text-sm font-medium tabular-nums">
        {kind === 'uptime' ? 'Uptime' : 'Latency'}: {formatTooltipValue(point.value, kind)}
      </p>
    </div>
  );
}

function EmptyChart({ message }: { message: string }) {
  return (
    <div className="flex h-48 items-center justify-center">
      <p className="text-[13px] text-slate-500">{message}</p>
    </div>
  );
}

const RESULT_STATUS_DOT: Record<CheckResult['status'], string> = {
  success: 'bg-emerald-500',
  failure: 'bg-rose-500',
  error: 'bg-amber-500',
  degraded: 'bg-amber-500',
};

function formatResultTime(value: string): string {
  return new Date(value).toISOString().replace('T', ' ').slice(0, 19);
}

function RecentResultsTable({ results }: { results: CheckResult[] }) {
  const runs = collapseResultRuns(results).slice(0, 10);

  return (
    <div className="glass overflow-hidden">
      <div className="px-5 py-4">
        <h3 className="text-[15px] font-semibold text-white">Recent results</h3>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full">
          <thead>
            <tr className="border-b border-divider">
              <th className="px-5 py-2.5 text-left text-xs font-medium text-slate-500">Time (UTC)</th>
              <th className="px-5 py-2.5 text-left text-xs font-medium text-slate-500">Status</th>
              <th className="px-5 py-2.5 text-left text-xs font-medium text-slate-500">Latency</th>
              <th className="px-5 py-2.5 text-left text-xs font-medium text-slate-500">Location</th>
              <th className="px-5 py-2.5 text-left text-xs font-medium text-slate-500">Source</th>
            </tr>
          </thead>
          <tbody>
            {runs.map(({ latest, earliest, count }) => (
              <tr key={latest.id} className="border-b border-divider transition-colors last:border-0 hover:bg-well">
                <td className="whitespace-nowrap px-5 py-3 font-mono text-[13px] text-slate-400">
                  {count > 1 ? (
                    <>
                      {formatResultTime(earliest.created_at)}
                      <span className="text-slate-500"> – </span>
                      {formatResultTime(latest.created_at).slice(11)}
                    </>
                  ) : (
                    formatResultTime(latest.created_at)
                  )}
                </td>
                <td className="whitespace-nowrap px-5 py-3 text-[13px] text-slate-300">
                  <span className="inline-flex items-center gap-2">
                    <span className={`h-2 w-2 shrink-0 rounded-full ${RESULT_STATUS_DOT[latest.status] ?? 'bg-slate-500'}`} />
                    {latest.status}
                    {count > 1 && <span className="text-slate-500">×{count}</span>}
                  </span>
                </td>
                <td className="px-5 py-3 text-[13px] tabular-nums text-slate-300">{formatLatency(latest.latency_ms)}</td>
                <td className="px-5 py-3 text-[13px]">
                  {latest.location_name ? (
                    <Pill size="xs">{latest.location_name}</Pill>
                  ) : (
                    <span className="text-slate-500">Default</span>
                  )}
                </td>
                <td className="px-5 py-3 text-[13px] text-slate-500">{latest.result_source}</td>
              </tr>
            ))}
            {runs.length === 0 && (
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
  // The gauge's target is the strictest SLA covering this monitor; with none,
  // the 99.9% default stands in.
  const [coveringSlas, setCoveringSlas] = useState<Sla[]>([]);
  useEffect(() => {
    let cancelled = false;
    getSlas({ monitor_id: monitor.id })
      .then((slas) => {
        if (!cancelled) setCoveringSlas(slas);
      })
      .catch(() => {
        if (!cancelled) setCoveringSlas([]);
      });
    return () => {
      cancelled = true;
    };
  }, [monitor.id]);
  const slaTarget = strictestTarget(coveringSlas);
  const targetSla = coveringSlas.find((s) => s.target_pct === slaTarget);

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
  const downtimeCount = analytics?.downtime_periods?.length ?? 0;
  const latencyHeadlineLabel = summary?.p95_latency_ms !== undefined ? 'P95 latency' : 'Avg latency';
  const latencyHeadlineValue = summary?.p95_latency_ms ?? summary?.avg_latency_ms;
  const hasData = Boolean(chartData.some((point) => point.hasData));
  const hasLatencyData = Boolean(chartData.some((point) => typeof point.latency === 'number'));
  const summaryHasData = summary?.has_data ?? false;
  const headlinePct = summary?.availability_pct ?? summary?.sla_pct ?? 0;
  const isSlaCompliant = summaryHasData && headlinePct >= slaTarget;
  // Groups aggregate member state and never record a latency of their own.
  const showLatencyChart = monitor.type !== 'group';

  if (loading && !analytics) {
    return <div className="py-12 text-center text-sm text-slate-500">Loading monitor analytics...</div>;
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center gap-1.5">
        {OVERVIEW_RANGES.map((range) => (
          <FilterChip
            key={range}
            selected={timeRange === range}
            onClick={() => onTimeRangeChange(range)}
          >
            {range}
          </FilterChip>
        ))}
      </div>

      <div className="glass relative z-10 p-5">
        <div className="grid gap-4 lg:grid-cols-[minmax(0,280px)_minmax(0,1fr)] lg:items-center">
          <div className="flex justify-center lg:justify-start">
            <UptimeHeroGauge uptime={headlinePct} hasData={summaryHasData} target={slaTarget} />
          </div>

          <div className="space-y-5">
            <div>
              <div className="flex items-center gap-1">
                <h3 className="text-[15px] font-semibold text-white">SLA / uptime</h3>
                <MetadataPopover
                  source={analytics?.source}
                  coverage={analytics?.is_partial ? 'Partial' : 'Complete'}
                  coverageStart={analytics?.coverage_start}
                  generatedAt={analytics?.generated_at}
                />
              </div>
              <p className="mt-0.5 text-[13px] text-slate-400">
                Target {formatTarget(slaTarget)}
                {targetSla && (
                  <>
                    {' '}
                    <Link
                      href={`/slas/${targetSla.id}`}
                      className="text-slate-500 transition-colors hover:text-cyan-300"
                    >
                      ({targetSla.name})
                    </Link>
                  </>
                )}
                {summaryHasData && !isSlaCompliant && <span className="text-rose-300"> · below target</span>}
                {summary?.method === 'interval' && summary.coverage_pct !== undefined && (
                  <span> · {summary.coverage_pct.toFixed(1)}% of window observed</span>
                )}
              </p>
            </div>

            <div>
              <p className="text-xs text-slate-400">{latencyHeadlineLabel}</p>
              <p className="mt-1 text-2xl font-semibold tabular-nums text-white">{formatLatency(latencyHeadlineValue)}</p>
            </div>
          </div>
        </div>
      </div>

      <div className={`grid gap-4 ${showLatencyChart ? 'lg:grid-cols-2' : ''}`}>
        <div className="glass p-5">
          <div className="mb-4 flex items-center justify-between gap-3">
            <h3 className="text-[15px] font-semibold text-white">Uptime trend</h3>
            <span className="flex items-center gap-1.5 text-xs text-slate-400">
              <span className="h-2 w-2 rounded-full bg-emerald-500" />
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
                    tick={CHART_AXIS_TICK}
                    tickMargin={8}
                    minTickGap={24}
                    tickFormatter={(value) => formatBucketLabel(value, timeRange)}
                  />
                  <YAxis
                    domain={[0, 100]}
                    width={44}
                    axisLine={false}
                    tickLine={false}
                    tick={CHART_AXIS_TICK}
                    tickFormatter={(value) => `${value}%`}
                  />
                  <Tooltip cursor={CHART_CURSOR} content={<ChartTooltip kind="uptime" range={timeRange} />} />
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

        {showLatencyChart && <div className="glass p-5">
          <div className="mb-4 flex items-center justify-between gap-3">
            <h3 className="text-[15px] font-semibold text-white">Latency & downtime</h3>
            <span className="flex items-center gap-1.5 text-xs text-slate-400">
              <span className="h-2 w-2 rounded-full bg-cyan-500" />
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
                    tick={CHART_AXIS_TICK}
                    tickMargin={8}
                    minTickGap={24}
                    tickFormatter={(value) => formatBucketLabel(value, timeRange)}
                  />
                  <YAxis
                    width={52}
                    axisLine={false}
                    tickLine={false}
                    tick={CHART_AXIS_TICK}
                    tickFormatter={(value) => `${value}ms`}
                  />
                  <Tooltip cursor={CHART_CURSOR} content={<ChartTooltip kind="latency" range={timeRange} />} />
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
        </div>}
      </div>

      <div className="glass p-5">
        <div className="mb-3 flex items-center justify-between gap-3">
          <h3 className="text-[15px] font-semibold text-white">Downtime periods</h3>
          <span className="text-xs tabular-nums text-slate-500">
            {downtimeCount} {downtimeCount === 1 ? 'period' : 'periods'}
          </span>
        </div>
        <div className="divide-y divide-divider">
          {(analytics?.downtime_periods || []).slice(0, 8).map((period) => (
            <div key={`${period.start_time}-${period.end_time}`} className="flex flex-wrap items-center gap-x-2 py-2.5 text-sm tabular-nums">
              <span className="text-slate-300">{formatTime(period.start_time)}</span>
              <span className="text-slate-500">to</span>
              <span className="text-slate-300">{formatTime(period.end_time)}</span>
            </div>
          ))}
        </div>
        {(!analytics?.downtime_periods || analytics.downtime_periods.length === 0) && (
          <div className="glass-well px-4 py-6 text-center text-sm text-slate-500">
            No downtime periods in the selected range.
          </div>
        )}
      </div>

      <RecentResultsTable results={results} />
    </div>
  );
}

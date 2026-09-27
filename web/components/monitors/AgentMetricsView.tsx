'use client';

// Curated host overview for agent monitors, fed by the generic metric store
// (POST /monitors/{id}/metrics/query) instead of per-check metrics_data.

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  MetricQueryRefResult,
  MetricQueryResponse,
  MetricQuerySpec,
  MetricRule,
  MetricSeriesData,
} from '@/lib/types';
import { getMonitorMetricSeries, queryMonitorMetrics } from '@/lib/api';
import { displayAttr, formatBytes, formatDurationSeconds, ratioToPercent } from '@/lib/metrics';
import {
  ChartRow,
  ChartSeriesInput,
  MetricChart,
  RANGE_MS,
  RANGE_STEP_SECONDS,
  ReferenceThreshold,
  SERIES_COLORS,
  TimeRange,
  buildChartRows,
} from './metric-chart';

export type { TimeRange } from './metric-chart';

interface AgentMetricsViewProps {
  monitorId: string;
  expectedIntervalSeconds: number;
  metricRules?: MetricRule[];
  timeRange?: TimeRange;
  onTimeRangeChange?: (range: TimeRange) => void;
}

const MAX_MOUNTS = 6;
const POLL_INTERVAL_MS = 10000;

// Curated dashboard panels — one batch POST per range change.
// system.filesystem.utilization carries NO `state` attribute (hostmetrics
// emits {device, mode, mountpoint, type}; the value is already the used
// fraction per mount) — never filter it on state. Only system.filesystem.usage
// has a state attr (used/free/reserved).
const CHART_QUERIES: MetricQuerySpec[] = [
  { ref: 'cpu', metric_name: 'system.cpu.utilization', attribute_filters: { state: 'used' }, agg: 'avg' },
  { ref: 'mem', metric_name: 'system.memory.utilization', attribute_filters: { state: 'used' }, agg: 'avg' },
  { ref: 'swap', metric_name: 'system.paging.utilization', attribute_filters: { state: 'used' }, agg: 'avg' },
  { ref: 'fs', metric_name: 'system.filesystem.utilization', agg: 'avg' },
  { ref: 'diskio', metric_name: 'system.disk.io', agg: 'avg', rate: true },
  { ref: 'net', metric_name: 'system.network.io', agg: 'avg', rate: true },
];

// Stat cards and gauges: last values over a short trailing window.
const STAT_QUERIES: MetricQuerySpec[] = [
  { ref: 'mem_used', metric_name: 'system.memory.usage', attribute_filters: { state: 'used' }, agg: 'last' },
  { ref: 'swap_used', metric_name: 'system.paging.usage', attribute_filters: { state: 'used' }, agg: 'last' },
  { ref: 'load1', metric_name: 'system.cpu.load_average.1m', agg: 'last' },
  { ref: 'load5', metric_name: 'system.cpu.load_average.5m', agg: 'last' },
  { ref: 'load15', metric_name: 'system.cpu.load_average.15m', agg: 'last' },
  { ref: 'procs', metric_name: 'system.processes.count', agg: 'last' },
  { ref: 'uptime', metric_name: 'system.uptime', agg: 'last' },
  { ref: 'cores', metric_name: 'system.cpu.logical.count', agg: 'last' },
];

function formatRate(bytesPerSecond: number): string {
  if (!Number.isFinite(bytesPerSecond) || bytesPerSecond <= 0) return '0 B/s';
  return `${formatBytes(bytesPerSecond)}/s`;
}

function formatRateTick(bytesPerSecond: number): string {
  if (!Number.isFinite(bytesPerSecond) || bytesPerSecond <= 0) return '0 B/s';
  const k = 1024;
  const sizes = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
  const i = Math.min(Math.floor(Math.log(bytesPerSecond) / Math.log(k)), sizes.length - 1);
  const value = bytesPerSecond / Math.pow(k, i);
  const precision = value >= 100 ? 0 : 1;
  return `${value.toFixed(precision)} ${sizes[i]}`;
}

function formatTimeAgo(seconds: number): string {
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

function getStatusColor(percent: number): string {
  if (percent < 60) return '#2fbd6a';
  if (percent < 80) return '#dc9e26';
  return '#f04a5a';
}

function CircularProgress({
  value,
  label,
  color,
  displayValue,
}: {
  value: number;
  label: string;
  color: string;
  displayValue?: string;
}) {
  const normalizedValue = Math.max(0, Math.min(100, value));
  const radius = 40;
  const circumference = 2 * Math.PI * radius;
  const strokeDashoffset = circumference - (normalizedValue / 100) * circumference;

  return (
    <div className="flex flex-col items-center">
      <div className="relative w-24 h-24">
        <svg className="w-full h-full -rotate-90">
          <circle cx="48" cy="48" r={radius} fill="none" stroke="rgba(255,255,255,0.05)" strokeWidth="8" />
          <circle
            cx="48"
            cy="48"
            r={radius}
            fill="none"
            stroke={color}
            strokeWidth="8"
            strokeLinecap="round"
            strokeDasharray={circumference}
            strokeDashoffset={strokeDashoffset}
            className="transition-all duration-500"
          />
        </svg>
        <div className="absolute inset-0 flex items-center justify-center">
          <span className="text-lg font-semibold text-white">{displayValue ?? `${normalizedValue.toFixed(0)}%`}</span>
        </div>
      </div>
      <span className="mt-2 text-xs text-slate-500">{label}</span>
    </div>
  );
}

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
      <p className="text-xs text-slate-500">{label}</p>
      <p className="mt-1 text-sm font-medium text-white">{value}</p>
    </div>
  );
}

function lastPoint(series?: MetricSeriesData): [number, number] | null {
  const points = series?.points;
  if (!points || points.length === 0) return null;
  return points[points.length - 1];
}

function lastValue(series?: MetricSeriesData): number | null {
  return lastPoint(series)?.[1] ?? null;
}

// Client-side aggregation across fan-out series sharing a direction attribute
// (per-second rates are server-side; this just sums devices per bucket).
function sumByDirection(result: MetricQueryRefResult | undefined, direction: string): [number, number][] {
  const totals = new Map<number, number>();
  for (const series of result?.series || []) {
    if ((series.attributes.direction || '') !== direction) continue;
    for (const [ts, value] of series.points) {
      totals.set(ts, (totals.get(ts) || 0) + value);
    }
  }
  return Array.from(totals.entries())
    .sort((a, b) => a[0] - b[0])
    .map(([ts, value]) => [ts, value] as [number, number]);
}

function ruleFiltersMatchAnySeries(
  rule: MetricRule,
  seriesList: MetricSeriesData[]
): boolean {
  if (!rule.attribute_filters || Object.keys(rule.attribute_filters).length === 0) return true;
  if (seriesList.length === 0) return true;
  return seriesList.some((series) =>
    Object.entries(rule.attribute_filters || {}).every(([key, value]) => series.attributes[key] === value)
  );
}

export default function AgentMetricsView({
  monitorId,
  expectedIntervalSeconds,
  metricRules,
  timeRange: controlledTimeRange,
  onTimeRangeChange,
}: AgentMetricsViewProps) {
  const [localTimeRange, setLocalTimeRange] = useState<TimeRange>('24h');
  const [nowMs, setNowMs] = useState(() => Date.now());
  const [chartResponse, setChartResponse] = useState<MetricQueryResponse | null>(null);
  const [statsResponse, setStatsResponse] = useState<MetricQueryResponse | null>(null);
  // Real ingest recency: newest last_seen_at across discovered series. Chart
  // points are bucket STARTS (up to one step old), so they must never feed
  // the staleness check.
  const [lastSeenMs, setLastSeenMs] = useState(0);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const timeRange = controlledTimeRange ?? localTimeRange;

  const safeInterval = expectedIntervalSeconds > 0 ? expectedIntervalSeconds : 60;
  const stepSeconds = Math.min(86400, Math.max(10, RANGE_STEP_SECONDS[timeRange], safeInterval));

  const handleTimeRangeChange = (range: TimeRange) => {
    if (!controlledTimeRange) {
      setLocalTimeRange(range);
    }
    onTimeRangeChange?.(range);
  };

  const loadMetrics = useCallback(
    async (opts?: { silent?: boolean }) => {
      const now = Date.now();
      const end = new Date(now).toISOString();
      const start = new Date(now - RANGE_MS[timeRange]).toISOString();
      const statsWindowSeconds = Math.min(86400, Math.max(3 * safeInterval, 300));
      const statsStart = new Date(now - statsWindowSeconds * 1000).toISOString();

      try {
        if (!opts?.silent) setLoading(true);
        const [charts, stats, discovery] = await Promise.all([
          queryMonitorMetrics(monitorId, {
            start,
            end,
            step_seconds: stepSeconds,
            queries: CHART_QUERIES,
          }),
          queryMonitorMetrics(monitorId, {
            start: statsStart,
            end,
            step_seconds: statsWindowSeconds,
            queries: STAT_QUERIES,
          }),
          getMonitorMetricSeries(monitorId),
        ]);
        setChartResponse(charts);
        setStatsResponse(stats);
        setLastSeenMs(
          (discovery.items || []).reduce((newest, item) => {
            const seen = Date.parse(item.last_seen_at);
            return Number.isFinite(seen) && seen > newest ? seen : newest;
          }, 0)
        );
        setLoadError('');
        setNowMs(Date.now());
      } catch (err) {
        console.error('Failed to load agent metrics:', err);
        if (!opts?.silent) setLoadError('Failed to load metrics');
      } finally {
        if (!opts?.silent) setLoading(false);
      }
    },
    [monitorId, timeRange, stepSeconds, safeInterval]
  );

  useEffect(() => {
    void loadMetrics();
  }, [loadMetrics]);

  useEffect(() => {
    const intervalId = window.setInterval(() => {
      if (document.visibilityState !== 'visible') return;
      void loadMetrics({ silent: true });
    }, POLL_INTERVAL_MS);
    return () => {
      window.clearInterval(intervalId);
    };
  }, [loadMetrics]);

  const refResults = useMemo(() => {
    const map = new Map<string, MetricQueryRefResult>();
    for (const result of chartResponse?.results || []) map.set(result.ref, result);
    return map;
  }, [chartResponse]);

  const statResults = useMemo(() => {
    const map = new Map<string, MetricQueryRefResult>();
    for (const result of statsResponse?.results || []) map.set(result.ref, result);
    return map;
  }, [statsResponse]);

  const statValue = useCallback(
    (ref: string): number | null => lastValue(statResults.get(ref)?.series?.[0]),
    [statResults]
  );

  const cpuSeries = refResults.get('cpu')?.series?.[0];
  const memSeries = refResults.get('mem')?.series?.[0];
  const swapSeries = refResults.get('swap')?.series?.[0];
  const fsResult = refResults.get('fs');
  const diskioResult = refResults.get('diskio');
  const netResult = refResults.get('net');

  // Mounts present in the range (capped), each gets a synthetic chart key.
  // Prefer writable filesystems when the collector reports a mode attribute
  // (hides read-only system volumes, e.g. on macOS), and dedupe by mountpoint.
  const mountSeries = useMemo(() => {
    const all = fsResult?.series || [];
    const writable = all.filter((series) => series.attributes.mode === 'rw');
    const candidates = writable.length > 0 ? writable : all;
    const seenMounts = new Set<string>();
    const deduped = candidates
      .slice()
      .sort((a, b) => (displayAttr(a.attributes) || '').localeCompare(displayAttr(b.attributes) || ''))
      .filter((series) => {
        const mount = series.attributes.mountpoint || series.series_key;
        if (seenMounts.has(mount)) return false;
        seenMounts.add(mount);
        return true;
      });
    return deduped.slice(0, MAX_MOUNTS).map((series, index) => ({
      key: `mount${index}`,
      label: displayAttr(series.attributes) || series.series_key,
      color: SERIES_COLORS[index % SERIES_COLORS.length],
      series,
    }));
  }, [fsResult]);

  // Unified row grid for every chart so the synced charts share an x-domain,
  // cursor, and brush; missing buckets stay null and break the areas.
  const chartRows = useMemo<ChartRow[]>(() => {
    const stepMs = (refResults.get('cpu')?.step_seconds ?? stepSeconds) * 1000;
    const toPercent = (v: number) => v * 100;
    const inputs: ChartSeriesInput[] = [
      { key: 'cpu', points: cpuSeries?.points || [], transform: toPercent },
      { key: 'mem', points: memSeries?.points || [], transform: toPercent },
      { key: 'swap', points: swapSeries?.points || [], transform: toPercent },
      ...mountSeries.map((mount) => ({
        key: mount.key,
        points: mount.series.points,
        transform: toPercent,
      })),
      { key: 'diskRead', points: sumByDirection(diskioResult, 'read') },
      { key: 'diskWrite', points: sumByDirection(diskioResult, 'write') },
      { key: 'netIn', points: sumByDirection(netResult, 'receive') },
      { key: 'netOut', points: sumByDirection(netResult, 'transmit') },
    ];
    return buildChartRows(inputs, stepMs);
  }, [refResults, stepSeconds, cpuSeries, memSeries, swapSeries, mountSeries, diskioResult, netResult]);

  if (loading && !chartResponse) {
    return (
      <div className="flex items-center justify-center py-12">
        <div className="text-slate-500">Loading metrics...</div>
      </div>
    );
  }

  if (lastSeenMs === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-16">
        <div className="inline-flex items-center justify-center w-16 h-16 rounded-full bg-slate-800/50 mb-4">
          <svg className="w-8 h-8 text-slate-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z"
            />
          </svg>
        </div>
        <p className="text-sm font-medium text-white">No metrics available</p>
        <p className="text-xs text-slate-500 mt-1">
          {loadError || 'Waiting for the collector to push metrics'}
        </p>
      </div>
    );
  }

  const cpuRatio = lastValue(cpuSeries);
  const memRatio = lastValue(memSeries);
  const swapRatio = lastValue(swapSeries);

  const cpuAvailable = cpuRatio !== null;
  const cpuPercent = cpuRatio !== null ? cpuRatio * 100 : 0;
  const memoryPercent = memRatio !== null ? memRatio * 100 : 0;
  const swapAvailable = swapRatio !== null && swapRatio > 0;
  const swapPercent = swapRatio !== null ? swapRatio * 100 : 0;

  // Filesystem aggregate gauge: the fullest mount in range.
  let worstMountPercent = 0;
  let worstMountLabel = '';
  for (const mount of mountSeries) {
    const value = lastValue(mount.series);
    if (value !== null && value * 100 > worstMountPercent) {
      worstMountPercent = value * 100;
      worstMountLabel = mount.label;
    }
  }

  const memUsedBytes = statValue('mem_used');
  const memTotalBytes =
    memUsedBytes !== null && memRatio !== null && memRatio > 0 ? memUsedBytes / memRatio : null;
  const swapUsedBytes = statValue('swap_used');
  const swapTotalBytes =
    swapUsedBytes !== null && swapRatio !== null && swapRatio > 0 ? swapUsedBytes / swapRatio : null;
  const load1 = statValue('load1');
  const load5 = statValue('load5');
  const load15 = statValue('load15');
  const uptimeSeconds = statValue('uptime');
  const coreCount = statValue('cores');
  // Processes: sum across per-status series.
  const processCount = (statResults.get('procs')?.series || []).reduce((sum, series) => {
    const value = lastValue(series);
    return value !== null ? sum + value : sum;
  }, 0);
  const hasProcessData = (statResults.get('procs')?.series || []).length > 0;

  const latestRow = chartRows.length > 0 ? chartRows[chartRows.length - 1] : null;
  const latestInRate = latestRow?.netIn ?? 0;
  const latestOutRate = latestRow?.netOut ?? 0;

  const timeSinceReportSec = Math.max(0, Math.floor((nowMs - lastSeenMs) / 1000));
  const staleAfterSec = Math.max(3 * safeInterval, 120);
  const isStale = timeSinceReportSec > staleAfterSec;

  const percentTick = (value: number) => `${value.toFixed(0)}%`;
  const percentValue = (value: number) => `${value.toFixed(1)}%`;

  // Threshold ReferenceLines from the monitor's metric_rules: rules whose
  // metric matches the panel and whose filters fit the panel's series.
  const rules = metricRules || [];
  const ratioRuleLines = (
    metricName: string,
    seriesList: MetricSeriesData[],
    prefix: string
  ): ReferenceThreshold[] =>
    rules
      .filter((rule) => rule.metric_name === metricName && ruleFiltersMatchAnySeries(rule, seriesList))
      .map((rule) => ({
        y: ratioToPercent(rule.threshold),
        label: `${prefix} ${ratioToPercent(rule.threshold)}%`,
      }));
  const rateRuleLines = (metricName: string, seriesList: MetricSeriesData[]): ReferenceThreshold[] =>
    rules
      .filter((rule) => rule.metric_name === metricName && ruleFiltersMatchAnySeries(rule, seriesList))
      .map((rule) => ({ y: rule.threshold, label: `alert ${formatRateTick(rule.threshold)}` }));

  const cpuRefLines = ratioRuleLines('system.cpu.utilization', cpuSeries ? [cpuSeries] : [], 'alert');
  const memSwapRefLines = [
    ...ratioRuleLines('system.memory.utilization', memSeries ? [memSeries] : [], 'mem'),
    ...ratioRuleLines('system.paging.utilization', swapSeries ? [swapSeries] : [], 'swap'),
  ];
  const diskRefLines = ratioRuleLines(
    'system.filesystem.utilization',
    mountSeries.map((mount) => mount.series),
    'alert'
  );
  const diskIORefLines = rateRuleLines('system.disk.io', diskioResult?.series || []);
  const netRefLines = rateRuleLines('system.network.io', netResult?.series || []);

  const bucketCount = chartRows.reduce(
    (count, row) => (row.cpu !== null && row.cpu !== undefined ? count + 1 : count),
    0
  );

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between rounded-lg border border-white/[0.06] bg-slate-900/50 px-4 py-3">
        <div className="flex items-center gap-3">
          <div className={`h-2 w-2 rounded-full ${isStale ? 'bg-amber-500' : 'bg-emerald-500'}`} />
          <span className="text-sm text-slate-300">{isStale ? 'Agent stale' : 'Agent healthy'}</span>
        </div>
        <span className="text-xs text-slate-500">Last report: {formatTimeAgo(timeSinceReportSec)}</span>
      </div>

      {isStale && (
        <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 px-4 py-3 text-xs text-amber-300">
          No metrics received in the last {formatDurationSeconds(staleAfterSec)} — the collector may be
          stopped or unable to reach the platform.
        </div>
      )}

      {chartRows.length === 0 && (
        <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 px-4 py-3 text-xs text-amber-300">
          No points in the selected range. Charts remain empty for this range.
        </div>
      )}

      <div className="grid grid-cols-3 gap-6 rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
        <CircularProgress
          value={cpuPercent}
          label={coreCount !== null && coreCount > 0 ? `CPU · ${Math.round(coreCount)} cores` : 'CPU'}
          color={cpuAvailable ? getStatusColor(cpuPercent) : '#64748b'}
          displayValue={cpuAvailable ? `${cpuPercent.toFixed(0)}%` : 'N/A'}
        />
        <CircularProgress value={memoryPercent} label="Memory" color={getStatusColor(memoryPercent)} />
        <CircularProgress
          value={worstMountPercent}
          label={worstMountLabel ? `Disk · ${worstMountLabel}` : 'Disk'}
          color={getStatusColor(worstMountPercent)}
        />
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label="Memory"
          value={
            memUsedBytes !== null
              ? `${formatBytes(memUsedBytes)}${memTotalBytes !== null ? ` / ${formatBytes(memTotalBytes)}` : ''}`
              : 'N/A'
          }
        />
        <StatCard
          label="Swap"
          value={
            swapAvailable && swapUsedBytes !== null
              ? `${formatBytes(swapUsedBytes)}${swapTotalBytes !== null ? ` / ${formatBytes(swapTotalBytes)}` : ''}`
              : 'None'
          }
        />
        <StatCard
          label="Load Average"
          value={
            load1 !== null && load5 !== null && load15 !== null
              ? `${load1.toFixed(2)} · ${load5.toFixed(2)} · ${load15.toFixed(2)}`
              : 'N/A'
          }
        />
        <StatCard label="Processes" value={hasProcessData ? `${Math.round(processCount)}` : 'N/A'} />
        <StatCard label="Uptime" value={uptimeSeconds !== null ? formatDurationSeconds(uptimeSeconds) : 'N/A'} />
        <StatCard label="CPU Cores" value={coreCount !== null && coreCount > 0 ? `${Math.round(coreCount)}` : 'N/A'} />
      </div>

      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
        <div className="flex items-center justify-between mb-6">
          <div>
            <h3 className="text-sm font-medium text-white">Metrics Over Time</h3>
            <p className="text-xs text-slate-500 mt-1">
              {bucketCount} buckets at {formatDurationSeconds(stepSeconds)} resolution
            </p>
          </div>
          <div className="flex gap-1">
            {(['1h', '6h', '24h', '7d'] as const).map((range) => (
              <button
                key={range}
                onClick={() => handleTimeRangeChange(range)}
                className={`rounded-lg px-3 py-1 text-xs transition-colors ${
                  timeRange === range ? 'bg-cyan-500 text-white' : 'text-slate-400 hover:text-white bg-slate-800/50'
                }`}
              >
                {range}
              </button>
            ))}
          </div>
        </div>

        <div className="space-y-6">
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs text-slate-500">CPU Usage</span>
              <span className="text-xs font-mono text-slate-400">{cpuAvailable ? `${cpuPercent.toFixed(1)}%` : 'N/A'}</span>
            </div>
            <div className="rounded-lg border border-white/[0.06] bg-slate-950/50 p-3">
              <MetricChart
                data={chartRows}
                series={[{ key: 'cpu', name: 'CPU', color: '#ff8a5c' }]}
                range={timeRange}
                yDomain={[0, 100]}
                yTickFormatter={percentTick}
                valueFormatter={percentValue}
                referenceLines={cpuRefLines}
              />
            </div>
          </div>

          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs text-slate-500">Memory & Swap</span>
              <span className="text-xs font-mono text-slate-400">
                {memoryPercent.toFixed(1)}%{swapAvailable ? ` · swap ${swapPercent.toFixed(1)}%` : ''}
              </span>
            </div>
            <div className="rounded-lg border border-white/[0.06] bg-slate-950/50 p-3">
              <MetricChart
                data={chartRows}
                series={[
                  { key: 'mem', name: 'Memory', color: '#6fb5dd' },
                  { key: 'swap', name: 'Swap', color: '#f472b6' },
                ]}
                range={timeRange}
                yDomain={[0, 100]}
                yTickFormatter={percentTick}
                valueFormatter={percentValue}
                referenceLines={memSwapRefLines}
              />
            </div>
          </div>

          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs text-slate-500">Disk Usage</span>
              <span className="text-xs font-mono text-slate-400">
                {worstMountPercent > 0 ? `${worstMountPercent.toFixed(1)}%` : 'N/A'}
              </span>
            </div>
            <div className="rounded-lg border border-white/[0.06] bg-slate-950/50 p-3">
              <MetricChart
                data={chartRows}
                series={mountSeries.map((mount) => ({ key: mount.key, name: mount.label, color: mount.color }))}
                range={timeRange}
                yDomain={[0, 100]}
                yTickFormatter={percentTick}
                valueFormatter={percentValue}
                showBrush
                referenceLines={diskRefLines}
              />
            </div>
            {mountSeries.length > 0 && (
              <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-slate-500">
                {mountSeries.map((mount) => (
                  <span key={mount.key} className="flex items-center gap-1.5">
                    <span className="h-1.5 w-1.5 rounded-full" style={{ backgroundColor: mount.color }} />
                    {mount.label}
                  </span>
                ))}
              </div>
            )}
          </div>

          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs text-slate-500">Disk I/O</span>
              <div className="flex gap-4 text-xs text-slate-400">
                <span>R {formatRate(latestRow?.diskRead ?? 0)}</span>
                <span>W {formatRate(latestRow?.diskWrite ?? 0)}</span>
              </div>
            </div>
            <div className="rounded-lg border border-white/[0.06] bg-slate-950/50 p-3">
              <MetricChart
                data={chartRows}
                series={[
                  { key: 'diskRead', name: 'Read', color: '#2fbd6a' },
                  { key: 'diskWrite', name: 'Write', color: '#3b82f6' },
                ]}
                range={timeRange}
                yDomain={[0, 'auto']}
                yTickFormatter={formatRateTick}
                valueFormatter={formatRate}
                referenceLines={diskIORefLines}
              />
            </div>
          </div>

          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs text-slate-500">Network Throughput</span>
              <div className="flex gap-4 text-xs text-slate-400">
                <span>↓ {formatRate(latestInRate)}</span>
                <span>↑ {formatRate(latestOutRate)}</span>
              </div>
            </div>
            <div className="rounded-lg border border-white/[0.06] bg-slate-950/50 p-3">
              <MetricChart
                data={chartRows}
                series={[
                  { key: 'netIn', name: 'In', color: '#2fbd6a' },
                  { key: 'netOut', name: 'Out', color: '#3b82f6' },
                ]}
                range={timeRange}
                yDomain={[0, 'auto']}
                yTickFormatter={formatRateTick}
                valueFormatter={formatRate}
                referenceLines={netRefLines}
              />
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

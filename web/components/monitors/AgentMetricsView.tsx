'use client';

// Curated host overview for agent monitors, fed by the generic metric store
// (POST /monitors/{id}/metrics/query) instead of per-check metrics_data.

import { ReactNode, useCallback, useEffect, useMemo, useState } from 'react';
import { ChartColumn } from 'lucide-react';
import {
  MetricQueryRefResult,
  MetricQueryResponse,
  MetricQuerySpec,
  MetricRule,
  MetricSeriesData,
} from '@/lib/types';
import { getMonitorMetricSeries, queryMonitorMetrics } from '@/lib/api';
import { displayAttr, formatBytes, formatDurationSeconds, ratioToPercent } from '@/lib/metrics';
import { useNow } from '@/lib/useNow';
import {
  DEFAULT_UTILIZATION_ALERT,
  matchingMetricRules,
  UPTIME_TONE_VAR,
  UTILIZATION_WARN_BAND,
  utilizationTone,
  type UptimeTone,
} from '@/lib/status-tones';
import Callout from '@/components/ui/Callout';
import { RelativeTime } from '@/components/ui/RelativeTime';
import EmptyState from '@/components/ui/EmptyState';
import RangePicker from '@/components/ui/RangePicker';
import {
  ChartRow,
  ChartSeries,
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

const AGENT_RANGES: readonly TimeRange[] = ['1h', '6h', '24h', '7d'];
const MAX_MOUNTS = 6;
const POLL_INTERVAL_MS = 10000;
// Wide enough for rate ticks ("95.4 MB/s" must not wrap); shared by every synced chart so
// their plot areas, and the hover cursor, line up.
const AXIS_WIDTH = 80;

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

const TONE_RANK: Record<UptimeTone, number> = { none: 0, good: 1, warn: 2, bad: 3 };

const NO_RULE_TITLE = `No metric rule covers this series: amber from ${ratioToPercent(
  DEFAULT_UTILIZATION_ALERT - UTILIZATION_WARN_BAND
)}%, red from ${ratioToPercent(DEFAULT_UTILIZATION_ALERT)}%. Nothing alerts on it.`;

/** `alert at 85%` from the rules covering a ring's series, else `no alert rule`. */
function ruleCaption(
  metricName: string,
  attributes: Record<string, string> | undefined,
  rules: MetricRule[]
): string {
  const matching = matchingMetricRules(metricName, attributes, rules);
  if (matching.length === 0) return 'no alert rule';
  return `alert ${matching
    .map((rule) => `${rule.operator === '<=' ? 'at or below' : 'at'} ${ratioToPercent(rule.threshold)}%`)
    .join(', ')}`;
}

function CircularProgress({
  value,
  label,
  tone,
  caption,
  displayValue,
}: {
  value: number;
  label: string;
  /** From utilizationTone: the monitor's own metric rule, else the default. */
  tone: UptimeTone;
  caption?: string;
  displayValue?: string;
}) {
  const normalizedValue = Math.max(0, Math.min(100, value));
  const radius = 40;
  const circumference = 2 * Math.PI * radius;
  const strokeDashoffset = circumference - (normalizedValue / 100) * circumference;

  return (
    <div className="flex flex-col items-center">
      <div className="relative h-24 w-24">
        <svg className="h-full w-full -rotate-90">
          <circle cx="48" cy="48" r={radius} fill="none" strokeWidth="6" className="stroke-white/[0.07]" />
          <circle
            cx="48"
            cy="48"
            r={radius}
            fill="none"
            stroke={UPTIME_TONE_VAR[tone]}
            strokeWidth="6"
            strokeLinecap="round"
            strokeDasharray={circumference}
            strokeDashoffset={strokeDashoffset}
            className="transition-[stroke-dashoffset,stroke] duration-slow ease-emphasized"
          />
        </svg>
        <div className="absolute inset-0 flex items-center justify-center">
          <span className="text-lg font-semibold tabular-nums text-white">
            {displayValue ?? `${normalizedValue.toFixed(0)}%`}
          </span>
        </div>
      </div>
      <span className="mt-2 text-center text-xs text-slate-400">{label}</span>
      {caption && (
        <span
          className="mt-0.5 text-center text-2xs tabular-nums text-slate-500"
          title={caption === 'no alert rule' ? NO_RULE_TITLE : undefined}
        >
          {caption}
        </span>
      )}
    </div>
  );
}

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="glass-well flex items-baseline justify-between gap-3 px-4 py-3 sm:block sm:p-4">
      <p className="text-xs text-slate-400">{label}</p>
      <p className="text-sm font-semibold tabular-nums text-white sm:mt-1 sm:text-lg">{value}</p>
    </div>
  );
}

function ChartTile({
  title,
  headline,
  legend,
  children,
}: {
  title: string;
  headline: ReactNode;
  legend?: ChartSeries[];
  children: ReactNode;
}) {
  return (
    <div className="glass-well p-4">
      <div className="mb-3 flex items-center justify-between gap-3">
        <span className="text-ui font-medium text-slate-200">{title}</span>
        <span className="text-xs tabular-nums text-slate-400">{headline}</span>
      </div>
      {children}
      {legend && legend.length > 0 && (
        <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs text-slate-400">
          {legend.map((entry) => (
            <span key={entry.key} className="flex items-center gap-1.5">
              <span className="h-2 w-2 shrink-0 rounded-full" style={{ backgroundColor: entry.color }} aria-hidden="true" />
              {entry.name}
            </span>
          ))}
        </div>
      )}
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
  // The shared 15s clock keeps "stale" honest between polls.
  const clockMs = useNow('minute');
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
        <div className="text-sm text-slate-500">Loading metrics...</div>
      </div>
    );
  }

  if (lastSeenMs === 0) {
    return (
      <EmptyState
        icon={<ChartColumn strokeWidth={1.75} />}
        title="No metrics available"
        description={loadError || 'Waiting for the collector to push metrics'}
      />
    );
  }

  const cpuRatio = lastValue(cpuSeries);
  const memRatio = lastValue(memSeries);
  const swapRatio = lastValue(swapSeries);

  const cpuAvailable = cpuRatio !== null;
  const cpuPercent = cpuRatio !== null ? cpuRatio * 100 : 0;
  const memoryAvailable = memRatio !== null;
  const memoryPercent = memRatio !== null ? memRatio * 100 : 0;
  const swapAvailable = swapRatio !== null && swapRatio > 0;
  const swapPercent = swapRatio !== null ? swapRatio * 100 : 0;

  // Ring tones come from the monitor's own metric_rules (lib/status-tones
  // utilizationTone), so a ring is red exactly when its alert would fire.
  const rules = metricRules || [];
  const cpuTone = utilizationTone(cpuRatio, 'system.cpu.utilization', cpuSeries?.attributes, rules);
  const memoryTone = utilizationTone(memRatio, 'system.memory.utilization', memSeries?.attributes, rules);

  // Filesystem gauge: the mount closest to its alert (worst tone), the
  // fullest one among equals.
  let worstMountPercent = 0;
  let worstMountLabel = '';
  let worstMount: (typeof mountSeries)[number] | null = null;
  let diskTone: UptimeTone = 'none';
  for (const mount of mountSeries) {
    const value = lastValue(mount.series);
    if (value === null) continue;
    const tone = utilizationTone(value, 'system.filesystem.utilization', mount.series.attributes, rules);
    if (
      worstMount === null ||
      TONE_RANK[tone] > TONE_RANK[diskTone] ||
      (TONE_RANK[tone] === TONE_RANK[diskTone] && value * 100 > worstMountPercent)
    ) {
      worstMount = mount;
      diskTone = tone;
      worstMountPercent = value * 100;
      worstMountLabel = mount.label;
    }
  }
  const diskAvailable = worstMount !== null;

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

  const timeSinceReportSec = Math.max(0, Math.floor((Math.max(nowMs, clockMs) - lastSeenMs) / 1000));
  const staleAfterSec = Math.max(3 * safeInterval, 120);
  const isStale = timeSinceReportSec > staleAfterSec;

  const percentTick = (value: number) => `${value.toFixed(0)}%`;
  const percentValue = (value: number) => `${value.toFixed(1)}%`;

  // Threshold ReferenceLines from the monitor's metric_rules: rules whose
  // metric matches the panel and whose filters fit the panel's series.
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
      {isStale && (
        <Callout tone="warning">
          No metrics received in the last {formatDurationSeconds(staleAfterSec)} — the collector may be
          stopped or unable to reach the platform.
        </Callout>
      )}

      <div className="glass p-5">
        <div className="mb-5 flex items-center justify-between gap-3">
          <div className="flex items-center gap-2.5">
            <span className={`h-2 w-2 shrink-0 rounded-full ${isStale ? 'bg-amber-500' : 'bg-emerald-500'}`} aria-hidden="true" />
            <h3 className="text-title font-semibold text-white">{isStale ? 'Agent stale' : 'Agent healthy'}</h3>
          </div>
          <RelativeTime
            value={lastSeenMs > 0 ? lastSeenMs : null}
            prefix="Last report "
            fallback="No report yet"
            className="text-xs text-slate-500"
          />
        </div>

        <div className="grid grid-cols-3 gap-6">
          <CircularProgress
            value={cpuPercent}
            label={coreCount !== null && coreCount > 0 ? `CPU (${Math.round(coreCount)} cores)` : 'CPU'}
            tone={cpuTone}
            caption={ruleCaption('system.cpu.utilization', cpuSeries?.attributes, rules)}
            displayValue={cpuAvailable ? `${cpuPercent.toFixed(0)}%` : '—'}
          />
          <CircularProgress
            value={memoryPercent}
            label="Memory"
            tone={memoryTone}
            caption={ruleCaption('system.memory.utilization', memSeries?.attributes, rules)}
            displayValue={memoryAvailable ? undefined : '—'}
          />
          <CircularProgress
            value={worstMountPercent}
            label={worstMountLabel ? `Disk ${worstMountLabel}` : 'Disk'}
            tone={diskTone}
            caption={
              worstMount
                ? ruleCaption('system.filesystem.utilization', worstMount.series.attributes, rules)
                : undefined
            }
            displayValue={diskAvailable ? undefined : '—'}
          />
        </div>

        <div className="mt-6 grid gap-2 sm:grid-cols-2 sm:gap-3 lg:grid-cols-3">
          <StatCard
            label="Memory"
            value={
              memUsedBytes !== null
                ? `${formatBytes(memUsedBytes)}${memTotalBytes !== null ? ` / ${formatBytes(memTotalBytes)}` : ''}`
                : '—'
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
            label="Load average"
            value={
              load1 !== null && load5 !== null && load15 !== null
                ? `${load1.toFixed(2)} / ${load5.toFixed(2)} / ${load15.toFixed(2)}`
                : '—'
            }
          />
          <StatCard label="Processes" value={hasProcessData ? `${Math.round(processCount)}` : '—'} />
          <StatCard label="Uptime" value={uptimeSeconds !== null ? formatDurationSeconds(uptimeSeconds) : '—'} />
          <StatCard label="CPU cores" value={coreCount !== null && coreCount > 0 ? `${Math.round(coreCount)}` : '—'} />
        </div>
      </div>

      <div className="glass p-5">
        <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-title font-semibold text-white">Metrics over time</h3>
            <p className="mt-0.5 text-xs text-slate-500">
              {bucketCount} buckets at {formatDurationSeconds(stepSeconds)} resolution
            </p>
          </div>
          {/* A controlled range is picked in the page's tabs row; only a
              standalone view carries its own picker. */}
          {controlledTimeRange === undefined && (
            <RangePicker ranges={AGENT_RANGES} value={timeRange} onChange={handleTimeRangeChange} />
          )}
        </div>

        {chartRows.length === 0 ? (
          <div className="glass-well px-4 py-6 text-center text-sm text-slate-500">
            No points in the selected range.
          </div>
        ) : (
          <div className="space-y-4">
            <ChartTile title="CPU usage" headline={cpuAvailable ? `${cpuPercent.toFixed(1)}%` : '—'}>
              <MetricChart
                data={chartRows}
                series={[{ key: 'cpu', name: 'CPU', color: SERIES_COLORS[0] }]}
                range={timeRange}
                yDomain={[0, 100]}
                yTickFormatter={percentTick}
                valueFormatter={percentValue}
                referenceLines={cpuRefLines}
                yAxisWidth={AXIS_WIDTH}
              />
            </ChartTile>

            <ChartTile
              title="Memory & swap"
              headline={
                memoryAvailable
                  ? `${memoryPercent.toFixed(1)}%${swapAvailable ? `, swap ${swapPercent.toFixed(1)}%` : ''}`
                  : '—'
              }
            >
              <MetricChart
                data={chartRows}
                series={[
                  { key: 'mem', name: 'Memory', color: SERIES_COLORS[0] },
                  { key: 'swap', name: 'Swap', color: SERIES_COLORS[1] },
                ]}
                range={timeRange}
                yDomain={[0, 100]}
                yTickFormatter={percentTick}
                valueFormatter={percentValue}
                referenceLines={memSwapRefLines}
                yAxisWidth={AXIS_WIDTH}
              />
            </ChartTile>

            <ChartTile
              title="Disk usage"
              headline={worstMountPercent > 0 ? `${worstMountPercent.toFixed(1)}%` : '—'}
              legend={mountSeries.map((mount) => ({ key: mount.key, name: mount.label, color: mount.color }))}
            >
              <MetricChart
                data={chartRows}
                series={mountSeries.map((mount) => ({ key: mount.key, name: mount.label, color: mount.color }))}
                range={timeRange}
                yDomain={[0, 100]}
                yTickFormatter={percentTick}
                valueFormatter={percentValue}
                showBrush
                referenceLines={diskRefLines}
                yAxisWidth={AXIS_WIDTH}
              />
            </ChartTile>

            <ChartTile
              title="Disk I/O"
              headline={
                <span className="flex gap-4">
                  <span>R {formatRate(latestRow?.diskRead ?? 0)}</span>
                  <span>W {formatRate(latestRow?.diskWrite ?? 0)}</span>
                </span>
              }
            >
              <MetricChart
                data={chartRows}
                series={[
                  { key: 'diskRead', name: 'Read', color: SERIES_COLORS[2] },
                  { key: 'diskWrite', name: 'Write', color: SERIES_COLORS[3] },
                ]}
                range={timeRange}
                yDomain={[0, 'auto']}
                yTickFormatter={formatRateTick}
                valueFormatter={formatRate}
                referenceLines={diskIORefLines}
                yAxisWidth={AXIS_WIDTH}
              />
            </ChartTile>

            <ChartTile
              title="Network throughput"
              headline={
                <span className="flex gap-4">
                  <span>↓ {formatRate(latestInRate)}</span>
                  <span>↑ {formatRate(latestOutRate)}</span>
                </span>
              }
            >
              <MetricChart
                data={chartRows}
                series={[
                  { key: 'netIn', name: 'In', color: SERIES_COLORS[2] },
                  { key: 'netOut', name: 'Out', color: SERIES_COLORS[3] },
                ]}
                range={timeRange}
                yDomain={[0, 'auto']}
                yTickFormatter={formatRateTick}
                valueFormatter={formatRate}
                referenceLines={netRefLines}
                yAxisWidth={AXIS_WIDTH}
              />
            </ChartTile>
          </div>
        )}
      </div>
    </div>
  );
}

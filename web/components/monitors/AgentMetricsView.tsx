'use client';

import { useEffect, useMemo, useState } from 'react';
import {
  Area,
  AreaChart,
  Brush,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { AgentMetrics, CheckResult, MetricThresholdsConfig } from '@/lib/types';

interface AgentMetricsViewProps {
  results: CheckResult[];
  loading?: boolean;
  timeRange?: TimeRange;
  onTimeRangeChange?: (range: TimeRange) => void;
  thresholds?: MetricThresholdsConfig;
}

interface ReferenceThreshold {
  y: number;
  label: string;
}

export type TimeRange = '1h' | '6h' | '24h' | '7d';

interface MetricPoint {
  timestampMs: number;
  cpuPercent: number;
  cpuCores: number;
  memoryUsed: number;
  memoryTotal: number;
  swapUsed: number;
  swapTotal: number;
  diskUsed: number;
  diskTotal: number;
  diskMounts: { path: string; used: number; total: number; fstype: string }[];
  diskReadBytes: number;
  diskWriteBytes: number;
  networkBytesIn: number;
  networkBytesOut: number;
  loadAvg1: number;
  loadAvg5: number;
  loadAvg15: number;
  processCount: number;
  uptimeSeconds: number;
}

interface SeriesPoint {
  timestampMs: number;
  value: number;
}

// A unified chart row keyed by timestamp; gap rows have all-null values so
// Recharts (connectNulls={false}) breaks the area across reporting gaps.
type ChartRow = Record<string, number | null> & { ts: number };

const RANGE_MS: Record<TimeRange, number> = {
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
  '7d': 7 * 24 * 60 * 60 * 1000,
};

const GAP_MIN_THRESHOLD_MS: Record<TimeRange, number> = {
  '1h': 90 * 1000,
  '6h': 2 * 60 * 1000,
  '24h': 3 * 60 * 1000,
  '7d': 15 * 60 * 1000,
};

const GAP_MAX_THRESHOLD_MS: Record<TimeRange, number> = {
  '1h': 10 * 60 * 1000,
  '6h': 30 * 60 * 1000,
  '24h': 2 * 60 * 60 * 1000,
  '7d': 12 * 60 * 60 * 1000,
};

const MOUNT_COLORS = ['#e6b23f', '#ff8a5c', '#6fb5dd', '#46d17f', '#f472b6', '#fb923c'];
const MAX_MOUNTS = 6;
const SYNC_ID = 'agent-metrics';

function isAgentMetrics(metrics: unknown): metrics is AgentMetrics {
  if (!metrics || typeof metrics !== 'object') return false;
  const candidate = metrics as AgentMetrics;
  return (
    typeof candidate.cpu_percent === 'number' &&
    typeof candidate.memory_used === 'number' &&
    typeof candidate.memory_total === 'number' &&
    typeof candidate.disk_used === 'number' &&
    typeof candidate.disk_total === 'number' &&
    typeof candidate.network_bytes_in === 'number' &&
    typeof candidate.network_bytes_out === 'number' &&
    typeof candidate.load_avg_1 === 'number' &&
    typeof candidate.load_avg_5 === 'number' &&
    typeof candidate.load_avg_15 === 'number' &&
    typeof candidate.process_count === 'number'
  );
}

function toEpochMs(metricsTimestamp: string | undefined, createdAt: string): number {
  const metricsMs = metricsTimestamp ? Date.parse(metricsTimestamp) : Number.NaN;
  if (Number.isFinite(metricsMs)) return metricsMs;
  const createdMs = Date.parse(createdAt);
  if (Number.isFinite(createdMs)) return createdMs;
  return Date.now();
}

function safePercent(used: number, total: number): number {
  if (total <= 0) return 0;
  return (used / total) * 100;
}

function formatBytes(bytes: number): string {
  if (bytes <= 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), sizes.length - 1);
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`;
}

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

function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return 'N/A';
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
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

function formatTimeLabel(timestampMs: number, range: TimeRange): string {
  const date = new Date(timestampMs);
  if (range === '7d') {
    return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  }
  return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}

function getSeriesGapThresholdMs(timestamps: number[], timeRange: TimeRange): number {
  if (timestamps.length < 2) return GAP_MIN_THRESHOLD_MS[timeRange];

  const intervals: number[] = [];
  for (let i = 1; i < timestamps.length; i += 1) {
    const diffMs = timestamps[i] - timestamps[i - 1];
    if (diffMs > 0) intervals.push(diffMs);
  }

  if (intervals.length === 0) return GAP_MIN_THRESHOLD_MS[timeRange];

  intervals.sort((a, b) => a - b);
  const denseIdx = Math.min(intervals.length - 1, Math.floor(intervals.length * 0.1));
  const denseInterval = intervals[denseIdx];
  const dynamicThreshold = denseInterval * 2;

  return Math.min(
    Math.max(dynamicThreshold, GAP_MIN_THRESHOLD_MS[timeRange]),
    GAP_MAX_THRESHOLD_MS[timeRange]
  );
}

interface ChartSeries {
  key: string;
  name: string;
  color: string;
}

function MetricTooltip({
  active,
  payload,
  label,
  range,
  formatter,
}: {
  active?: boolean;
  payload?: Array<{ color?: string; name?: string; value?: number | null }>;
  label?: number;
  range: TimeRange;
  formatter: (value: number) => string;
}) {
  if (!active || !payload?.length || typeof label !== 'number') return null;
  const entries = payload.filter((entry) => entry.value !== null && entry.value !== undefined);
  if (entries.length === 0) return null;

  return (
    <div className="rounded-lg border border-white/10 bg-slate-900 px-3 py-2 shadow-xl">
      <p className="text-xs text-slate-400">{formatTimeLabel(label, range)}</p>
      {entries.map((entry, index) => (
        <p key={`${entry.name}-${index}`} className="mt-1 flex items-center gap-2 text-sm text-white">
          <span className="h-2 w-2 rounded-full" style={{ backgroundColor: entry.color }} />
          <span className="text-slate-400">{entry.name}:</span>
          <span className="font-medium">{formatter(entry.value as number)}</span>
        </p>
      ))}
    </div>
  );
}

function MetricChart({
  data,
  series,
  range,
  yDomain,
  yTickFormatter,
  valueFormatter,
  height = 180,
  showBrush = false,
  referenceLines,
}: {
  data: ChartRow[];
  series: ChartSeries[];
  range: TimeRange;
  yDomain?: [number | string, number | string];
  yTickFormatter: (value: number) => string;
  valueFormatter: (value: number) => string;
  height?: number;
  showBrush?: boolean;
  referenceLines?: ReferenceThreshold[];
}) {
  const hasData = data.some((row) => series.some((s) => row[s.key] !== null && row[s.key] !== undefined));
  if (!hasData) {
    return (
      <div className="flex items-center justify-center text-xs text-slate-500" style={{ height }}>
        No data in range
      </div>
    );
  }

  return (
    <div style={{ height }}>
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} syncId={SYNC_ID} margin={{ top: 6, right: 8, left: 0, bottom: 0 }}>
          <defs>
            {series.map((s) => (
              <linearGradient key={s.key} id={`agentGradient-${s.key}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={s.color} stopOpacity={0.25} />
                <stop offset="100%" stopColor={s.color} stopOpacity={0} />
              </linearGradient>
            ))}
          </defs>
          <XAxis
            dataKey="ts"
            type="number"
            domain={['dataMin', 'dataMax']}
            scale="time"
            axisLine={false}
            tickLine={false}
            tick={{ fill: '#64748b', fontSize: 11 }}
            tickFormatter={(value) => formatTimeLabel(value, range)}
          />
          <YAxis
            domain={yDomain ?? ['auto', 'auto']}
            axisLine={false}
            tickLine={false}
            width={56}
            tick={{ fill: '#64748b', fontSize: 11 }}
            tickFormatter={(value) => yTickFormatter(value)}
          />
          <Tooltip content={<MetricTooltip range={range} formatter={valueFormatter} />} />
          {referenceLines?.map((line) => (
            <ReferenceLine
              key={`ref-${line.label}-${line.y}`}
              y={line.y}
              stroke="#f04a5a"
              strokeDasharray="4 4"
              strokeOpacity={0.7}
              ifOverflow="extendDomain"
              label={{ value: line.label, position: 'insideTopRight', fill: '#f87171', fontSize: 10 }}
            />
          ))}
          {series.map((s) => (
            <Area
              key={s.key}
              type="monotone"
              dataKey={s.key}
              name={s.name}
              stroke={s.color}
              strokeWidth={2}
              fill={`url(#agentGradient-${s.key})`}
              connectNulls={false}
              dot={false}
              isAnimationActive={false}
            />
          ))}
          {showBrush && (
            <Brush
              dataKey="ts"
              height={20}
              travellerWidth={8}
              stroke="var(--border-default)"
              fill="rgba(16,19,26,0.6)"
              tickFormatter={(value) => formatTimeLabel(value as number, range)}
            />
          )}
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

export default function AgentMetricsView({
  results,
  loading = false,
  timeRange: controlledTimeRange,
  onTimeRangeChange,
  thresholds,
}: AgentMetricsViewProps) {
  const [localTimeRange, setLocalTimeRange] = useState<TimeRange>('24h');
  const [nowMs, setNowMs] = useState(() => Date.now());
  const timeRange = controlledTimeRange ?? localTimeRange;

  const handleTimeRangeChange = (range: TimeRange) => {
    if (!controlledTimeRange) {
      setLocalTimeRange(range);
    }
    onTimeRangeChange?.(range);
  };

  useEffect(() => {
    const intervalId = window.setInterval(() => {
      setNowMs(Date.now());
    }, 10000);

    return () => {
      window.clearInterval(intervalId);
    };
  }, []);

  const metricPoints = useMemo<MetricPoint[]>(() => {
    return results
      .filter((result): result is CheckResult & { metrics_data: AgentMetrics } => isAgentMetrics(result.metrics_data))
      .map((result) => {
        const metrics = result.metrics_data;
        return {
          timestampMs: toEpochMs(metrics.timestamp, result.created_at),
          cpuPercent: metrics.cpu_percent,
          cpuCores: metrics.cpu_cores ?? 0,
          memoryUsed: metrics.memory_used,
          memoryTotal: metrics.memory_total,
          swapUsed: metrics.swap_used ?? 0,
          swapTotal: metrics.swap_total ?? 0,
          diskUsed: metrics.disk_used,
          diskTotal: metrics.disk_total,
          diskMounts: metrics.disk_mounts ?? [],
          diskReadBytes: metrics.disk_read_bytes ?? 0,
          diskWriteBytes: metrics.disk_write_bytes ?? 0,
          networkBytesIn: metrics.network_bytes_in,
          networkBytesOut: metrics.network_bytes_out,
          loadAvg1: metrics.load_avg_1,
          loadAvg5: metrics.load_avg_5,
          loadAvg15: metrics.load_avg_15,
          processCount: metrics.process_count,
          uptimeSeconds: metrics.uptime_seconds ?? 0,
        };
      })
      .sort((a, b) => a.timestampMs - b.timestampMs);
  }, [results]);

  const filteredPoints = useMemo(() => {
    const cutoff = nowMs - RANGE_MS[timeRange];
    return metricPoints.filter((point) => point.timestampMs >= cutoff);
  }, [metricPoints, nowMs, timeRange]);

  const hasRangeData = filteredPoints.length > 0;
  const latestPoint = metricPoints.length > 0 ? metricPoints[metricPoints.length - 1] : null;

  // Mount paths present in the range (capped), each gets a synthetic chart key.
  const mountSeries = useMemo<Array<{ key: string; path: string; color: string }>>(() => {
    const seen: string[] = [];
    for (const point of filteredPoints) {
      for (const mount of point.diskMounts) {
        if (!seen.includes(mount.path)) seen.push(mount.path);
      }
    }
    return seen.slice(0, MAX_MOUNTS).map((path, index) => ({
      key: `mount${index}`,
      path,
      color: MOUNT_COLORS[index % MOUNT_COLORS.length],
    }));
  }, [filteredPoints]);

  // Unified dataset for every chart: one row per sample, plus all-null gap rows
  // so the synced charts share an x-domain, cursor, and brush.
  const chartData = useMemo<ChartRow[]>(() => {
    if (filteredPoints.length === 0) return [];
    const timestamps = filteredPoints.map((point) => point.timestampMs);
    const gapMs = getSeriesGapThresholdMs(timestamps, timeRange);

    const nullRow = (ts: number): ChartRow => {
      const row: ChartRow = { ts } as ChartRow;
      row.cpu = null;
      row.mem = null;
      row.swap = null;
      row.disk = null;
      row.netIn = null;
      row.netOut = null;
      row.diskRead = null;
      row.diskWrite = null;
      for (const mount of mountSeries) row[mount.key] = null;
      return row;
    };

    const rows: ChartRow[] = [];
    for (let i = 0; i < filteredPoints.length; i += 1) {
      const point = filteredPoints[i];
      const prev = i > 0 ? filteredPoints[i - 1] : null;
      const isGap = prev !== null && point.timestampMs - prev.timestampMs > gapMs;

      if (isGap && prev) {
        rows.push(nullRow((prev.timestampMs + point.timestampMs) / 2));
      }

      let netIn: number | null = null;
      let netOut: number | null = null;
      let diskRead: number | null = null;
      let diskWrite: number | null = null;
      if (prev && !isGap) {
        const dtSeconds = (point.timestampMs - prev.timestampMs) / 1000;
        if (dtSeconds > 0) {
          netIn = Math.max(0, point.networkBytesIn - prev.networkBytesIn) / dtSeconds;
          netOut = Math.max(0, point.networkBytesOut - prev.networkBytesOut) / dtSeconds;
          diskRead = Math.max(0, point.diskReadBytes - prev.diskReadBytes) / dtSeconds;
          diskWrite = Math.max(0, point.diskWriteBytes - prev.diskWriteBytes) / dtSeconds;
        }
      }

      const row: ChartRow = { ts: point.timestampMs } as ChartRow;
      row.cpu = point.cpuPercent >= 0 ? point.cpuPercent : null;
      row.mem = safePercent(point.memoryUsed, point.memoryTotal);
      row.swap = point.swapTotal > 0 ? safePercent(point.swapUsed, point.swapTotal) : null;
      row.disk = safePercent(point.diskUsed, point.diskTotal);
      row.netIn = netIn;
      row.netOut = netOut;
      row.diskRead = diskRead;
      row.diskWrite = diskWrite;
      for (const mount of mountSeries) {
        const found = point.diskMounts.find((m) => m.path === mount.path);
        row[mount.key] = found && found.total > 0 ? safePercent(found.used, found.total) : null;
      }
      rows.push(row);
    }
    return rows;
  }, [filteredPoints, timeRange, mountSeries]);

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <div className="text-slate-500">Loading metrics...</div>
      </div>
    );
  }

  if (!latestPoint) {
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
        <p className="text-xs text-slate-500 mt-1">Waiting for agent to report</p>
      </div>
    );
  }

  const memoryPercent = safePercent(latestPoint.memoryUsed, latestPoint.memoryTotal);
  const diskPercent = safePercent(latestPoint.diskUsed, latestPoint.diskTotal);
  const swapPercent = latestPoint.swapTotal > 0 ? safePercent(latestPoint.swapUsed, latestPoint.swapTotal) : 0;
  const swapAvailable = latestPoint.swapTotal > 0;
  const cpuAvailable = latestPoint.cpuPercent >= 0;
  const cpuPercent = cpuAvailable ? latestPoint.cpuPercent : 0;

  const latestRow = chartData.length > 0 ? chartData[chartData.length - 1] : null;
  const latestInRate = latestRow?.netIn ?? 0;
  const latestOutRate = latestRow?.netOut ?? 0;

  const timeSinceReportSec = Math.max(0, Math.floor((nowMs - latestPoint.timestampMs) / 1000));
  const isStale = timeSinceReportSec > 300;

  const percentTick = (value: number) => `${value.toFixed(0)}%`;
  const percentValue = (value: number) => `${value.toFixed(1)}%`;

  const thresholdLine = (value: number | undefined, label: string): ReferenceThreshold[] =>
    value != null && value > 0 ? [{ y: value, label: `${label} ${value}%` }] : [];
  const cpuRefLines = thresholdLine(thresholds?.cpu_percent, 'alert');
  const memSwapRefLines = [
    ...thresholdLine(thresholds?.memory_percent, 'mem'),
    ...thresholdLine(thresholds?.swap_percent, 'swap'),
  ];
  const diskRefLines = thresholdLine(thresholds?.disk_percent, 'alert');

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between rounded-lg border border-white/[0.06] bg-slate-900/50 px-4 py-3">
        <div className="flex items-center gap-3">
          <div className={`h-2 w-2 rounded-full ${isStale ? 'bg-amber-500' : 'bg-emerald-500'}`} />
          <span className="text-sm text-slate-300">{isStale ? 'Agent stale' : 'Agent healthy'}</span>
        </div>
        <span className="text-xs text-slate-500">Last report: {formatTimeAgo(timeSinceReportSec)}</span>
      </div>

      {!hasRangeData && (
        <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 px-4 py-3 text-xs text-amber-300">
          No points in the selected range. Charts remain empty for this range.
        </div>
      )}

      <div className="grid grid-cols-3 gap-6 rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
        <CircularProgress
          value={cpuPercent}
          label={latestPoint.cpuCores > 0 ? `CPU · ${latestPoint.cpuCores} cores` : 'CPU'}
          color={cpuAvailable ? getStatusColor(cpuPercent) : '#64748b'}
          displayValue={cpuAvailable ? `${cpuPercent.toFixed(0)}%` : 'N/A'}
        />
        <CircularProgress value={memoryPercent} label="Memory" color={getStatusColor(memoryPercent)} />
        <CircularProgress value={diskPercent} label="Disk" color={getStatusColor(diskPercent)} />
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard label="Memory" value={`${formatBytes(latestPoint.memoryUsed)} / ${formatBytes(latestPoint.memoryTotal)}`} />
        <StatCard
          label="Swap"
          value={swapAvailable ? `${formatBytes(latestPoint.swapUsed)} / ${formatBytes(latestPoint.swapTotal)}` : 'None'}
        />
        <StatCard label="Disk" value={`${formatBytes(latestPoint.diskUsed)} / ${formatBytes(latestPoint.diskTotal)}`} />
        <StatCard
          label="Load Average"
          value={`${latestPoint.loadAvg1.toFixed(2)} · ${latestPoint.loadAvg5.toFixed(2)} · ${latestPoint.loadAvg15.toFixed(2)}`}
        />
        <StatCard label="Processes" value={`${latestPoint.processCount}`} />
        <StatCard label="Uptime" value={formatUptime(latestPoint.uptimeSeconds)} />
        <StatCard label="CPU Cores" value={latestPoint.cpuCores > 0 ? `${latestPoint.cpuCores}` : 'N/A'} />
      </div>

      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
        <div className="flex items-center justify-between mb-6">
          <div>
            <h3 className="text-sm font-medium text-white">Metrics Over Time</h3>
            <p className="text-xs text-slate-500 mt-1">{filteredPoints.length} samples in selected range</p>
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
                data={chartData}
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
                data={chartData}
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
              <span className="text-xs font-mono text-slate-400">{diskPercent.toFixed(1)}%</span>
            </div>
            <div className="rounded-lg border border-white/[0.06] bg-slate-950/50 p-3">
              <MetricChart
                data={chartData}
                series={
                  mountSeries.length > 0
                    ? mountSeries.map((mount) => ({ key: mount.key, name: mount.path, color: mount.color }))
                    : [{ key: 'disk', name: 'Disk', color: '#e6b23f' }]
                }
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
                    {mount.path}
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
                data={chartData}
                series={[
                  { key: 'diskRead', name: 'Read', color: '#2fbd6a' },
                  { key: 'diskWrite', name: 'Write', color: '#3b82f6' },
                ]}
                range={timeRange}
                yDomain={[0, 'auto']}
                yTickFormatter={formatRateTick}
                valueFormatter={formatRate}
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
                data={chartData}
                series={[
                  { key: 'netIn', name: 'In', color: '#2fbd6a' },
                  { key: 'netOut', name: 'Out', color: '#3b82f6' },
                ]}
                range={timeRange}
                yDomain={[0, 'auto']}
                yTickFormatter={formatRateTick}
                valueFormatter={formatRate}
              />
            </div>
            <div className="mt-2 flex gap-4 text-[11px] text-slate-500">
              <span>Total in: {formatBytes(latestPoint.networkBytesIn)}</span>
              <span>Total out: {formatBytes(latestPoint.networkBytesOut)}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

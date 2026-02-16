'use client';

import { useEffect, useMemo, useState } from 'react';
import { AgentMetrics, CheckResult } from '@/lib/types';

interface AgentMetricsViewProps {
  results: CheckResult[];
  loading?: boolean;
  timeRange?: TimeRange;
  onTimeRangeChange?: (range: TimeRange) => void;
}

export type TimeRange = '1h' | '6h' | '24h' | '7d';

interface MetricPoint {
  timestampMs: number;
  cpuPercent: number;
  memoryUsed: number;
  memoryTotal: number;
  diskUsed: number;
  diskTotal: number;
  networkBytesIn: number;
  networkBytesOut: number;
  loadAvg1: number;
  loadAvg5: number;
  loadAvg15: number;
  processCount: number;
}

interface SeriesPoint {
  timestampMs: number;
  value: number;
}

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

function getStatusColor(percent: number): string {
  if (percent < 60) return '#22c55e';
  if (percent < 80) return '#f59e0b';
  return '#ef4444';
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

function formatTimeLabel(timestampMs: number, range: TimeRange): string {
  const date = new Date(timestampMs);
  if (range === '7d') {
    return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  }
  return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}

function getSeriesGapThresholdMs(series: SeriesPoint[], timeRange: TimeRange): number {
  if (series.length < 2) return GAP_MIN_THRESHOLD_MS[timeRange];

  const intervals: number[] = [];
  for (let i = 1; i < series.length; i += 1) {
    const diffMs = series[i].timestampMs - series[i - 1].timestampMs;
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

function splitSeriesByGaps(series: SeriesPoint[], timeRange: TimeRange): SeriesPoint[][] {
  if (series.length === 0) return [];
  if (series.length === 1) return [[series[0]]];

  const thresholdMs = getSeriesGapThresholdMs(series, timeRange);
  const segments: SeriesPoint[][] = [];
  let current: SeriesPoint[] = [series[0]];

  for (let i = 1; i < series.length; i += 1) {
    const point = series[i];
    const prev = series[i - 1];
    const diffMs = point.timestampMs - prev.timestampMs;

    if (diffMs > thresholdMs) {
      segments.push(current);
      current = [point];
      continue;
    }

    current.push(point);
  }

  if (current.length > 0) {
    segments.push(current);
  }

  return segments;
}

function TimeSeriesChart({
  series,
  color = '#22d3ee',
  yAxisLabel,
  yValueFormatter,
  timeRange,
  fixedMin,
  fixedMax,
  height = 88,
}: {
  series: SeriesPoint[];
  color?: string;
  yAxisLabel: string;
  yValueFormatter: (value: number) => string;
  timeRange: TimeRange;
  fixedMin?: number;
  fixedMax?: number;
  height?: number;
}) {
  const normalizedSeries = series
    .filter((point) => Number.isFinite(point.timestampMs) && Number.isFinite(point.value))
    .sort((a, b) => a.timestampMs - b.timestampMs);

  if (normalizedSeries.length < 2) {
    return <div className="h-20 flex items-center justify-center text-xs text-slate-500">No data in range</div>;
  }

  const values = normalizedSeries.map((point) => point.value).filter((value) => Number.isFinite(value));
  if (values.length < 2) {
    return <div className="h-20 flex items-center justify-center text-xs text-slate-500">No data in range</div>;
  }

  const rawMin = Math.min(...values);
  const rawMax = Math.max(...values);

  let min = fixedMin ?? rawMin;
  let max = fixedMax ?? rawMax;

  if (max - min < 0.0001) {
    const pad = Math.max(Math.abs(max) * 0.05, 1);
    min -= pad;
    max += pad;
  }

  const yRange = max - min;
  const startTs = normalizedSeries[0].timestampMs;
  const endTs = normalizedSeries[normalizedSeries.length - 1].timestampMs;
  const xRange = Math.max(endTs - startTs, 1);

  const toSvgPoint = (point: SeriesPoint) => {
    const x = ((point.timestampMs - startTs) / xRange) * 100;
    const y = 100 - ((point.value - min) / yRange) * 100;
    return { x, y };
  };

  const segments = splitSeriesByGaps(normalizedSeries, timeRange).map((segment) => segment.map(toSvgPoint));
  const polylineSegments = segments.filter((segment) => segment.length >= 2);
  const singlePoints = segments.filter((segment) => segment.length === 1).map((segment) => segment[0]);

  const yTicks = [max, min + yRange / 2, min];
  const xTicks = [startTs, startTs + xRange / 2, endTs];

  return (
    <div>
      <div className="grid grid-cols-[56px_1fr] gap-2 items-stretch">
        <div
          className="flex flex-col justify-between text-[10px] text-slate-500 pr-1 select-none"
          style={{ height }}
          aria-label={`${yAxisLabel} axis ticks`}
        >
          {yTicks.map((tick, index) => (
            <span key={`${tick}-${index}`} className="leading-none">
              {yValueFormatter(tick)}
            </span>
          ))}
        </div>
        <div className="relative" style={{ height }}>
          <div className="pointer-events-none absolute inset-0 flex flex-col justify-between">
            <div className="border-t border-white/[0.08]" />
            <div className="border-t border-white/[0.06]" />
            <div className="border-t border-white/[0.08]" />
          </div>
          <svg viewBox="0 0 100 100" preserveAspectRatio="none" className="relative z-10 h-full w-full">
            {polylineSegments.map((segment, index) => (
              <polyline
                key={`seg-${index}`}
                fill="none"
                stroke={color}
                strokeWidth="2"
                points={segment.map((point) => `${point.x},${point.y}`).join(' ')}
                strokeLinecap="round"
                strokeLinejoin="round"
                vectorEffect="non-scaling-stroke"
              />
            ))}
            {singlePoints.map((point, index) => (
              <circle
                key={`single-${index}`}
                cx={point.x}
                cy={point.y}
                r="1.8"
                fill={color}
                vectorEffect="non-scaling-stroke"
              />
            ))}
          </svg>
        </div>
      </div>
      <div className="mt-2 grid grid-cols-[56px_1fr] gap-2">
        <span className="text-[10px] uppercase tracking-wide text-slate-600">{yAxisLabel}</span>
        <div className="flex items-center justify-between text-[10px] text-slate-500">
          {xTicks.map((tick, index) => (
            <span key={`${tick}-${index}`}>{formatTimeLabel(tick, timeRange)}</span>
          ))}
        </div>
      </div>
      <div className="pl-[64px] mt-0.5 text-[10px] uppercase tracking-wide text-slate-600">Time</div>
    </div>
  );
}

export default function AgentMetricsView({
  results,
  loading = false,
  timeRange: controlledTimeRange,
  onTimeRangeChange,
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
          memoryUsed: metrics.memory_used,
          memoryTotal: metrics.memory_total,
          diskUsed: metrics.disk_used,
          diskTotal: metrics.disk_total,
          networkBytesIn: metrics.network_bytes_in,
          networkBytesOut: metrics.network_bytes_out,
          loadAvg1: metrics.load_avg_1,
          loadAvg5: metrics.load_avg_5,
          loadAvg15: metrics.load_avg_15,
          processCount: metrics.process_count,
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

  const cpuSeries: SeriesPoint[] = filteredPoints
    .filter((point) => point.cpuPercent >= 0)
    .map((point) => ({ timestampMs: point.timestampMs, value: point.cpuPercent }));
  const memorySeries: SeriesPoint[] = filteredPoints.map((point) => ({
    timestampMs: point.timestampMs,
    value: safePercent(point.memoryUsed, point.memoryTotal),
  }));
  const diskSeries: SeriesPoint[] = filteredPoints.map((point) => ({
    timestampMs: point.timestampMs,
    value: safePercent(point.diskUsed, point.diskTotal),
  }));

  const networkRateData = useMemo(() => {
    const rates: Array<{ timestampMs: number; inRate: number; outRate: number }> = [];

    for (let i = 1; i < filteredPoints.length; i += 1) {
      const previous = filteredPoints[i - 1];
      const current = filteredPoints[i];
      const dtSeconds = (current.timestampMs - previous.timestampMs) / 1000;
      if (dtSeconds <= 0) continue;

      const inDelta = current.networkBytesIn - previous.networkBytesIn;
      const outDelta = current.networkBytesOut - previous.networkBytesOut;

      rates.push({
        timestampMs: current.timestampMs,
        inRate: inDelta >= 0 ? inDelta / dtSeconds : 0,
        outRate: outDelta >= 0 ? outDelta / dtSeconds : 0,
      });
    }

    return rates;
  }, [filteredPoints]);

  const networkInRateSeries: SeriesPoint[] = networkRateData.map((point) => ({
    timestampMs: point.timestampMs,
    value: point.inRate,
  }));
  const networkOutRateSeries: SeriesPoint[] = networkRateData.map((point) => ({
    timestampMs: point.timestampMs,
    value: point.outRate,
  }));

  const latestInRate = networkRateData.length > 0 ? networkRateData[networkRateData.length - 1].inRate : 0;
  const latestOutRate = networkRateData.length > 0 ? networkRateData[networkRateData.length - 1].outRate : 0;

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
  const cpuAvailable = latestPoint.cpuPercent >= 0;
  const cpuPercent = cpuAvailable ? latestPoint.cpuPercent : 0;

  const timeSinceReportSec = Math.max(0, Math.floor((nowMs - latestPoint.timestampMs) / 1000));
  const isStale = timeSinceReportSec > 300;

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
          label="CPU"
          color={cpuAvailable ? getStatusColor(cpuPercent) : '#64748b'}
          displayValue={cpuAvailable ? `${cpuPercent.toFixed(0)}%` : 'N/A'}
        />
        <CircularProgress value={memoryPercent} label="Memory" color={getStatusColor(memoryPercent)} />
        <CircularProgress value={diskPercent} label="Disk" color={getStatusColor(diskPercent)} />
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
          <p className="text-xs text-slate-500">Memory</p>
          <p className="mt-1 text-sm font-medium text-white">
            {formatBytes(latestPoint.memoryUsed)} / {formatBytes(latestPoint.memoryTotal)}
          </p>
        </div>
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
          <p className="text-xs text-slate-500">Disk</p>
          <p className="mt-1 text-sm font-medium text-white">
            {formatBytes(latestPoint.diskUsed)} / {formatBytes(latestPoint.diskTotal)}
          </p>
        </div>
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
          <p className="text-xs text-slate-500">Load Average</p>
          <p className="mt-1 text-sm font-medium text-white">
            {latestPoint.loadAvg1.toFixed(2)} · {latestPoint.loadAvg5.toFixed(2)} · {latestPoint.loadAvg15.toFixed(2)}
          </p>
        </div>
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
          <p className="text-xs text-slate-500">Processes</p>
          <p className="mt-1 text-sm font-medium text-white">{latestPoint.processCount}</p>
        </div>
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
              <TimeSeriesChart
                series={cpuSeries}
                color="#22d3ee"
                yAxisLabel="CPU %"
                yValueFormatter={(value) => `${value.toFixed(0)}%`}
                timeRange={timeRange}
                fixedMin={0}
                fixedMax={100}
              />
            </div>
          </div>

          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs text-slate-500">Memory Usage</span>
              <span className="text-xs font-mono text-slate-400">{memoryPercent.toFixed(1)}%</span>
            </div>
            <div className="rounded-lg border border-white/[0.06] bg-slate-950/50 p-3">
              <TimeSeriesChart
                series={memorySeries}
                color="#a78bfa"
                yAxisLabel="Memory %"
                yValueFormatter={(value) => `${value.toFixed(0)}%`}
                timeRange={timeRange}
                fixedMin={0}
                fixedMax={100}
              />
            </div>
          </div>

          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs text-slate-500">Disk Usage</span>
              <span className="text-xs font-mono text-slate-400">{diskPercent.toFixed(1)}%</span>
            </div>
            <div className="rounded-lg border border-white/[0.06] bg-slate-950/50 p-3">
              <TimeSeriesChart
                series={diskSeries}
                color="#fbbf24"
                yAxisLabel="Disk %"
                yValueFormatter={(value) => `${value.toFixed(0)}%`}
                timeRange={timeRange}
                fixedMin={0}
                fixedMax={100}
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
            <div className="grid grid-cols-2 gap-3">
              <div className="rounded-lg border border-white/[0.06] bg-slate-950/50 p-3">
                <TimeSeriesChart
                  series={networkInRateSeries}
                  color="#22c55e"
                  yAxisLabel="In Rate"
                  yValueFormatter={(value) => formatRateTick(value)}
                  timeRange={timeRange}
                />
              </div>
              <div className="rounded-lg border border-white/[0.06] bg-slate-950/50 p-3">
                <TimeSeriesChart
                  series={networkOutRateSeries}
                  color="#3b82f6"
                  yAxisLabel="Out Rate"
                  yValueFormatter={(value) => formatRateTick(value)}
                  timeRange={timeRange}
                />
              </div>
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

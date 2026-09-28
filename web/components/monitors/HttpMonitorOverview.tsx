'use client';

import React, { useState, useRef, useCallback, useMemo } from 'react';
import { CheckResult, Monitor, HTTPMetricsEnvelope, HTTPTimingInfo, HTTPTLSInfo } from '@/lib/types';
import { calculateUptime, calculateLatencyStats, getOperationalResults } from '@/lib/monitor-utils';
import { UptimeHeroGauge } from './UptimeHeroGauge';
import { CHART_TOOLTIP_CLASS } from './metric-chart';
import Callout from '@/components/ui/Callout';
import FilterChip from '@/components/ui/FilterChip';
import Pill, { PillTone } from '@/components/ui/Pill';

export type TimeRange = '1h' | '6h' | '24h' | '7d';

const RANGE_MS: Record<TimeRange, number> = {
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
  '7d': 7 * 24 * 60 * 60 * 1000,
};

const GAP_MIN_MS: Record<TimeRange, number> = {
  '1h': 90 * 1000,
  '6h': 2 * 60 * 1000,
  '24h': 3 * 60 * 1000,
  '7d': 15 * 60 * 1000,
};

const GAP_MAX_MS: Record<TimeRange, number> = {
  '1h': 10 * 60 * 1000,
  '6h': 30 * 60 * 1000,
  '24h': 2 * 60 * 60 * 1000,
  '7d': 12 * 60 * 60 * 1000,
};

interface SeriesPoint {
  timestampMs: number;
  value: number;
  status: CheckResult['status'];
}

function getHttpMetrics(result: CheckResult): HTTPMetricsEnvelope['http'] | undefined {
  const envelope = result.metrics_data as HTTPMetricsEnvelope | undefined;
  return envelope?.http;
}

function formatMs(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
  return `${ms.toFixed(0)}ms`;
}

function formatTimeLabel(timestampMs: number, range: TimeRange): string {
  const date = new Date(timestampMs);
  if (range === '7d') {
    return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  }
  return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}

function formatTimestamp(timestampMs: number): string {
  return new Date(timestampMs).toISOString().replace('T', ' ').slice(0, 19) + ' UTC';
}

function getTimeAgo(dateStr: string): string {
  const diffSec = Math.floor((Date.now() - Date.parse(dateStr)) / 1000);
  if (diffSec < 60) return `${diffSec}s ago`;
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;
  return `${Math.floor(diffSec / 86400)}d ago`;
}

function getGapThreshold(series: SeriesPoint[], timeRange: TimeRange): number {
  if (series.length < 2) return GAP_MIN_MS[timeRange];
  const intervals: number[] = [];
  for (let i = 1; i < series.length; i++) {
    const diff = series[i].timestampMs - series[i - 1].timestampMs;
    if (diff > 0) intervals.push(diff);
  }
  if (!intervals.length) return GAP_MIN_MS[timeRange];
  intervals.sort((a, b) => a - b);
  const denseInterval = intervals[Math.floor(intervals.length * 0.1)];
  return Math.min(Math.max(denseInterval * 2, GAP_MIN_MS[timeRange]), GAP_MAX_MS[timeRange]);
}

function splitByGaps(series: SeriesPoint[], timeRange: TimeRange): SeriesPoint[][] {
  if (!series.length) return [];
  const threshold = getGapThreshold(series, timeRange);
  const segments: SeriesPoint[][] = [];
  let current: SeriesPoint[] = [series[0]];
  for (let i = 1; i < series.length; i++) {
    if (series[i].timestampMs - series[i - 1].timestampMs > threshold) {
      segments.push(current);
      current = [series[i]];
    } else {
      current.push(series[i]);
    }
  }
  segments.push(current);
  return segments;
}

// ─── Latency Time Series Chart ──────────────────────────────────────────────

function LatencyTimeSeriesChart({
  series,
  timeRange,
}: {
  series: SeriesPoint[];
  timeRange: TimeRange;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<{ xPct: number; point: SeriesPoint } | null>(null);

  const sorted = useMemo(
    () => [...series].sort((a, b) => a.timestampMs - b.timestampMs),
    [series],
  );

  const handleMouseMove = useCallback(
    (e: React.MouseEvent<HTMLDivElement>) => {
      if (!containerRef.current || !sorted.length) return;
      const rect = containerRef.current.getBoundingClientRect();
      const xRatio = (e.clientX - rect.left) / rect.width;
      const startTs = sorted[0].timestampMs;
      const endTs = sorted[sorted.length - 1].timestampMs;
      const xSpan = Math.max(endTs - startTs, 1);
      const targetTs = startTs + xRatio * xSpan;

      let nearest = sorted[0];
      let minDiff = Math.abs(nearest.timestampMs - targetTs);
      for (const pt of sorted) {
        const diff = Math.abs(pt.timestampMs - targetTs);
        if (diff < minDiff) {
          minDiff = diff;
          nearest = pt;
        }
      }
      const nearestXPct = (nearest.timestampMs - startTs) / xSpan;
      setHover({ xPct: nearestXPct, point: nearest });
    },
    [sorted],
  );

  if (sorted.length < 2) {
    return (
      <div className="glass-well flex h-24 items-center justify-center text-sm text-slate-500">
        Not enough data in selected range
      </div>
    );
  }

  const values = sorted.map((p) => p.value).filter(Number.isFinite);
  const rawMin = Math.min(...values);
  const rawMax = Math.max(...values);
  const pad = rawMax - rawMin < 1 ? 10 : (rawMax - rawMin) * 0.12;
  const min = Math.max(0, rawMin - pad);
  const max = rawMax + pad;
  const yRange = max - min;
  const startTs = sorted[0].timestampMs;
  const endTs = sorted[sorted.length - 1].timestampMs;
  const xSpan = Math.max(endTs - startTs, 1);

  const toSvg = (p: SeriesPoint) => ({
    x: ((p.timestampMs - startTs) / xSpan) * 100,
    y: 100 - ((p.value - min) / yRange) * 100,
  });

  // Build colored polyline segments (green for success, red for failure)
  interface ColoredSegment {
    points: Array<{ x: number; y: number }>;
    isSuccess: boolean;
  }
  const coloredSegments: ColoredSegment[] = [];
  for (const seg of splitByGaps(sorted, timeRange)) {
    if (seg.length < 2) continue;
    for (let i = 0; i < seg.length - 1; i++) {
      const isSuccess = seg[i + 1].status === 'success';
      const last = coloredSegments[coloredSegments.length - 1];
      if (last && last.isSuccess === isSuccess) {
        last.points.push(toSvg(seg[i + 1]));
      } else {
        coloredSegments.push({ points: [toSvg(seg[i]), toSvg(seg[i + 1])], isSuccess });
      }
    }
  }

  const yTicks = [max, min + yRange / 2, min];
  const xTicks = [startTs, startTs + xSpan / 2, endTs];
  const chartHeight = 100;
  const hoverSvgPoint = hover ? toSvg(hover.point) : null;

  return (
    <div>
      <div className="grid grid-cols-[56px_1fr] items-stretch gap-2">
        <div
          className="flex select-none flex-col justify-between pr-1 text-[11px] tabular-nums text-slate-500"
          style={{ height: chartHeight }}
        >
          {yTicks.map((tick, i) => (
            <span key={i} className="leading-none">
              {formatMs(tick)}
            </span>
          ))}
        </div>
        <div
          ref={containerRef}
          className="relative cursor-crosshair"
          style={{ height: chartHeight }}
          onMouseMove={handleMouseMove}
          onMouseLeave={() => setHover(null)}
        >
          {/* Grid lines */}
          <div className="pointer-events-none absolute inset-0 flex flex-col justify-between">
            <div className="border-t border-divider" />
            <div className="border-t border-divider" />
            <div className="border-t border-divider" />
          </div>

          <svg
            viewBox="0 0 100 100"
            preserveAspectRatio="none"
            className="relative z-10 h-full w-full"
          >
            {coloredSegments.map((seg, i) => (
              <polyline
                key={i}
                fill="none"
                stroke={seg.isSuccess ? '#2fbd6a' : '#f04a5a'}
                strokeWidth="2"
                strokeOpacity="0.85"
                points={seg.points.map((p) => `${p.x},${p.y}`).join(' ')}
                strokeLinecap="round"
                strokeLinejoin="round"
                vectorEffect="non-scaling-stroke"
              />
            ))}
            {hover && (
              <line
                x1={hover.xPct * 100}
                y1={0}
                x2={hover.xPct * 100}
                y2={100}
                className="stroke-white/20"
                strokeWidth="1"
                vectorEffect="non-scaling-stroke"
              />
            )}
            {hoverSvgPoint && (
              <circle
                cx={hoverSvgPoint.x}
                cy={hoverSvgPoint.y}
                r="3"
                fill={hover!.point.status === 'success' ? '#2fbd6a' : '#f04a5a'}
                vectorEffect="non-scaling-stroke"
              />
            )}
          </svg>

          {/* Hover tooltip */}
          {hover && (
            <div
              className="pointer-events-none absolute top-0 z-20"
              style={{
                left: `${hover.xPct * 100}%`,
                transform:
                  hover.xPct > 0.72
                    ? 'translateX(-100%) translateX(-10px)'
                    : 'translateX(10px)',
              }}
            >
              <div className={`${CHART_TOOLTIP_CLASS} whitespace-nowrap`}>
                <div className="font-mono text-[11px] text-slate-400">
                  {formatTimestamp(hover.point.timestampMs)}
                </div>
                <div className="mt-1 flex items-center gap-2">
                  <span
                    className={`h-2 w-2 rounded-full ${hover.point.status === 'success' ? 'bg-emerald-500' : 'bg-rose-500'}`}
                  />
                  <span className="text-sm font-semibold tabular-nums text-white">
                    {formatMs(hover.point.value)}
                  </span>
                  <span className="text-xs text-slate-400">{hover.point.status}</span>
                </div>
              </div>
            </div>
          )}
        </div>
      </div>

      <div className="mt-2 grid grid-cols-[56px_1fr] gap-2">
        <span className="text-[11px] text-slate-500">ms</span>
        <div className="flex items-center justify-between text-[11px] text-slate-500">
          {xTicks.map((tick, i) => (
            <span key={i}>{formatTimeLabel(tick, timeRange)}</span>
          ))}
        </div>
      </div>
    </div>
  );
}

// ─── Timing Breakdown Bar ────────────────────────────────────────────────────

interface TimingPhase {
  label: string;
  color: string;
  ms: number;
}

function computeTimingPhases(timing: HTTPTimingInfo): TimingPhase[] {
  const dns = timing.dns_ms ?? 0;
  const connect = timing.connect_ms ?? 0;
  const tls = timing.tls_handshake_ms ?? 0;
  const ttfb = timing.ttfb_ms ?? 0;
  const total = timing.total_ms ?? dns + connect + tls + ttfb;
  const body = Math.max(0, total - dns - connect - tls - ttfb);

  return [
    { label: 'DNS', color: '#6fb5dd', ms: dns },
    { label: 'TCP', color: '#38bdf8', ms: connect },
    { label: 'TLS', color: '#e6b23f', ms: tls },
    { label: 'TTFB', color: '#46d17f', ms: ttfb },
    { label: 'Body', color: '#7c9ef2', ms: body },
  ].filter((p) => p.ms > 0);
}

function TimingBar({ timing, total }: { timing: HTTPTimingInfo; total: number }) {
  const phases = computeTimingPhases(timing);

  if (!phases.length) {
    return (
      <div className="glass-well px-3 py-2 text-xs text-slate-500">
        No timing breakdown available
      </div>
    );
  }

  return (
    <div>
      <div className="flex h-2.5 gap-px overflow-hidden rounded-full">
        {phases.map((phase) => (
          <div
            key={phase.label}
            style={{
              width: `${(phase.ms / total) * 100}%`,
              backgroundColor: phase.color,
              minWidth: 2,
            }}
            title={`${phase.label}: ${formatMs(phase.ms)}`}
          />
        ))}
      </div>
      <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1">
        {phases.map((phase) => (
          <div key={phase.label} className="flex items-center gap-1.5 text-xs">
            <span className="h-2 w-2 rounded-full" style={{ backgroundColor: phase.color }} />
            <span className="text-slate-500">{phase.label}</span>
            <span className="tabular-nums text-slate-300">{formatMs(phase.ms)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function TimingBreakdownPanel({ results }: { results: CheckResult[] }) {
  const resultsWithTiming = useMemo(
    () =>
      getOperationalResults(results)
        .filter((r) => {
          const t = getHttpMetrics(r)?.timing;
          return t && t.total_ms && t.total_ms > 0;
        })
        .slice(0, 30),
    [results],
  );

  const avgTiming = useMemo<HTTPTimingInfo | null>(() => {
    if (!resultsWithTiming.length) return null;
    const sums = { dns_ms: 0, connect_ms: 0, tls_handshake_ms: 0, ttfb_ms: 0, total_ms: 0 };
    for (const r of resultsWithTiming) {
      const t = getHttpMetrics(r)?.timing;
      if (!t) continue;
      const dns = t.dns_ms ?? 0;
      const connect = t.connect_ms ?? 0;
      const tls = t.tls_handshake_ms ?? 0;
      const ttfb = t.ttfb_ms ?? 0;
      const total = t.total_ms ?? dns + connect + tls + ttfb;
      sums.dns_ms += dns;
      sums.connect_ms += connect;
      sums.tls_handshake_ms += tls;
      sums.ttfb_ms += ttfb;
      sums.total_ms += total;
    }
    const n = resultsWithTiming.length;
    return {
      dns_ms: sums.dns_ms / n,
      connect_ms: sums.connect_ms / n,
      tls_handshake_ms: sums.tls_handshake_ms / n,
      ttfb_ms: sums.ttfb_ms / n,
      total_ms: sums.total_ms / n,
    };
  }, [resultsWithTiming]);

  if (!resultsWithTiming.length) {
    return (
      <div className="glass p-5">
        <h3 className="text-[15px] font-semibold text-white">Request timing breakdown</h3>
        <p className="mb-4 mt-0.5 text-xs text-slate-500">DNS · TCP · TLS · TTFB · Body</p>
        <Callout tone="info">
          Enable <span className="font-medium text-white">Collect timing</span> in monitor settings to
          see per-phase request breakdown.
        </Callout>
      </div>
    );
  }

  const latestTiming = getHttpMetrics(resultsWithTiming[0])!.timing!;
  const latestTotal = latestTiming.total_ms ?? 1;
  const avgTotal = avgTiming?.total_ms ?? 1;

  return (
    <div className="glass p-5">
      <div className="mb-4 flex items-start justify-between gap-3">
        <div>
          <h3 className="text-[15px] font-semibold text-white">Request timing breakdown</h3>
          <p className="mt-0.5 text-xs text-slate-500">
            DNS · TCP · TLS · TTFB · Body
          </p>
        </div>
        <span className="text-xs tabular-nums text-slate-400">{formatMs(latestTotal)} latest</span>
      </div>

      <div className="space-y-5">
        <div>
          <p className="mb-2 text-xs text-slate-500">Latest check</p>
          <TimingBar timing={latestTiming} total={latestTotal} />
        </div>
        {avgTiming && (
          <div>
            <p className="mb-2 text-xs text-slate-500">
              Average ({resultsWithTiming.length} checks)
            </p>
            <TimingBar timing={avgTiming} total={avgTotal} />
          </div>
        )}
      </div>
    </div>
  );
}

// ─── TLS Certificate Panel ───────────────────────────────────────────────────

function TLSCertificatePanel({ results }: { results: CheckResult[] }) {
  const latestTLS = useMemo<HTTPTLSInfo | null>(() => {
    for (const r of getOperationalResults(results)) {
      const tls = getHttpMetrics(r)?.tls;
      if (tls) return tls;
    }
    return null;
  }, [results]);

  if (!latestTLS) return null;

  const days = latestTLS.days_until_expiry;
  const severity = days === undefined ? 'unknown' : days > 30 ? 'ok' : days > 7 ? 'soon' : 'urgent';
  const expiryPct = days !== undefined ? Math.min(100, Math.max(0, (days / 90) * 100)) : 0;

  const palette: Record<typeof severity, { bar: string; tone: PillTone }> = {
    ok: { bar: 'bg-emerald-500', tone: 'success' },
    soon: { bar: 'bg-amber-500', tone: 'warning' },
    urgent: { bar: 'bg-rose-500', tone: 'danger' },
    unknown: { bar: 'bg-slate-500', tone: 'neutral' },
  };
  const c = palette[severity];

  return (
    <div className="glass p-5">
      <div className="mb-4 flex items-start justify-between gap-3">
        <div>
          <h3 className="text-[15px] font-semibold text-white">TLS certificate</h3>
          <p className="mt-0.5 text-xs text-slate-500">From latest successful check</p>
        </div>
        {days !== undefined && (
          <Pill tone={c.tone}>{days > 0 ? `${days}d left` : 'Expired'}</Pill>
        )}
      </div>

      {days !== undefined && (
        <div className="mb-4">
          <div className="mb-1.5 flex justify-between text-xs text-slate-500">
            <span>Validity</span>
            {latestTLS.not_after && (
              <span>Expires {new Date(latestTLS.not_after).toLocaleDateString()}</span>
            )}
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-well-hover">
            <div
              className={`h-full rounded-full transition-all ${c.bar}`}
              style={{ width: `${expiryPct}%` }}
            />
          </div>
        </div>
      )}

      <div className="space-y-2 text-[13px]">
        {latestTLS.subject && (
          <div className="flex justify-between gap-3">
            <span className="shrink-0 text-slate-500">Subject</span>
            <span className="truncate font-mono text-xs text-slate-300">{latestTLS.subject}</span>
          </div>
        )}
        {latestTLS.issuer && (
          <div className="flex justify-between gap-3">
            <span className="shrink-0 text-slate-500">Issuer</span>
            <span className="truncate text-right text-slate-300">{latestTLS.issuer}</span>
          </div>
        )}
        {latestTLS.version && (
          <div className="flex justify-between gap-3">
            <span className="shrink-0 text-slate-500">Version</span>
            <span className="text-slate-300">{latestTLS.version}</span>
          </div>
        )}
        {latestTLS.cipher_suite && (
          <div className="flex justify-between gap-3">
            <span className="shrink-0 text-slate-500">Cipher</span>
            <span className="truncate text-right font-mono text-xs text-slate-300">
              {latestTLS.cipher_suite}
            </span>
          </div>
        )}
        {latestTLS.dns_names && latestTLS.dns_names.length > 0 && (
          <div className="pt-1">
            <p className="mb-1.5 text-xs text-slate-500">
              SANs ({latestTLS.dns_names.length})
            </p>
            <div className="flex flex-wrap gap-1">
              {latestTLS.dns_names.slice(0, 8).map((name) => (
                <span
                  key={name}
                  className="rounded-md bg-well-hover px-1.5 py-0.5 font-mono text-[11px] text-slate-300"
                >
                  {name}
                </span>
              ))}
              {latestTLS.dns_names.length > 8 && (
                <span className="px-1.5 py-0.5 text-[11px] text-slate-500">
                  +{latestTLS.dns_names.length - 8} more
                </span>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

// ─── Enhanced Results Table ──────────────────────────────────────────────────

function HttpResultsTable({ results }: { results: CheckResult[] }) {
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const displayed = results.slice(0, 15);

  return (
    <div className="glass overflow-hidden">
      <div className="px-5 py-4">
        <h3 className="text-[15px] font-semibold text-white">Recent results</h3>
        <p className="mt-0.5 text-xs text-slate-500">Click any row to expand timing &amp; details</p>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full">
          <thead>
            <tr className="border-b border-divider">
              <th className="px-5 py-2.5 text-left text-xs font-medium text-slate-500">Time (UTC)</th>
              <th className="px-5 py-2.5 text-left text-xs font-medium text-slate-500">Status</th>
              <th className="px-5 py-2.5 text-left text-xs font-medium text-slate-500">Code</th>
              <th className="px-5 py-2.5 text-left text-xs font-medium text-slate-500">Latency</th>
              <th className="px-5 py-2.5 text-left text-xs font-medium text-slate-500">Flags</th>
            </tr>
          </thead>
          <tbody>
            {displayed.map((result) => {
              const time = new Date(result.created_at)
                .toISOString()
                .replace('T', ' ')
                .slice(0, 19);
              const httpMetrics = getHttpMetrics(result);
              const timing = httpMetrics?.timing;
              const tls = httpMetrics?.tls;
              const assertionsFailed = httpMetrics?.assertions_failed;
              const redirects = httpMetrics?.redirects;
              const finalUrl = httpMetrics?.final_url;
              const hasTiming = !!(timing?.total_ms && timing.total_ms > 0);
              const isExpanded = expandedId === result.id;

              const dotColor =
                result.status === 'success'
                  ? 'bg-emerald-500'
                  : result.status === 'degraded'
                    ? 'bg-amber-500'
                    : 'bg-rose-500';
              const codeColor =
                result.http_status && result.http_status < 300
                  ? 'text-slate-300'
                  : result.http_status && result.http_status < 500
                    ? 'text-amber-300'
                    : 'text-rose-300';

              return (
                <React.Fragment key={result.id}>
                  <tr
                    onClick={() => setExpandedId(isExpanded ? null : result.id)}
                    className={`cursor-pointer transition-colors hover:bg-well ${
                      isExpanded ? 'bg-well' : 'border-b border-divider last:border-0'
                    }`}
                  >
                    <td className="whitespace-nowrap px-5 py-3 font-mono text-[13px] text-slate-400">
                      {time}
                      {result.location_name && (
                        <Pill size="xs" className="ml-2 font-sans">
                          {result.location_name}
                        </Pill>
                      )}
                    </td>
                    <td className="px-5 py-3">
                      <span className="inline-flex items-center gap-2 text-[13px] text-slate-300">
                        <span className={`h-2 w-2 rounded-full ${dotColor}`} />
                        {result.status === 'success'
                          ? 'OK'
                          : result.status === 'degraded'
                            ? 'Degraded'
                            : 'Failed'}
                      </span>
                    </td>
                    <td className="px-5 py-3">
                      <span className={`text-[13px] tabular-nums ${codeColor}`}>
                        {result.http_status ?? 'N/A'}
                      </span>
                    </td>
                    <td className="px-5 py-3 text-[13px] tabular-nums text-slate-300">
                      {result.latency_ms ? formatMs(result.latency_ms) : '—'}
                    </td>
                    <td className="px-5 py-3 text-xs">
                      {assertionsFailed?.length ? (
                        <span className="text-rose-300">
                          {assertionsFailed.length} assertion
                          {assertionsFailed.length > 1 ? 's' : ''} failed
                        </span>
                      ) : redirects ? (
                        <span className="text-sky-300">
                          {redirects} redirect{redirects > 1 ? 's' : ''}
                        </span>
                      ) : hasTiming ? (
                        <span className="text-slate-500">timing ↓</span>
                      ) : (
                        <span className="text-slate-500">—</span>
                      )}
                    </td>
                  </tr>

                  {isExpanded && (
                    <tr className="border-b border-divider bg-well last:border-0">
                      <td colSpan={5} className="px-5 pb-4 pt-1">
                        <div className="space-y-4">
                          {hasTiming && (
                            <div>
                              <p className="mb-2 text-xs text-slate-500">Request timing</p>
                              <TimingBar timing={timing!} total={timing!.total_ms!} />
                            </div>
                          )}
                          {tls?.days_until_expiry !== undefined && (
                            <div className="flex flex-wrap items-center gap-3 text-xs">
                              <span className="text-slate-500">TLS</span>
                              <span
                                className={
                                  tls.days_until_expiry > 30
                                    ? 'text-slate-300'
                                    : tls.days_until_expiry > 7
                                      ? 'text-amber-300'
                                      : 'text-rose-300'
                                }
                              >
                                {tls.days_until_expiry}d until expiry
                              </span>
                              {tls.version && (
                                <span className="font-mono text-slate-500">{tls.version}</span>
                              )}
                              {tls.cipher_suite && (
                                <span className="font-mono text-slate-500">{tls.cipher_suite}</span>
                              )}
                            </div>
                          )}
                          {assertionsFailed?.length ? (
                            <div>
                              <p className="mb-1.5 text-xs text-rose-300">Failed assertions</p>
                              <ul className="space-y-1 font-mono text-xs text-slate-300">
                                {assertionsFailed.map((msg, i) => (
                                  <li key={i}>{msg}</li>
                                ))}
                              </ul>
                            </div>
                          ) : null}
                          {redirects && finalUrl && (
                            <p className="text-xs">
                              <span className="text-slate-500">
                                Redirected {redirects}× →{' '}
                              </span>
                              <span className="break-all font-mono text-slate-300">{finalUrl}</span>
                            </p>
                          )}
                          {result.error_message && (
                            <Callout tone="danger">
                              <span className="break-words font-mono text-xs">{result.error_message}</span>
                            </Callout>
                          )}
                          {!hasTiming &&
                            !assertionsFailed?.length &&
                            !result.error_message &&
                            !redirects &&
                            !tls && (
                              <p className="text-xs text-slate-500">
                                No additional details available for this check.
                              </p>
                            )}
                        </div>
                      </td>
                    </tr>
                  )}
                </React.Fragment>
              );
            })}
            {results.length === 0 && (
              <tr>
                <td colSpan={5} className="px-5 py-8 text-center text-sm text-slate-500">
                  No check results available yet
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// ─── Main Component ───────────────────────────────────────────────────────────

interface HttpMonitorOverviewProps {
  monitor: Monitor;
  results: CheckResult[];
  loading?: boolean;
  timeRange?: TimeRange;
  onTimeRangeChange?: (range: TimeRange) => void;
}

export default function HttpMonitorOverview({
  monitor,
  results,
  loading = false,
  timeRange: controlledTimeRange,
  onTimeRangeChange,
}: HttpMonitorOverviewProps) {
  const [localTimeRange, setLocalTimeRange] = useState<TimeRange>('24h');
  const timeRange = controlledTimeRange ?? localTimeRange;

  const handleTimeRangeChange = useCallback(
    (range: TimeRange) => {
      setLocalTimeRange(range);
      onTimeRangeChange?.(range);
    },
    [onTimeRangeChange],
  );

  const operationalResults = useMemo(() => getOperationalResults(results), [results]);
  const uptime = useMemo(() => calculateUptime(results), [results]);
  const latencyStats = useMemo(() => calculateLatencyStats(results), [results]);

  const latencySeries = useMemo<SeriesPoint[]>(() => {
    const cutoffMs = Date.now() - RANGE_MS[timeRange];
    return operationalResults
      .filter((r) => r.latency_ms && Date.parse(r.created_at) >= cutoffMs)
      .map((r) => ({
        timestampMs: Date.parse(r.created_at),
        value: r.latency_ms!,
        status: r.status,
      }))
      .sort((a, b) => a.timestampMs - b.timestampMs);
  }, [operationalResults, timeRange]);

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <div className="text-slate-500">Loading monitor data…</div>
      </div>
    );
  }

  const latestResult = operationalResults[0];
  const operationalCount = operationalResults.length;
  const successCount = operationalResults.filter((r) => r.status === 'success').length;
  const downtimePct = operationalCount > 0 ? 100 - uptime : 0;

  return (
    <div className="space-y-6">
      <div className="glass p-5">
        <UptimeHeroGauge uptime={uptime} hasData={operationalCount > 0} />

        {latestResult && (
          <div className="mt-1 flex flex-wrap items-center justify-center gap-x-3 gap-y-1 text-xs text-slate-400">
            <span className="flex items-center gap-2">
              <span
                className={`h-2 w-2 rounded-full ${
                  latestResult.status === 'success'
                    ? 'bg-emerald-500'
                    : latestResult.status === 'degraded'
                      ? 'bg-amber-500'
                      : 'bg-rose-500'
                }`}
              />
              Last check <span className="text-slate-300">{getTimeAgo(latestResult.created_at)}</span>
            </span>
            <span aria-hidden="true" className="text-slate-500">·</span>
            <span className="tabular-nums text-slate-300">
              {latestResult.latency_ms ? formatMs(latestResult.latency_ms) : '—'}
            </span>
            <span aria-hidden="true" className="text-slate-500">·</span>
            <span
              className={`tabular-nums ${
                latestResult.http_status && latestResult.http_status < 400
                  ? 'text-slate-300'
                  : 'text-rose-300'
              }`}
            >
              HTTP {latestResult.http_status ?? 'N/A'}
            </span>
          </div>
        )}

        <div className="mt-5 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div className="glass-well p-4">
            <p className="text-xs text-slate-400">P95 latency</p>
            <p className="mt-1 text-2xl font-semibold tabular-nums text-white">
              {latencyStats.p95 ? formatMs(latencyStats.p95) : 'N/A'}
            </p>
            <p className="mt-1 text-xs text-slate-500">
              Median{' '}
              <span className="tabular-nums text-slate-400">
                {latencyStats.median ? formatMs(latencyStats.median) : 'N/A'}
              </span>
            </p>
          </div>
          <div className="glass-well p-4">
            <p className="text-xs text-slate-400">Latest response</p>
            <p className="mt-1 text-2xl font-semibold tabular-nums text-white">
              {latestResult?.latency_ms ? formatMs(latestResult.latency_ms) : 'N/A'}
            </p>
            <p className="mt-1 text-xs text-slate-500">
              {latestResult ? (
                <span
                  className={`tabular-nums ${
                    latestResult.http_status && latestResult.http_status < 400
                      ? 'text-slate-400'
                      : 'text-rose-300'
                  }`}
                >
                  HTTP {latestResult.http_status ?? 'N/A'}
                </span>
              ) : '—'}
            </p>
          </div>
          <div className="glass-well p-4">
            <p className="text-xs text-slate-400">Total checks</p>
            <p className="mt-1 text-2xl font-semibold tabular-nums text-white">{operationalCount}</p>
            <p className="mt-1 text-xs text-slate-500">
              <span className="tabular-nums text-slate-400">{successCount}</span> successful
            </p>
          </div>
          <div className="glass-well p-4">
            <p className="text-xs text-slate-400">Downtime</p>
            <p
              className={`mt-1 text-2xl font-semibold tabular-nums ${
                downtimePct > 0 ? 'text-rose-300' : 'text-white'
              }`}
            >
              {operationalCount > 0 ? `${downtimePct.toFixed(3)}%` : 'N/A'}
            </p>
            <p className="mt-1 text-xs text-slate-500">
              {downtimePct === 0 ? 'No downtime recorded' : `${(operationalCount - successCount)} failed checks`}
            </p>
          </div>
        </div>
      </div>

      <div className="glass p-5">
        <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-[15px] font-semibold text-white">Response latency</h3>
            <p className="mt-0.5 text-xs text-slate-500">{latencySeries.length} points</p>
          </div>
          <div className="flex flex-wrap items-center gap-4">
            <div className="flex items-center gap-3 text-xs text-slate-400">
              <span className="flex items-center gap-1.5">
                <span className="h-2 w-2 rounded-full bg-emerald-500" />
                OK
              </span>
              <span className="flex items-center gap-1.5">
                <span className="h-2 w-2 rounded-full bg-rose-500" />
                Failed
              </span>
            </div>
            <div className="flex gap-1.5">
              {(['1h', '6h', '24h', '7d'] as const).map((range) => (
                <FilterChip
                  key={range}
                  selected={timeRange === range}
                  onClick={() => handleTimeRangeChange(range)}
                >
                  {range}
                </FilterChip>
              ))}
            </div>
          </div>
        </div>
        <LatencyTimeSeriesChart series={latencySeries} timeRange={timeRange} />
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <TimingBreakdownPanel results={results} />
        <TLSCertificatePanel results={results} />
      </div>

      <HttpResultsTable results={results} />

      {monitor.tags && monitor.tags.length > 0 && (
        <div className="flex items-center gap-2">
          <span className="text-xs text-slate-500">Tags:</span>
          <div className="flex flex-wrap gap-1.5">
            {monitor.tags.map((tag) => (
              <Pill key={tag} tone="neutral" size="xs">{tag}</Pill>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

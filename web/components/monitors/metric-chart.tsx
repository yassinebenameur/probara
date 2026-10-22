'use client';

// Reusable Recharts internals for metric-store charts (agent dashboard and
// the metric explorer): synced area charts, tooltip, gap-row building.

import type { ReactNode } from 'react';
import {
  Area,
  AreaChart,
  Brush,
  CartesianGrid,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { formatDate, formatTimestamp } from '@/lib/format';
import type { MonitorAnalyticsRange } from '@/lib/types';

export type TimeRange = '1h' | '6h' | '24h' | '7d';

// A monitor overview range as a chart range: the chart only distinguishes
// time-of-day from date axis labels, so every range past a day reads as 7d.
export const chartRangeFor = (range: MonitorAnalyticsRange): TimeRange =>
  range === '1h' || range === '6h' || range === '24h' ? range : '7d';

export const RANGE_MS: Record<TimeRange, number> = {
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
  '7d': 7 * 24 * 60 * 60 * 1000,
};

// Base query resolution per range (~60-340 buckets); callers clamp the step
// up to the agent's reporting interval so filled buckets stay contiguous.
export const RANGE_STEP_SECONDS: Record<TimeRange, number> = {
  '1h': 60,
  '6h': 120,
  '24h': 300,
  '7d': 1800,
};

export const METRIC_SYNC_ID = 'agent-metrics';

// Categorical series colours, theme-aware through the --chart-* tokens in
// app/globals.css: no status hue (a series is not "up" or "warning") and no
// orange (that means "act"), >= 3:1 on glass and wells in both themes.
// Order matters: charts pair adjacent entries (1+2, 2+3, 3+4 ...), so
// neighbours are far apart in hue.
export const SERIES_COLORS = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
  'var(--chart-6)',
];

/** Latency (dashboard trend, monitor response time): the first series colour. */
export const LATENCY_COLOR = SERIES_COLORS[0];

/** Horizontal gridlines for every chart: quiet, but enough to read a value across a wide chart. */
export const CHART_GRID = { stroke: 'var(--chart-grid)', vertical: false } as const;

// Chart chrome shared by every chart in the monitor views: a frosted tooltip,
// muted axis ticks and a hairline hover cursor, all theme-aware.
export const CHART_TOOLTIP_CLASS =
  'rounded-xl border border-edge-strong bg-glass-strong px-3 py-2 text-white shadow-overlay backdrop-blur-[20px]';
export const CHART_AXIS_TICK = { fill: 'var(--text-muted)', fontSize: 11 };
export const CHART_CURSOR = { stroke: 'var(--glass-edge-strong)', strokeWidth: 1 };

// A unified chart row keyed by timestamp; missing buckets have null values so
// Recharts (connectNulls={false}) breaks the area across reporting gaps. A
// bucket isolated between two gaps has no segment to draw, so it is drawn as
// a dot (isolatedDot).
export type ChartRow = Record<string, number | null> & { ts: number };

/**
 * The `dot` renderer for an area with gaps: a non-null bucket between two
 * empty ones (or alone) has no line segment, so it gets a dot; every other
 * point draws nothing, so dense series look the same.
 */
export function isolatedDot(rows: ReadonlyArray<Record<string, unknown> | undefined>, key: string, color: string) {
  const IsolatedDot = (props: { cx?: number; cy?: number; index?: number }) => {
    const i = props.index ?? -1;
    const isNum = (row?: Record<string, unknown>) => typeof row?.[key] === 'number';
    const isolated = isNum(rows[i]) && !isNum(rows[i - 1]) && !isNum(rows[i + 1]);
    return isolated && props.cx !== undefined && props.cy !== undefined ? (
      // A ring of panel colour keeps the point a distinct mark, not a speck.
      <circle
        key={`${key}-${i}`}
        cx={props.cx}
        cy={props.cy}
        r={3.5}
        fill={color}
        stroke="rgb(var(--panel-rgb))"
        strokeWidth={1.5}
      />
    ) : (
      <g key={`${key}-${i}`} />
    );
  };
  return IsolatedDot;
}

export interface ChartSeries {
  key: string;
  name: string;
  color: string;
}

export interface ReferenceThreshold {
  y: number;
  label: string;
}

export interface ChartSeriesInput {
  key: string;
  // [epoch_ms, value] bucket points; buckets with no samples are omitted by
  // the server, so absent grid slots become null gap rows here.
  points: [number, number][];
  transform?: (value: number) => number;
}

// Builds the shared row grid: one row per step bucket between the earliest
// and latest observed bucket, null where a series has no sample.
export function buildChartRows(inputs: ChartSeriesInput[], stepMs: number): ChartRow[] {
  if (stepMs <= 0) return [];
  let minTs = Infinity;
  let maxTs = -Infinity;
  const valueMaps = inputs.map((input) => {
    const map = new Map<number, number>();
    for (const [ts, value] of input.points) {
      // Snap to the grid in case of sub-step jitter.
      const bucket = Math.floor(ts / stepMs) * stepMs;
      map.set(bucket, input.transform ? input.transform(value) : value);
      if (bucket < minTs) minTs = bucket;
      if (bucket > maxTs) maxTs = bucket;
    }
    return map;
  });
  if (!Number.isFinite(minTs)) return [];

  const bucketCount = Math.floor((maxTs - minTs) / stepMs) + 1;
  if (bucketCount > 5000) return []; // defensive: never render an unbounded grid

  const rows: ChartRow[] = [];
  for (let ts = minTs; ts <= maxTs; ts += stepMs) {
    const row = { ts } as ChartRow;
    inputs.forEach((input, index) => {
      row[input.key] = valueMaps[index].get(ts) ?? null;
    });
    rows.push(row);
  }
  return rows;
}

// Axis and tooltip labels go through lib/format so every chart on the page
// reads `15:19` / `2 Oct` like the rest of the UI, whatever the browser locale.
export function formatTimeLabel(timestampMs: number, range: TimeRange): string {
  if (range === '7d') return formatDate(timestampMs);
  return formatTimestamp(timestampMs, { seconds: false }).slice(11);
}

export function MetricTooltip({
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
    <div className={CHART_TOOLTIP_CLASS}>
      <p className="text-xs text-slate-400">{formatTimeLabel(label, range)}</p>
      {entries.map((entry, index) => (
        <p key={`${entry.name}-${index}`} className="mt-1 flex items-center gap-2 text-sm">
          <span className="h-2 w-2 rounded-full" style={{ backgroundColor: entry.color }} aria-hidden="true" />
          <span className="text-slate-400">{entry.name}</span>
          <span className="font-medium tabular-nums">{formatter(entry.value as number)}</span>
        </p>
      ))}
    </div>
  );
}

/** Five evenly spaced ticks across a time window, ends included. */
function windowTicks([start, end]: [number, number]): number[] {
  return [0, 1, 2, 3, 4].map((i) => Math.round(start + ((end - start) * i) / 4));
}

function BrushHandle({ x, y, width, height }: { x: number; y: number; width: number; height: number }) {
  return <rect x={x} y={y} width={width} height={height} rx={3} fill="var(--control-edge-strong)" />;
}

export function MetricChart({
  data,
  series,
  range,
  yDomain,
  yTickFormatter,
  valueFormatter,
  height = 180,
  showBrush = false,
  referenceLines,
  syncId = METRIC_SYNC_ID,
  yAxisWidth = 56,
  ariaLabel,
  xDomain,
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
  // Pass '' to opt out of cross-chart hover sync (each chart tooltips alone).
  syncId?: string;
  // Widen for long tick labels (byte values) so they don't wrap.
  yAxisWidth?: number;
  /** Text alternative; defaults to each series' latest value. */
  ariaLabel?: string;
  /**
   * The x window in epoch ms. Charts that sit under the monitor overview pass
   * the selected range, so a series that only covers its last hour reads as
   * such and lines up with the response-time chart; omitted, the axis spans
   * the data.
   */
  xDomain?: [number, number];
}) {
  const hasData = data.some((row) => series.some((s) => row[s.key] !== null && row[s.key] !== undefined));
  if (!hasData) {
    return <p className="py-3 text-center text-ui text-slate-500">No data in range</p>;
  }

  // The SVG says nothing to a screen reader; name the chart by the values a
  // sighted reader takes from its right edge.
  const summary =
    ariaLabel ??
    `Chart, latest values: ${series
      .map((s) => {
        for (let i = data.length - 1; i >= 0; i--) {
          const v = data[i][s.key];
          if (typeof v === 'number') return `${s.name} ${valueFormatter(v)}`;
        }
        return `${s.name} no data`;
      })
      .join(', ')}`;

  return (
    <div style={{ height }} role="img" aria-label={summary}>
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} syncId={syncId || undefined} margin={{ top: 6, right: 8, left: 0, bottom: 0 }}>
          <defs>
            {series.map((s) => (
              <linearGradient key={s.key} id={`agentGradient-${s.key}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={s.color} stopOpacity={0.25} />
                <stop offset="100%" stopColor={s.color} stopOpacity={0} />
              </linearGradient>
            ))}
          </defs>
          <CartesianGrid {...CHART_GRID} />
          <XAxis
            dataKey="ts"
            type="number"
            domain={xDomain ?? ['dataMin', 'dataMax']}
            allowDataOverflow={xDomain !== undefined}
            // A fixed window gets evenly spaced ticks across it; left to
            // itself the axis ticks only where the data is.
            ticks={xDomain ? windowTicks(xDomain) : undefined}
            scale="time"
            axisLine={false}
            tickLine={false}
            tick={CHART_AXIS_TICK}
            tickMargin={6}
            minTickGap={40}
            tickFormatter={(value) => formatTimeLabel(value, range)}
          />
          <YAxis
            domain={yDomain ?? ['auto', 'auto']}
            axisLine={false}
            tickLine={false}
            width={yAxisWidth}
            tick={CHART_AXIS_TICK}
            tickFormatter={(value) => yTickFormatter(value)}
          />
          <Tooltip
            cursor={CHART_CURSOR}
            content={<MetricTooltip range={range} formatter={valueFormatter} />}
          />
          {referenceLines?.map((line) => (
            <ReferenceLine
              key={`ref-${line.label}-${line.y}`}
              y={line.y}
              // Full-strength danger, dashed so it stays distinct from the
              // series (a faded stroke drops under 3:1 on a well).
              stroke="var(--danger)"
              strokeDasharray="4 4"
              ifOverflow="extendDomain"
              label={{ value: line.label, position: 'insideTopRight', fill: 'var(--danger)', fontSize: 11 }}
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
              dot={isolatedDot(data, s.key, s.color)}
              isAnimationActive={false}
            />
          ))}
          {showBrush && (
            <Brush
              dataKey="ts"
              height={16}
              travellerWidth={6}
              stroke="var(--control-edge)"
              fill="transparent"
              traveller={BrushHandle}
              tickFormatter={(value) => formatTimeLabel(value as number, range)}
            />
          )}
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

/** Colour key for a chart with more than one series. */
export function ChartLegend({ series }: { series: ChartSeries[] }) {
  if (series.length < 2) return null;
  return (
    <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs text-slate-400">
      {series.map((s) => (
        <span key={s.key} className="flex items-center gap-1.5">
          <span className="h-2 w-2 shrink-0 rounded-full" style={{ backgroundColor: s.color }} aria-hidden="true" />
          {s.name}
        </span>
      ))}
    </div>
  );
}

/**
 * One chart inside a metrics panel (agent host, Mongo cluster, SIP call
 * timing): a well with the title left, the latest reading right, and the
 * legend under the chart when it draws more than one series.
 */
export function ChartTile({
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
    <div className="glass-well min-w-0 p-4">
      <div className="mb-3 flex items-baseline justify-between gap-3">
        <span className="truncate text-ui font-medium text-slate-200">{title}</span>
        <span className="shrink-0 text-xs tabular-nums text-slate-400">{headline}</span>
      </div>
      {children}
      {legend && <ChartLegend series={legend} />}
    </div>
  );
}

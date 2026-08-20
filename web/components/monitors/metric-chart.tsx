'use client';

// Reusable Recharts internals for metric-store charts (agent dashboard and
// the metric explorer): synced area charts, tooltip, gap-row building.

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

export type TimeRange = '1h' | '6h' | '24h' | '7d';

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

export const SERIES_COLORS = ['#e6b23f', '#ff8a5c', '#6fb5dd', '#46d17f', '#f472b6', '#fb923c'];

// A unified chart row keyed by timestamp; missing buckets have null values so
// Recharts (connectNulls={false}) breaks the area across reporting gaps.
export type ChartRow = Record<string, number | null> & { ts: number };

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

export function formatTimeLabel(timestampMs: number, range: TimeRange): string {
  const date = new Date(timestampMs);
  if (range === '7d') {
    return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  }
  return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
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
        <AreaChart data={data} syncId={syncId || undefined} margin={{ top: 6, right: 8, left: 0, bottom: 0 }}>
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
            width={yAxisWidth}
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

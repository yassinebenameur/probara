'use client';

import { useMemo } from 'react';
import { ComposedChart, Area, CartesianGrid, Line, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts';
import { formatLatency, formatUptime, formatPercent } from '@/lib/format';
import { UPTIME_TONE_VAR, uptimeTone } from '@/lib/status-tones';
import { CHART_AXIS_TICK, CHART_GRID, CHART_TOOLTIP_CLASS, LATENCY_COLOR } from '@/components/monitors/metric-chart';
import { type TrendPoint } from '@/components/dashboard/layout';

const UPTIME_TICK_STEPS = [0.1, 0.2, 0.5, 1, 2, 5, 10, 20, 25];

// Recharts clones this element and fills in active/payload/label.
function CustomTooltip({
  active,
  payload,
  label,
  uptimeColor,
}: {
  active?: boolean;
  payload?: Array<{ name?: string; value?: number | string | null }>;
  label?: string;
  uptimeColor: string;
}) {
  if (!active || !payload?.length) return null;
  return (
    <div className={CHART_TOOLTIP_CLASS}>
      <p className="mb-1.5 text-xs font-medium text-slate-400">{label}</p>
      <div className="space-y-1">
        {payload.map((entry, idx) => {
          const isLatency = entry.name === 'responseTime';
          return (
            <div key={idx} className="flex items-center justify-between gap-4">
              <span className="flex items-center gap-1.5 text-xs text-slate-400">
                <span className="h-1.5 w-1.5 rounded-full" style={{ background: isLatency ? LATENCY_COLOR : uptimeColor }} />
                {isLatency ? 'Avg HTTP latency' : 'Uptime'}
              </span>
              <span className="text-sm font-semibold tabular-nums text-white">
                {typeof entry.value === 'number'
                  ? isLatency
                    ? formatLatency(entry.value)
                    : formatUptime(entry.value)
                  : entry.value}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function chartSummary(trendData: TrendPoint[], rangeLabel: string): string {
  const uptimes = trendData.map((p) => p.uptime).filter((v): v is number => typeof v === 'number');
  const latencies = trendData.map((p) => p.responseTime).filter((v): v is number => typeof v === 'number' && v > 0);
  if (uptimes.length === 0) return `Uptime and latency, ${rangeLabel.toLowerCase()}: no data`;
  const avg = uptimes.reduce((a, b) => a + b, 0) / uptimes.length;
  const parts = [`Uptime ${formatUptime(avg)} on average over the ${rangeLabel.toLowerCase()}, lowest ${formatUptime(Math.min(...uptimes))}`];
  if (latencies.length > 0) {
    parts.push(`average HTTP latency ${formatLatency(latencies.reduce((a, b) => a + b, 0) / latencies.length)}`);
  }
  return parts.join('; ');
}

export function UptimeResponseChart({
  trendData,
  hasEnoughTrendData,
  noMatchingMonitors,
  rangeLabel,
}: {
  trendData: TrendPoint[];
  hasEnoughTrendData: boolean;
  noMatchingMonitors: boolean;
  rangeLabel: string;
}) {
  const { uptimeAxis, uptimeColor } = useMemo(() => {
    const values = trendData
      .map((point) => point.uptime)
      .filter((value): value is number => typeof value === 'number');
    const min = values.length === 0 ? 98.5 : Math.min(...values);
    const padded = Math.max(0, min - Math.max(0.25, (100 - min) * 0.08));
    // Evenly spaced ticks ending at 100%, so the axis never skips a label.
    const step = UPTIME_TICK_STEPS.find((s) => (100 - padded) / s <= 4) ?? 25;
    const count = Math.max(1, Math.ceil((100 - padded) / step - 1e-9));
    const ticks = Array.from({ length: count + 1 }, (_, i) => Math.round((100 - step * (count - i)) * 10) / 10);
    // The line is neutral ink while uptime is healthy and takes the shared
    // tone of its worst bucket otherwise, so green never draws an outage.
    const tone = uptimeTone(values.length === 0 ? null : min);
    return {
      uptimeAxis: { domain: [ticks[0], 100] as [number, number], ticks },
      uptimeColor: tone === 'warn' || tone === 'bad' ? UPTIME_TONE_VAR[tone] : 'var(--text-secondary)',
    };
  }, [trendData]);

  return (
    <section className="glass flex min-w-0 flex-col overflow-hidden xl:h-full">
      <div className="shrink-0 px-5 py-4">
        <h2 className="text-title font-semibold text-white">Uptime and latency</h2>
        <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-slate-400">
          <span className="flex items-center gap-1.5">
            <span className="h-2 w-2 rounded-full" style={{ background: uptimeColor }} aria-hidden="true" /> Uptime
          </span>
          <span className="flex items-center gap-1.5">
            <span className="h-2 w-2 rounded-full" style={{ background: LATENCY_COLOR }} aria-hidden="true" /> Avg HTTP latency
          </span>
        </div>
      </div>

      <div className="flex min-h-0 flex-1 flex-col px-5 pb-5">
        {hasEnoughTrendData ? (
          <div className="h-64 min-w-0 xl:h-auto xl:min-h-0 xl:flex-1" role="img" aria-label={chartSummary(trendData, rangeLabel)}>
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={trendData} margin={{ top: 10, right: 6, bottom: 0, left: 4 }}>
                <defs>
                  <linearGradient id="combinedUptimeGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={uptimeColor} stopOpacity={0.18} />
                    <stop offset="100%" stopColor={uptimeColor} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid {...CHART_GRID} />
                <XAxis dataKey="date" axisLine={false} tickLine={false} tick={CHART_AXIS_TICK} minTickGap={24} />
                <YAxis
                  yAxisId="uptime"
                  domain={uptimeAxis.domain}
                  ticks={uptimeAxis.ticks}
                  interval={0}
                  width={58}
                  axisLine={false}
                  tickLine={false}
                  tick={CHART_AXIS_TICK}
                  tickFormatter={(v) => formatPercent(Number(v), { trim: true })}
                />
                <YAxis
                  yAxisId="latency"
                  orientation="right"
                  // Room for "200 ms" on one line.
                  width={60}
                  axisLine={false}
                  tickLine={false}
                  tick={CHART_AXIS_TICK}
                  tickFormatter={(v) => formatLatency(Number(v))}
                />
                <Tooltip
                  content={<CustomTooltip uptimeColor={uptimeColor} />}
                  cursor={{ stroke: 'var(--glass-edge-strong)', strokeDasharray: '3 3' }}
                />
                <Area
                  yAxisId="uptime"
                  type="monotone"
                  dataKey="uptime"
                  stroke={uptimeColor}
                  strokeWidth={2}
                  fill="url(#combinedUptimeGradient)"
                  name="uptime"
                  // Drawn at once: a redraw animation on every refresh adds
                  // nothing on an ops dashboard and ignores reduced motion.
                  isAnimationActive={false}
                  activeDot={{ r: 3.5, fill: uptimeColor, stroke: 'var(--bg-elevated)', strokeWidth: 2 }}
                />
                <Line
                  yAxisId="latency"
                  type="monotone"
                  dataKey="responseTime"
                  stroke={LATENCY_COLOR}
                  strokeWidth={2}
                  dot={false}
                  name="responseTime"
                  isAnimationActive={false}
                  activeDot={{ r: 3.5, fill: LATENCY_COLOR, stroke: 'var(--bg-elevated)', strokeWidth: 2 }}
                />
              </ComposedChart>
            </ResponsiveContainer>
          </div>
        ) : (
          <div className="glass-well flex h-64 items-center justify-center px-4 text-center xl:h-auto xl:flex-1">
            <div>
              <p className="text-sm font-medium text-slate-300">
                {noMatchingMonitors ? 'No monitors match these tags' : 'Not enough trend data yet'}
              </p>
              <p className="mt-1 text-ui text-slate-500">
                {noMatchingMonitors ? 'Add or change tags to widen the dashboard scope.' : 'Trend lines will appear after at least two populated buckets.'}
              </p>
            </div>
          </div>
        )}
      </div>
    </section>
  );
}

'use client';

import { Area, AreaChart, CartesianGrid, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import Pill, { type PillTone } from '@/components/ui/Pill';
import { CHART_AXIS_TICK, CHART_CURSOR, CHART_GRID, CHART_TOOLTIP_CLASS, SERIES_COLORS } from '@/components/monitors/metric-chart';
import { formatSlaDate, formatSlaDuration, formatSlaPct, type SlaBurnPoint, type SlaTone } from '@/lib/sla';
import { NO_DATA_BAR, UPTIME_TONE_BAR } from '@/lib/status-tones';

const PILL_TONE: Record<SlaTone, PillTone> = { good: 'success', warn: 'warning', bad: 'danger', none: 'neutral' };

export const TONE_TEXT: Record<SlaTone, string> = {
  good: 'text-emerald-300',
  warn: 'text-amber-300',
  bad: 'text-rose-300',
  none: 'text-slate-400',
};

// The same fills as every other bar (check traces, hourly bars), at full
// strength: a faded green reads as a different state.
export const TONE_BG: Record<SlaTone, string> = {
  good: UPTIME_TONE_BAR.good,
  warn: UPTIME_TONE_BAR.warn,
  bad: UPTIME_TONE_BAR.bad,
  none: NO_DATA_BAR,
};

// The empty part of a budget bar: visible enough to show where 100% ends.
const BUDGET_TRACK = 'bg-[rgb(var(--ink-rgb)/0.12)]';

export function SlaStatusPill({ met, running }: { met: boolean | null | undefined; running?: boolean }) {
  if (met === null || met === undefined) return <Pill tone="neutral" size="xs">No data</Pill>;
  const tone: SlaTone = met ? 'good' : 'bad';
  const label = met ? (running ? 'On track' : 'Met') : running ? 'Below target' : 'Breached';
  return <Pill tone={PILL_TONE[tone]} size="xs">{label}</Pill>;
}

/**
 * Remaining error budget as a bar. An overspent budget is an empty track with
 * a rose edge labelled "Exhausted": a full red bar under "budget left" would
 * read as a full budget.
 */
export function BudgetBar({ remainingPct }: { remainingPct: number | null | undefined }) {
  if (remainingPct === null || remainingPct === undefined) {
    return <div className={`h-1.5 w-full rounded-full ${BUDGET_TRACK}`} aria-label="No error budget data" />;
  }
  const exhausted = remainingPct <= 0;
  const clamped = Math.max(0, Math.min(100, remainingPct));
  const tone: SlaTone = exhausted ? 'bad' : remainingPct < 25 ? 'warn' : 'good';
  return (
    <div className="flex items-center gap-2">
      <div
        className={`h-1.5 w-full overflow-hidden rounded-full ${
          exhausted ? 'bg-rose-500/15 shadow-[inset_0_0_0_1px_rgb(var(--tw-rose-500)/0.45)]' : BUDGET_TRACK
        }`}
        role="meter"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(clamped)}
        aria-valuetext={exhausted ? 'Exhausted' : `${Math.round(clamped)}% left`}
        aria-label="Error budget remaining"
      >
        {!exhausted && <div className={`h-full rounded-full ${TONE_BG[tone]}`} style={{ width: `${clamped}%` }} />}
      </div>
      <span className={`w-16 shrink-0 text-right text-xs tabular-nums ${TONE_TEXT[tone]}`}>
        {exhausted ? 'Exhausted' : `${Math.round(remainingPct)}%`}
      </span>
    </div>
  );
}

export function AvailabilityFigure({ value, tone, className = '' }: { value: number | null; tone: SlaTone; className?: string }) {
  return <span className={`tabular-nums ${TONE_TEXT[tone]} ${className}`}>{formatSlaPct(value)}</span>;
}

/** Saves a downloaded blob under its server-provided filename. */
export function saveDownload({ blob, filename }: { blob: Blob; filename: string }) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

/** A report day (`2026-10-01`, already in the SLA's zone) as `1 Oct`. */
function shortDay(date: string): string {
  const full = formatSlaDate(`${date}T12:00:00Z`, 'UTC', false);
  return full ? full.replace(/ \d{4}$/, '') : date;
}

const BURN_COLOR = SERIES_COLORS[0];

interface BurnRow {
  date: string;
  remaining: number | null;
  consumed: number;
}

function BurnTooltip({ active, payload }: { active?: boolean; payload?: Array<{ payload?: BurnRow }> }) {
  const row = payload?.[0]?.payload;
  if (!active || !row) return null;
  return (
    <div className={CHART_TOOLTIP_CLASS}>
      <p className="text-xs text-slate-400">End of {shortDay(row.date)}</p>
      <p className="mt-1 text-sm font-medium tabular-nums">
        {row.remaining === null ? 'No data yet' : `${row.remaining.toFixed(1)}% of the period's budget left`}
      </p>
      <p className="text-xs tabular-nums text-slate-400">{formatSlaDuration(row.consumed)} consumed</p>
    </div>
  );
}

/**
 * Error-budget burn-down (S-U8): the full period's budget left after each
 * day, from 100% down; below the danger line at 0 the budget is overspent.
 * The series is a chart colour, the zero line the danger status hue.
 */
export function BurnDownChart({ points }: { points: SlaBurnPoint[] }) {
  const rows: BurnRow[] = points.map((p) => ({ date: p.date, remaining: p.remaining_pct, consumed: p.consumed_seconds }));
  const values = rows.flatMap((r) => (r.remaining === null ? [] : [r.remaining]));
  const lowest = values.length ? Math.min(...values) : 0;
  const last = values.length ? values[values.length - 1] : null;
  const floor = lowest < 0 ? Math.floor(lowest / 25) * 25 : 0;
  return (
    <div
      className="h-36"
      role="img"
      aria-label={
        last === null
          ? 'Error budget burn-down: no data yet'
          : `Error budget burn-down: ${last.toFixed(1)}% of the full period's budget left after ${shortDay(rows[rows.length - 1].date)}`
      }
    >
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={rows} margin={{ top: 6, right: 8, left: 0, bottom: 0 }}>
          <defs>
            <linearGradient id="slaBurnGradient" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={BURN_COLOR} stopOpacity={0.25} />
              <stop offset="100%" stopColor={BURN_COLOR} stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid {...CHART_GRID} />
          <XAxis
            dataKey="date"
            axisLine={false}
            tickLine={false}
            tick={CHART_AXIS_TICK}
            tickMargin={8}
            minTickGap={24}
            tickFormatter={shortDay}
          />
          <YAxis
            width={44}
            axisLine={false}
            tickLine={false}
            tick={CHART_AXIS_TICK}
            domain={[floor, 100]}
            allowDataOverflow
            tickFormatter={(v: number) => `${v}%`}
          />
          <Tooltip cursor={CHART_CURSOR} content={<BurnTooltip />} />
          <ReferenceLine y={0} stroke="var(--danger)" strokeDasharray="4 3" ifOverflow="extendDomain" />
          <Area
            type="linear"
            dataKey="remaining"
            stroke={BURN_COLOR}
            strokeWidth={2}
            fill="url(#slaBurnGradient)"
            connectNulls={false}
            isAnimationActive={false}
            name="Budget left"
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

'use client';

import { useMemo } from 'react';
import { CartesianGrid, Line, LineChart, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import Pill, { type PillTone } from '@/components/ui/Pill';
import { CHART_AXIS_TICK, CHART_CURSOR, CHART_GRID, CHART_TOOLTIP_CLASS } from '@/components/monitors/metric-chart';
import { formatPercent } from '@/lib/format';
import { burnDownRows, formatSlaDate, formatSlaPct, type BurnDownRow, type SlaReport, type SlaTone } from '@/lib/sla';
import { NO_DATA_BAR, UPTIME_TONE_BAR, UPTIME_TONE_VAR } from '@/lib/status-tones';

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

/** A chart instant as a day in the SLA's timezone, without the year: `5 Oct`. */
function chartDay(ts: number, timeZone: string): string {
  return (formatSlaDate(new Date(ts).toISOString(), timeZone, false) ?? '').replace(/ \d{4}$/, '');
}

function BurnDownTooltip({ active, payload, timeZone }: {
  active?: boolean;
  payload?: Array<{ payload?: BurnDownRow }>;
  timeZone: string;
}) {
  const row = payload?.[0]?.payload;
  if (!active || !row) return null;
  return (
    <div className={CHART_TOOLTIP_CLASS}>
      <p className="mb-1 text-xs font-medium text-slate-400">{formatSlaDate(new Date(row.ts).toISOString(), timeZone) ?? ''}</p>
      <p className="text-sm font-semibold tabular-nums text-white">
        {row.remaining === null ? 'No data' : `${formatPercent(row.remaining)} left`}
      </p>
      <p className="text-xs tabular-nums text-slate-400">Even spend: {formatPercent(row.even)}</p>
    </div>
  );
}

/**
 * The error-budget burn-down (S-U8) across the whole period: the full
 * period's budget left after each day, against the even spend from 100% to
 * 0%, with the 0% line a status line. The budget line is neutral ink while
 * it stays above the even spend and takes the warn or bad tone below it or
 * once spent, so a healthy period draws no colour.
 */
export function BurnDownChart({ report }: { report: SlaReport }) {
  const rows = useMemo(() => burnDownRows(report), [report]);
  const timeZone = report.definition.timezone;
  const { domain, ticks, color, summary } = useMemo(() => {
    const values = rows.map((r) => r.remaining).filter((v): v is number => v !== null);
    const lowest = Math.max(-100, Math.min(0, ...values));
    const floor = Math.floor(lowest / 25) * 25;
    const steps: number[] = [];
    for (let v = floor; v <= 100; v += 25) steps.push(v);
    const reported = rows.filter((r) => r.remaining !== null && r.ts > rows[0].ts);
    const last = reported[reported.length - 1];
    let tone: 'good' | 'warn' | 'bad' = 'good';
    if (last && last.remaining !== null) {
      if (last.remaining <= 0) tone = 'bad';
      else if (last.remaining < last.even) tone = 'warn';
    }
    return {
      domain: [floor, 100] as [number, number],
      ticks: steps,
      color: tone === 'good' ? 'var(--text-secondary)' : UPTIME_TONE_VAR[tone],
      summary: last && last.remaining !== null
        ? `Error budget burn-down: ${formatPercent(last.remaining)} of the period's budget left on ${chartDay(last.ts, timeZone)}, against ${formatPercent(last.even)} at an even spend`
        : 'Error budget burn-down: no data yet',
    };
  }, [rows, timeZone]);
  if (rows.length < 2) return null;
  const start = rows[0].ts;
  const end = rows[rows.length - 1].ts;

  return (
    <div className="h-56 min-w-0" role="img" aria-label={summary}>
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={rows} margin={{ top: 8, right: 8, bottom: 0, left: 4 }}>
          <CartesianGrid {...CHART_GRID} />
          <XAxis dataKey="ts" type="number" scale="time" domain={[start, end]} axisLine={false} tickLine={false}
            tick={CHART_AXIS_TICK} minTickGap={32} tickFormatter={(v) => chartDay(Number(v), timeZone)} />
          <YAxis domain={domain} ticks={ticks} interval={0} width={48} axisLine={false} tickLine={false} tick={CHART_AXIS_TICK}
            tickFormatter={(v) => formatPercent(Number(v), { digits: 0 })} />
          <ReferenceLine y={0} stroke={UPTIME_TONE_VAR.bad} strokeWidth={1} />
          <Tooltip content={<BurnDownTooltip timeZone={timeZone} />} cursor={CHART_CURSOR} />
          <Line type="linear" dataKey="even" stroke="var(--text-muted)" strokeDasharray="4 4" strokeWidth={1.5} dot={false}
            activeDot={false} isAnimationActive={false} name="Even spend" />
          <Line type="linear" dataKey="remaining" stroke={color} strokeWidth={2} dot={false} connectNulls={false}
            isAnimationActive={false} name="Budget left"
            activeDot={{ r: 3.5, fill: color, stroke: 'var(--bg-elevated)', strokeWidth: 2 }} />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}

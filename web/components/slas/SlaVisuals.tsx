'use client';

import Pill, { type PillTone } from '@/components/ui/Pill';
import { formatSlaPct, type SlaTone } from '@/lib/sla';
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

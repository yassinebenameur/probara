'use client';

import Pill, { type PillTone } from '@/components/ui/Pill';
import { formatSlaPct, type SlaTone } from '@/lib/sla';

const PILL_TONE: Record<SlaTone, PillTone> = { good: 'success', warn: 'warning', bad: 'danger', none: 'neutral' };

export const TONE_TEXT: Record<SlaTone, string> = {
  good: 'text-emerald-300',
  warn: 'text-amber-300',
  bad: 'text-rose-300',
  none: 'text-slate-400',
};

export const TONE_BG: Record<SlaTone, string> = {
  good: 'bg-emerald-500/80',
  warn: 'bg-amber-500/80',
  bad: 'bg-rose-500/80',
  none: 'bg-slate-700/60',
};

export function SlaStatusPill({ met, running }: { met: boolean | null | undefined; running?: boolean }) {
  if (met === null || met === undefined) return <Pill tone="neutral" size="xs">No data</Pill>;
  const tone: SlaTone = met ? 'good' : 'bad';
  const label = met ? (running ? 'On track' : 'Met') : running ? 'Below target' : 'Breached';
  return <Pill tone={PILL_TONE[tone]} size="xs">{label}</Pill>;
}

/** Remaining error budget as a bar; overspent budgets fill red past zero. */
export function BudgetBar({ remainingPct }: { remainingPct: number | null | undefined }) {
  if (remainingPct === null || remainingPct === undefined) {
    return <div className="h-1.5 w-full rounded-full bg-slate-800" aria-label="No error budget data" />;
  }
  const clamped = Math.max(0, Math.min(100, remainingPct));
  const tone: SlaTone = remainingPct < 0 ? 'bad' : remainingPct < 25 ? 'warn' : 'good';
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-slate-800" role="meter" aria-valuemin={0}
        aria-valuemax={100} aria-valuenow={Math.round(remainingPct)} aria-label="Error budget remaining">
        <div className={`h-full rounded-full ${remainingPct < 0 ? 'w-full bg-rose-500/70' : TONE_BG[tone]}`}
          style={remainingPct < 0 ? undefined : { width: `${clamped}%` }} />
      </div>
      <span className={`w-12 shrink-0 text-right font-mono text-[11px] ${TONE_TEXT[tone]}`}>
        {remainingPct < 0 ? 'over' : `${Math.round(remainingPct)}%`}
      </span>
    </div>
  );
}

export function AvailabilityFigure({ value, tone, className = '' }: { value: number | null; tone: SlaTone; className?: string }) {
  return <span className={`font-mono tabular-nums ${TONE_TEXT[tone]} ${className}`}>{formatSlaPct(value)}</span>;
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

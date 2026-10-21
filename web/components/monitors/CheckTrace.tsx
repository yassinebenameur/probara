'use client';

import { useRef } from 'react';
import type { CheckResult } from '@/lib/types';
import { isPlatformResult } from '@/lib/monitor-utils';
import { CHECK_RESULT_BAR, checkResultLabel, NO_DATA_BAR, type CheckResultStatus } from '@/lib/status-tones';
import { formatDateTime, formatRelativeTime } from '@/lib/format';

// Fixed bars, never stretched, so the trace has the same density on every
// surface and at every width: md is 20 × 3px + 19 × 1px = 79px, sm (10
// slots) 10 × 2px + 9 × 1px = 29px. Every bar, empty slots included, is the
// same height: outcome is colour, and the text alternative (the summary
// below, the Success column next to it) carries it for anyone who can't
// tell the colours apart. Callers that reserve a column use these widths.
const SIZE = {
  md: { row: 'h-4 gap-px', bar: 'h-4 w-[3px]' },
  sm: { row: 'h-3 gap-px', bar: 'h-3 w-[2px]' },
} as const;

type Outcome = 'pass' | 'degraded' | 'fail' | 'notRun';

function outcomeOf(result: CheckResult): Outcome {
  if (isPlatformResult(result)) return 'notRun';
  if (result.status === 'success') return 'pass';
  if (result.status === 'degraded') return 'degraded';
  return 'fail';
}

/**
 * Screen-reader (and compact tooltip) summary: every outcome counted, zero
 * parts left out, then how the latest check ended and when.
 * "Last 20 checks: 15 passed, 2 degraded, 1 failed, 2 not run; latest failed 25s ago".
 */
export function checkTraceSummary(results: CheckResult[] | null, slots: number): string {
  if (results === null) return 'Loading recent checks';
  const shown = results.slice(0, slots);
  if (shown.length === 0) return 'No checks yet';
  const counts: Record<Outcome, number> = { pass: 0, degraded: 0, fail: 0, notRun: 0 };
  shown.forEach((r) => {
    counts[outcomeOf(r)] += 1;
  });
  const parts = [
    counts.pass && `${counts.pass} passed`,
    counts.degraded && `${counts.degraded} degraded`,
    counts.fail && `${counts.fail} failed`,
    counts.notRun && `${counts.notRun} not run`,
  ].filter(Boolean);
  const latest = shown[0];
  const latestLabel = isPlatformResult(latest) ? 'not run' : checkResultLabel(latest.status).toLowerCase();
  const noun = shown.length === 1 ? 'check' : 'checks';
  return `Last ${shown.length} ${noun}: ${parts.join(', ')}; latest ${latestLabel} ${formatRelativeTime(latest.created_at)}`;
}

/**
 * The check-history trace: one bar per recent check, newest at the right
 * edge. Missing checks pad on the left, so the newest check always sits at
 * the same x and a new monitor's trace fills in from the right. Platform
 * results (a job that expired before a worker ran it) are grey: they say
 * nothing about the target. When a refresh brings a new check, its bar grows
 * in (animate-bar-in); the first render never animates.
 *
 * This is the one check-history trace (monitors list, dashboard "Needs
 * attention"); keep every surface on it so the instrument reads the same.
 *
 * `muted` is for a monitor that is not being checked (paused): its last
 * results are history, not live health, so the trace goes grey and faint
 * instead of showing a healthy-looking green run.
 */
export default function CheckTrace({
  results,
  slots = 20,
  size = 'md',
  muted = false,
  className = '',
}: {
  /** Newest first, as the results API returns them; null while loading. */
  results: CheckResult[] | null;
  slots?: number;
  size?: keyof typeof SIZE;
  /** Not currently checked (paused): drawn desaturated and faint. */
  muted?: boolean;
  className?: string;
}) {
  const loading = results === null;
  const shown = (results ?? []).slice(0, slots).reverse();
  const dims = SIZE[size];
  const summary = checkTraceSummary(results, slots);

  // The newest id the trace first rendered with: a later newest is a check
  // that arrived while the trace was on screen, so it animates in.
  const newestId = shown.length > 0 ? shown[shown.length - 1].id : null;
  const baselineNewest = useRef<string | null | undefined>(undefined);
  if (baselineNewest.current === undefined && !loading) baselineNewest.current = newestId;
  const animateNewest =
    baselineNewest.current !== undefined && newestId !== null && newestId !== baselineNewest.current;

  return (
    <div
      role="img"
      aria-label={summary}
      title={size === 'sm' ? summary : undefined}
      className={`flex items-end justify-end ${dims.row} ${loading ? 'animate-pulse' : ''} ${
        muted ? 'opacity-40 saturate-0' : ''
      } ${className}`.trim()}
    >
      {Array.from({ length: slots - shown.length }, (_, i) => (
        <span
          key={`empty-${i}`}
          className={`${dims.bar} flex-none rounded-[0.5px] ${NO_DATA_BAR}`}
        />
      ))}
      {shown.map((result, i) => {
        const outcome = outcomeOf(result);
        const fill =
          outcome === 'notRun'
            ? 'bg-slate-500'
            : CHECK_RESULT_BAR[result.status as CheckResultStatus] ?? 'bg-slate-500';
        const label = outcome === 'notRun' ? 'Not run' : checkResultLabel(result.status);
        const isNewest = i === shown.length - 1;
        // A monitor that is degraded right now pulses its newest bar only:
        // "happening now" without turning a list of traces into a flicker
        // (older degraded bars stay still). Reduced motion keeps it solid.
        const motion =
          isNewest && outcome === 'degraded' && !muted
            ? 'animate-status-pulse'
            : isNewest && animateNewest
              ? 'animate-bar-in'
              : '';
        return (
          <span
            key={result.id ?? i}
            title={size === 'md' ? `${label}, ${formatDateTime(result.created_at)}` : undefined}
            className={`${dims.bar} flex-none rounded-[0.5px] ${fill} ${motion}`}
          />
        );
      })}
    </div>
  );
}

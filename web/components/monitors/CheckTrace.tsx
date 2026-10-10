import type { CheckResult } from '@/lib/types';
import { isPlatformResult } from '@/lib/monitor-utils';
import { CHECK_RESULT_BAR, checkResultLabel, type CheckResultStatus } from '@/lib/status-tones';
import { formatDateTime } from '@/lib/format';

const SIZE = {
  // Fills its column: bars grow to 8px, so every row's trace lines up.
  md: { bar: 'h-5 min-w-[3px] max-w-[8px] flex-1', gap: 'gap-[2px]' },
  // Fixed bars for the compact mobile cell.
  sm: { bar: 'h-3.5 w-[4px]', gap: 'gap-[2px]' },
} as const;

/**
 * The check-history trace: one bar per recent check, newest at the right
 * edge. Missing checks pad on the left, so the newest check always sits at
 * the same x and a new monitor's trace fills in from the right. Platform
 * results (a job that expired before a worker ran it) are grey: they say
 * nothing about the target.
 *
 * This is the one check-history trace (monitors list, dashboard "Needs
 * attention"); keep every surface on it so the instrument reads the same.
 */
export default function CheckTrace({
  results,
  slots = 20,
  size = 'md',
  className = '',
}: {
  /** Newest first, as the results API returns them; null while loading. */
  results: CheckResult[] | null;
  slots?: number;
  size?: keyof typeof SIZE;
  className?: string;
}) {
  const loading = results === null;
  const shown = (results ?? []).slice(0, slots).reverse();
  const dims = SIZE[size];
  const operational = shown.filter((r) => !isPlatformResult(r));
  const passed = operational.filter((r) => r.status === 'success').length;
  const summary = loading
    ? 'Loading recent checks'
    : shown.length === 0
      ? 'No checks yet'
      : `Last ${shown.length} checks: ${passed} passed, ${operational.length - passed} failed`;

  return (
    <div
      role="img"
      aria-label={summary}
      title={size === 'sm' ? summary : undefined}
      className={`flex items-center justify-end ${dims.gap} ${loading ? 'animate-pulse' : ''} ${className}`.trim()}
    >
      {Array.from({ length: slots - shown.length }, (_, i) => (
        <span key={`empty-${i}`} className={`${dims.bar} rounded-[2px] bg-slate-500/20`} />
      ))}
      {shown.map((result, i) => {
        const platform = isPlatformResult(result);
        const fill = platform
          ? 'bg-slate-500'
          : CHECK_RESULT_BAR[result.status as CheckResultStatus] ?? 'bg-slate-500';
        const label = platform ? 'Not run' : checkResultLabel(result.status);
        return (
          <span
            key={result.id ?? i}
            title={size === 'md' ? `${label}, ${formatDateTime(result.created_at)}` : undefined}
            className={`${dims.bar} rounded-[2px] ${fill}`}
          />
        );
      })}
    </div>
  );
}

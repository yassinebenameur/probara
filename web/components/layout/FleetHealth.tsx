'use client';

import Link from 'next/link';
import { Bell } from 'lucide-react';
import { FOCUS_RING, FOCUS_RING_INSET } from '@/components/ui/focus';
import { useTooltip } from '@/components/ui/Tooltip';
import { RAIL_ROW_MOTION, railLabel, railRowPadding } from './rail';

// The active-alerts view: the alerts page reads ?status= on load. "Active"
// (not "open"): the alerts page's Open view also holds acknowledged alerts,
// which this count leaves out.
export const OPEN_ALERTS_HREF = '/alerts?status=active';

/**
 * The shell's one count badge (active alerts here, open incidents in the
 * sidebar nav). Solid red with literal white text: text-white flips to ink in
 * light mode, and white on #c42b3c is 5.6:1 in both themes. `srLabel` is read
 * after the number ("3 open").
 */
export function CountBadge({
  count,
  srLabel,
  overlay = false,
}: {
  count: number;
  srLabel?: string;
  /**
   * Collapsed sidebar rail: a small pill pinned past the top-right corner of
   * the row's 16px icon (the parent box must be `relative`), so it covers the
   * corner and not the glyph, ringed in the panel colour so it reads as
   * separate from the icon. Caps at 99+. Decorative: the row's name carries
   * the count (an sr-only label here would be read before the row's name),
   * so `srLabel` is ignored.
   */
  overlay?: boolean;
}) {
  if (overlay) {
    return (
      <span
        aria-hidden="true"
        className="pointer-events-none absolute -right-2.5 -top-2 min-w-3.5 animate-pop-in rounded-full bg-[#c42b3c] px-[3px] text-center text-2xs font-semibold leading-[14px] tabular-nums text-[#fff] ring-2 ring-[rgb(var(--panel-rgb))]"
      >
        {count > 99 ? '99+' : count}
      </span>
    );
  }
  return (
    <span className="ml-auto min-w-5 flex-shrink-0 rounded-md bg-[#c42b3c] px-1.5 text-center text-2xs font-semibold leading-5 tabular-nums text-[#fff]">
      {count}
      {srLabel && <span className="sr-only"> {srLabel}</span>}
    </span>
  );
}

/**
 * One row under the brand that answers "is anything wrong?" on every page,
 * and the only place fleet health shows (the page itself is never tinted).
 * A neutral row: a status dot, a label, and the solid count badge while
 * anything is active. It links to the active alerts, the list the number
 * counts. In the collapsed sidebar rail (`rail`) it becomes a bell icon with
 * the count pinned over its corner (a green dot when all clear), and the
 * label moves to a tooltip. Dot and bell crossfade in one 16px box, and the
 * row badge fades rather than unmounting, so the row keeps its shape while
 * the sidebar's width animates. The accessible name always contains the
 * visible words (WCAG 2.5.3), so "All clear" is in it too.
 */
export default function FleetHealth({
  openAlerts,
  onNavigate,
  compact = false,
  rail = false,
}: {
  openAlerts: number | null;
  onNavigate?: () => void;
  compact?: boolean;
  rail?: boolean;
}) {
  const alerting = openAlerts !== null && openAlerts > 0;
  const label = openAlerts === null ? 'Checking alerts' : alerting ? 'Active alerts' : 'All clear';
  const fullLabel =
    openAlerts === null
      ? 'Checking alerts'
      : alerting
        ? `${openAlerts} active alert${openAlerts === 1 ? '' : 's'}`
        : 'All clear, no active alerts';
  const tip = useTooltip<HTMLAnchorElement>({ label: alerting ? fullLabel : label, disabled: !rail });

  return (
    <>
      <Link
        ref={tip.ref}
        {...tip.triggerProps}
        href={alerting ? OPEN_ALERTS_HREF : '/alerts'}
        onClick={onNavigate}
        title={rail ? undefined : fullLabel}
        aria-label={fullLabel}
        className={`flex items-center gap-2.5 rounded-lg text-ui text-slate-300 hover:bg-well hover:text-white active:bg-well-hover ${
          compact
            ? `px-2 py-1 transition-colors ${FOCUS_RING}`
            : `overflow-hidden whitespace-nowrap ${RAIL_ROW_MOTION} ${railRowPadding(rail)} ${FOCUS_RING_INSET}`
        }`}
      >
        {/* The dot sits in a 16px box so the label lines up with the nav icons. */}
        <span className="relative flex h-4 w-4 flex-shrink-0 items-center justify-center" aria-hidden="true">
          {!compact && (
            <Bell
              className={`absolute inset-0 h-4 w-4 text-slate-400 transition-opacity duration-base ${rail ? 'opacity-100' : 'opacity-0'}`}
              strokeWidth={1.75}
            />
          )}
          <span
            className={`h-2 w-2 rounded-full transition-opacity duration-base ${rail ? 'opacity-0' : 'opacity-100'} ${
              openAlerts === null
                ? 'bg-slate-500'
                : alerting
                  ? 'bg-rose-500 ring-4 ring-rose-500/15'
                  : 'bg-emerald-500'
            }`}
          />
          {rail &&
            (alerting ? (
              <CountBadge count={openAlerts} overlay />
            ) : (
              openAlerts !== null && (
                <span className="absolute -right-0.5 -top-0.5 h-2 w-2 animate-pop-in rounded-full bg-emerald-500 ring-2 ring-[rgb(var(--panel-rgb))]" />
              )
            ))}
        </span>
        {compact ? (
          // Narrow top bar: the badge stays visible, the words come back from sm.
          <span aria-hidden="true" className="hidden sm:inline">
            {label}
          </span>
        ) : (
          <span aria-hidden="true" className={railLabel(rail)}>
            {label}
          </span>
        )}
        {alerting &&
          (compact ? (
            <span aria-hidden="true" className="contents">
              <CountBadge count={openAlerts} />
            </span>
          ) : (
            // Kept mounted in the rail (clipped by the 40px row) and faded with
            // the label, so collapsing does not drop it mid-animation.
            <span
              aria-hidden="true"
              className={`-my-px ml-auto flex flex-shrink-0 transition-opacity ${rail ? 'opacity-0 duration-fast' : 'opacity-100 duration-base delay-[var(--dur-fast)]'}`}
            >
              <CountBadge count={openAlerts} />
            </span>
          ))}
      </Link>
      {tip.tooltip}
    </>
  );
}

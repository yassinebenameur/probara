'use client';

import { ButtonHTMLAttributes, ReactNode, Ref } from 'react';
import { FOCUS_RING, FOCUS_RING_INSET } from './focus';

interface FilterChipProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  /**
   * Toggle state, announced as aria-pressed. Leave it out for action chips
   * ("Select") so they read as plain buttons; disclosure chips that pass
   * aria-expanded never get aria-pressed.
   */
  selected?: boolean;
  count?: number;
  icon?: ReactNode;
  children: ReactNode;
  /** The trigger of an anchored popup (FilterPopover) positions from it. */
  ref?: Ref<HTMLButtonElement>;
  /**
   * Draw the focus ring inside the chip: for the halves of a split chip
   * (FilterPopover's summary + ×), where an outer ring would outline only
   * half the pill.
   */
  insetFocus?: boolean;
}

/** Selected chip colours; FilterPopover's clear (×) half reuses them. */
export const FILTER_CHIP_SELECTED = 'border-white bg-white text-[rgb(var(--panel-rgb))] hover:bg-white/90';

/** The chip's count pill; FilterPopover's selection summary reuses it. */
export function FilterChipCount({ count, selected }: { count: number; selected?: boolean }) {
  return (
    <span
      className={`ml-0.5 rounded-full px-1.5 text-2xs leading-4 tabular-nums ${
        selected ? 'bg-[rgb(var(--panel-rgb)/0.2)] text-[rgb(var(--panel-rgb))]' : 'bg-well-hover text-slate-400'
      }`}
    >
      {count}
    </span>
  );
}

export default function FilterChip({
  selected,
  count,
  icon,
  insetFocus = false,
  className = '',
  children,
  ...props
}: FilterChipProps) {
  const base =
    'inline-flex h-7 items-center gap-1.5 whitespace-nowrap rounded-full border px-3 text-xs font-medium ' +
    'transition-[background-color,border-color,color,box-shadow,transform] duration-fast ease-standard ' +
    'active:scale-[0.97] motion-reduce:active:scale-100 disabled:active:scale-100 ' +
    `${insetFocus ? FOCUS_RING_INSET : FOCUS_RING} disabled:opacity-40 disabled:cursor-not-allowed`;
  // Selected is inverted ink: a solid ink chip with panel-coloured text
  // (bg-white flips to ink in light), so an applied filter reads at a glance
  // next to idle chips. Still neutral: orange is for actions, and a selected
  // filter sits next to the page's primary button.
  const palette = selected
    ? FILTER_CHIP_SELECTED
    : 'border-edge-control bg-control text-slate-400 hover:bg-control-hover hover:text-white';
  return (
    <button
      type="button"
      aria-pressed={selected === undefined || props['aria-expanded'] !== undefined ? undefined : selected}
      className={`${base} ${palette} ${className}`.trim()}
      {...props}
    >
      {icon && <span className="h-3 w-3 inline-flex items-center" aria-hidden="true">{icon}</span>}
      <span>{children}</span>
      {typeof count === 'number' && <FilterChipCount count={count} selected={selected} />}
    </button>
  );
}

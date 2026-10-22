'use client';

import { ReactNode, RefObject } from 'react';
import Link from 'next/link';
import { Loader2 } from 'lucide-react';
import { useMinDuration } from '@/lib/useMinDuration';
import { useTooltip } from './Tooltip';
import { FOCUS_RING } from './focus';

interface IconButtonProps {
  icon: ReactNode;
  /** Tooltip and accessible name — the button has no visible text. */
  label: string;
  onClick?: () => void;
  href?: string;
  /**
   * With `href`: open it in a new tab as a plain anchor (another origin, such
   * as a public status page) instead of an in-app Link.
   */
  external?: boolean;
  danger?: boolean;
  disabled?: boolean;
  loading?: boolean;
  /**
   * While `loading`, spin the passed icon instead of swapping in a spinner —
   * for Refresh (a RefreshCw that turns reads as "refreshing").
   */
  spinIcon?: boolean;
  /** `sm` (28px) for dense in-panel lists; `md` (32px) default. */
  size?: 'sm' | 'md';
  className?: string;
}

// Square so the icon sits dead centre; RowMenu's trigger uses the same box.
export const ICON_BUTTON_CLASS =
  'inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-slate-400 ' +
  'transition-[background-color,color,transform] duration-fast ease-standard ' +
  'hover:bg-well-hover hover:text-white active:scale-95 active:bg-control-hover motion-reduce:active:scale-100 ' +
  `${FOCUS_RING} disabled:cursor-not-allowed disabled:opacity-40 disabled:active:scale-100 ` +
  'aria-disabled:cursor-progress aria-disabled:active:scale-100 [&_svg]:h-4 [&_svg]:w-4';

export const ICON_BUTTON_SM_CLASS = '!h-7 !w-7 [&_svg]:!h-3.5 [&_svg]:!w-3.5';

const DANGER_CLASS = 'hover:!bg-rose-500/10 hover:!text-rose-300 active:!bg-rose-500/20';

// Row action for tables and lists: icon only, named by its tooltip. Use up to
// three per row; beyond that, put the actions in a RowMenu.
export default function IconButton({
  icon,
  label,
  onClick,
  href,
  external = false,
  danger,
  disabled,
  loading = false,
  spinIcon = false,
  size = 'md',
  className: extra = '',
}: IconButtonProps) {
  // Held for a minimum time: a refresh that returns in 80ms still visibly spins.
  const busy = useMinDuration(loading);
  // The label shows above the icon at once on keyboard focus and after a
  // short hover delay — the native title tooltip took about a second and
  // never showed on focus. The aria-label stays the accessible name.
  const tip = useTooltip<HTMLElement>({ label, side: 'top' });
  const className = `${ICON_BUTTON_CLASS} ${size === 'sm' ? ICON_BUTTON_SM_CLASS : ''} ${danger ? DANGER_CLASS : ''} ${
    busy && spinIcon ? '[&_svg]:animate-spin' : ''
  } ${extra}`;
  const content = busy && !spinIcon ? <Loader2 className="animate-spin" aria-hidden="true" /> : icon;
  if (href && external) {
    return (
      <>
        <a
          ref={tip.ref as RefObject<HTMLAnchorElement>}
          href={href}
          target="_blank"
          rel="noopener noreferrer"
          aria-label={label}
          className={className}
          {...tip.triggerProps}
        >
          {content}
        </a>
        {tip.tooltip}
      </>
    );
  }
  if (href) {
    return (
      <>
        <Link
          ref={tip.ref as RefObject<HTMLAnchorElement>}
          href={href}
          aria-label={label}
          className={className}
          {...tip.triggerProps}
        >
          {content}
        </Link>
        {tip.tooltip}
      </>
    );
  }
  // Busy keeps focus (aria-disabled + click guard): disabling the focused
  // button would drop keyboard focus to <body> mid-refresh.
  const blocked = busy && !disabled;
  return (
    <>
      <button
        ref={tip.ref as RefObject<HTMLButtonElement>}
        type="button"
        onClick={blocked ? undefined : onClick}
        disabled={disabled}
        aria-disabled={blocked || undefined}
        aria-busy={busy || undefined}
        aria-label={label}
        className={className}
        {...tip.triggerProps}
      >
        {content}
      </button>
      {tip.tooltip}
    </>
  );
}

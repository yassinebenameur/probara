'use client';

import { useCallback } from 'react';
import { Check, Copy, ExternalLink } from 'lucide-react';
import { FOCUS_RING } from './focus';
import { useClipboard } from '@/lib/useClipboard';

export type CopyableTargetSize = 'xs' | 'sm';

interface CopyableTargetProps {
  /** The endpoint to display and copy (URL, host:port, …). */
  value: string;
  /** What the value is, used in tooltips and screen-reader labels. */
  label?: string;
  size?: CopyableTargetSize;
  /**
   * Link to open in a new tab. Derived from `value` when omitted; pass `null`
   * to suppress the open action entirely.
   */
  href?: string | null;
  /**
   * Fade the actions out until the enclosing `group` element is hovered.
   * Pointer devices only — touch layouts keep them visible.
   */
  revealOnHover?: boolean;
  className?: string;
  /** Extra classes for the value text (colour, weight, max width). */
  textClassName?: string;
  /**
   * False takes the copy and open actions out of the tab order — for dense
   * lists whose row already links to a page that offers them, so a row
   * costs fewer Tab stops. Pointer users are unaffected.
   */
  focusable?: boolean;
}

const SIZE: Record<CopyableTargetSize, { text: string; icon: string; gap: string }> = {
  xs: { text: 'text-2xs', icon: 'h-3 w-3', gap: 'gap-1' },
  sm: { text: 'text-xs', icon: 'h-3.5 w-3.5', gap: 'gap-1.5' },
};

/** Only http(s) endpoints are openable; host:port, sip: and ws: targets are not. */
export function openableHref(value: string): string | null {
  const trimmed = value.trim();
  return /^https?:\/\/\S+$/i.test(trimmed) ? trimmed : null;
}

/**
 * An endpoint rendered as a one-click-to-copy chip with an optional
 * open-in-new-tab action. Safe inside clickable rows: every interaction
 * stops propagation so it never triggers the row's own navigation.
 */
export default function CopyableTarget({
  value,
  label = 'URL',
  size = 'sm',
  href,
  revealOnHover = false,
  className = '',
  textClassName = '',
  focusable = true,
}: CopyableTargetProps) {
  const { copy, copied, failed } = useClipboard();
  const state = copied ? 'copied' : failed ? 'failed' : 'idle';
  const link = href === undefined ? openableHref(value) : href;
  const dims = SIZE[size];

  const handleCopy = useCallback(
    (event: React.MouseEvent) => {
      event.preventDefault();
      event.stopPropagation();
      void copy(value);
    },
    [copy, value]
  );

  const actionClasses = [
    'inline-flex shrink-0 items-center justify-center rounded transition-opacity',
    revealOnHover
      ? 'opacity-100 focus-visible:opacity-100 md:opacity-0 md:group-hover:opacity-100'
      : 'opacity-100',
  ].join(' ');

  return (
    <span className={`inline-flex min-w-0 max-w-full items-center ${dims.gap} ${dims.text} ${className}`}>
      <button
        type="button"
        onClick={handleCopy}
        title={state === 'copied' ? `Copied ${label}` : `${value}\n\nClick to copy`}
        aria-label={`Copy ${label}: ${value}`}
        tabIndex={focusable ? undefined : -1}
        className={`inline-flex min-w-0 items-center ${dims.gap} cursor-pointer rounded text-left transition-colors duration-fast hover:text-cyan-300 ${FOCUS_RING} ${textClassName}`}
      >
        <span className="truncate font-mono">{value}</span>
        <span className={`${actionClasses} ${state === 'copied' ? 'text-emerald-400' : state === 'failed' ? 'text-rose-400' : 'text-slate-500'}`}>
          {state === 'copied' ? (
            <Check className={dims.icon} strokeWidth={2.5} aria-hidden="true" />
          ) : (
            <Copy className={dims.icon} strokeWidth={2} aria-hidden="true" />
          )}
        </span>
      </button>
      {link && (
        <a
          href={link}
          target="_blank"
          rel="noopener noreferrer"
          onClick={(event) => event.stopPropagation()}
          title={`Open ${label} in a new tab`}
          aria-label={`Open ${label} in a new tab: ${value}`}
          tabIndex={focusable ? undefined : -1}
          className={`${actionClasses} -m-1 p-1 text-slate-500 hover:text-cyan-300 ${FOCUS_RING}`}
        >
          <ExternalLink className={dims.icon} strokeWidth={2} aria-hidden="true" />
        </a>
      )}
      <span role="status" aria-live="polite" className="sr-only">
        {state === 'copied' ? `${label} copied to clipboard` : state === 'failed' ? `Could not copy ${label}` : ''}
      </span>
    </span>
  );
}

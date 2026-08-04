'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { Check, Copy, ExternalLink } from 'lucide-react';

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
}

const SIZE: Record<CopyableTargetSize, { text: string; icon: string; gap: string }> = {
  xs: { text: 'text-[10px]', icon: 'h-3 w-3', gap: 'gap-1' },
  sm: { text: 'text-xs', icon: 'h-3.5 w-3.5', gap: 'gap-1.5' },
};

/** Only http(s) endpoints are openable; host:port, sip: and ws: targets are not. */
export function openableHref(value: string): string | null {
  const trimmed = value.trim();
  return /^https?:\/\/\S+$/i.test(trimmed) ? trimmed : null;
}

async function writeClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    /* falls back below — clipboard API is unavailable on non-secure origins */
  }
  try {
    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.setAttribute('readonly', '');
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    document.body.appendChild(textarea);
    textarea.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(textarea);
    return ok;
  } catch {
    return false;
  }
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
}: CopyableTargetProps) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const link = href === undefined ? openableHref(value) : href;
  const dims = SIZE[size];

  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);

  const handleCopy = useCallback(
    async (event: React.MouseEvent) => {
      event.preventDefault();
      event.stopPropagation();
      const ok = await writeClipboard(value);
      setState(ok ? 'copied' : 'failed');
      if (timer.current) clearTimeout(timer.current);
      timer.current = setTimeout(() => setState('idle'), 1800);
    },
    [value]
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
        className={`inline-flex min-w-0 items-center ${dims.gap} cursor-pointer rounded text-left transition-colors hover:text-cyan-300 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-cyan-500/50 ${textClassName}`}
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
          className={`${actionClasses} text-slate-500 hover:text-cyan-300 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-cyan-500/50`}
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

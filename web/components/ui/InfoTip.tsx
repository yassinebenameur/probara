'use client';

import { ReactNode, useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Info } from 'lucide-react';

export interface InfoTipEntry {
  label: string;
  value: ReactNode;
}

interface InfoTipProps {
  title?: string;
  entries?: InfoTipEntry[];
  children?: ReactNode;
  /** When true, renders the trigger icon at icon-sm (h-3.5) — used inside form labels. */
  inLabel?: boolean;
  ariaLabel?: string;
}

export default function InfoTip({
  title,
  entries,
  children,
  inLabel = false,
  ariaLabel = 'More information',
}: InfoTipProps) {
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState({
    left: 0,
    top: 0,
    placement: 'top' as 'top' | 'bottom',
    ready: false,
  });
  const wrapRef = useRef<HTMLSpanElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const popoverRef = useRef<HTMLDivElement>(null);

  const updatePosition = useCallback(() => {
    const trigger = triggerRef.current;
    if (!trigger) return;
    const rect = trigger.getBoundingClientRect();
    const popoverWidth = popoverRef.current?.offsetWidth ?? 256;
    const popoverHeight = popoverRef.current?.offsetHeight ?? 0;
    const viewportPadding = 12;
    const offset = 8;
    const canPlaceAbove = rect.top >= popoverHeight + offset + viewportPadding;
    const left = Math.min(
      window.innerWidth - viewportPadding - popoverWidth / 2,
      Math.max(viewportPadding + popoverWidth / 2, rect.left + rect.width / 2)
    );
    setPosition({
      left,
      top: canPlaceAbove ? rect.top - offset : rect.bottom + offset,
      placement: canPlaceAbove ? 'top' : 'bottom',
      ready: true,
    });
  }, []);

  useEffect(() => {
    if (!open) return;
    const frame = window.requestAnimationFrame(updatePosition);
    const onClickOutside = (e: MouseEvent) => {
      const target = e.target as Node;
      if (wrapRef.current?.contains(target) || popoverRef.current?.contains(target)) return;
      setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onClickOutside);
    document.addEventListener('keydown', onKey);
    window.addEventListener('resize', updatePosition);
    document.addEventListener('scroll', updatePosition, true);
    return () => {
      window.cancelAnimationFrame(frame);
      document.removeEventListener('mousedown', onClickOutside);
      document.removeEventListener('keydown', onKey);
      window.removeEventListener('resize', updatePosition);
      document.removeEventListener('scroll', updatePosition, true);
    };
  }, [open, updatePosition]);

  const iconSize = inLabel ? 'h-3.5 w-3.5' : 'h-4 w-4';

  return (
    <span ref={wrapRef} className="relative inline-flex">
      <button
        ref={triggerRef}
        type="button"
        aria-label={ariaLabel}
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="inline-flex items-center justify-center rounded-full text-slate-500 transition-colors hover:text-slate-300 focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/40"
      >
        <Info className={iconSize} strokeWidth={1.75} aria-hidden="true" />
      </button>
      {open && typeof document !== 'undefined' && createPortal(
        <div
          ref={popoverRef}
          role="dialog"
          className="fixed z-[100] w-64 rounded-xl border border-white/10 bg-slate-900/95 p-3 text-xs shadow-[0_18px_45px_rgba(0,0,0,0.5)] backdrop-blur-md"
          style={{
            left: position.left,
            top: position.top,
            opacity: position.ready ? 1 : 0,
            transform: position.placement === 'top' ? 'translate(-50%, -100%)' : 'translate(-50%, 0)',
          }}
        >
          {title && (
            <div className="mb-2 border-b border-white/[0.06] pb-2 text-[11px] font-semibold uppercase tracking-wider text-slate-400">
              {title}
            </div>
          )}
          {entries && entries.length > 0 && (
            <dl className="space-y-1.5">
              {entries.map((e, i) => (
                <div key={i} className="flex items-center justify-between gap-3">
                  <dt className="text-xs text-slate-500">{e.label}</dt>
                  <dd className="text-xs font-medium text-slate-200">{e.value}</dd>
                </div>
              ))}
            </dl>
          )}
          {children && <div className="text-slate-300 leading-relaxed">{children}</div>}
        </div>,
        document.body
      )}
    </span>
  );
}

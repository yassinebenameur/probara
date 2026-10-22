'use client';

import { FocusEvent, PointerEvent, ReactNode, RefObject, useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';

// Hover waits a beat so sweeping the pointer across a rail does not flash a
// tooltip per item; once one has shown, its neighbours show at once for a
// short grace period (the "warm" window). Keyboard focus never waits.
const HOVER_DELAY_MS = 300;
// Row and toolbar icon buttons are aimed at, not swept past: a shorter wait.
const TOP_HOVER_DELAY_MS = 120;
const WARM_WINDOW_MS = 400;
let lastHiddenAt = 0;
let anyVisible = false;

const GAP = 10;
const VIEWPORT_PADDING = 8;

interface UseTooltipOptions {
  label: ReactNode;
  /**
   * Shown as a key cap after the label, and used as the trigger's
   * aria-keyshortcuts unless `ariaShortcut` is given. A display cap like `⌘B`
   * or `Ctrl+B` is not ARIA syntax, so pass `ariaShortcut` (`Meta+B`,
   * `Control+B`) whenever the two differ.
   */
  shortcut?: string;
  /** aria-keyshortcuts value (ARIA syntax); defaults to `shortcut`. */
  ariaShortcut?: string;
  /** No tooltip at all (for example while the label is visible next to the icon). */
  disabled?: boolean;
  /**
   * Point aria-describedby at the tooltip. Leave off when the label repeats
   * the trigger's accessible name, or screen readers announce it twice.
   */
  describe?: boolean;
  /**
   * `right` (rail items) or `top` (row and toolbar icon buttons; flips below
   * when there is no room above).
   */
  side?: 'right' | 'top';
}

// Rough half-width cap for clamping a top tooltip inside the viewport before
// it has rendered (labels are short).
const TOP_EDGE_CLEARANCE = 72;

/**
 * A small label beside its trigger (`side`), on hover (after a short delay)
 * and on keyboard focus (at once); Escape, blur, pointer leave, a press and
 * scrolling hide it. Hover means a mouse: touch and pen taps send pointer
 * events with no matching leave, and the sidebar stays mounted across
 * navigations, so a tap-triggered tooltip would stick. Portaled to <body> with position:fixed, so a glass ancestor's
 * backdrop-filter cannot offset or clip it. A hook rather than a wrapper so
 * the trigger keeps its place in the layout (a display:contents wrapper would
 * break `space-y-*` on the parent):
 *
 *   const tip = useTooltip<HTMLAnchorElement>({ label: 'Monitors' });
 *   <Link ref={tip.ref} {...tip.triggerProps} />{tip.tooltip}
 */
export function useTooltip<T extends HTMLElement>({
  label,
  shortcut,
  ariaShortcut,
  disabled = false,
  describe = false,
  side = 'right',
  anchorRef,
}: UseTooltipOptions & {
  /** Use a ref the trigger already has (RowMenu's trigger measures itself). */
  anchorRef?: RefObject<T | null>;
}) {
  const ownRef = useRef<T>(null);
  const ref = anchorRef ?? ownRef;
  const timer = useRef<number | null>(null);
  const [pos, setPos] = useState<{ left: number; top: number; transform: string } | null>(null);
  // Mirrors `pos !== null` synchronously. The warm-window bookkeeping must
  // happen in the event itself: a setState updater runs lazily (often only at
  // the next render), after the neighbour's enter has already read it.
  const shown = useRef(false);
  const id = useId();

  const clearTimer = () => {
    if (timer.current !== null) {
      window.clearTimeout(timer.current);
      timer.current = null;
    }
  };

  const show = useCallback(() => {
    const el = ref.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    shown.current = true;
    anyVisible = true;
    if (side === 'top') {
      const left = Math.min(
        window.innerWidth - TOP_EDGE_CLEARANCE,
        Math.max(TOP_EDGE_CLEARANCE, rect.left + rect.width / 2)
      );
      const below = rect.top < 40;
      setPos({
        left,
        top: below ? rect.bottom + 6 : rect.top - 6,
        transform: below ? 'translateX(-50%)' : 'translate(-50%, -100%)',
      });
    } else {
      const top = Math.min(window.innerHeight - VIEWPORT_PADDING, Math.max(VIEWPORT_PADDING, rect.top + rect.height / 2));
      setPos({ left: rect.right + GAP, top, transform: 'translateY(-50%)' });
    }
  }, [ref, side]);

  const hide = useCallback(() => {
    clearTimer();
    if (shown.current) {
      shown.current = false;
      lastHiddenAt = Date.now();
      anyVisible = false;
    }
    setPos(null);
  }, []);

  useEffect(() => {
    if (disabled) hide();
  }, [disabled, hide]);

  useEffect(() => clearTimer, []);

  const visible = pos !== null;
  useEffect(() => {
    if (!visible) return;
    const onKeyDown = (event: KeyboardEvent) => {
      // Consumed: the Escape was for the tooltip, so page-level Escape
      // handlers that respect defaultPrevented (the monitors list clearing
      // its selection) leave it alone. Propagation continues for dialogs.
      if (event.key !== 'Escape') return;
      event.preventDefault();
      hide();
    };
    document.addEventListener('keydown', onKeyDown);
    window.addEventListener('scroll', hide, true);
    window.addEventListener('resize', hide);
    return () => {
      // Also runs when a visible tooltip's trigger unmounts (a route change).
      if (shown.current) {
        shown.current = false;
        anyVisible = false;
      }
      document.removeEventListener('keydown', onKeyDown);
      window.removeEventListener('scroll', hide, true);
      window.removeEventListener('resize', hide);
    };
  }, [visible, hide]);

  const triggerProps = {
    onPointerEnter: (event: PointerEvent<T>) => {
      if (disabled || event.pointerType !== 'mouse') return;
      clearTimer();
      if (anyVisible || Date.now() - lastHiddenAt < WARM_WINDOW_MS) show();
      else timer.current = window.setTimeout(show, side === 'top' ? TOP_HOVER_DELAY_MS : HOVER_DELAY_MS);
    },
    onPointerLeave: hide,
    onPointerDown: hide,
    onFocus: (event: FocusEvent<T>) => {
      // Keyboard focus only: a click already has the pointer over the
      // trigger, and its hover handles the tooltip.
      if (disabled || !event.currentTarget.matches(':focus-visible')) return;
      clearTimer();
      show();
    },
    onBlur: hide,
    'aria-describedby': describe && visible ? id : undefined,
    'aria-keyshortcuts': ariaShortcut ?? shortcut,
  };

  // Keep a rendered tooltip inside the viewport: the anchor position is set
  // before its width is known, so nudge it once it has laid out.
  const tipRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = tipRef.current;
    if (!el || !pos) return;
    const rect = el.getBoundingClientRect();
    const overflowRight = rect.right - (window.innerWidth - VIEWPORT_PADDING);
    const overflowLeft = VIEWPORT_PADDING - rect.left;
    if (overflowRight > 0) el.style.left = `${pos.left - overflowRight}px`;
    else if (overflowLeft > 0) el.style.left = `${pos.left + overflowLeft}px`;
  }, [pos]);

  const tooltip =
    visible && !disabled && typeof document !== 'undefined'
      ? createPortal(
          <div
            ref={tipRef}
            id={id}
            role="tooltip"
            className="glass-overlay pointer-events-none fixed z-[100] flex animate-fade-in items-center gap-2 whitespace-nowrap !rounded-lg px-2.5 py-1.5 text-xs font-medium text-white"
            style={{ left: pos.left, top: pos.top, transform: pos.transform }}
          >
            {label}
            {shortcut && (
              <kbd className="rounded border border-edge px-1 font-mono text-2xs font-normal leading-4 text-slate-400">
                {shortcut}
              </kbd>
            )}
          </div>,
          document.body
        )
      : null;

  return { ref, triggerProps, tooltip, hide };
}

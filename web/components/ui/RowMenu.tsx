'use client';

import { createContext, KeyboardEvent, ReactNode, useContext, useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { createPortal } from 'react-dom';
import { MoreVertical } from 'lucide-react';
import { ICON_BUTTON_CLASS, ICON_BUTTON_SM_CLASS } from './IconButton';
import { FOCUS_RING_INSET } from './focus';
import { useTooltip } from './Tooltip';

const CloseContext = createContext<() => void>(() => {});

const MENU_WIDTH = 160;

// Every focusable entry: RowMenuItems plus custom content (option buttons).
const ITEM_SELECTOR = 'button:not([disabled]), a[href]';

// "⋮" menu for rows with more actions than fit as icon buttons. The menu is
// position:fixed so it escapes scrolling/overflow containers, and flips up
// when there is no room below the trigger. It portals to <body>: rows sit in
// glass panels, and backdrop-filter would make the panel the containing block
// of a fixed child, offsetting and clipping the menu.
//
// Keyboard: opening moves focus to the first item; arrows, Home and End move
// through the items; Escape and Tab close it and return focus to the ⋮
// trigger. The portal puts the menu at the end of <body>, so without this
// the items would be unreachable by Tab.
export default function RowMenu({
  label,
  children,
  disabled = false,
  size = 'md',
}: {
  label: string;
  children: ReactNode;
  /** Disables the ⋮ trigger, matching the row's IconButtons. */
  disabled?: boolean;
  /** Matches IconButton `size` so the trigger lines up with them. */
  size?: 'sm' | 'md';
}) {
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  const [pos, setPos] = useState<{ top: number; left: number; up: boolean } | null>(null);
  const menuId = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const tip = useTooltip<HTMLButtonElement>({ label, side: 'top', anchorRef: triggerRef });
  const menuRef = useRef<HTMLDivElement>(null);
  const close = () => {
    setAnchor(null);
    setPos(null);
  };
  const closeToTrigger = () => {
    close();
    triggerRef.current?.focus();
  };
  const items = () => Array.from(menuRef.current?.querySelectorAll<HTMLElement>(ITEM_SELECTOR) ?? []);

  useLayoutEffect(() => {
    if (!anchor || !menuRef.current) return;
    const height = menuRef.current.offsetHeight;
    const openUp = anchor.bottom + 4 + height > window.innerHeight;
    setPos({
      top: openUp ? anchor.top - 4 - height : anchor.bottom + 4,
      left: Math.max(8, anchor.right - MENU_WIDTH),
      up: openUp,
    });
  }, [anchor]);

  // Once placed, focus the first item (preventScroll: a scroll would close it).
  const placed = pos !== null;
  useEffect(() => {
    if (placed) menuRef.current?.querySelector<HTMLElement>(ITEM_SELECTOR)?.focus({ preventScroll: true });
  }, [placed]);

  useEffect(() => {
    if (!anchor) return;
    // Scrolling inside the menu itself (a long custom list) keeps it open.
    const onScroll = (e: Event) => {
      if (e.target instanceof Node && menuRef.current?.contains(e.target)) return;
      close();
    };
    window.addEventListener('click', close);
    window.addEventListener('scroll', onScroll, true);
    window.addEventListener('resize', close);
    return () => {
      window.removeEventListener('click', close);
      window.removeEventListener('scroll', onScroll, true);
      window.removeEventListener('resize', close);
    };
  }, [anchor]);

  const onMenuKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Escape' || e.key === 'Tab') {
      e.preventDefault();
      e.stopPropagation();
      closeToTrigger();
      return;
    }
    const list = items();
    if (list.length === 0) return;
    const current = list.indexOf(document.activeElement as HTMLElement);
    let next: number | null = null;
    if (e.key === 'ArrowDown') next = current < 0 ? 0 : (current + 1) % list.length;
    else if (e.key === 'ArrowUp') next = current <= 0 ? list.length - 1 : current - 1;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = list.length - 1;
    if (next === null) return;
    e.preventDefault();
    list[next].focus({ preventScroll: true });
  };

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        {...tip.triggerProps}
        onClick={(e) => {
          e.stopPropagation();
          if (anchor) close();
          else setAnchor(triggerRef.current?.getBoundingClientRect() ?? null);
        }}
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={Boolean(anchor)}
        aria-controls={anchor ? menuId : undefined}
        disabled={disabled}
        className={`${ICON_BUTTON_CLASS} ${size === 'sm' ? ICON_BUTTON_SM_CLASS : ''}`}
      >
        <MoreVertical strokeWidth={1.75} />
      </button>
      {!anchor && tip.tooltip}
      {anchor && createPortal(
        <div
          ref={menuRef}
          id={menuId}
          role="menu"
          aria-label={label}
          // Grows out of the corner nearest the trigger: top-right when it
          // opens below, bottom-right when it flips up. Mounted once placed,
          // so the animation runs from the final position.
          className={`glass-overlay fixed z-50 !rounded-xl p-1 ${
            pos ? `animate-pop-in ${pos.up ? 'origin-bottom-right' : 'origin-top-right'}` : ''
          }`}
          style={{
            width: MENU_WIDTH,
            top: pos?.top ?? 0,
            left: pos?.left ?? 0,
            visibility: pos ? 'visible' : 'hidden',
          }}
          onClick={(e) => e.stopPropagation()}
          onKeyDown={onMenuKeyDown}
        >
          {/* Items close back to the trigger, so a dialog an item opens
              (Delete) returns focus there instead of to <body>. */}
          <CloseContext.Provider value={closeToTrigger}>{children}</CloseContext.Provider>
        </div>,
        document.body
      )}
    </>
  );
}

export function RowMenuItem({
  icon,
  onSelect,
  href,
  danger,
  children,
}: {
  icon?: ReactNode;
  onSelect?: () => void;
  href?: string;
  danger?: boolean;
  children: ReactNode;
}) {
  const close = useContext(CloseContext);
  // Focus is the item fill (the menu's roving highlight) plus an inset ring:
  // the fill alone is a ~6% tint, too faint to find the keyboard by.
  const className = `flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-ui transition-colors duration-fast ${FOCUS_RING_INSET} ${
    danger
      ? 'text-rose-300 hover:bg-rose-500/10 hover:text-rose-200 focus-visible:bg-rose-500/10 focus-visible:text-rose-200 active:bg-rose-500/20'
      : 'text-slate-300 hover:bg-well-hover hover:text-white focus-visible:bg-well-hover focus-visible:text-white active:bg-control-hover'
  }`;
  const content = (
    <>
      {icon && <span className="inline-flex h-3.5 w-3.5 items-center">{icon}</span>}
      {children}
    </>
  );
  if (href) {
    return (
      <Link href={href} role="menuitem" tabIndex={-1} className={className} onClick={close}>
        {content}
      </Link>
    );
  }
  return (
    <button
      type="button"
      role="menuitem"
      tabIndex={-1}
      className={className}
      onClick={() => {
        close();
        onSelect?.();
      }}
    >
      {content}
    </button>
  );
}

export function RowMenuSeparator() {
  return <div role="separator" className="my-1 border-t border-divider" />;
}

/**
 * A labelled run of items inside a RowMenu ("Snooze alerts" + 1h/4h/24h).
 * The caption is visible; the group carries the same name for screen readers.
 */
export function RowMenuGroup({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div role="group" aria-label={label}>
      <div className="px-2.5 pb-1 pt-1.5 text-2xs font-medium text-slate-500" aria-hidden="true">
        {label}
      </div>
      {children}
    </div>
  );
}

// Closes the menu from custom content (e.g. a row of option buttons).
export function useCloseRowMenu() {
  return useContext(CloseContext);
}

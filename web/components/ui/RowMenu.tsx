'use client';

import { createContext, ReactNode, useContext, useEffect, useLayoutEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { MoreVertical } from 'lucide-react';
import { ICON_BUTTON_CLASS } from './IconButton';

const CloseContext = createContext<() => void>(() => {});

const MENU_WIDTH = 160;

// "⋮" menu for rows with more actions than fit as icon buttons. The menu is
// position:fixed so it escapes scrolling/overflow containers, and flips up
// when there is no room below the trigger.
export default function RowMenu({ label, children }: { label: string; children: ReactNode }) {
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const close = () => {
    setAnchor(null);
    setPos(null);
  };

  useLayoutEffect(() => {
    if (!anchor || !menuRef.current) return;
    const height = menuRef.current.offsetHeight;
    const openUp = anchor.bottom + 4 + height > window.innerHeight;
    setPos({
      top: openUp ? anchor.top - 4 - height : anchor.bottom + 4,
      left: Math.max(8, anchor.right - MENU_WIDTH),
    });
  }, [anchor]);

  useEffect(() => {
    if (!anchor) return;
    window.addEventListener('click', close);
    window.addEventListener('scroll', close, true);
    window.addEventListener('resize', close);
    return () => {
      window.removeEventListener('click', close);
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('resize', close);
    };
  }, [anchor]);

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          if (anchor) close();
          else setAnchor(triggerRef.current?.getBoundingClientRect() ?? null);
        }}
        title={label}
        aria-label={label}
        aria-expanded={Boolean(anchor)}
        className={ICON_BUTTON_CLASS}
      >
        <MoreVertical strokeWidth={1.75} />
      </button>
      {anchor && (
        <div
          ref={menuRef}
          role="menu"
          className="fixed z-50 rounded-lg border border-white/[0.08] bg-slate-900/95 p-1 shadow-lg backdrop-blur-sm"
          style={{
            width: MENU_WIDTH,
            top: pos?.top ?? 0,
            left: pos?.left ?? 0,
            visibility: pos ? 'visible' : 'hidden',
          }}
          onClick={(e) => e.stopPropagation()}
        >
          <CloseContext.Provider value={close}>{children}</CloseContext.Provider>
        </div>
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
  const className = `flex w-full items-center gap-2 rounded px-2.5 py-1.5 text-left text-xs transition-colors ${
    danger
      ? 'text-rose-300 hover:bg-rose-500/10 hover:text-rose-200'
      : 'text-slate-300 hover:bg-white/[0.06] hover:text-white'
  }`;
  const content = (
    <>
      {icon && <span className="inline-flex h-3.5 w-3.5 items-center">{icon}</span>}
      {children}
    </>
  );
  if (href) {
    return (
      <Link href={href} role="menuitem" className={className} onClick={close}>
        {content}
      </Link>
    );
  }
  return (
    <button
      type="button"
      role="menuitem"
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
  return <div className="my-1 border-t border-white/[0.06]" />;
}

// Closes the menu from custom content (e.g. a row of option buttons).
export function useCloseRowMenu() {
  return useContext(CloseContext);
}

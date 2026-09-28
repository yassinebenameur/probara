'use client';

import { ReactNode } from 'react';
import Link from 'next/link';
import { Loader2 } from 'lucide-react';

interface IconButtonProps {
  icon: ReactNode;
  /** Tooltip and accessible name — the button has no visible text. */
  label: string;
  onClick?: () => void;
  href?: string;
  danger?: boolean;
  disabled?: boolean;
  loading?: boolean;
}

// Square so the icon sits dead centre; RowMenu's trigger uses the same box.
export const ICON_BUTTON_CLASS =
  'inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-slate-400 transition-colors ' +
  'hover:bg-white/[0.06] hover:text-white focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/40 ' +
  'disabled:cursor-not-allowed disabled:opacity-40 [&_svg]:h-3.5 [&_svg]:w-3.5';

const DANGER_CLASS = 'hover:!bg-rose-500/10 hover:!text-rose-300';

// Row action for tables and lists: icon only, named by its tooltip. Use up to
// three per row; beyond that, put the actions in a RowMenu.
export default function IconButton({ icon, label, onClick, href, danger, disabled, loading }: IconButtonProps) {
  const className = `${ICON_BUTTON_CLASS} ${danger ? DANGER_CLASS : ''}`;
  const content = loading ? <Loader2 className="animate-spin" aria-hidden="true" /> : icon;
  if (href) {
    return (
      <Link href={href} title={label} aria-label={label} className={className}>
        {content}
      </Link>
    );
  }
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled || loading}
      title={label}
      aria-label={label}
      className={className}
    >
      {content}
    </button>
  );
}

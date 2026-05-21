'use client';

import { ButtonHTMLAttributes, ReactNode } from 'react';

interface FilterChipProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  selected?: boolean;
  count?: number;
  icon?: ReactNode;
  children: ReactNode;
}

export default function FilterChip({
  selected = false,
  count,
  icon,
  className = '',
  children,
  ...props
}: FilterChipProps) {
  const base =
    'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-medium transition-colors duration-150 focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/40 disabled:opacity-40 disabled:cursor-not-allowed';
  const palette = selected
    ? 'border-cyan-500/40 bg-cyan-500/15 text-cyan-200'
    : 'border-white/10 bg-white/[0.04] text-slate-400 hover:bg-white/[0.08] hover:text-white';
  return (
    <button
      type="button"
      aria-pressed={selected}
      className={`${base} ${palette} ${className}`.trim()}
      {...props}
    >
      {icon && <span className="h-3 w-3 inline-flex items-center" aria-hidden="true">{icon}</span>}
      <span>{children}</span>
      {typeof count === 'number' && (
        <span
          className={`ml-0.5 rounded-full px-1.5 py-px text-[0.6rem] ${
            selected ? 'bg-cyan-500/25 text-cyan-100' : 'bg-white/[0.06] text-slate-400'
          }`}
        >
          {count}
        </span>
      )}
    </button>
  );
}

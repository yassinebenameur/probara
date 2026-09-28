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
    'inline-flex h-7 items-center gap-1.5 whitespace-nowrap rounded-full border px-3 text-xs font-medium transition-colors duration-150 focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50 disabled:opacity-40 disabled:cursor-not-allowed';
  const palette = selected
    ? 'border-cyan-500/40 bg-cyan-500/[0.14] text-cyan-300'
    : 'border-edge-control bg-control text-slate-400 hover:bg-control-hover hover:text-white';
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
          className={`ml-0.5 rounded-full px-1.5 text-[11px] leading-4 tabular-nums ${
            selected ? 'bg-cyan-500/20 text-cyan-200' : 'bg-well-hover text-slate-400'
          }`}
        >
          {count}
        </span>
      )}
    </button>
  );
}

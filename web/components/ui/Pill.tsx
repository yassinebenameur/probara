import { ReactNode } from 'react';

export type PillTone = 'success' | 'danger' | 'warning' | 'info' | 'neutral' | 'tag';
export type PillSize = 'xs' | 'sm';

export interface PillTagColor {
  bg: string;
  text: string;
  border: string;
}

interface PillProps {
  tone?: PillTone;
  size?: PillSize;
  dot?: boolean;
  icon?: ReactNode;
  color?: PillTagColor;
  className?: string;
  children: ReactNode;
}

const TONE: Record<Exclude<PillTone, 'tag'>, { bg: string; text: string; border: string }> = {
  success: {
    bg: 'bg-emerald-500/[0.13]',
    text: 'text-emerald-300',
    border: 'border-transparent',
  },
  danger: {
    bg: 'bg-rose-500/[0.13]',
    text: 'text-rose-300',
    border: 'border-transparent',
  },
  warning: {
    bg: 'bg-amber-500/[0.15]',
    text: 'text-amber-300',
    border: 'border-transparent',
  },
  info: {
    bg: 'bg-sky-500/[0.14]',
    text: 'text-sky-300',
    border: 'border-transparent',
  },
  neutral: {
    bg: 'bg-well-hover',
    text: 'text-slate-300',
    border: 'border-transparent',
  },
};

const SIZE: Record<PillSize, string> = {
  xs: 'text-[0.7rem] leading-4 px-1.5 py-px gap-1',
  sm: 'text-xs leading-4 px-2 py-0.5 gap-1.5',
};

export default function Pill({
  tone = 'neutral',
  size = 'sm',
  dot = false,
  icon,
  color,
  className = '',
  children,
}: PillProps) {
  const palette =
    tone === 'tag' && color
      ? { bg: color.bg, text: color.text, border: color.border }
      : TONE[(tone === 'tag' ? 'neutral' : tone) as Exclude<PillTone, 'tag'>];
  const classes = `inline-flex items-center whitespace-nowrap rounded-full border font-medium ${palette.bg} ${palette.text} ${palette.border} ${SIZE[size]} ${className}`.trim();
  return (
    <span className={classes}>
      {dot && (
        <span
          className="h-1.5 w-1.5 rounded-full bg-current"
          aria-hidden="true"
        />
      )}
      {icon && (
        <span className="h-3 w-3 inline-flex items-center" aria-hidden="true">
          {icon}
        </span>
      )}
      {children}
    </span>
  );
}

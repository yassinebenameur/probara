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

const TONE: Record<Exclude<PillTone, 'tag'>, { bg: string; text: string; border: string; dotGlow: string }> = {
  success: {
    bg: 'bg-emerald-500/12',
    text: 'text-emerald-200',
    border: 'border-emerald-500/35',
    dotGlow: 'shadow-[0_0_0_3px_rgba(70,209,127,0.18)]',
  },
  danger: {
    bg: 'bg-rose-500/12',
    text: 'text-rose-200',
    border: 'border-rose-500/35',
    dotGlow: 'shadow-[0_0_0_3px_rgba(240,74,90,0.18)]',
  },
  warning: {
    bg: 'bg-amber-500/12',
    text: 'text-amber-200',
    border: 'border-amber-500/35',
    dotGlow: 'shadow-[0_0_0_3px_rgba(230,178,63,0.18)]',
  },
  info: {
    bg: 'bg-cyan-500/12',
    text: 'text-cyan-200',
    border: 'border-cyan-500/35',
    dotGlow: 'shadow-[0_0_0_3px_rgba(255,90,36,0.18)]',
  },
  neutral: {
    bg: 'bg-white/[0.04]',
    text: 'text-slate-300',
    border: 'border-white/10',
    dotGlow: 'shadow-[0_0_0_3px_rgba(174,182,194,0.15)]',
  },
};

const SIZE: Record<PillSize, string> = {
  xs: 'text-[0.7rem] px-2 py-0.5 gap-1',
  sm: 'text-xs px-2.5 py-0.5 gap-1.5',
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
      ? { bg: color.bg, text: color.text, border: color.border, dotGlow: '' }
      : TONE[(tone === 'tag' ? 'neutral' : tone) as Exclude<PillTone, 'tag'>];
  const classes = `inline-flex items-center rounded-full border font-medium ${palette.bg} ${palette.text} ${palette.border} ${SIZE[size]} ${className}`.trim();
  return (
    <span className={classes}>
      {dot && (
        <span
          className={`h-1.5 w-1.5 rounded-full bg-current ${palette.dotGlow}`}
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

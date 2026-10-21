'use client';

import { ButtonHTMLAttributes, Children, cloneElement, isValidElement, ReactElement, ReactNode } from 'react';
import { Loader2 } from 'lucide-react';
import { useMinDuration } from '@/lib/useMinDuration';
import { FOCUS_RING } from './focus';

export type ButtonVariant = 'ghost' | 'accent' | 'danger' | 'subtle';
export type ButtonSize = 'sm' | 'xs';

interface ButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  icon?: ReactNode;
  iconTrailing?: ReactNode;
  loading?: boolean;
  asChild?: boolean;
  children: ReactNode;
}

// Each variant has a pressed state as well as a hover: on touch there is no
// hover, and a click with no visible response reads as a missed tap.
const VARIANT: Record<ButtonVariant, string> = {
  ghost:
    'border border-edge-control bg-control text-slate-200 hover:bg-control-hover hover:text-white active:bg-well-hover',
  accent:
    'bg-cyan-500 font-semibold text-onaccent shadow-accent hover:bg-cyan-400 active:bg-cyan-600 active:shadow-none ' +
    'disabled:bg-slate-700 disabled:text-slate-400 disabled:shadow-none disabled:opacity-100',
  danger:
    'border border-rose-500/25 bg-rose-500/10 text-rose-300 hover:bg-rose-500/[0.16] hover:text-rose-200 active:bg-rose-500/20',
  subtle:
    'text-slate-400 hover:bg-well-hover hover:text-white active:bg-well-hover',
};

const SIZE: Record<ButtonSize, string> = {
  sm: 'h-9 px-3.5 text-ui gap-2 rounded-lg',
  xs: 'h-7 px-2.5 text-xs gap-1.5 rounded-lg',
};

const ICON_SIZE: Record<ButtonSize, string> = {
  sm: 'h-4 w-4',
  xs: 'h-3.5 w-3.5',
};

const BASE =
  'inline-flex items-center justify-center whitespace-nowrap font-medium ' +
  'transition-[background-color,border-color,color,box-shadow,transform] duration-fast ease-standard ' +
  'active:scale-[0.98] motion-reduce:active:scale-100 disabled:active:scale-100 aria-disabled:active:scale-100 ' +
  `${FOCUS_RING} disabled:opacity-50 disabled:cursor-not-allowed aria-disabled:cursor-progress`;

export default function Button({
  variant = 'ghost',
  size = 'sm',
  icon,
  iconTrailing,
  loading = false,
  asChild = false,
  className = '',
  children,
  disabled,
  onClick,
  ...props
}: ButtonProps) {
  // Held for a minimum time so a fast save still shows its spinner.
  const busy = useMinDuration(loading);
  const iconClass = ICON_SIZE[size];
  const renderedIcon = busy ? (
    <Loader2 className={`${iconClass} animate-spin`} aria-hidden="true" />
  ) : icon ? (
    <span className={`${iconClass} inline-flex items-center [&_svg]:h-full [&_svg]:w-full`}>{icon}</span>
  ) : null;
  const renderedTrailing = !busy && iconTrailing ? (
    <span className={`${iconClass} inline-flex items-center [&_svg]:h-full [&_svg]:w-full`}>{iconTrailing}</span>
  ) : null;

  const classes = `${BASE} ${VARIANT[variant]} ${SIZE[size]} ${className}`.trim();

  if (asChild) {
    const child = Children.only(children);
    if (!isValidElement(child)) {
      throw new Error('Button asChild requires a single React element child');
    }
    const el = child as ReactElement<{ className?: string; children?: ReactNode }>;
    return cloneElement(el, {
      className: `${classes} ${el.props.className ?? ''}`.trim(),
      children: (
        <>
          {renderedIcon}
          <span>{el.props.children}</span>
          {renderedTrailing}
        </>
      ),
    });
  }

  // While busy the button stays focusable (aria-disabled, not disabled):
  // disabling the focused button would drop keyboard focus to <body>. The
  // click guard also stops a form's implicit submit while it is busy.
  const blocked = busy && !disabled;
  return (
    <button
      className={classes}
      disabled={disabled}
      aria-disabled={blocked || undefined}
      aria-busy={busy || undefined}
      onClick={(event) => {
        if (blocked) {
          event.preventDefault();
          return;
        }
        onClick?.(event);
      }}
      {...props}
    >
      {renderedIcon}
      <span>{children}</span>
      {renderedTrailing}
    </button>
  );
}

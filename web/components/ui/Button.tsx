'use client';

import { ButtonHTMLAttributes, Children, cloneElement, isValidElement, ReactElement, ReactNode } from 'react';
import { Loader2 } from 'lucide-react';

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

const VARIANT: Record<ButtonVariant, string> = {
  ghost:
    'border border-edge-control bg-control text-slate-200 hover:bg-control-hover hover:text-white',
  accent:
    'bg-cyan-500 font-semibold text-onaccent shadow-accent hover:bg-[#ff6d3c] ' +
    'disabled:bg-slate-700 disabled:text-slate-400 disabled:shadow-none disabled:opacity-100',
  danger:
    'border border-rose-500/25 bg-rose-500/10 text-rose-300 hover:bg-rose-500/[0.16] hover:text-rose-200',
  subtle:
    'text-slate-400 hover:bg-well-hover hover:text-white',
};

const SIZE: Record<ButtonSize, string> = {
  sm: 'h-9 px-3.5 text-[13px] gap-2 rounded-lg',
  xs: 'h-7 px-2.5 text-xs gap-1.5 rounded-lg',
};

const ICON_SIZE: Record<ButtonSize, string> = {
  sm: 'h-4 w-4',
  xs: 'h-3.5 w-3.5',
};

const BASE =
  'inline-flex items-center justify-center whitespace-nowrap font-medium transition-colors duration-150 ' +
  'focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50 focus-visible:ring-offset-0 ' +
  'disabled:opacity-50 disabled:cursor-not-allowed';

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
  ...props
}: ButtonProps) {
  const iconClass = ICON_SIZE[size];
  const renderedIcon = loading ? (
    <Loader2 className={`${iconClass} animate-spin`} aria-hidden="true" />
  ) : icon ? (
    <span className={`${iconClass} inline-flex items-center [&_svg]:h-full [&_svg]:w-full`}>{icon}</span>
  ) : null;
  const renderedTrailing = !loading && iconTrailing ? (
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

  return (
    <button
      className={classes}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      {...props}
    >
      {renderedIcon}
      <span>{children}</span>
      {renderedTrailing}
    </button>
  );
}

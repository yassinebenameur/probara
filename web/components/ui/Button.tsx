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
    'border border-white/10 bg-white/[0.04] text-slate-200 hover:bg-white/[0.08] hover:text-white',
  accent:
    'bg-cyan-500 text-white hover:bg-cyan-400 shadow-[0_4px_20px_rgba(255,90,36,0.18)]',
  danger:
    'border border-rose-500/30 bg-rose-500/10 text-rose-200 hover:bg-rose-500/20 hover:text-rose-100',
  subtle:
    'text-slate-400 hover:bg-white/[0.05] hover:text-white',
};

const SIZE: Record<ButtonSize, string> = {
  sm: 'px-3 py-2 text-xs gap-2 rounded-[12px]',
  xs: 'px-2.5 py-1.5 text-[0.7rem] gap-1.5 rounded-[10px]',
};

const ICON_SIZE: Record<ButtonSize, string> = {
  sm: 'h-4 w-4',
  xs: 'h-3.5 w-3.5',
};

const BASE =
  'inline-flex items-center justify-center font-medium transition-colors duration-150 ' +
  'focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/40 focus-visible:ring-offset-0 ' +
  'disabled:opacity-40 disabled:cursor-not-allowed';

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
    <span className={`${iconClass} inline-flex items-center`}>{icon}</span>
  ) : null;
  const renderedTrailing = !loading && iconTrailing ? (
    <span className={`${iconClass} inline-flex items-center`}>{iconTrailing}</span>
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

'use client';

import { ChangeEvent, InputHTMLAttributes, ReactNode } from 'react';
import { CHECK_GLYPH_CLASS, CHECK_INPUT_CLASS } from './Checkbox';

type NativeInputProps = Omit<
  InputHTMLAttributes<HTMLInputElement>,
  'type' | 'size' | 'onChange' | 'checked' | 'className' | 'children'
>;

export interface RadioProps extends NativeInputProps {
  checked?: boolean;
  /** Fires when this option becomes selected. */
  onChange?: (checked: boolean, event: ChangeEvent<HTMLInputElement>) => void;
  label?: ReactNode;
  description?: ReactNode;
  size?: 'sm' | 'md';
  className?: string;
}

const DIMS = {
  sm: { box: 'h-3.5 w-3.5', dot: 'h-1.5 w-1.5' },
  md: { box: 'h-4 w-4', dot: 'h-[6px] w-[6px]' },
} as const;

/**
 * The one radio button: a native input (arrow keys between same-`name`
 * options come for free), same tokens as Checkbox. Selected = neutral ink
 * fill with a dot in the panel colour.
 */
export default function Radio({
  checked,
  onChange,
  label,
  description,
  size = 'md',
  disabled = false,
  className = '',
  ...inputProps
}: RadioProps) {
  const dims = DIMS[size];
  const box = (
    <span className={`relative inline-flex shrink-0 ${dims.box} ${label ? 'mt-0.5' : ''}`}>
      <input
        type="radio"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange?.(event.target.checked, event)}
        className={`${CHECK_INPUT_CLASS} ${dims.box} rounded-full`}
        {...inputProps}
      />
      <span className={`${CHECK_GLYPH_CLASS} hidden rounded-full bg-[rgb(var(--panel-rgb))] animate-pop-in peer-checked:block ${dims.dot}`} />
    </span>
  );
  const state = disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer';

  if (label === undefined && description === undefined) {
    return (
      <label
        className={`group/check relative inline-flex shrink-0 before:absolute before:-inset-1 before:content-[''] ${state} ${className}`.trim()}
      >
        {box}
      </label>
    );
  }

  return (
    <label className={`group/check inline-flex items-start gap-2.5 py-1 ${state} ${className}`.trim()}>
      {box}
      <span className="min-w-0">
        {label !== undefined && <span className="block text-sm text-slate-200">{label}</span>}
        {description !== undefined && <span className="mt-0.5 block text-xs text-slate-500">{description}</span>}
      </span>
    </label>
  );
}

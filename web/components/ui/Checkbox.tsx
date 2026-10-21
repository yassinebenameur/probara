'use client';

import { ChangeEvent, InputHTMLAttributes, ReactNode, useEffect, useRef } from 'react';
import { FOCUS_RING } from './focus';

type NativeInputProps = Omit<
  InputHTMLAttributes<HTMLInputElement>,
  'type' | 'size' | 'onChange' | 'checked' | 'className' | 'children'
>;

export interface CheckboxProps extends NativeInputProps {
  checked?: boolean;
  /** The "some selected" dash; announced as mixed. */
  indeterminate?: boolean;
  onChange?: (checked: boolean, event: ChangeEvent<HTMLInputElement>) => void;
  /** Visible label; clicking it toggles the box. Omit and pass aria-label for a bare box. */
  label?: ReactNode;
  /** One muted line under the label. */
  description?: ReactNode;
  /** `sm` (14px) for dense pickers; `md` (16px) default. */
  size?: 'sm' | 'md';
  /**
   * Stop clicks reaching the row around it (selection boxes inside a
   * clickable row or link).
   */
  stopPropagation?: boolean;
  /** Let the label column take the remaining width (full-width picker rows). */
  stretch?: boolean;
  /** Classes for the outer label. */
  className?: string;
}

const DIMS = {
  sm: { box: 'h-3.5 w-3.5', glyph: 'h-2 w-2' },
  md: { box: 'h-4 w-4', glyph: 'h-2.5 w-2.5' },
} as const;

// Native input, restyled: label association, Space, form submission and
// :indeterminate come for free. Unchecked is a 1px --input-edge outline (≥3:1);
// checked and mixed fill with neutral ink (bg-white flips to ink in light) and
// draw the glyph in the panel colour. Selection is never orange: orange is
// for actions.
export const CHECK_INPUT_CLASS =
  'peer m-0 shrink-0 cursor-[inherit] appearance-none border border-[var(--input-edge,var(--control-edge-strong))] bg-transparent ' +
  'transition-[background-color,border-color] duration-fast ease-standard ' +
  'group-hover/check:border-slate-400 checked:!border-white checked:bg-white ' +
  `indeterminate:!border-white indeterminate:bg-white ${FOCUS_RING}`;

export const CHECK_GLYPH_CLASS = 'pointer-events-none absolute inset-0 m-auto text-[rgb(var(--panel-rgb))]';

/**
 * The one checkbox. Labelled (`label`, optional `description`) or bare with
 * an `aria-label`; either way the hit area is at least 24px.
 */
export default function Checkbox({
  checked,
  indeterminate = false,
  onChange,
  label,
  description,
  size = 'md',
  stopPropagation = false,
  stretch = false,
  disabled = false,
  className = '',
  ...inputProps
}: CheckboxProps) {
  const ref = useRef<HTMLInputElement>(null);
  const dims = DIMS[size];

  useEffect(() => {
    if (ref.current) ref.current.indeterminate = indeterminate;
  }, [indeterminate]);

  const box = (
    <span className={`relative inline-flex shrink-0 ${dims.box} ${label ? 'mt-0.5' : ''}`}>
      <input
        ref={ref}
        type="checkbox"
        checked={checked}
        disabled={disabled}
        aria-checked={indeterminate ? 'mixed' : undefined}
        onChange={(event) => onChange?.(event.target.checked, event)}
        className={`${CHECK_INPUT_CLASS} ${dims.box} rounded-[4px]`}
        {...inputProps}
      />
      {!indeterminate && (
        <svg
          className={`${CHECK_GLYPH_CLASS} hidden animate-pop-in peer-checked:block ${dims.glyph}`}
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={3.5}
          aria-hidden="true"
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
        </svg>
      )}
      {indeterminate && (
        <svg className={`${CHECK_GLYPH_CLASS} animate-pop-in ${dims.glyph}`} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <rect x="4" y="10.5" width="16" height="3" rx="1.5" />
        </svg>
      )}
    </span>
  );

  const state = disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer';
  const stop = stopPropagation ? (event: { stopPropagation: () => void }) => event.stopPropagation() : undefined;

  if (label === undefined && description === undefined) {
    // Bare box: the label element only widens the hit area to 24px.
    return (
      <label
        className={`group/check relative inline-flex shrink-0 before:absolute before:-inset-1 before:content-[''] ${state} ${className}`.trim()}
        onClick={stop}
      >
        {box}
      </label>
    );
  }

  return (
    <label className={`group/check inline-flex items-start gap-2.5 py-1 ${state} ${className}`.trim()} onClick={stop}>
      {box}
      <span className={stretch ? 'min-w-0 flex-1' : 'min-w-0'}>
        {label !== undefined && <span className="block text-sm text-slate-200">{label}</span>}
        {description !== undefined && <span className="mt-0.5 block text-xs text-slate-500">{description}</span>}
      </span>
    </label>
  );
}

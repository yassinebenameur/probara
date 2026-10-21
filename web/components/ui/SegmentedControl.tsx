'use client';

import { FOCUS_RING_INSET } from './focus';
import { handleRovingKeyDown, SEGMENT_ITEM, SELECTED_SEGMENT } from './roving';

export interface SegmentOption<T extends string> {
  value: T;
  /** Visible text; keep it short ("7d", "Medium"). */
  label: string;
  /** Long name ("7 days"): tooltip and accessible name when it differs from the label. */
  title?: string;
}

interface SegmentedControlProps<T extends string> {
  options: readonly SegmentOption<T>[];
  value: T;
  onChange: (value: T) => void;
  /** Accessible name; or point `labelledBy` at a visible heading. */
  label?: string;
  labelledBy?: string;
  describedBy?: string;
  /** `sm` (32px, beside `input-sm` selects) default; `xs` (28px) in dense toolbars. */
  size?: 'sm' | 'xs';
  disabled?: boolean;
  className?: string;
}

/**
 * A single choice among a few discrete stops (sensitivity, a window length):
 * a radiogroup drawn as the RangePicker/Tabs well, with the selected stop in
 * neutral raised glass. Only the selected segment is in the tab order and the
 * arrow keys move the selection. Time ranges that scope a page stay
 * RangePicker; this is for a setting inside a form.
 */
export default function SegmentedControl<T extends string>({
  options,
  value,
  onChange,
  label,
  labelledBy,
  describedBy,
  size = 'sm',
  disabled = false,
  className = '',
}: SegmentedControlProps<T>) {
  const item = size === 'xs' ? 'h-6 px-2 text-2xs' : 'h-7 min-w-[2.75rem] px-2.5 text-xs';
  const ids = options.map((o) => o.value);
  return (
    <div
      role="radiogroup"
      aria-label={labelledBy ? undefined : label}
      aria-labelledby={labelledBy}
      aria-describedby={describedBy}
      aria-disabled={disabled || undefined}
      onKeyDown={(e) => {
        if (!disabled) handleRovingKeyDown(e, ids, value, onChange);
      }}
      className={`inline-flex w-fit max-w-full items-center gap-0.5 overflow-x-auto rounded-lg bg-well p-0.5 shadow-[inset_0_0_0_1px_var(--glass-edge)] ${
        disabled ? 'opacity-50' : ''
      } ${className}`.trim()}
    >
      {options.map((option) => {
        const selected = option.value === value;
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            data-roving-id={option.value}
            aria-checked={selected}
            aria-label={option.title}
            title={option.title}
            tabIndex={selected ? 0 : -1}
            disabled={disabled}
            onClick={() => onChange(option.value)}
            className={`inline-flex items-center justify-center whitespace-nowrap rounded-md font-medium tabular-nums ${SEGMENT_ITEM} ${FOCUS_RING_INSET} ${item} disabled:cursor-not-allowed ${
              selected
                ? SELECTED_SEGMENT
                : 'text-slate-400 enabled:hover:bg-well-hover enabled:hover:text-slate-100'
            }`}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );
}

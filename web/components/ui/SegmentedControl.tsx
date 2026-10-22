'use client';

import { FOCUS_RING_INSET } from './focus';
import { handleRovingKeyDown, SEGMENT_ITEM, SELECTED_SEGMENT } from './roving';

export interface SegmentOption<T extends string> {
  value: T;
  /** Visible text; keep it short ("7d", "Medium"). */
  label: string;
  /**
   * Long name ("7 days"), shown as a tooltip and announced as the
   * description. The visible label stays the accessible name, so voice
   * control ("click 7d") matches what is on screen (WCAG 2.5.3).
   */
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
  /** `sm` (34px, the height of an `input-sm` select) default; `xs` (28px) in dense toolbars. */
  size?: 'sm' | 'xs';
  disabled?: boolean;
  /**
   * Every segment as wide as the widest (at least 3.75rem), so controls
   * stacked in one form share their left edges and step by whole segments.
   */
  equalWidth?: boolean;
  /** Fill the container width with equal segments (implies `equalWidth`). */
  stretch?: boolean;
  /**
   * Let long labels wrap onto further rows on a narrow screen instead of
   * scrolling sideways (a few sentence-length choices, not short stops).
   */
  wrap?: boolean;
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
  equalWidth = false,
  stretch = false,
  wrap = false,
  className = '',
}: SegmentedControlProps<T>) {
  const equal = equalWidth || stretch;
  const item =
    size === 'xs'
      ? 'h-6 px-2 text-2xs'
      : `h-[1.875rem] text-xs ${equal ? 'min-w-[3.75rem] px-2' : 'min-w-[2.75rem] px-2.5'}`;
  // Equal segments are a single-row grid of 1fr tracks: in a content-sized
  // grid each fr track resolves to the widest segment's max-content.
  const layout = stretch
    ? 'grid w-full auto-cols-fr grid-flow-col overflow-x-auto'
    : equal
      ? 'inline-grid w-fit max-w-full auto-cols-fr grid-flow-col overflow-x-auto'
      : wrap
        ? 'flex w-fit max-w-full flex-wrap'
        : 'inline-flex w-fit max-w-full overflow-x-auto';
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
      className={`${layout} items-center gap-0.5 rounded-lg bg-well p-0.5 shadow-[inset_0_0_0_1px_var(--glass-edge)] ${
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
            title={option.title}
            tabIndex={selected ? 0 : -1}
            disabled={disabled}
            // Like a native radio: choosing the selected stop is not a change.
            onClick={() => {
              if (!selected) onChange(option.value);
            }}
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

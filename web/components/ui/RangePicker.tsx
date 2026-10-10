'use client';

import { RangeId, RANGES } from '@/lib/format';
import { handleRovingKeyDown, SELECTED_SEGMENT } from './roving';

interface RangePickerProps<T extends RangeId> {
  /** The ranges this page supports, in display order (ids from RANGES). */
  ranges: readonly T[];
  value: T;
  onChange: (id: T) => void;
  /** Accessible name; defaults to "Time range". */
  label?: string;
  /** `sm` (28px, default) for headers and tab rows; `xs` (24px) inside dense toolbars. */
  size?: 'sm' | 'xs';
  className?: string;
}

/**
 * The one time-range control: a segmented well like Tabs, short labels
 * ("24h") with the long one ("Last 24 hours") as tooltip and accessible name.
 * A range scopes the whole page, so place it in the page header or at the end
 * of the tabs row (Tabs `trailing`), never inside a single card.
 */
export default function RangePicker<T extends RangeId>({
  ranges,
  value,
  onChange,
  label = 'Time range',
  size = 'sm',
  className = '',
}: RangePickerProps<T>) {
  const item = size === 'xs' ? 'h-6 px-2 text-2xs' : 'h-7 px-2.5 text-xs';
  return (
    <div
      role="radiogroup"
      aria-label={label}
      onKeyDown={(e) => handleRovingKeyDown(e, ranges, value, onChange)}
      className={`inline-flex w-fit max-w-full items-center gap-0.5 overflow-x-auto rounded-lg bg-well p-0.5 shadow-[inset_0_0_0_1px_var(--glass-edge)] ${className}`.trim()}
    >
      {ranges.map((id) => {
        const def = RANGES[id];
        const selected = id === value;
        return (
          <button
            key={id}
            type="button"
            role="radio"
            data-roving-id={id}
            aria-checked={selected}
            aria-label={def.long}
            title={def.long}
            tabIndex={selected ? 0 : -1}
            onClick={() => onChange(id)}
            className={`inline-flex items-center justify-center whitespace-nowrap rounded-md font-medium tabular-nums transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50 ${item} ${
              selected ? SELECTED_SEGMENT : 'text-slate-400 hover:text-slate-100'
            }`}
          >
            {def.short}
          </button>
        );
      })}
    </div>
  );
}

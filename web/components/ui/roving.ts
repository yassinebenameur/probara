import type { KeyboardEvent } from 'react';

/**
 * Arrow-key handling for a single-select group (Tabs, RangePicker): only the
 * selected item is in the tab order, and Left/Right/Home/End move the
 * selection and focus together. Items carry `data-roving-id`.
 */
export function handleRovingKeyDown<T extends string>(
  event: KeyboardEvent<HTMLElement>,
  ids: readonly T[],
  active: T,
  onChange: (id: T) => void
) {
  const index = ids.indexOf(active);
  let next: number;
  switch (event.key) {
    case 'ArrowRight':
    case 'ArrowDown':
      next = (index + 1) % ids.length;
      break;
    case 'ArrowLeft':
    case 'ArrowUp':
      next = (index - 1 + ids.length) % ids.length;
      break;
    case 'Home':
      next = 0;
      break;
    case 'End':
      next = ids.length - 1;
      break;
    default:
      return;
  }
  event.preventDefault();
  const id = ids[next];
  onChange(id);
  const target = event.currentTarget.querySelector<HTMLElement>(`[data-roving-id="${CSS.escape(id)}"]`);
  target?.focus();
}

/** Neutral raised glass: the one "selected" look for tabs, chips and segments. */
export const SELECTED_SEGMENT = 'bg-glass-strong text-white shadow-segment';

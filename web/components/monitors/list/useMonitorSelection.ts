'use client';

import { useState, useEffect, useRef } from 'react';

interface MonitorSelection {
  /** Selected ids. An id stays selected while its row is filtered out. */
  selected: Set<string>;
  /** True as soon as one box is ticked. */
  active: boolean;
  /** Toggle one row; with Shift held, the range from the last toggled row. */
  toggle: (id: string) => void;
  /** Select every shown top-level row, or deselect them when all are selected. */
  toggleAll: () => void;
  /** Empties the selection after a bulk action; the Shift anchor stays. */
  deselectAll: () => void;
  /** Empties the selection and forgets the Shift anchor (Escape, the bar's ×). */
  clear: () => void;
  allSelected: boolean;
  someSelected: boolean;
}

/**
 * Row selection for the monitors list (the alerts list's useAlertSelection
 * follows it). `order` is the shown top-level rows; `onClear` runs with
 * `clear`, for state that belongs to the selection (the bulk bar's inputs).
 */
export function useMonitorSelection(order: string[], onClear?: () => void): MonitorSelection {
  const [selectedMonitorIds, setSelectedMonitorIds] = useState<Set<string>>(new Set());
  // No separate select mode: a selection exists as soon as one box is ticked
  // (boxes reveal on row hover), and the floating action bar follows it.
  const selectionMode = selectedMonitorIds.size > 0;

  // Shift-click extends from the last toggled row to this one, over the
  // top-level rows in their shown order (members inside an expanded group
  // toggle singly). The change event carries no modifier, so the shift state
  // is tracked from the pointer/key event that precedes it.
  const shiftHeldRef = useRef(false);
  const selectionAnchorRef = useRef<string | null>(null);
  const shownOrderRef = useRef<string[]>([]);
  useEffect(() => {
    const track = (e: PointerEvent | globalThis.KeyboardEvent) => {
      shiftHeldRef.current = e.shiftKey;
    };
    window.addEventListener('pointerdown', track, true);
    window.addEventListener('keydown', track, true);
    window.addEventListener('keyup', track, true);
    return () => {
      window.removeEventListener('pointerdown', track, true);
      window.removeEventListener('keydown', track, true);
      window.removeEventListener('keyup', track, true);
    };
  }, []);

  const toggleMonitorSelection = (id: string) => {
    const anchor = selectionAnchorRef.current;
    const order = shownOrderRef.current;
    const from = anchor ? order.indexOf(anchor) : -1;
    const to = order.indexOf(id);
    const range = shiftHeldRef.current && from >= 0 && to >= 0 && from !== to;
    selectionAnchorRef.current = id;
    setSelectedMonitorIds((prev) => {
      const next = new Set(prev);
      const select = !prev.has(id);
      const ids = range ? order.slice(Math.min(from, to), Math.max(from, to) + 1) : [id];
      ids.forEach((rowId) => (select ? next.add(rowId) : next.delete(rowId)));
      return next;
    });
  };

  // Read through a ref so clearSelection stays stable for the Escape effect.
  const onClearRef = useRef(onClear);
  onClearRef.current = onClear;
  const clearSelection = () => {
    setSelectedMonitorIds(new Set());
    selectionAnchorRef.current = null;
    onClearRef.current?.();
  };

  shownOrderRef.current = order;

  // Escape drops the selection (unless a field or dialog has the key).
  useEffect(() => {
    if (!selectionMode) return;
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return;
      const target = e.target as HTMLElement | null;
      // A text field or dialog keeps its own Escape; the row boxes do not.
      if (target?.closest('input:not([type="checkbox"]), textarea, select, [role="dialog"]')) return;
      clearSelection();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [selectionMode]);

  const allFilteredSelected = order.length > 0 && order.every((id) => selectedMonitorIds.has(id));
  const someFilteredSelected = !allFilteredSelected && order.some((id) => selectedMonitorIds.has(id));

  const toggleSelectAllFiltered = () => {
    if (allFilteredSelected) {
      setSelectedMonitorIds((prev) => {
        const next = new Set(prev);
        order.forEach((id) => next.delete(id));
        return next;
      });
      return;
    }
    setSelectedMonitorIds((prev) => {
      const next = new Set(prev);
      order.forEach((id) => next.add(id));
      return next;
    });
  };

  return {
    selected: selectedMonitorIds,
    active: selectionMode,
    toggle: toggleMonitorSelection,
    toggleAll: toggleSelectAllFiltered,
    deselectAll: () => setSelectedMonitorIds(new Set()),
    clear: clearSelection,
    allSelected: allFilteredSelected,
    someSelected: someFilteredSelected,
  };
}

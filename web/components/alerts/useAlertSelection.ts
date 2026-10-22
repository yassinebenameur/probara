'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { pruneSelection, toggleSelection } from '@/lib/alert-list';

const EMPTY: ReadonlySet<string> = new Set();

export interface AlertSelection {
  /** Selected ids, limited to the rows currently selectable. */
  selected: ReadonlySet<string>;
  /** Toggle one row; with Shift held, the range from the last toggled row. */
  toggle: (id: string) => void;
  /** Select every selectable row, or clear them when all are selected. */
  toggleAll: () => void;
  clear: () => void;
  allSelected: boolean;
  someSelected: boolean;
}

/**
 * Row selection for the alert list, with the monitors list's behaviour:
 * Shift-click selects the range from the last toggled row (over `order`, the
 * selectable rows in their shown order), Escape clears unless a field or
 * dialog has the key, and the selection belongs to `resetKey` (filters,
 * sort, page): changing it starts empty, so a bulk action never reaches rows
 * that are no longer on screen. Rows that stop being selectable (an alert
 * resolved meanwhile, a refetch) drop out of `selected` on their own.
 */
export function useAlertSelection(order: readonly string[], resetKey: string): AlertSelection {
  const [state, setState] = useState<{ key: string; ids: ReadonlySet<string> }>({ key: resetKey, ids: EMPTY });
  const raw = state.key === resetKey ? state.ids : EMPTY;
  const anchorRef = useRef<string | null>(null);
  const shiftHeldRef = useRef(false);

  const orderKey = order.join(',');
  const selected = useMemo(() => pruneSelection(raw, orderKey ? orderKey.split(',') : []), [raw, orderKey]);

  // The checkbox change event carries no modifier, so the Shift state comes
  // from the pointer/key event that precedes it.
  useEffect(() => {
    const track = (e: PointerEvent | KeyboardEvent) => {
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

  const toggle = useCallback(
    (id: string) => {
      const next = toggleSelection(selected, order, anchorRef.current, id, shiftHeldRef.current);
      anchorRef.current = id;
      setState({ key: resetKey, ids: next });
    },
    [order, selected, resetKey]
  );

  const allSelected = order.length > 0 && order.every((id) => selected.has(id));
  const someSelected = !allSelected && selected.size > 0;

  const toggleAll = useCallback(() => {
    setState({ key: resetKey, ids: allSelected ? EMPTY : new Set(order) });
  }, [allSelected, order, resetKey]);

  const clear = useCallback(() => {
    anchorRef.current = null;
    setState({ key: resetKey, ids: EMPTY });
  }, [resetKey]);

  // Escape drops the selection (a text field or dialog keeps its own Escape).
  const active = selected.size > 0;
  useEffect(() => {
    if (!active) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return;
      const target = e.target as HTMLElement | null;
      if (target?.closest('input:not([type="checkbox"]), textarea, select, [role="dialog"], [role="menu"]')) return;
      clear();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [active, clear]);

  return { selected, toggle, toggleAll, clear, allSelected, someSelected };
}

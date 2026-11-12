'use client';

import { createContext, useContext, type CSSProperties, type KeyboardEvent, type MouseEvent } from 'react';

// ── Row layout ───────────────────────────────────────────────────────────
//
// Header, monitor rows and group rows share one grid template, so every
// column lines up without spacer elements. The lead column holds the
// selection checkbox, the group chevron slot and the status dot; its width
// is the --row-lead variable the list sets. Below the wide breakpoint a row
// is name + compact trace + menu (--row-lead-narrow). The chevron keeps its
// slot at every width: it is the only way to reach members for Select mode,
// and a group expanded at lg must stay collapsible when the preview opens
// and the wide columns drop out. While the preview panel takes its width,
// the wide columns start at xl with trace and success only (the panel has
// the latency and the recent checks) and the full set returns at 2xl.
//
// The trace column is the trace's own fixed width (20 × 5px bars + gaps =
// 79px, CheckTrace): extra width never stretches it. On very wide screens
// (3xl list, 4xl with the preview) the target moves out of the name cell
// into its own column, so the room goes to the name and the target.
export interface RowLayout {
  cols: string;
  header: string;
  /** Trace and success columns. */
  wide: string;
  /** Latency and last-check columns. */
  full: string;
  narrow: string;
  /** The target column (very wide screens only). */
  target: string;
  /** The target under the name, where there is no target column. */
  targetInline: string;
}

export const ROW_LAYOUTS: Record<'list' | 'panel', RowLayout> = {
  list: {
    cols:
      'grid grid-cols-[var(--row-lead-narrow)_minmax(0,1fr)_auto_32px] ' +
      'lg:grid-cols-[var(--row-lead)_minmax(0,1fr)_80px_64px_72px_88px_32px] ' +
      '3xl:grid-cols-[var(--row-lead)_minmax(0,3fr)_minmax(0,2fr)_80px_72px_80px_96px_32px]',
    header: 'hidden lg:grid',
    wide: 'hidden lg:flex',
    full: 'hidden lg:flex',
    narrow: 'flex lg:hidden',
    target: 'hidden 3xl:flex',
    targetInline: '3xl:hidden',
  },
  panel: {
    cols:
      'grid grid-cols-[var(--row-lead-narrow)_minmax(0,1fr)_auto_32px] ' +
      'xl:grid-cols-[var(--row-lead)_minmax(0,1fr)_80px_64px_32px] ' +
      '2xl:grid-cols-[var(--row-lead)_minmax(0,1fr)_80px_64px_72px_88px_32px] ' +
      '4xl:grid-cols-[var(--row-lead)_minmax(0,3fr)_minmax(0,2fr)_80px_72px_80px_96px_32px]',
    header: 'hidden xl:grid',
    wide: 'hidden xl:flex',
    full: 'hidden 2xl:flex',
    narrow: 'flex xl:hidden',
    target: 'hidden 4xl:flex',
    targetInline: '4xl:hidden',
  },
};

export const RowLayoutContext = createContext<RowLayout>(ROW_LAYOUTS.list);

export function useRowLayout() {
  return useContext(RowLayoutContext);
}

/**
 * Lead column width: [checkbox] [chevron slot] dot, 8px apart. The checkbox
 * slot is reserved whenever rows are selectable (it reveals on hover), so
 * starting a selection never shifts the row.
 */
function leadWidth(selectable: boolean, hasGroups: boolean): number {
  return 8 + (selectable ? 16 + 8 : 0) + (hasGroups ? 24 + 8 : 0);
}

export function leadStyle(selectable: boolean, hasGroups: boolean): CSSProperties {
  return {
    '--row-lead': `${leadWidth(selectable, hasGroups)}px`,
    '--row-lead-narrow': `${leadWidth(selectable, hasGroups)}px`,
  } as CSSProperties;
}

// Rows are selectable only for writers: every bulk action is a write.
export const SelectableContext = createContext(false);

// Fills step up: hover (well) < previewed (well-hover) < checked
// (well-strong), so hovering a row never looks like checking it. Selection is
// never orange: orange is for actions. Keyboard focus lands on the row's name
// link and rings the whole row in the focus colour.
export function rowStateClass(isSelected: boolean, isChecked: boolean) {
  const focus = 'has-[.row-link:focus-visible]:shadow-[inset_0_0_0_2px_var(--focus)]';
  if (isChecked) return `bg-well-strong ${focus}`;
  if (isSelected) return `bg-well-hover ${focus}`;
  return `hover:bg-well active:bg-well-hover ${focus}`;
}

// Rows whose status changed since the last refresh flash once (useRowFlash);
// the list provides the class per monitor id.
export const RowFlashContext = createContext<(id: string) => string>(() => '');

// Arrow keys move between the rows' name links (each row is one link in
// the tab order, plus its copy and ⋮ controls).
export function handleRowArrowKeys(event: KeyboardEvent<HTMLElement>) {
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
  const current = event.target as HTMLElement;
  if (!current.classList.contains('row-link')) return;
  const links = Array.from(event.currentTarget.querySelectorAll<HTMLElement>('.row-link'));
  const next = links[links.indexOf(current) + (event.key === 'ArrowDown' ? 1 : -1)];
  if (!next) return;
  event.preventDefault();
  next.focus();
}

// The name is a real link (focusable, opens in a new tab with a modifier),
// but a plain mouse click is left to the row: it previews on desktop and
// navigates below lg, the same as clicking anywhere else on the row.
// Keyboard activation (Enter fires a click with detail 0) navigates, as the
// link announces: the preview sits after every row in tab order and would
// open with no focus move or announcement.
export function handleRowLinkClick(event: MouseEvent<HTMLAnchorElement>) {
  if (event.detail === 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) {
    event.stopPropagation();
    return;
  }
  event.preventDefault();
}

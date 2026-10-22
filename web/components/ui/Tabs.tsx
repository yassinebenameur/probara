'use client';

import { ReactNode, useEffect, useId, useState } from 'react';
import { FOCUS_RING } from './focus';
import { handleRovingKeyDown, SEGMENT_ITEM, SELECTED_SEGMENT } from './roving';

interface TabsProps<T extends string> {
  tabs: readonly { id: T; label: string; count?: number }[];
  active: T;
  onChange: (id: T) => void;
  /** Accessible name for the tab list. */
  label?: string;
  /**
   * Rendered at the far end of the tabs row (e.g. a RangePicker that scopes
   * the whole page). Wraps under the tabs on narrow screens.
   */
  trailing?: ReactNode;
  /**
   * Stable id prefix shared with the page's <TabPanel idBase=…>. When set,
   * each tab gets aria-controls pointing at its panel.
   */
  idBase?: string;
}

// Tab ids for aria wiring: a panel can point back with
// aria-labelledby={tabElementId(baseId, id)} when it needs to.
export function tabElementId(baseId: string, id: string): string {
  return `${baseId}-tab-${id}`;
}

export function tabPanelId(baseId: string, id: string): string {
  return `${baseId}-panel-${id}`;
}

export default function Tabs<T extends string>({ tabs, active, onChange, label, trailing, idBase }: TabsProps<T>) {
  const generatedId = useId();
  const baseId = idBase ?? generatedId;
  const ids = tabs.map((t) => t.id);
  const list = (
    <div
      role="tablist"
      aria-label={label}
      onKeyDown={(e) => handleRovingKeyDown(e, ids, active, onChange)}
      className="flex w-fit max-w-full items-center gap-0.5 overflow-x-auto rounded-xl bg-well p-1 shadow-[inset_0_0_0_1px_var(--glass-edge)]"
    >
      {tabs.map((tab) => {
        const selected = active === tab.id;
        return (
          <button
            key={tab.id}
            id={tabElementId(baseId, tab.id)}
            type="button"
            role="tab"
            data-roving-id={tab.id}
            aria-selected={selected}
            aria-controls={idBase ? tabPanelId(idBase, tab.id) : undefined}
            tabIndex={selected ? 0 : -1}
            onClick={() => onChange(tab.id)}
            className={`inline-flex items-center gap-1.5 whitespace-nowrap rounded-lg px-3.5 py-1.5 text-ui font-medium ${SEGMENT_ITEM} ${FOCUS_RING} ${
              selected ? SELECTED_SEGMENT : 'text-slate-400 hover:bg-well-hover hover:text-slate-100'
            }`}
          >
            {tab.label}
            {typeof tab.count === 'number' && (
              <span
                className={`rounded-full bg-well-hover px-1.5 text-2xs tabular-nums ${
                  selected ? 'text-slate-200' : 'text-slate-400'
                }`}
              >
                {tab.count}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
  // One structure whether or not there is a trailing slot: pages set it per
  // tab, and swapping the root would remount the tablist on an arrow-key tab
  // change and drop keyboard focus to <body>.
  return (
    <div className="flex flex-wrap items-center justify-between gap-3">
      {list}
      {trailing ? <div className="flex items-center gap-2">{trailing}</div> : null}
    </div>
  );
}

/**
 * The content of one tab, tied to its tab for assistive tech. Pass the same
 * `idBase` as the <Tabs idBase=…> above it; render only the active panel.
 */
export function TabPanel({
  idBase,
  id,
  className = '',
  children,
}: {
  idBase: string;
  id: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div
      role="tabpanel"
      id={tabPanelId(idBase, id)}
      aria-labelledby={tabElementId(idBase, id)}
      className={`rounded-2xl ${FOCUS_RING} ${className}`.trim()}
    >
      {children}
    </div>
  );
}

// Tab selection kept in `?tab=` so a tab can be linked to (and survives a
// reload). The first id is the default and is left out of the URL.
export function useUrlTab<T extends string>(ids: readonly T[]): [T, (id: T) => void] {
  const [tab, setTab] = useState<T>(ids[0]);

  useEffect(() => {
    const requested = new URLSearchParams(window.location.search).get('tab');
    if (requested && (ids as readonly string[]).includes(requested)) setTab(requested as T);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const select = (id: T) => {
    setTab(id);
    const url = new URL(window.location.href);
    if (id === ids[0]) url.searchParams.delete('tab');
    else url.searchParams.set('tab', id);
    window.history.replaceState(null, '', url);
  };

  return [tab, select];
}

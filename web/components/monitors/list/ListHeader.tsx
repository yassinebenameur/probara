'use client';

import { useEffect } from 'react';
import { useSearchParams } from 'next/navigation';
import Checkbox from '@/components/ui/Checkbox';
import SortableHeader from '@/components/ui/SortableHeader';
import { MONITOR_SORT_DEFAULT_DIR, MONITOR_SORT_KEYS, MonitorListParams, MonitorSortKey, parseMonitorListParams } from '@/lib/monitor-list';
import { RECENT_CHECKS, type SortMode } from '@/components/monitors/list/constants';
import { useRowLayout } from '@/components/monitors/list/row-layout';

// Sort choices for the narrow layouts, where the column header is hidden
// (phones, or the preview panel taking the width): one select covering
// every column and direction.
export const SORT_LABEL: Record<MonitorSortKey, string> = {
  status: 'Status',
  name: 'Name',
  success: 'Success',
  latency: 'Latency',
  last_check: 'Last check',
};

const SORT_DIR_LABEL: Record<MonitorSortKey, { asc: string; desc: string }> = {
  status: { asc: 'worst first', desc: 'best first' },
  name: { asc: 'A to Z', desc: 'Z to A' },
  success: { asc: 'lowest first', desc: 'highest first' },
  latency: { asc: 'fastest first', desc: 'slowest first' },
  last_check: { asc: 'stalest first', desc: 'most recent first' },
};

export function sortDescription(sort: SortMode): string {
  return `${SORT_LABEL[sort.key]}, ${SORT_DIR_LABEL[sort.key][sort.dir]}`;
}

export function NarrowSortSelect({ sort, onSortChange }: { sort: SortMode; onSortChange: (sort: SortMode) => void }) {
  const layout = useRowLayout();
  return (
    <label className={`${layout.narrow} flex-shrink-0 items-center`}>
      <span className="sr-only">Sort monitors by</span>
      <select
        value={`${sort.key}:${sort.dir}`}
        onChange={(e) => {
          const [key, dir] = e.target.value.split(':') as [MonitorSortKey, 'asc' | 'desc'];
          onSortChange({ key, dir });
        }}
        className="input input-sm h-8 !w-auto py-0"
      >
        {MONITOR_SORT_KEYS.flatMap((key) => {
          const first = MONITOR_SORT_DEFAULT_DIR[key];
          const second: SortMode['dir'] = first === 'asc' ? 'desc' : 'asc';
          return [first, second].map((dir) => (
            <option key={`${key}:${dir}`} value={`${key}:${dir}`}>
              {SORT_LABEL[key]}: {SORT_DIR_LABEL[key][dir]}
            </option>
          ));
        })}
      </select>
    </label>
  );
}

// Reads the list filters from the URL on every navigation, including
// same-route ones (sidebar link to /monitors, back/forward), which keep this
// page mounted. useSearchParams needs a Suspense boundary for the prerender,
// so it lives in this leaf rather than in the page.
export function MonitorListUrlSync({ onParams }: { onParams: (params: MonitorListParams) => void }) {
  const searchParams = useSearchParams();
  const search = searchParams.toString();
  useEffect(() => {
    onParams(parseMonitorListParams(search));
  }, [search, onParams]);
  return null;
}

// Column headers on the rows' grid, shown where the wide columns are. The
// status dot has no column of its own wide enough for a label, so the first
// header cell spans the lead and name columns and holds two column headers,
// Status then Name (each data row has a lead cell and a name cell, so the
// header and row cells still pair up for assistive technology).
export function ListHeader({
  sort,
  onSortChange,
  ranking,
  selection,
}: {
  sort: SortMode;
  onSortChange: (sort: SortMode) => void;
  /** Status sort requested but results still loading: the order is by name for now. */
  ranking: boolean;
  /**
   * The "select all shown" box: revealed while the pointer is over the list
   * (the table is `group/list`), pinned once a selection exists.
   */
  selection?: { checked: boolean; indeterminate: boolean; pinned: boolean; onChange: () => void };
}) {
  const layout = useRowLayout();
  const header = (key: MonitorSortKey, label: string, align: 'left' | 'right' = 'right', title?: string, className = '') => (
    <SortableHeader
      as="div"
      label={label}
      sortKey={key}
      sort={sort}
      onSort={onSortChange}
      defaultDir={MONITOR_SORT_DEFAULT_DIR}
      align={align}
      title={title}
      className={className}
    />
  );
  return (
    <div
      role="row"
      className={`${layout.header} ${layout.cols} h-10 items-center gap-x-3 border-b border-divider px-4 text-xs font-medium text-slate-500`}
    >
      {/* Lead cell over the rows' checkbox slot; sort labels start at the
          name column, so "Name" sits over the names. */}
      <div role="columnheader" className="flex items-center">
        {selection ? (
          <Checkbox
            checked={selection.checked}
            indeterminate={selection.indeterminate}
            onChange={selection.onChange}
            aria-label={selection.checked ? 'Deselect all shown monitors' : 'Select all shown monitors'}
            className={
              selection.pinned || selection.checked
                ? 'opacity-100'
                : 'opacity-0 transition-opacity duration-fast ease-standard focus-within:opacity-100 group-hover/list:opacity-100'
            }
          />
        ) : (
          <span className="sr-only">Status</span>
        )}
      </div>
      <div role="none" className="flex min-w-0 items-center gap-5">
        {header('name', 'Name', 'left')}
        {header('status', ranking ? 'Status (ranking…)' : 'Status', 'left', 'Worst first: down, degraded, no data, up, maintenance, paused. A group ranks as its worst member.')}
      </div>
      <div role="columnheader" className={`${layout.target} min-w-0`}>Target</div>
      <div role="columnheader" className="whitespace-nowrap text-right" title="One bar per check, newest on the right">
        Last {RECENT_CHECKS} checks
      </div>
      {header('success', 'Success', 'right', `Share of the last ${RECENT_CHECKS} checks that passed`)}
      {header('latency', 'Latency', 'right', `Median of the successful checks among the last ${RECENT_CHECKS}`, `${layout.full} justify-end`)}
      {header('last_check', 'Last check', 'right', 'Stalest first, then most recent first', `${layout.full} justify-end whitespace-nowrap`)}
      <div role="columnheader">
        <span className="sr-only">Actions</span>
      </div>
    </div>
  );
}

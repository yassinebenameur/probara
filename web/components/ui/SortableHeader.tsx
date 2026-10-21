'use client';

import { ReactNode, useCallback, useEffect, useState } from 'react';
import { ArrowDown, ArrowUp, ArrowUpDown } from 'lucide-react';
import {
  type DefaultDirFor,
  nextSort,
  readSortParams,
  type SortState,
  writeSortParams,
} from '@/lib/sort';
import { FOCUS_RING } from './focus';

interface SortableHeaderProps<K extends string> {
  /** `th` inside a <table>; `div` (role=columnheader) in a grid list. */
  as?: 'th' | 'div';
  /** Visible label; it is also the button's whole accessible name. */
  label: ReactNode;
  sortKey: K;
  sort: SortState<K>;
  onSort: (next: SortState<K>) => void;
  /** Direction this column starts in when first clicked (per key or one for all). */
  defaultDir?: DefaultDirFor<K>;
  align?: 'left' | 'right' | 'center';
  /** Tooltip, e.g. what "worst first" means for this column. */
  title?: string;
  className?: string;
}

const JUSTIFY = { left: 'justify-start', right: 'justify-end', center: 'justify-center' } as const;

/**
 * A sortable column header: the cell carries aria-sort (only on the active
 * column), its content is a button. Active shows ArrowUp/ArrowDown; an
 * inactive column shows a faint ArrowUpDown on hover/focus, in a reserved
 * slot so the label never shifts. Right-aligned columns put the icon on the
 * inner side. Sort the full filtered set with lib/sort.ts, then page — or,
 * for a server-paged list, pass the sort to the API — and keep the state in
 * the URL (useUrlSort).
 */
export default function SortableHeader<K extends string>({
  as = 'th',
  label,
  sortKey,
  sort,
  onSort,
  defaultDir,
  align = 'left',
  title,
  className = '',
}: SortableHeaderProps<K>) {
  const active = sort.key === sortKey;
  const Icon = !active ? ArrowUpDown : sort.dir === 'asc' ? ArrowUp : ArrowDown;
  const ariaSort = active ? (sort.dir === 'asc' ? 'ascending' : 'descending') : undefined;
  const Cell = as;
  return (
    <Cell
      role={as === 'div' ? 'columnheader' : undefined}
      aria-sort={ariaSort}
      className={`${align === 'right' ? 'text-right' : align === 'center' ? 'text-center' : 'text-left'} ${className}`.trim()}
    >
      <span className={`flex ${JUSTIFY[align]}`}>
        <button
          type="button"
          title={title}
          onClick={() => onSort(nextSort(sort, sortKey, defaultDir))}
          className={`group/sort -mx-1 inline-flex min-w-0 items-center gap-1 rounded px-1 transition-colors duration-fast hover:text-white active:opacity-80 ${
            align === 'right' ? 'flex-row-reverse' : ''
          } ${active ? 'text-slate-200' : ''} ${FOCUS_RING}`}
        >
          <span className="truncate">{label}</span>
          <Icon
            className={`h-3 w-3 flex-shrink-0 transition-opacity duration-fast ${
              active ? 'opacity-100' : 'opacity-0 group-hover/sort:opacity-60 group-focus-visible/sort:opacity-60'
            }`}
            strokeWidth={2}
            aria-hidden="true"
          />
        </button>
      </span>
    </Cell>
  );
}

/**
 * Sort state kept in `?sort=&dir=` (defaults left out), so a sorted view can
 * be linked to and survives a reload. Reads the URL after mount, like
 * Tabs' useUrlTab. /monitors keeps its own params in lib/monitor-list.ts.
 */
export function useUrlSort<K extends string>(
  keys: readonly K[],
  fallback: SortState<K>,
  defaultDirFor?: DefaultDirFor<K>
): [SortState<K>, (next: SortState<K>) => void] {
  const [sort, setSort] = useState<SortState<K>>(fallback);

  useEffect(() => {
    setSort(readSortParams(new URLSearchParams(window.location.search), keys, fallback, defaultDirFor));
    // Read once on mount; the URL is written by `select` from then on.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const select = useCallback(
    (next: SortState<K>) => {
      setSort(next);
      const url = new URL(window.location.href);
      writeSortParams(url.searchParams, next, fallback, defaultDirFor);
      window.history.replaceState(null, '', url);
    },
    // fallback/defaultDirFor are page constants.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    []
  );

  return [sort, select];
}

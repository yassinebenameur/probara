'use client';

import { Search, Tag, BellOff, Layers } from 'lucide-react';
import FilterChip from '@/components/ui/FilterChip';
import FilterPopover, { type FilterOption } from '@/components/ui/FilterPopover';
import { type StatusFilter, STATUS_FILTER_LABEL } from '@/components/monitors/list/constants';
import { type RowLayout, RowLayoutContext } from '@/components/monitors/list/row-layout';
import { NarrowSortSelect } from '@/components/monitors/list/ListHeader';
import { type MonitorListFilterState } from '@/components/monitors/list/useMonitorListFilters';

// On phones the chip rows scroll sideways instead of wrapping into a wall.
const scrollRow =
  '-mx-4 flex flex-nowrap items-center gap-1.5 overflow-x-auto px-4 [scrollbar-width:none] sm:-mx-6 sm:px-6 lg:mx-0 lg:flex-wrap lg:overflow-visible lg:px-0';

interface MonitorListToolbarProps {
  filters: MonitorListFilterState;
  visibleStatusFilters: readonly StatusFilter[];
  statusCounts: Record<StatusFilter, number>;
  typeOptions: FilterOption[];
  tagOptions: FilterOption[];
  /** Whether any tag exists to filter by. */
  hasTags: boolean;
  unroutedCount: number;
  /** The narrow layouts' sort select, shown while rows are. */
  showSort: boolean;
  rowLayout: RowLayout;
}

/** The monitors list's search box, status chips, Type/Tags/routing filters and narrow sort. */
export default function MonitorListToolbar({
  filters,
  visibleStatusFilters,
  statusCounts,
  typeOptions,
  tagOptions,
  hasTags,
  unroutedCount,
  showSort,
  rowLayout,
}: MonitorListToolbarProps) {
  return (
    <div className="space-y-3">
      <div className="flex flex-col gap-2.5 lg:flex-row lg:flex-wrap lg:items-center lg:gap-3">
        {/* Grows to 16rem but gives width back (down to 9rem) before the
            row wraps, so setting a Type/Tags filter (its chip gains a
            value and a ×) does not push the list down at laptop widths. */}
        <div className="relative w-full lg:w-auto lg:min-w-0 lg:max-w-64 lg:flex-[1_1_9rem]">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" strokeWidth={1.75} aria-hidden="true" />
          <input
            type="search"
            placeholder="Search name, URL, host or tag"
            aria-label="Search monitors by name, URL, host or tag"
            value={filters.searchTerm}
            onChange={(e) => filters.setSearchTerm(e.target.value)}
            className="input input-sm h-8 py-0 !pl-8"
          />
        </div>

        <div role="group" aria-label="Status" className={scrollRow}>
          {visibleStatusFilters.map((id) => (
            <FilterChip
              key={id}
              selected={filters.statusFilter === id}
              count={statusCounts[id]}
              onClick={() => filters.setStatusFilter(id)}
            >
              {STATUS_FILTER_LABEL[id]}
            </FilterChip>
          ))}
        </div>

        {/* A selected Type/Tags chip shows a short value or "Tags ⓝ", so its
            width stays bounded; the search field above absorbs it. */}
        <div className={`${scrollRow} lg:min-w-max lg:flex-none lg:flex-nowrap`}>
          <FilterPopover
            label="Type"
            noun="type"
            icon={<Layers strokeWidth={1.75} />}
            options={typeOptions}
            selected={filters.selectedTypes}
            onChange={filters.setSelectedTypes}
          />

          {hasTags && (
            <FilterPopover
              label="Tags"
              noun="tag"
              icon={<Tag strokeWidth={1.75} />}
              options={tagOptions}
              selected={filters.selectedTags}
              onChange={filters.setSelectedTags}
            />
          )}

          {(unroutedCount > 0 || filters.unroutedOnly) && (
            <FilterChip
              selected={filters.unroutedOnly}
              count={unroutedCount > 0 ? unroutedCount : undefined}
              icon={<BellOff strokeWidth={1.75} />}
              onClick={() => filters.setUnroutedOnly((v) => !v)}
              title="Monitors whose alerts would notify nobody"
            >
              No alert route
            </FilterChip>
          )}

          {showSort && (
            <RowLayoutContext.Provider value={rowLayout}>
              <NarrowSortSelect sort={filters.sortMode} onSortChange={filters.setSortMode} />
            </RowLayoutContext.Provider>
          )}
        </div>
      </div>
    </div>
  );
}

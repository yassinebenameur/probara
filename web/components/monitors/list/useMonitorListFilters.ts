'use client';

import { useCallback, useState, useEffect } from 'react';
import { DEFAULT_MONITOR_SORT, formatMonitorListParams, MonitorListParams } from '@/lib/monitor-list';
import { type SortMode, type StatusFilter } from '@/components/monitors/list/constants';

/**
 * The monitors list's search, filters and sort, with the URL round trip
 * (MonitorListUrlSync calls `applyUrlParams`).
 */
export function useMonitorListFilters() {
  const [searchTerm, setSearchTerm] = useState('');
  const [selectedTypes, setSelectedTypes] = useState<Set<string>>(new Set());
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  // Deep-linked from the dashboard's unrouted warning (?routing=unrouted).
  const [unroutedOnly, setUnroutedOnly] = useState(false);
  const [sortMode, setSortMode] = useState<SortMode>(DEFAULT_MONITOR_SORT);
  const [selectedTags, setSelectedTags] = useState<Set<string>>(new Set());

  // Filters live in the URL so other pages can deep-link them (the
  // dashboard's ?status=down, ?tag=…, ?routing=unrouted) and a reload keeps
  // them. MonitorListUrlSync applies the URL on every navigation; state
  // changes are written back with replaceState, which Next mirrors into
  // useSearchParams, so the re-read then finds nothing to change.
  const [urlRead, setUrlRead] = useState(false);
  const applyUrlParams = useCallback((params: MonitorListParams) => {
    setStatusFilter(params.status);
    setUnroutedOnly(params.unroutedOnly);
    setSortMode((current) =>
      current.key === params.sort.key && current.dir === params.sort.dir ? current : params.sort
    );
    setSelectedTags((current) =>
      current.size === params.tags.length && params.tags.every((tag) => current.has(tag))
        ? current
        : new Set(params.tags)
    );
    setUrlRead(true);
  }, []);

  useEffect(() => {
    if (!urlRead) return;
    const search = formatMonitorListParams(
      { status: statusFilter, tags: Array.from(selectedTags), unroutedOnly, sort: sortMode },
      window.location.search
    );
    if (search !== window.location.search.replace(/^\?/, '')) {
      window.history.replaceState(null, '', `${window.location.pathname}${search ? `?${search}` : ''}${window.location.hash}`);
    }
  }, [urlRead, statusFilter, selectedTags, unroutedOnly, sortMode]);

  const hasActiveFilters =
    searchTerm.trim() !== '' || selectedTypes.size > 0 || statusFilter !== 'all' || selectedTags.size > 0 ||
    unroutedOnly;

  const clearFilters = () => {
    setSearchTerm('');
    setSelectedTypes(new Set());
    setStatusFilter('all');
    setSelectedTags(new Set());
    setUnroutedOnly(false);
  };

  return {
    searchTerm,
    setSearchTerm,
    selectedTypes,
    setSelectedTypes,
    statusFilter,
    setStatusFilter,
    unroutedOnly,
    setUnroutedOnly,
    sortMode,
    setSortMode,
    selectedTags,
    setSelectedTags,
    applyUrlParams,
    hasActiveFilters,
    clearFilters,
  };
}

export type MonitorListFilterState = ReturnType<typeof useMonitorListFilters>;

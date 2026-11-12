'use client';

import { useState, useEffect, useMemo, useRef } from 'react';
import { useRowFlash } from '@/lib/useRowFlash';
import { Monitor, CheckResult } from '@/lib/types';
import { getEffectiveMonitorStatus } from '@/lib/monitor-utils';
import { MONITOR_STATUS_LABEL } from '@/lib/status-tones';
import { sortDescription } from '@/components/monitors/list/ListHeader';
import { type MonitorListFilterState } from '@/components/monitors/list/useMonitorListFilters';

interface MonitorListAnnouncementsOptions {
  monitors: Monitor[];
  checkResultsMap: Record<string, CheckResult[]>;
  resultsReady: boolean;
  /** Top-level rows on screen. */
  shownCount: number;
  filters: Pick<
    MonitorListFilterState,
    'sortMode' | 'searchTerm' | 'statusFilter' | 'selectedTypes' | 'selectedTags' | 'unroutedOnly'
  >;
}

/**
 * The row flash for status changes and the two polite live-region texts the
 * list announces.
 */
export function useMonitorListAnnouncements({
  monitors,
  checkResultsMap,
  resultsReady,
  shownCount,
  filters,
}: MonitorListAnnouncementsOptions) {
  const { sortMode, searchTerm, statusFilter, selectedTypes, selectedTags, unroutedOnly } = filters;
  // Rows whose status moved since the last refresh flash once. The baseline
  // is the first complete results pass, so loading never flashes.
  const statusRows = useMemo(
    () =>
      resultsReady
        ? monitors.map((m) => ({ id: m.id, name: m.name, status: getEffectiveMonitorStatus(m, checkResultsMap[m.id] || []) }))
        : null,
    [monitors, checkResultsMap, resultsReady]
  );
  const rowFlash = useRowFlash(
    statusRows,
    (row) => row.id,
    (row) => row.status
  );

  // Screen-reader news, politely: status changes the flash shows, and the
  // shown count after a filter or sort change (debounced while typing).
  const [statusNews, setStatusNews] = useState('');
  const announced = useRef(new Set<string>());
  useEffect(() => {
    const fresh = Array.from(rowFlash.flashing).filter((id) => !announced.current.has(id));
    announced.current = new Set(rowFlash.flashing);
    if (fresh.length === 0 || !statusRows) return;
    const byId = new Map(statusRows.map((row) => [row.id, row]));
    const parts = fresh
      .map((id) => byId.get(id))
      .filter((row): row is NonNullable<typeof row> => Boolean(row))
      .map((row) => `${row.name} is now ${MONITOR_STATUS_LABEL[row.status].toLowerCase()}`);
    if (parts.length > 0) setStatusNews(parts.length > 3 ? `${parts.length} monitors changed status` : parts.join('. '));
  }, [rowFlash.flashing, statusRows]);

  const [listNews, setListNews] = useState('');
  const sortKeyDir = `${sortMode.key}:${sortMode.dir}`;
  useEffect(() => {
    if (!resultsReady) return;
    const timer = window.setTimeout(() => {
      setListNews(`${shownCount} ${shownCount === 1 ? 'row' : 'rows'} shown, sorted by ${sortDescription(sortMode)}`);
    }, 600);
    return () => window.clearTimeout(timer);
    // sortMode is read through its key:dir string.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shownCount, sortKeyDir, resultsReady, searchTerm, statusFilter, selectedTypes, selectedTags, unroutedOnly]);

  return { rowFlash, statusNews, listNews };
}

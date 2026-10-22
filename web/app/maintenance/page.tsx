'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { CircleStop, Pencil, Plus, Trash2, Wrench } from 'lucide-react';
import type { MaintenanceWindow, MaintenanceWindowStatus } from '@/lib/types';
import { deleteMaintenanceWindow, getMaintenanceWindows, updateMaintenanceWindow } from '@/lib/api';
import { formatDateTime, formatDateTimeFull, formatDurationBetween } from '@/lib/format';
import { sortRows, type SortState, type SortValue } from '@/lib/sort';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import PageHeader from '@/components/ui/PageHeader';
import Page from '@/components/ui/Page';
import EmptyState from '@/components/ui/EmptyState';
import TableSkeleton from '@/components/ui/TableSkeleton';
import Callout from '@/components/ui/Callout';
import Pill from '@/components/ui/Pill';
import Tabs from '@/components/ui/Tabs';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { RelativeTime } from '@/components/ui/RelativeTime';
import SortableHeader, { useUrlSort } from '@/components/ui/SortableHeader';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { MaintenanceWindowModal } from '@/components/maintenance/MaintenanceWindowModal';

type Tab = MaintenanceWindowStatus;

const TAB_ORDER: Tab[] = ['active', 'upcoming', 'past'];
const TAB_LABELS: Record<Tab, string> = { active: 'Active', upcoming: 'Upcoming', past: 'Past' };

const EMPTY_COPY: Record<Tab, { title: string; description: string }> = {
  active: {
    title: 'No maintenance in progress',
    description: 'Schedule a window to silence alerts during planned work. Affected monitors show as “Maintenance” on dashboards and status pages.',
  },
  upcoming: {
    title: 'Nothing scheduled',
    description: 'Schedule a window ahead of planned work so its alerts stay quiet while it runs.',
  },
  past: {
    title: 'No past windows',
    description: 'Windows that have ended are listed here.',
  },
};

const MAX_MONITOR_CHIPS = 3;
// The API caps page_size at 100. Each tab reads up to MAX_FETCH windows in the
// server's order (soonest first for active/upcoming, newest first for past),
// so a cut only ever drops the least relevant ones, and says so.
const API_PAGE = 100;
const MAX_FETCH = 500;

const SORT_KEYS = ['auto', 'title', 'starts', 'ends', 'monitors'] as const;
type MaintenanceSortKey = (typeof SORT_KEYS)[number];
// 'auto' is each tab's natural order (the API's): no explicit sort in the URL.
const AUTO_SORT: SortState<MaintenanceSortKey> = { key: 'auto', dir: 'asc' };
const TAB_ORDER_SORT: Record<Tab, SortState<MaintenanceSortKey>> = {
  active: { key: 'ends', dir: 'asc' },
  upcoming: { key: 'starts', dir: 'asc' },
  past: { key: 'ends', dir: 'desc' },
};
const DEFAULT_DIR = { monitors: 'desc' } as const;

const SORT_VALUE: Record<Exclude<MaintenanceSortKey, 'auto'>, (w: MaintenanceWindow) => SortValue> = {
  title: (w) => w.title,
  starts: (w) => Date.parse(w.starts_at),
  ends: (w) => Date.parse(w.ends_at),
  monitors: (w) => (w.monitors ?? []).length,
};

/** When the window starts or ends, relative to now, as the row's lead fact. */
function relativeTiming(window: MaintenanceWindow) {
  if (window.status === 'active') {
    return (
      <Pill tone="info" size="xs" dot>
        <RelativeTime value={window.ends_at} prefix="Ends " />
      </Pill>
    );
  }
  if (window.status === 'upcoming') {
    return (
      <Pill tone="neutral" size="xs">
        <RelativeTime value={window.starts_at} prefix="Starts " />
      </Pill>
    );
  }
  return <RelativeTime value={window.ends_at} prefix="Ended " className="text-xs text-slate-500" />;
}

async function fetchStatus(status: Tab): Promise<{ items: MaintenanceWindow[]; total: number }> {
  const items: MaintenanceWindow[] = [];
  let total = 0;
  for (let page = 1; items.length < MAX_FETCH; page++) {
    const response = await getMaintenanceWindows({ status, page, page_size: API_PAGE });
    const batch = response?.items || [];
    total = response?.total ?? items.length + batch.length;
    items.push(...batch);
    if (batch.length < API_PAGE || items.length >= total) break;
  }
  return { items, total };
}

export default function MaintenancePage() {
  const { showToast } = useToast();
  const canWrite = useCanWrite();
  const [tab, setTab] = useState<Tab>('active');
  const [windowsByTab, setWindowsByTab] = useState<Record<Tab, MaintenanceWindow[]>>({ active: [], upcoming: [], past: [] });
  const [totals, setTotals] = useState<Record<Tab, number> | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<MaintenanceWindow | undefined>(undefined);
  const [pendingDelete, setPendingDelete] = useState<MaintenanceWindow | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [sort, setSort] = useUrlSort(SORT_KEYS, AUTO_SORT, DEFAULT_DIR);
  // Until the operator picks a tab, the first load lands on the first one
  // with something in it instead of an empty "Active".
  const tabChosen = useRef(false);

  // All three statuses load together so the tabs can carry counts.
  const loadWindows = useCallback(async () => {
    try {
      setError('');
      const responses = await Promise.all(TAB_ORDER.map(fetchStatus));
      const next = { active: [], upcoming: [], past: [] } as Record<Tab, MaintenanceWindow[]>;
      const counts = { active: 0, upcoming: 0, past: 0 } as Record<Tab, number>;
      TAB_ORDER.forEach((status, i) => {
        next[status] = responses[i].items;
        counts[status] = responses[i].total;
      });
      setWindowsByTab(next);
      setTotals(counts);
      if (!tabChosen.current) {
        tabChosen.current = true;
        setTab(TAB_ORDER.find((status) => counts[status] > 0) ?? 'active');
      }
    } catch (err: any) {
      setError(err.message || 'Failed to load maintenance windows');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadWindows();
  }, [loadWindows]);

  const chooseTab = (next: Tab) => {
    tabChosen.current = true;
    setTab(next);
  };

  const handleDelete = async () => {
    if (!pendingDelete) return;
    setDeleting(true);
    try {
      await deleteMaintenanceWindow(pendingDelete.id);
      showToast('Maintenance window deleted', 'success');
      setPendingDelete(null);
      loadWindows();
    } catch (err: any) {
      showToast(err.message || 'Failed to delete maintenance window', 'error');
    } finally {
      setDeleting(false);
    }
  };

  const handleEndNow = async (window: MaintenanceWindow) => {
    try {
      await updateMaintenanceWindow(window.id, { ends_at: new Date().toISOString() });
      showToast('Maintenance window ended', 'success');
      loadWindows();
    } catch (err: any) {
      showToast(err.message || 'Failed to end maintenance window', 'error');
    }
  };

  const openCreate = () => {
    setEditing(undefined);
    setModalOpen(true);
  };

  const openEdit = (window: MaintenanceWindow) => {
    setEditing(window);
    setModalOpen(true);
  };

  const loaded = windowsByTab[tab];
  // Until a column is picked the tab's natural order applies (and its arrow
  // shows); it is applied here too, not just trusted from the server.
  const shownSort = sort.key === 'auto' ? TAB_ORDER_SORT[tab] : sort;
  const windows = useMemo(() => {
    const key = shownSort.key === 'auto' ? 'starts' : shownSort.key;
    return sortRows(loaded, SORT_VALUE[key], shownSort.dir, (w) => w.title);
  }, [loaded, shownSort.key, shownSort.dir]);
  const changeSort = (next: SortState<MaintenanceSortKey>) => {
    const natural = TAB_ORDER_SORT[tab];
    setSort(next.key === natural.key && next.dir === natural.dir ? AUTO_SORT : next);
  };
  // The empty state carries the one CTA on the tabs that offer it.
  const emptyWithCta = !loading && !error && windows.length === 0 && tab !== 'past';

  return (
    <Page width="data">
      <PageHeader
        title="Maintenance"
        subtitle="Planned windows during which alerts are suppressed. Checks keep running and status pages show maintenance."
        action={
          canWrite && !emptyWithCta ? (
            <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} onClick={openCreate}>
              Schedule maintenance
            </Button>
          ) : undefined
        }
      />

      <Tabs
        tabs={TAB_ORDER.map((id) => ({ id, label: TAB_LABELS[id], count: totals ? totals[id] : undefined }))}
        active={tab}
        onChange={chooseTab}
        label="Maintenance windows"
      />

      {error && (
        <Callout
          tone="danger"
          action={<Button variant="ghost" size="xs" onClick={loadWindows} loading={loading}>Retry</Button>}
        >
          {error}
        </Callout>
      )}

      {loading ? (
        <TableSkeleton rows={3} columns={1} label="Loading maintenance windows" />
      ) : error && !totals ? null : windows.length === 0 ? (
        <EmptyState
          icon={<Wrench strokeWidth={1.5} />}
          title={EMPTY_COPY[tab].title}
          description={EMPTY_COPY[tab].description}
          action={
            canWrite && tab !== 'past'
              ? { label: 'Schedule maintenance', onClick: openCreate, icon: <Plus strokeWidth={1.75} /> }
              : undefined
          }
        />
      ) : (
        <div className="glass overflow-hidden">
          <div className="overflow-x-auto">
            <table className="data-table text-sm">
              <thead>
                <tr>
                  <SortableHeader
                    label="Window"
                    sortKey="title"
                    sort={shownSort}
                    onSort={changeSort}
                    defaultDir={DEFAULT_DIR}
                    className="w-full"
                  />
                  <SortableHeader label="Starts" sortKey="starts" sort={shownSort} onSort={changeSort} defaultDir={DEFAULT_DIR} />
                  <SortableHeader label="Ends" sortKey="ends" sort={shownSort} onSort={changeSort} defaultDir={DEFAULT_DIR} />
                  <SortableHeader
                    label="Monitors"
                    sortKey="monitors"
                    sort={shownSort}
                    onSort={changeSort}
                    defaultDir={DEFAULT_DIR}
                    align="right"
                    title="Most monitors first"
                    className="hidden md:table-cell"
                  />
                  {canWrite && (
                    <th>
                      <span className="sr-only">Actions</span>
                    </th>
                  )}
                </tr>
              </thead>
              <tbody>
                {windows.map((window) => {
                  const monitors = window.monitors ?? [];
                  return (
                    <tr key={window.id}>
                      <td className="max-w-0">
                        <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                          <p className="min-w-0 break-words font-medium text-white">{window.title}</p>
                          {relativeTiming(window)}
                        </div>
                        {monitors.length > 0 && (
                          <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
                            {monitors.slice(0, MAX_MONITOR_CHIPS).map((m) => (
                              <span
                                key={m.id}
                                className="inline-flex max-w-[16rem] items-center gap-1 overflow-hidden rounded-md bg-well-hover px-2 py-0.5 text-xs text-slate-300"
                                // A group target mutes its members too — say so
                                // instead of reading like a single monitor.
                                title={m.type === 'group' ? `${m.name} (group: its members are covered too)` : m.name}
                              >
                                <span className="truncate">{m.name}</span>
                                {m.type === 'group' && <span className="flex-shrink-0 text-slate-500">group</span>}
                              </span>
                            ))}
                            {monitors.length > MAX_MONITOR_CHIPS && (
                              <span
                                className="text-xs text-slate-500"
                                title={monitors.slice(MAX_MONITOR_CHIPS).map((m) => m.name).join(', ')}
                              >
                                +{monitors.length - MAX_MONITOR_CHIPS} more
                              </span>
                            )}
                          </div>
                        )}
                      </td>
                      <td className="whitespace-nowrap text-ui tabular-nums text-slate-300" title={formatDateTimeFull(window.starts_at)}>
                        {formatDateTime(window.starts_at)}
                      </td>
                      <td className="whitespace-nowrap text-ui tabular-nums" title={formatDateTimeFull(window.ends_at)}>
                        <div className="text-slate-300">{formatDateTime(window.ends_at)}</div>
                        <div className="mt-0.5 text-xs text-slate-500">
                          {formatDurationBetween(window.starts_at, window.ends_at)} long
                        </div>
                      </td>
                      <td className="hidden text-right text-ui tabular-nums text-slate-300 md:table-cell">
                        {monitors.length}
                      </td>
                      {canWrite && (
                        <td>
                          <div className="flex items-center justify-end gap-0.5">
                            {window.status === 'active' && (
                              <IconButton
                                icon={<CircleStop strokeWidth={1.75} />}
                                label="End now"
                                onClick={() => handleEndNow(window)}
                              />
                            )}
                            {window.status !== 'past' && (
                              <IconButton
                                icon={<Pencil strokeWidth={1.75} />}
                                label="Edit"
                                onClick={() => openEdit(window)}
                              />
                            )}
                            <IconButton
                              icon={<Trash2 strokeWidth={1.75} />}
                              label="Delete"
                              onClick={() => setPendingDelete(window)}
                              danger
                            />
                          </div>
                        </td>
                      )}
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          {totals && totals[tab] > loaded.length && (
            <p className="border-t border-divider px-4 py-2.5 text-xs text-slate-500">
              Showing the {loaded.length} {tab === 'past' ? 'most recent' : 'soonest'} of {totals[tab]} windows.
            </p>
          )}
        </div>
      )}

      {canWrite && modalOpen && (
        <MaintenanceWindowModal
          window={editing}
          onDone={() => {
            setModalOpen(false);
            setEditing(undefined);
            showToast(editing ? 'Maintenance window updated' : 'Maintenance scheduled', 'success');
            loadWindows();
          }}
          onCancel={() => {
            setModalOpen(false);
            setEditing(undefined);
          }}
        />
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        title="Delete maintenance window"
        description={
          pendingDelete
            ? `Delete “${pendingDelete.title}”? Alerts for the affected monitors resume immediately.`
            : ''
        }
        confirmLabel="Delete"
        onConfirm={handleDelete}
        onCancel={() => setPendingDelete(null)}
        loading={deleting}
      />
    </Page>
  );
}

'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { CalendarClock, CircleStop, Pencil, Plus, Trash2, Wrench } from 'lucide-react';
import type { MaintenanceWindow, MaintenanceWindowStatus } from '@/lib/types';
import { deleteMaintenanceWindow, getMaintenanceWindows, updateMaintenanceWindow } from '@/lib/api';
import { formatDateTime, formatDateTimeFull, formatRelativeTime } from '@/lib/format';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import TableSkeleton from '@/components/ui/TableSkeleton';
import Callout from '@/components/ui/Callout';
import Pill from '@/components/ui/Pill';
import Tabs from '@/components/ui/Tabs';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
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

/** `2 Oct, 14:00 – 16:00`, or both full ends when the window spans days. */
function formatWindowRange(startsAt: string, endsAt: string): string {
  const start = new Date(startsAt);
  const end = new Date(endsAt);
  if (start.toDateString() === end.toDateString()) {
    const pad = (n: number) => String(n).padStart(2, '0');
    return `${formatDateTime(start)} – ${pad(end.getHours())}:${pad(end.getMinutes())}`;
  }
  return `${formatDateTime(start)} – ${formatDateTime(end)}`;
}

/** When the window starts or ends, relative to now, as the row's lead fact. */
function relativeTiming(window: MaintenanceWindow) {
  if (window.status === 'active') {
    return (
      <Pill tone="info" size="xs" dot>
        Ends {formatRelativeTime(window.ends_at)}
      </Pill>
    );
  }
  if (window.status === 'upcoming') {
    return (
      <Pill tone="neutral" size="xs">
        Starts {formatRelativeTime(window.starts_at)}
      </Pill>
    );
  }
  return <span className="text-xs text-slate-500">Ended {formatRelativeTime(window.ends_at)}</span>;
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
  // Until the operator picks a tab, the first load lands on the first one
  // with something in it instead of an empty "Active".
  const tabChosen = useRef(false);

  // All three statuses load together so the tabs can carry counts.
  const loadWindows = useCallback(async () => {
    try {
      setError('');
      const responses = await Promise.all(
        TAB_ORDER.map((status) => getMaintenanceWindows({ status, page_size: 100 }))
      );
      const next = { active: [], upcoming: [], past: [] } as Record<Tab, MaintenanceWindow[]>;
      const counts = { active: 0, upcoming: 0, past: 0 } as Record<Tab, number>;
      TAB_ORDER.forEach((status, i) => {
        next[status] = responses[i]?.items || [];
        counts[status] = responses[i]?.total ?? next[status].length;
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

  const windows = windowsByTab[tab];
  // The empty state carries the one CTA on the tabs that offer it.
  const emptyWithCta = !loading && !error && windows.length === 0 && tab !== 'past';

  return (
    <div className="space-y-6">
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
        <Callout tone="danger" action={<Button variant="ghost" size="xs" onClick={loadWindows}>Retry</Button>}>
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
        <div className="glass divide-y divide-divider overflow-hidden">
          {windows.map((window) => {
            const monitors = window.monitors ?? [];
            return (
              <div key={window.id} className="flex items-start gap-3 px-5 py-3.5">
                <div
                  className={`mt-0.5 hidden h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg sm:flex ${
                    window.status === 'active' ? 'bg-sky-500/[0.14]' : 'bg-well-hover'
                  }`}
                  aria-hidden="true"
                >
                  <CalendarClock
                    className={`h-4 w-4 ${window.status === 'active' ? 'text-sky-400' : 'text-slate-400'}`}
                    strokeWidth={1.75}
                  />
                </div>

                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                    <p className="break-words text-sm font-medium text-white">{window.title}</p>
                    {relativeTiming(window)}
                  </div>
                  <p
                    className="mt-0.5 text-ui tabular-nums text-slate-400"
                    title={`${formatDateTimeFull(window.starts_at)} – ${formatDateTimeFull(window.ends_at)}`}
                  >
                    {formatWindowRange(window.starts_at, window.ends_at)}
                  </p>
                  {monitors.length > 0 && (
                    <div className="mt-2 flex flex-wrap items-center gap-1.5">
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
                </div>

                {canWrite && (
                  <div className="flex flex-shrink-0 items-center gap-0.5">
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
                )}
              </div>
            );
          })}
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
    </div>
  );
}

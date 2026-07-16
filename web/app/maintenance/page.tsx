'use client';

import { useCallback, useEffect, useState } from 'react';
import { CalendarClock, Pencil, Plus, Square, Trash2, Wrench } from 'lucide-react';
import type { MaintenanceWindow, MaintenanceWindowStatus } from '@/lib/types';
import { deleteMaintenanceWindow, getMaintenanceWindows, updateMaintenanceWindow } from '@/lib/api';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { MaintenanceWindowModal } from '@/components/maintenance/MaintenanceWindowModal';

type Tab = MaintenanceWindowStatus;

const TABS: { id: Tab; label: string }[] = [
  { id: 'active', label: 'Active' },
  { id: 'upcoming', label: 'Upcoming' },
  { id: 'past', label: 'Past' },
];

function formatRange(startsAt: string, endsAt: string): string {
  const start = new Date(startsAt);
  const end = new Date(endsAt);
  const sameDay = start.toDateString() === end.toDateString();
  const dateOpts: Intl.DateTimeFormatOptions = { month: 'short', day: 'numeric' };
  const timeOpts: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit' };
  if (sameDay) {
    return `${start.toLocaleDateString(undefined, dateOpts)}, ${start.toLocaleTimeString(undefined, timeOpts)} – ${end.toLocaleTimeString(undefined, timeOpts)}`;
  }
  return `${start.toLocaleDateString(undefined, dateOpts)}, ${start.toLocaleTimeString(undefined, timeOpts)} – ${end.toLocaleDateString(undefined, dateOpts)}, ${end.toLocaleTimeString(undefined, timeOpts)}`;
}

export default function MaintenancePage() {
  const { showToast } = useToast();
  const [tab, setTab] = useState<Tab>('active');
  const [windows, setWindows] = useState<MaintenanceWindow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<MaintenanceWindow | undefined>(undefined);
  const [pendingDelete, setPendingDelete] = useState<MaintenanceWindow | null>(null);
  const [deleting, setDeleting] = useState(false);

  const loadWindows = useCallback(async (status: Tab) => {
    try {
      setLoading(true);
      setError('');
      const response = await getMaintenanceWindows({ status, page_size: 100 });
      setWindows(response?.items || []);
    } catch (err: any) {
      setError(err.message || 'Failed to load maintenance windows');
      setWindows([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadWindows(tab);
  }, [tab, loadWindows]);

  const handleDelete = async () => {
    if (!pendingDelete) return;
    setDeleting(true);
    try {
      await deleteMaintenanceWindow(pendingDelete.id);
      setWindows((prev) => prev.filter((w) => w.id !== pendingDelete.id));
      showToast('Maintenance window deleted', 'success');
      setPendingDelete(null);
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
      loadWindows(tab);
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

  return (
    <div className="space-y-5">
      <PageHeader
        title="Maintenance"
        subtitle="Planned windows during which alerts are suppressed. Checks keep running and status pages show maintenance."
        action={
          <Button size="sm" icon={<Plus strokeWidth={1.75} />} onClick={openCreate}>
            Schedule maintenance
          </Button>
        }
      />

      {/* Tabs */}
      <div className="flex items-center gap-1 rounded-lg border border-white/[0.06] bg-slate-900/50 p-1 w-fit">
        {TABS.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={`rounded-md px-4 py-1.5 text-xs font-medium transition-colors ${
              tab === t.id ? 'bg-white/[0.08] text-white' : 'text-slate-400 hover:text-white'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {error && (
        <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">
          {error}
        </div>
      )}

      {loading ? (
        <div className="py-12 text-center text-sm text-slate-500">Loading…</div>
      ) : windows.length === 0 ? (
        <EmptyState
          icon={<Wrench strokeWidth={1.5} />}
          title={
            tab === 'active'
              ? 'No active maintenance'
              : tab === 'upcoming'
              ? 'No upcoming maintenance'
              : 'No past maintenance windows'
          }
          description="Schedule a window to silence alerts during planned work. Affected monitors show as “Maintenance” on dashboards and status pages."
          action={
            tab !== 'past' ? (
              <Button size="sm" icon={<Plus strokeWidth={1.75} />} onClick={openCreate}>
                Schedule maintenance
              </Button>
            ) : undefined
          }
        />
      ) : (
        <div className="space-y-2">
          {windows.map((window) => (
            <div
              key={window.id}
              className="group flex items-center gap-3 rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3"
            >
              <div
                className={`flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-md ${
                  window.status === 'active' ? 'bg-sky-500/20' : 'bg-slate-700/40'
                }`}
              >
                <CalendarClock
                  className={`h-4 w-4 ${window.status === 'active' ? 'text-sky-400' : 'text-slate-400'}`}
                  strokeWidth={1.75}
                />
              </div>

              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="truncate text-sm font-medium text-white">{window.title}</span>
                  <Pill tone={window.status === 'active' ? 'info' : 'neutral'} size="xs">
                    {window.status === 'active' ? 'In progress' : window.status === 'upcoming' ? 'Scheduled' : 'Ended'}
                  </Pill>
                </div>
                <p className="mt-0.5 text-xs text-slate-500">
                  {formatRange(window.starts_at, window.ends_at)}
                  {window.monitors && window.monitors.length > 0 && (
                    <>
                      {' · '}
                      {window.monitors
                        .slice(0, 3)
                        .map((m) => m.name)
                        .join(', ')}
                      {window.monitors.length > 3 && ` +${window.monitors.length - 3} more`}
                    </>
                  )}
                </p>
              </div>

              <div className="flex flex-shrink-0 items-center gap-1">
                {window.status === 'active' && (
                  <Button
                    variant="ghost"
                    size="sm"
                    icon={<Square strokeWidth={1.75} />}
                    onClick={() => handleEndNow(window)}
                  >
                    End now
                  </Button>
                )}
                {window.status !== 'past' && (
                  <Button
                    variant="ghost"
                    size="sm"
                    icon={<Pencil strokeWidth={1.75} />}
                    onClick={() => openEdit(window)}
                    aria-label={`Edit ${window.title}`}
                  >
                    Edit
                  </Button>
                )}
                <Button
                  variant="ghost"
                  size="sm"
                  icon={<Trash2 strokeWidth={1.75} />}
                  onClick={() => setPendingDelete(window)}
                  aria-label={`Delete ${window.title}`}
                >
                  Delete
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}

      {modalOpen && (
        <MaintenanceWindowModal
          window={editing}
          onDone={() => {
            setModalOpen(false);
            setEditing(undefined);
            showToast(editing ? 'Maintenance window updated' : 'Maintenance scheduled', 'success');
            loadWindows(tab);
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

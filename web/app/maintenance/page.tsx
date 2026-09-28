'use client';

import { useCallback, useEffect, useState } from 'react';
import { CalendarClock, CircleStop, Pencil, Plus, Trash2, Wrench } from 'lucide-react';
import type { MaintenanceWindow, MaintenanceWindowStatus } from '@/lib/types';
import { deleteMaintenanceWindow, getMaintenanceWindows, updateMaintenanceWindow } from '@/lib/api';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import Callout from '@/components/ui/Callout';
import Tabs from '@/components/ui/Tabs';
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
    <div className="space-y-6">
      <PageHeader
        title="Maintenance"
        subtitle="Planned windows during which alerts are suppressed. Checks keep running and status pages show maintenance."
        action={
          <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} onClick={openCreate}>
            Schedule maintenance
          </Button>
        }
      />

      <Tabs tabs={TABS} active={tab} onChange={setTab} />

      {error && <Callout tone="danger">{error}</Callout>}

      {loading ? (
        <div className="glass p-5 text-sm text-slate-500">Loading…</div>
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
        <div className="glass divide-y divide-divider overflow-hidden">
          {windows.map((window) => (
            <div
              key={window.id}
              className="group flex items-center gap-3 px-5 py-3.5 transition-colors hover:bg-well"
            >
              <div
                className={`hidden h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg sm:flex ${
                  window.status === 'active' ? 'bg-sky-500/[0.14]' : 'bg-well-hover'
                }`}
              >
                <CalendarClock
                  className={`h-4 w-4 ${window.status === 'active' ? 'text-sky-400' : 'text-slate-400'}`}
                  strokeWidth={1.75}
                />
              </div>

              <div className="min-w-0 flex-1">
                <p className="break-words text-sm font-medium text-white">{window.title}</p>
                <p className="mt-0.5 text-[13px] text-slate-400">
                  {formatRange(window.starts_at, window.ends_at)}
                  {window.monitors && window.monitors.length > 0 && (
                    <>
                      {' · '}
                      {window.monitors
                        .slice(0, 3)
                        // A group target mutes its members too — say so instead of
                        // reading like a single monitor.
                        .map((m) => (m.type === 'group' ? `${m.name} (group)` : m.name))
                        .join(', ')}
                      {window.monitors.length > 3 && ` +${window.monitors.length - 3} more`}
                    </>
                  )}
                </p>
              </div>

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

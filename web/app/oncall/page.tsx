'use client';

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { CalendarClock, Pencil, Plus, Trash2 } from 'lucide-react';
import { deleteOnCallSchedule, getOnCallSchedules } from '@/lib/api';
import { describeRotation, formatZoned, type OnCallSchedule } from '@/lib/oncall';
import { formatRelativeTime } from '@/lib/format';
import { FOCUS_RING } from '@/components/ui/focus';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import PageHeader from '@/components/ui/PageHeader';
import Page from '@/components/ui/Page';
import Pill from '@/components/ui/Pill';
import EmptyState from '@/components/ui/EmptyState';
import Callout from '@/components/ui/Callout';
import TableSkeleton from '@/components/ui/TableSkeleton';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { ScheduleFormModal } from '@/components/oncall/ScheduleFormModal';

export default function OnCallPage() {
  const { showToast } = useToast();
  const canWrite = useCanWrite();
  const [schedules, setSchedules] = useState<OnCallSchedule[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<OnCallSchedule | undefined>(undefined);
  const [pendingDelete, setPendingDelete] = useState<OnCallSchedule | null>(null);
  const [deleting, setDeleting] = useState(false);

  const load = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      setSchedules(await getOnCallSchedules());
    } catch (err: any) {
      setError(err.message || 'Failed to load schedules');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const handleDelete = async () => {
    if (!pendingDelete) return;
    setDeleting(true);
    try {
      await deleteOnCallSchedule(pendingDelete.id);
      setSchedules((prev) => prev.filter((s) => s.id !== pendingDelete.id));
      showToast('Schedule deleted', 'success');
      setPendingDelete(null);
    } catch (err: any) {
      showToast(err.message || 'Failed to delete schedule', 'error');
    } finally {
      setDeleting(false);
    }
  };

  const openCreate = () => {
    setEditing(undefined);
    setModalOpen(true);
  };

  return (
    <Page width="data">
      <PageHeader
        title="On-call"
        subtitle="Who is on call, by rotation and override. Schedules do not page anyone yet: route alerts to channels as before."
        action={
          canWrite && !(loading || error || schedules.length === 0) ? (
            <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} onClick={openCreate}>
              New schedule
            </Button>
          ) : undefined
        }
      />

      {error && (
        <Callout tone="danger" action={<Button variant="ghost" size="xs" onClick={load} loading={loading}>Retry</Button>}>
          {error}
        </Callout>
      )}

      {loading && schedules.length === 0 ? (
        <TableSkeleton rows={3} columns={3} label="Loading schedules" />
      ) : error && schedules.length === 0 ? null : schedules.length === 0 ? (
        <EmptyState
          icon={<CalendarClock strokeWidth={1.5} />}
          title="No on-call schedules yet"
          description="Set up a rotation — daily or weekly handoffs in your time zone — to see who is on call, swap shifts with overrides and subscribe to it from a calendar app."
          action={canWrite ? { label: 'New schedule', onClick: openCreate, icon: <Plus strokeWidth={1.75} /> } : undefined}
        />
      ) : (
        <div className="glass overflow-hidden">
          <div className="overflow-x-auto">
            <table className="data-table min-w-[720px] text-sm">
              <thead>
                <tr>
                  <th>Schedule</th>
                  <th>On call now</th>
                  <th>Next</th>
                  <th><span className="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {schedules.map((s) => (
                  <tr key={s.id}>
                    <td>
                      <Link href={`/oncall/${s.id}`}
                        className={`rounded-sm font-medium text-white transition-colors hover:text-cyan-300 ${FOCUS_RING}`}>
                        {s.name}
                      </Link>
                      <div className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-slate-500">
                        <span>{s.timezone}</span>
                        {s.rotations.map((r) => <span key={r.id}>{describeRotation(r)}</span>)}
                      </div>
                    </td>
                    <td>
                      {s.on_call ? (
                        <>
                          <div className="flex items-center gap-2 text-slate-200">
                            {s.on_call.user.username}
                            {s.on_call.source === 'override' && <Pill tone="info" size="xs">Override</Pill>}
                          </div>
                          <div className="mt-0.5 text-xs text-slate-500" title={formatZoned(s.on_call.end, s.timezone)}>
                            until {formatZoned(s.on_call.end, s.timezone)}
                          </div>
                        </>
                      ) : (
                        <span className="text-amber-300">Nobody</span>
                      )}
                    </td>
                    <td>
                      {s.next ? (
                        <>
                          <div className="text-slate-300">{s.next.user.username}</div>
                          <div className="mt-0.5 text-xs text-slate-500">{formatRelativeTime(s.next.start)}</div>
                        </>
                      ) : (
                        <span className="text-slate-500">—</span>
                      )}
                    </td>
                    <td>
                      {canWrite && (
                        <div className="flex items-center justify-end gap-0.5">
                          <IconButton icon={<Pencil strokeWidth={1.75} />} label="Edit" onClick={() => {
                            setEditing(s);
                            setModalOpen(true);
                          }} />
                          <IconButton icon={<Trash2 strokeWidth={1.75} />} label="Delete" danger
                            onClick={() => setPendingDelete(s)} />
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {canWrite && modalOpen && (
        <ScheduleFormModal
          schedule={editing}
          onDone={() => {
            setModalOpen(false);
            showToast(editing ? 'Schedule updated' : 'Schedule created', 'success');
            setEditing(undefined);
            load();
          }}
          onCancel={() => {
            setModalOpen(false);
            setEditing(undefined);
          }}
        />
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        title="Delete schedule"
        description={pendingDelete ? `Delete “${pendingDelete.name}” with its rotations, overrides and calendar link?` : ''}
        confirmLabel="Delete"
        tone="danger"
        onConfirm={handleDelete}
        onCancel={() => setPendingDelete(null)}
        loading={deleting}
      />
    </Page>
  );
}

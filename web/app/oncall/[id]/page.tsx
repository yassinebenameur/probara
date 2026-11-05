'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import { CalendarPlus, Copy, Link2Off, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react';
import {
  createOnCallFeedLink, createOnCallOverride, deleteOnCallOverride, deleteOnCallSchedule, disableOnCallFeed,
  getOnCallOverrides, getOnCallPeople, getOnCallSchedule, previewOnCallSchedule,
} from '@/lib/api';
import {
  describeRotation, feedURL, formatZoned, isoToZonedLocal, zonedLocalToISO,
  type OnCallOverride, type OnCallPerson, type OnCallSchedule, type Shift,
} from '@/lib/oncall';
import { formatDurationBetween } from '@/lib/format';
import Page from '@/components/ui/Page';
import PageHeader from '@/components/ui/PageHeader';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import FormField from '@/components/ui/FormField';
import Input from '@/components/ui/Input';
import Select from '@/components/ui/Select';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { ScheduleFormModal } from '@/components/oncall/ScheduleFormModal';

const PREVIEW_DAYS = 14;

export default function OnCallSchedulePage() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const router = useRouter();
  const { showToast } = useToast();
  const canWrite = useCanWrite();
  const [schedule, setSchedule] = useState<OnCallSchedule | null>(null);
  const [shifts, setShifts] = useState<Shift[]>([]);
  const [overrides, setOverrides] = useState<OnCallOverride[]>([]);
  const [error, setError] = useState('');
  const [editing, setEditing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [addingOverride, setAddingOverride] = useState(false);
  const [removingOverride, setRemovingOverride] = useState<OnCallOverride | null>(null);
  const [feedLink, setFeedLink] = useState('');
  const [feedBusy, setFeedBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setError('');
      const now = new Date();
      const to = new Date(now.getTime() + PREVIEW_DAYS * 86400_000);
      const [s, p, o] = await Promise.all([
        getOnCallSchedule(id),
        previewOnCallSchedule(id, now.toISOString(), to.toISOString()),
        getOnCallOverrides(id),
      ]);
      setSchedule(s);
      setShifts(p.shifts || []);
      setOverrides(o);
    } catch (err: any) {
      setError(err.message || 'Failed to load the schedule');
    }
  }, [id]);

  useEffect(() => {
    load();
  }, [load]);

  const remove = async () => {
    setDeleting(true);
    try {
      await deleteOnCallSchedule(id);
      showToast('Schedule deleted', 'success');
      router.push('/oncall');
    } catch (err: any) {
      showToast(err.message || 'Failed to delete schedule', 'error');
      setDeleting(false);
    }
  };

  const removeOverride = async () => {
    if (!removingOverride) return;
    try {
      await deleteOnCallOverride(id, removingOverride.id);
      setRemovingOverride(null);
      showToast('Override removed', 'success');
      load();
    } catch (err: any) {
      showToast(err.message || 'Failed to remove override', 'error');
    }
  };

  const makeFeedLink = async () => {
    setFeedBusy(true);
    try {
      const tok = await createOnCallFeedLink(id);
      setFeedLink(feedURL(tok, window.location.origin));
      setSchedule((s) => (s ? { ...s, ical_enabled: true } : s));
    } catch (err: any) {
      showToast(err.message || 'Failed to create the calendar link', 'error');
    } finally {
      setFeedBusy(false);
    }
  };

  const turnOffFeed = async () => {
    setFeedBusy(true);
    try {
      await disableOnCallFeed(id);
      setFeedLink('');
      setSchedule((s) => (s ? { ...s, ical_enabled: false } : s));
      showToast('Calendar link turned off', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to turn off the calendar link', 'error');
    } finally {
      setFeedBusy(false);
    }
  };

  if (error && !schedule) {
    return (
      <Page width="data">
        <PageHeader breadcrumb={[{ label: 'On-call', href: '/oncall' }]} title="Schedule" />
        <Callout tone="danger" action={<Button variant="ghost" size="xs" onClick={load}>Retry</Button>}>{error}</Callout>
      </Page>
    );
  }
  if (!schedule) {
    return (
      <Page width="data">
        <PageHeader breadcrumb={[{ label: 'On-call', href: '/oncall' }]} title="Schedule" />
        <p className="text-sm text-slate-500">Loading schedule…</p>
      </Page>
    );
  }

  const tz = schedule.timezone;
  const rotationName = (rid?: string) => schedule.rotations.find((r) => r.id === rid)?.name;

  return (
    <Page width="data">
      <PageHeader
        breadcrumb={[{ label: 'On-call', href: '/oncall' }]}
        title={schedule.name}
        subtitle={[tz, ...schedule.rotations.map(describeRotation)]}
        action={
          canWrite ? (
            <>
              <Button size="sm" icon={<Pencil strokeWidth={1.75} />} onClick={() => setEditing(true)}>Edit</Button>
              <Button size="sm" variant="danger" icon={<Trash2 strokeWidth={1.75} />} onClick={() => setConfirmDelete(true)}>
                Delete
              </Button>
            </>
          ) : undefined
        }
      />

      {schedule.description && <p className="mb-6 max-w-prose text-sm text-slate-400">{schedule.description}</p>}

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Panel title="On call now">
          {schedule.on_call ? (
            <ShiftLine shift={schedule.on_call} tz={tz} rotationName={rotationName(schedule.on_call.rotation_id)} />
          ) : (
            <p className="text-sm text-amber-300">Nobody is on call right now.</p>
          )}
          {schedule.next && (
            <div className="mt-4 border-t border-divider pt-4">
              <p className="mb-1 text-xs text-slate-500">Next</p>
              <ShiftLine shift={schedule.next} tz={tz} rotationName={rotationName(schedule.next.rotation_id)} />
            </div>
          )}
        </Panel>

        <Panel
          title="Calendar feed"
          subtitle="Subscribe from Google Calendar, Outlook or Apple Calendar. Anyone with the link sees this schedule's shifts and usernames."
        >
          {feedLink ? (
            <div className="space-y-3">
              <Callout tone="warning">Copy the link now: it is shown only once. Making a new link revokes this one.</Callout>
              <div className="flex items-center gap-2">
                <input readOnly value={feedLink} className="input font-mono text-xs" aria-label="Calendar link" onFocus={(e) => e.target.select()} />
                <IconButton icon={<Copy strokeWidth={1.75} />} label="Copy link" onClick={() => {
                  navigator.clipboard?.writeText(feedLink).then(() => showToast('Link copied', 'success')).catch(() => {});
                }} />
              </div>
            </div>
          ) : (
            <p className="text-sm text-slate-400">
              {schedule.ical_enabled ? 'A calendar link is active.' : 'No calendar link.'}
            </p>
          )}
          {canWrite && (
            <div className="mt-4 flex flex-wrap gap-2">
              <Button size="xs" loading={feedBusy} onClick={makeFeedLink}
                icon={schedule.ical_enabled ? <RefreshCw strokeWidth={1.75} /> : <CalendarPlus strokeWidth={1.75} />}>
                {schedule.ical_enabled ? 'New link' : 'Create link'}
              </Button>
              {schedule.ical_enabled && (
                <Button size="xs" variant="ghost" disabled={feedBusy} onClick={turnOffFeed} icon={<Link2Off strokeWidth={1.75} />}>
                  Turn off
                </Button>
              )}
            </div>
          )}
        </Panel>
      </div>

      <div className="mt-6 grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Panel title={`Next ${PREVIEW_DAYS} days`} subtitle={`Times in ${tz}.`}>
          {shifts.length === 0 ? (
            <p className="text-sm text-slate-400">Nobody is scheduled in this window.</p>
          ) : (
            <ul className="-mx-5 -mb-5 divide-y divide-divider">
              {shifts.map((s) => (
                <li key={`${s.start}-${s.user.id}`} className="flex items-center justify-between gap-3 px-5 py-2.5 text-sm">
                  <span className="flex min-w-0 items-center gap-2 text-slate-200">
                    <span className="truncate">{s.user.username}</span>
                    {s.source === 'override' && <Pill tone="info" size="xs">Override</Pill>}
                  </span>
                  <span className="flex-shrink-0 text-xs tabular-nums text-slate-500">
                    {formatZoned(s.start, tz)} → {formatZoned(s.end, tz)}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </Panel>

        <Panel
          title="Overrides"
          subtitle="Put someone on call over a window, above every rotation."
          actions={canWrite ? (
            <Button size="xs" icon={<Plus strokeWidth={1.75} />} onClick={() => setAddingOverride(true)}>Add override</Button>
          ) : undefined}
        >
          {overrides.length === 0 ? (
            <p className="text-sm text-slate-400">No current or upcoming overrides.</p>
          ) : (
            <ul className="-mx-5 -mb-5 divide-y divide-divider">
              {overrides.map((o) => (
                <li key={o.id} className="flex items-center justify-between gap-3 px-5 py-2.5 text-sm">
                  <div className="min-w-0">
                    <p className="truncate text-slate-200">{o.user.username}</p>
                    <p className="text-xs tabular-nums text-slate-500">
                      {formatZoned(o.starts_at, tz)} → {formatZoned(o.ends_at, tz)}
                      {o.note && <span className="ml-2 text-slate-400">{o.note}</span>}
                    </p>
                  </div>
                  {canWrite && (
                    <IconButton icon={<Trash2 strokeWidth={1.75} />} label="Remove override" danger
                      onClick={() => setRemovingOverride(o)} />
                  )}
                </li>
              ))}
            </ul>
          )}
        </Panel>
      </div>

      {editing && (
        <ScheduleFormModal
          schedule={schedule}
          onDone={() => {
            setEditing(false);
            showToast('Schedule updated', 'success');
            load();
          }}
          onCancel={() => setEditing(false)}
        />
      )}

      {addingOverride && (
        <OverrideDialog
          scheduleId={id}
          tz={tz}
          onCancel={() => setAddingOverride(false)}
          onDone={() => {
            setAddingOverride(false);
            showToast('Override added', 'success');
            load();
          }}
        />
      )}

      <ConfirmDialog
        open={confirmDelete}
        title="Delete schedule"
        description={`Delete “${schedule.name}” with its rotations, overrides and calendar link?`}
        confirmLabel="Delete"
        tone="danger"
        loading={deleting}
        onConfirm={remove}
        onCancel={() => setConfirmDelete(false)}
      />
      <ConfirmDialog
        open={removingOverride !== null}
        title="Remove override?"
        description="The rotation applies again over its window."
        confirmLabel="Remove"
        tone="danger"
        onConfirm={removeOverride}
        onCancel={() => setRemovingOverride(null)}
      />
    </Page>
  );
}

function ShiftLine({ shift, tz, rotationName }: { shift: Shift; tz: string; rotationName?: string }) {
  return (
    <div>
      <p className="flex flex-wrap items-center gap-2 text-sm font-medium text-white">
        {shift.user.username}
        {shift.source === 'override' ? (
          <Pill tone="info" size="xs">Override</Pill>
        ) : rotationName ? (
          <Pill size="xs">{rotationName}</Pill>
        ) : null}
        {!shift.user.eligible && <Pill tone="warning" size="xs">No longer a member</Pill>}
        {shift.user.eligible && shift.user.verified_contacts === 0 && (
          <Pill tone="warning" size="xs">No verified contact</Pill>
        )}
      </p>
      <p className="mt-0.5 text-xs tabular-nums text-slate-500">
        {formatZoned(shift.start, tz)} → {formatZoned(shift.end, tz)} · {formatDurationBetween(shift.start, shift.end)}
      </p>
    </div>
  );
}

function OverrideDialog({
  scheduleId,
  tz,
  onCancel,
  onDone,
}: {
  scheduleId: string;
  tz: string;
  onCancel: () => void;
  onDone: () => void;
}) {
  const now = useMemo(() => new Date(), []);
  const [people, setPeople] = useState<OnCallPerson[]>([]);
  const [userId, setUserId] = useState('');
  const [start, setStart] = useState(() => isoToZonedLocal(now.toISOString(), tz));
  const [end, setEnd] = useState(() => isoToZonedLocal(new Date(now.getTime() + 8 * 3600_000).toISOString(), tz));
  const [note, setNote] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    getOnCallPeople().then(setPeople).catch((err: any) => setError(err.message || 'Failed to load people'));
  }, []);

  const save = async () => {
    const startsAt = zonedLocalToISO(start, tz);
    const endsAt = zonedLocalToISO(end, tz);
    if (!userId) return setError('Choose who is on call.');
    if (!startsAt || !endsAt) return setError('Set the start and end.');
    if (new Date(endsAt) <= new Date(startsAt)) return setError('The end must be after the start.');
    setSaving(true);
    setError('');
    try {
      await createOnCallOverride(scheduleId, { user_id: userId, starts_at: startsAt, ends_at: endsAt, note: note.trim() || undefined });
      onDone();
    } catch (err: any) {
      setError(err.message || 'Failed to add the override');
      setSaving(false);
    }
  };

  return (
    <Dialog
      title="Add override"
      description={`Times in ${tz}.`}
      onClose={onCancel}
      busy={saving}
      footer={
        <>
          <Button variant="ghost" onClick={onCancel} disabled={saving}>Cancel</Button>
          <Button variant="accent" onClick={save} loading={saving}>Add override</Button>
        </>
      }
    >
      <form noValidate className="space-y-4" onSubmit={(e) => { e.preventDefault(); save(); }}>
        <FormField label="On call" required>
          <Select value={userId} onChange={(e) => setUserId(e.target.value)} autoFocus>
            <option value="">Choose a person…</option>
            {people.map((p) => <option key={p.id} value={p.id}>{p.username}</option>)}
          </Select>
        </FormField>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <FormField label="From" required>
            <Input type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} />
          </FormField>
          <FormField label="Until" required>
            <Input type="datetime-local" value={end} onChange={(e) => setEnd(e.target.value)} />
          </FormField>
        </div>
        <FormField label="Note" description="Optional, e.g. Covering for a day off.">
          <Input value={note} maxLength={200} onChange={(e) => setNote(e.target.value)} />
        </FormField>
        {error && <Callout tone="danger">{error}</Callout>}
      </form>
    </Dialog>
  );
}

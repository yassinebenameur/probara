'use client';

import { useCallback, useEffect, useState } from 'react';
import { Mail, Pencil, Plus, Send, Trash2 } from 'lucide-react';
import {
  createSlaReportSchedule, deleteSlaReportSchedule, getSlaReportSchedules, sendSlaReportSchedule, updateSlaReportSchedule,
} from '@/lib/api';
import {
  SLA_SCHEDULE_FORMAT_LABELS, describeSendOutcome, parseRecipients, scheduleInputError,
  type Sla, type SlaReportSchedule, type SlaReportScheduleInput, type SlaReportScheduleList, type SlaScheduleFormat,
} from '@/lib/sla';
import { formatDateTime, formatRelativeTime } from '@/lib/format';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Checkbox from '@/components/ui/Checkbox';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import Dialog from '@/components/ui/Dialog';
import EmptyState from '@/components/ui/EmptyState';
import FormField from '@/components/ui/FormField';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import Switch from '@/components/ui/Switch';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';

const FORMATS: SlaScheduleFormat[] = ['pdf', 'csv'];

function errorText(err: unknown, fallback: string): string {
  if (err instanceof Error) return err.message;
  return (err as { message?: string })?.message || fallback;
}

/**
 * The SLA's emailed report schedules: who gets the report when a period
 * closes, in which formats, and how the last send went. Enabling needs SMTP
 * and APP_BASE_URL on the API; the list response says when either is
 * missing, so the toggle explains itself before anyone tries.
 */
export function SlaSchedules({ sla }: { sla: Sla }) {
  const { showToast } = useToast();
  const canWrite = useCanWrite();
  const [list, setList] = useState<SlaReportScheduleList | null>(null);
  const [error, setError] = useState('');
  const [editing, setEditing] = useState<SlaReportSchedule | 'new' | null>(null);
  const [deleting, setDeleting] = useState<SlaReportSchedule | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setList(await getSlaReportSchedules(sla.id));
      setError('');
    } catch (err) {
      setError(errorText(err, 'Failed to load report schedules'));
    }
  }, [sla.id]);

  useEffect(() => {
    load();
  }, [load]);

  const toggle = async (s: SlaReportSchedule, enabled: boolean) => {
    setBusy(`toggle-${s.id}`);
    try {
      await updateSlaReportSchedule(s.id, { enabled });
      await load();
    } catch (err) {
      showToast(errorText(err, 'Failed to update the schedule'), 'error');
    } finally {
      setBusy(null);
    }
  };

  const sendNow = async (s: SlaReportSchedule) => {
    setBusy(`send-${s.id}`);
    try {
      const out = await sendSlaReportSchedule(s.id);
      const copy = describeSendOutcome(out);
      showToast(copy.text, copy.tone);
      await load();
    } catch (err) {
      showToast(errorText(err, 'Failed to send the report'), 'error');
    } finally {
      setBusy(null);
    }
  };

  const remove = async () => {
    if (!deleting) return;
    setBusy(`delete-${deleting.id}`);
    try {
      await deleteSlaReportSchedule(deleting.id);
      setDeleting(null);
      showToast('Report schedule deleted', 'success');
      await load();
    } catch (err) {
      showToast(errorText(err, 'Failed to delete the schedule'), 'error');
    } finally {
      setBusy(null);
    }
  };

  if (!list) {
    return error ? <Callout tone="danger">{error}</Callout> : (
      <div className="glass p-8 text-center text-sm text-slate-500" role="status">Loading…</div>
    );
  }

  const mailReady = list.mail_ready;

  return (
    <div className="space-y-4">
      {!mailReady && (
        <Callout tone="warning" title="Report email is not configured">
          {list.mail_problem} Schedules can be kept disabled until it is.
        </Callout>
      )}
      {error && <Callout tone="danger">{error}</Callout>}

      {list.items.length === 0 ? (
        <EmptyState
          icon={<Mail strokeWidth={1.5} />}
          title="No report schedules"
          description="Email this SLA's report to your team or customers about an hour after each period closes, issued first so everyone gets the same frozen document."
          action={canWrite ? { label: 'Add schedule', icon: <Plus strokeWidth={1.75} />, onClick: () => setEditing('new') } : undefined}
        />
      ) : (
        <div className="glass overflow-hidden">
          <div className="flex items-center justify-between gap-3 px-5 pt-4">
            <p className="text-xs text-slate-500">
              Sent about an hour after each {sla.period.replace('ly', '')} closes in {sla.timezone}, one message per recipient.
            </p>
            {canWrite && (
              <Button variant="ghost" size="xs" icon={<Plus strokeWidth={1.75} />} onClick={() => setEditing('new')}>
                Add schedule
              </Button>
            )}
          </div>
          <div className="overflow-x-auto">
            <table className="data-table min-w-[720px] text-sm">
              <thead>
                <tr>
                  <th>Recipients</th>
                  <th>Attachments</th>
                  <th>Next report</th>
                  <th>Last sent</th>
                  <th>Enabled</th>
                  <th><span className="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {list.items.map((s) => (
                  <tr key={s.id}>
                    <td className="max-w-[22rem]">
                      <div className="flex flex-wrap gap-1">
                        {s.recipients.map((r) => {
                          const out = s.unsubscribed.includes(r);
                          return (
                            <span key={r} className={`text-ui ${out ? 'text-slate-500 line-through' : 'text-slate-200'}`}
                              title={out ? 'Unsubscribed through the link in a report email' : undefined}>
                              {r}{out ? ' (unsubscribed)' : ''}
                            </span>
                          );
                        })}
                      </div>
                    </td>
                    <td className="whitespace-nowrap text-slate-300">
                      {s.formats.map((f) => SLA_SCHEDULE_FORMAT_LABELS[f]).join(' + ')}
                      <div className="text-xs text-slate-500">{s.auto_issue ? 'Issued first' : 'Not issued'}</div>
                    </td>
                    <td className="whitespace-nowrap text-ui tabular-nums text-slate-400">
                      {s.enabled ? formatDateTime(s.next_run_at) : '—'}
                    </td>
                    <td className="text-ui tabular-nums text-slate-400">
                      {s.last_run_at ? (
                        <>
                          <span title={formatDateTime(s.last_run_at)}>{formatRelativeTime(s.last_run_at)}</span>
                          <div className="text-xs text-slate-500">
                            {s.last_period_key && `${s.last_period_key} · `}
                            {s.last_sent_count} sent
                          </div>
                          {s.last_error && <div className="max-w-[16rem] text-xs text-rose-300">{s.last_error}</div>}
                        </>
                      ) : (
                        'Never'
                      )}
                    </td>
                    <td>
                      {canWrite ? (
                        <Switch
                          checked={s.enabled}
                          onChange={(next) => toggle(s, next)}
                          disabled={busy === `toggle-${s.id}` || (!s.enabled && !mailReady)}
                          label={s.enabled ? 'Disable schedule' : 'Enable schedule'}
                        />
                      ) : (
                        <Pill tone={s.enabled ? 'success' : 'neutral'} size="xs">{s.enabled ? 'On' : 'Off'}</Pill>
                      )}
                    </td>
                    <td>
                      {canWrite && (
                        <div className="flex justify-end gap-0.5">
                          <IconButton icon={<Send strokeWidth={1.75} />} label="Send the last closed period now"
                            disabled={!mailReady} loading={busy === `send-${s.id}`} onClick={() => sendNow(s)} />
                          <IconButton icon={<Pencil strokeWidth={1.75} />} label="Edit schedule" onClick={() => setEditing(s)} />
                          <IconButton icon={<Trash2 strokeWidth={1.75} />} label="Delete schedule" danger onClick={() => setDeleting(s)} />
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

      {editing && (
        <ScheduleDialog
          slaId={sla.id}
          schedule={editing === 'new' ? null : editing}
          mailReady={mailReady}
          onDone={async (message) => {
            setEditing(null);
            showToast(message, 'success');
            await load();
          }}
          onCancel={() => setEditing(null)}
        />
      )}
      <ConfirmDialog
        open={!!deleting}
        title="Delete report schedule?"
        description="Its recipients get no more reports from it, and the unsubscribe links it sent stop working. Issued reports are kept."
        confirmLabel="Delete"
        tone="danger"
        loading={!!deleting && busy === `delete-${deleting.id}`}
        onConfirm={remove}
        onCancel={() => setDeleting(null)}
      />
    </div>
  );
}

function ScheduleDialog({ slaId, schedule, mailReady, onDone, onCancel }: {
  slaId: string;
  schedule: SlaReportSchedule | null;
  mailReady: boolean;
  onDone: (message: string) => void;
  onCancel: () => void;
}) {
  const [recipients, setRecipients] = useState(schedule ? schedule.recipients.join('\n') : '');
  const [formats, setFormats] = useState<SlaScheduleFormat[]>(schedule ? schedule.formats : ['pdf']);
  const [autoIssue, setAutoIssue] = useState(schedule ? schedule.auto_issue : true);
  const [enabled, setEnabled] = useState(schedule ? schedule.enabled : mailReady);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const save = async () => {
    const input: SlaReportScheduleInput = { recipients: parseRecipients(recipients), formats, auto_issue: autoIssue, enabled };
    const problem = scheduleInputError(input);
    if (problem) {
      setError(problem);
      return;
    }
    setSaving(true);
    setError('');
    try {
      if (schedule) {
        await updateSlaReportSchedule(schedule.id, input);
        onDone('Report schedule updated');
      } else {
        await createSlaReportSchedule(slaId, input);
        onDone('Report schedule added');
      }
    } catch (err) {
      setError(errorText(err, 'Failed to save the schedule'));
      setSaving(false);
    }
  };

  const toggleFormat = (f: SlaScheduleFormat, on: boolean) =>
    setFormats((prev) => FORMATS.filter((x) => (x === f ? on : prev.includes(x))));

  return (
    <Dialog
      title={schedule ? 'Edit report schedule' : 'Add report schedule'}
      description="The report goes out about an hour after each period closes."
      onClose={onCancel}
      busy={saving}
      size="md"
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onCancel} disabled={saving}>Cancel</Button>
          <Button variant="accent" size="sm" onClick={save} loading={saving} disabled={saving}>
            {schedule ? 'Save' : 'Add schedule'}
          </Button>
        </>
      }
    >
      <FormField
        label="Recipients"
        required
        description="Email addresses, one per line or comma-separated (up to 50). Anyone can receive the report; each gets their own message with an unsubscribe link."
      >
        {(control) => (
          <textarea {...control} className="input font-mono" rows={4} value={recipients} autoFocus
            onChange={(e) => setRecipients(e.target.value)} placeholder={'ops@example.com\ncustomer@example.org'} />
        )}
      </FormField>
      <div className="space-y-2">
        <div className="text-xs text-slate-400">Attachments</div>
        <div className="flex gap-4">
          {FORMATS.map((f) => (
            <Checkbox key={f} checked={formats.includes(f)} onChange={(on) => toggleFormat(f, on)} label={SLA_SCHEDULE_FORMAT_LABELS[f]} />
          ))}
        </div>
      </div>
      <Checkbox
        checked={autoIssue}
        onChange={setAutoIssue}
        label="Issue the period before sending"
        description="Freezes the closed period as an issued report, so every recipient gets the same document. Off sends the issued copy when there is one, else the live report."
      />
      <div className="flex items-center justify-between gap-3">
        <div>
          <div className="text-sm text-slate-200">Enabled</div>
          {!mailReady && <div className="text-xs text-slate-500">Needs SMTP and APP_BASE_URL on the API.</div>}
        </div>
        <Switch checked={enabled} onChange={setEnabled} disabled={!mailReady && !enabled} label="Enabled" />
      </div>
      {schedule && schedule.unsubscribed.length > 0 && (
        <p className="text-xs text-slate-500">
          {schedule.unsubscribed.length} recipient{schedule.unsubscribed.length === 1 ? '' : 's'} unsubscribed and will
          get nothing from this schedule, even if kept in the list. To mail them again, create a new schedule.
        </p>
      )}
      {error && <Callout tone="danger">{error}</Callout>}
    </Dialog>
  );
}

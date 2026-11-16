'use client';

import { useCallback, useEffect, useState } from 'react';
import { Mail, Pencil, RotateCcw, Trash2 } from 'lucide-react';
import { deleteSlaReportSchedule, getSlaReportSchedule, putSlaReportSchedule } from '@/lib/api';
import {
  SLA_REPORT_DELIVERY_LABELS, SLA_REPORT_MAX_RECIPIENTS, formatSlaDate, parseRecipients, recipientsError,
  type Sla, type SlaReportDelivery, type SlaReportSchedule, type SlaReportScheduleInput, type SlaReportScheduleResponse,
} from '@/lib/sla';
import { reportDeliveryTone } from '@/lib/status-tones';
import { errorMessage } from '@/lib/errors';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Checkbox from '@/components/ui/Checkbox';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import EmptyState from '@/components/ui/EmptyState';
import FormField from '@/components/ui/FormField';
import IconButton from '@/components/ui/IconButton';
import Panel from '@/components/ui/Panel';
import Pill from '@/components/ui/Pill';
import Switch from '@/components/ui/Switch';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';

// The Schedules tab of an SLA: who gets the report emailed when each period
// closes (api/internal/services/reportschedules). One schedule per SLA.

/** The tenant-facing reason scheduled reports are off, from the API's mail_problems. */
function MailOff({ problems }: { problems: string[] }) {
  return (
    <Callout tone="warning" title="Scheduled reports are off on this installation">
      <p>A schedule can be kept but not enabled until an administrator configures the API:</p>
      <ul className="mt-1 list-disc space-y-0.5 pl-5">
        {problems.map((p) => <li key={p}>{p}</li>)}
      </ul>
    </Callout>
  );
}

function ScheduleForm({ sla, schedule, mailReady, onSaved, onCancel }: {
  sla: Sla;
  schedule: SlaReportSchedule | null;
  mailReady: boolean;
  onSaved: (resp: SlaReportScheduleResponse) => void;
  onCancel: () => void;
}) {
  const { showToast } = useToast();
  const [text, setText] = useState((schedule?.recipients ?? []).join('\n'));
  // Without mail a schedule can only be kept off.
  const [enabled, setEnabled] = useState(mailReady && (schedule?.enabled ?? true));
  const [includeCsv, setIncludeCsv] = useState(schedule?.include_csv ?? false);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    const parsed = parseRecipients(text);
    const problem = recipientsError(parsed);
    if (problem) {
      setError(problem);
      return;
    }
    setSaving(true);
    setError('');
    try {
      const input: SlaReportScheduleInput = { enabled, recipients: parsed.recipients, include_csv: includeCsv };
      onSaved(await putSlaReportSchedule(sla.id, input));
      showToast('Report schedule saved', 'success');
    } catch (err) {
      setError(errorMessage(err, 'Failed to save the schedule'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Panel title={schedule ? 'Edit report schedule' : 'Set up a report schedule'}>
      <form className="space-y-4" onSubmit={save} noValidate>
        <div className="flex items-center justify-between gap-3">
          <div>
            <div className="text-sm text-slate-200">Email the report when each period closes</div>
            <div className="text-xs text-slate-500">
              The period&apos;s report is issued (frozen) and sent with the PDF attached, an hour after the period ends.
            </div>
          </div>
          <Switch checked={enabled} onChange={setEnabled} disabled={!mailReady} label="Email the report when each period closes" />
        </div>
        <FormField
          label="Recipients"
          description={`Plain email addresses, one per line or separated by commas; up to ${SLA_REPORT_MAX_RECIPIENTS}. Each gets their own copy and unsubscribe link.`}
          error={error || undefined}
          required
        >
          <textarea className="input font-mono" rows={5} value={text} onChange={(e) => setText(e.target.value)}
            placeholder={'ops@example.com\ncustomer@example.org'} spellCheck={false} />
        </FormField>
        <Checkbox checked={includeCsv} onChange={setIncludeCsv} label="Attach the CSV export too" />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" type="button" onClick={onCancel}>Cancel</Button>
          <Button variant="accent" size="sm" type="submit" loading={saving}>Save</Button>
        </div>
      </form>
    </Panel>
  );
}

function DeliveryCell({ delivery, timeZone }: { delivery?: SlaReportDelivery; timeZone: string }) {
  if (!delivery) return <span className="text-slate-500">—</span>;
  const detail = delivery.status === 'pending' && delivery.next_attempt_at
    ? `retry ${formatSlaDate(delivery.next_attempt_at, timeZone) ?? ''} (attempt ${delivery.attempts + 1})`
    : delivery.status === 'sent' && delivery.sent_at
      ? formatSlaDate(delivery.sent_at, timeZone)
      : null;
  return (
    <div className="space-y-0.5">
      <Pill tone={reportDeliveryTone(delivery.status)} size="xs">{SLA_REPORT_DELIVERY_LABELS[delivery.status] ?? delivery.status}</Pill>
      {detail && <div className="text-xs tabular-nums text-slate-500">{detail}</div>}
      {delivery.last_error && delivery.status !== 'sent' && delivery.status !== 'skipped' && (
        <div className="max-w-md break-words text-xs text-rose-300">{delivery.last_error}</div>
      )}
    </div>
  );
}

export function SlaSchedules({ sla }: { sla: Sla }) {
  const canWrite = useCanWrite();
  const { showToast } = useToast();
  const [data, setData] = useState<SlaReportScheduleResponse | null>(null);
  const [loadError, setLoadError] = useState('');
  const [editing, setEditing] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setData(await getSlaReportSchedule(sla.id));
      setLoadError('');
    } catch (err) {
      setLoadError(errorMessage(err, 'Failed to load the report schedule'));
    }
  }, [sla.id]);

  useEffect(() => {
    load();
  }, [load]);

  if (loadError) return <Callout tone="danger">{loadError}</Callout>;
  if (!data) {
    return <div className="glass p-8 text-center text-sm text-slate-500" role="status">Loading…</div>;
  }
  const schedule = data.schedule;
  const tz = sla.timezone;

  const resubscribe = async (address: string) => {
    if (!schedule) return;
    setBusy(address);
    try {
      setData(await putSlaReportSchedule(sla.id, {
        enabled: schedule.enabled, recipients: schedule.recipients, include_csv: schedule.include_csv, resubscribe: [address],
      }));
      showToast(`${address} will get the report again`, 'success');
    } catch (err) {
      showToast(errorMessage(err, 'Failed to re-subscribe'), 'error');
    } finally {
      setBusy(null);
    }
  };

  const remove = async () => {
    setBusy('delete');
    try {
      await deleteSlaReportSchedule(sla.id);
      setConfirmDelete(false);
      showToast('Report schedule deleted', 'success');
      await load();
    } catch (err) {
      showToast(errorMessage(err, 'Failed to delete the schedule'), 'error');
    } finally {
      setBusy(null);
    }
  };

  const deliveries = new Map((schedule?.deliveries ?? []).map((d) => [d.address, d]));

  return (
    <div className="space-y-4">
      {!data.mail_ready && <MailOff problems={data.mail_problems} />}

      {editing ? (
        <ScheduleForm sla={sla} schedule={schedule} mailReady={data.mail_ready}
          onSaved={(resp) => {
            setData(resp);
            setEditing(false);
          }}
          onCancel={() => setEditing(false)} />
      ) : !schedule ? (
        <EmptyState
          icon={<Mail strokeWidth={1.5} />}
          title="No report schedule"
          description="When a period closes, its report is issued and emailed to the recipients you list, with the PDF attached and an unsubscribe link."
          action={canWrite ? { label: 'Set up a schedule', onClick: () => setEditing(true) } : undefined}
        />
      ) : (
        <>
          <Panel
            title="Emailed reports"
            actions={canWrite ? (
              <>
                <Button variant="ghost" size="xs" icon={<Pencil strokeWidth={1.75} />} onClick={() => setEditing(true)}>Edit</Button>
                <IconButton icon={<Trash2 strokeWidth={1.75} />} label="Delete schedule" danger onClick={() => setConfirmDelete(true)} />
              </>
            ) : undefined}
          >
            <dl className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-3">
              <div>
                <dt className="text-xs text-slate-400">Status</dt>
                <dd className="mt-1">
                  {schedule.enabled
                    ? <Pill tone="success" size="xs">Enabled</Pill>
                    : <Pill tone="neutral" size="xs">Off</Pill>}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-slate-400">Next send</dt>
                <dd className="mt-0.5 tabular-nums text-slate-200">
                  {schedule.enabled ? formatSlaDate(schedule.next_run_at, tz) ?? '—' : '—'}
                </dd>
                {schedule.enabled && <dd className="text-xs text-slate-500">When the running period closes, plus an hour for late results</dd>}
              </div>
              <div>
                <dt className="text-xs text-slate-400">Attachments</dt>
                <dd className="mt-0.5 text-slate-200">{schedule.include_csv ? 'PDF and CSV' : 'PDF'}</dd>
              </div>
            </dl>
            {schedule.last_period_key && (
              <p className="mt-4 text-xs text-slate-400">
                Last period sent: <span className="tabular-nums text-slate-300">{schedule.last_period_key}</span>
                {schedule.last_run_at && <> on {formatSlaDate(schedule.last_run_at, tz)}</>}
              </p>
            )}
            {schedule.last_error && (
              <Callout tone="danger" title="Not every recipient has the last report" className="mt-3">
                {schedule.last_error}
              </Callout>
            )}
          </Panel>

          <Panel
            title={`Recipients (${schedule.recipients.length})`}
            subtitle={schedule.last_period_key ? `Delivery of ${schedule.last_period_key}` : 'Nothing sent yet'}
          >
            <div className="overflow-x-auto">
              <table className="w-full min-w-[520px] text-sm">
                <thead>
                  <tr className="border-b border-divider text-left text-xs text-slate-500">
                    <th className="py-2 pr-4 font-medium">Address</th>
                    <th className="py-2 pr-4 font-medium">Last period</th>
                    <th className="py-2 font-medium"><span className="sr-only">Actions</span></th>
                  </tr>
                </thead>
                <tbody>
                  {schedule.recipients.map((address) => {
                    const unsubscribed = schedule.unsubscribed.includes(address);
                    return (
                      <tr key={address} className="border-b border-divider align-top last:border-0">
                        <td className="py-2 pr-4">
                          <span className="break-all text-slate-200">{address}</span>
                          {unsubscribed && <Pill tone="neutral" size="xs" className="ml-2">Unsubscribed</Pill>}
                        </td>
                        <td className="py-2 pr-4"><DeliveryCell delivery={deliveries.get(address)} timeZone={tz} /></td>
                        <td className="py-2">
                          <div className="flex justify-end">
                            {canWrite && unsubscribed && (
                              <IconButton icon={<RotateCcw strokeWidth={1.75} />} label={`Re-subscribe ${address}`}
                                loading={busy === address} onClick={() => resubscribe(address)} />
                            )}
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            {schedule.unsubscribed.length > 0 && (
              <p className="mt-3 text-xs text-slate-500">
                Unsubscribed addresses followed the link in a report email and are skipped. Re-subscribe one only at its owner&apos;s request.
              </p>
            )}
          </Panel>
        </>
      )}

      <ConfirmDialog
        open={confirmDelete}
        title="Delete the report schedule?"
        description="No more reports are emailed for this SLA. Its recipients, their unsubscribes and every unsubscribe link already sent are forgotten; issued reports stay."
        confirmLabel="Delete"
        tone="danger"
        loading={busy === 'delete'}
        onConfirm={remove}
        onCancel={() => setConfirmDelete(false)}
      />
    </div>
  );
}

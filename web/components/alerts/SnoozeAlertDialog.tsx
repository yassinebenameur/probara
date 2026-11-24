'use client';

import { useId, useState } from 'react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import SegmentedControl from '@/components/ui/SegmentedControl';
import { SNOOZE_PRESETS, snoozeRequest, toLocalInput, type SnoozeChoice } from '@/lib/alert-mute';
import { formatDateTimeFull } from '@/lib/format';
import { errorMessage } from '@/lib/errors';

const CHOICES = [...SNOOZE_PRESETS.map(({ value, label, title }) => ({ value, label, title })), { value: 'custom' as const, label: 'Custom', title: 'Until a time you choose' }];

interface SnoozeAlertDialogProps {
  /** What is snoozed, under the title: one alert's subject, or "3 alerts". */
  subject: string;
  /** Plural copy for a bulk snooze. */
  count?: number;
  onClose: () => void;
  /** Sends the snooze (one alert, or the bulk endpoint); a throw shows in the dialog. */
  onSubmit: (body: { duration_minutes: number } | { until: string }) => Promise<void>;
}

/**
 * Snooze open alerts for a preset length or until a chosen time (1 minute to
 * 7 days); the row menu and the bulk bar share it. They stay active and
 * unacknowledged; only their reminders and the escalation tiers not fired
 * yet are held, and the resolve is still sent.
 */
export default function SnoozeAlertDialog({ subject, count = 1, onClose, onSubmit }: SnoozeAlertDialogProps) {
  const [choice, setChoice] = useState<SnoozeChoice>('1h');
  const [custom, setCustom] = useState(() => toLocalInput(Date.now() + 3_600_000));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const inputId = useId();
  const errorId = `${inputId}-error`;
  const request = snoozeRequest(choice, custom);
  const invalid = 'error' in request ? request.error : '';

  const submit = async () => {
    if ('error' in request) return;
    try {
      setSaving(true);
      setError('');
      await onSubmit(request.body);
    } catch (err) {
      setError(errorMessage(err, 'Failed to snooze alert'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog
      onClose={onClose}
      busy={saving}
      size="sm"
      title={count === 1 ? 'Snooze alert' : 'Snooze alerts'}
      description={<span className="block truncate">{subject}</span>}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={saving}>
            Cancel
          </Button>
          <Button variant="accent" onClick={() => void submit()} loading={saving} disabled={invalid !== ''}>
            Snooze
          </Button>
        </>
      }
    >
      <p className="text-ui leading-relaxed text-slate-300">
        Reminders and escalation steps not sent yet wait until the snooze ends, then resume from that moment.{' '}
        {count === 1 ? 'The alert stays active, and its recovery notice is still sent.' : 'The alerts stay active, and their recovery notices are still sent.'}
      </p>
      <SegmentedControl options={CHOICES} value={choice} onChange={setChoice} label="Snooze for" stretch />
      {choice === 'custom' && (
        <div>
          <label htmlFor={inputId} className="mb-1.5 block text-ui font-medium text-slate-300">
            Until
          </label>
          <input
            id={inputId}
            type="datetime-local"
            value={custom}
            onChange={(event) => setCustom(event.target.value)}
            className="input h-10"
            aria-invalid={invalid ? true : undefined}
            aria-describedby={invalid ? errorId : undefined}
          />
          {invalid && (
            <p id={errorId} className="mt-1 text-xs text-rose-300">
              {invalid}
            </p>
          )}
        </div>
      )}
      {'end' in request && (
        <p className="text-xs text-slate-400">Until {formatDateTimeFull(request.end)}</p>
      )}
      {error && <Callout tone="danger">{error}</Callout>}
    </Dialog>
  );
}

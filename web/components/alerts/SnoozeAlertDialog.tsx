'use client';

import { useState } from 'react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import SegmentedControl from '@/components/ui/SegmentedControl';
import { snoozeAlert } from '@/lib/api';
import { SNOOZE_PRESETS } from '@/lib/alert-list';
import type { Alert } from '@/lib/types';

interface SnoozeAlertDialogProps {
  alert: Alert;
  /** The alert's subject as the list shows it. */
  subject: string;
  onClose: () => void;
  onSnoozed: (alert: Alert) => void;
}

type Choice = `${(typeof SNOOZE_PRESETS)[number]['minutes']}` | 'custom';

const MAX_HOURS = 168;

/**
 * Snooze one alert: no escalation tier, reminder or suspected-cause
 * follow-up until it ends. Unlike snoozing the monitor it opens no
 * maintenance window, so SLA accounting is untouched.
 */
export default function SnoozeAlertDialog({ alert, subject, onClose, onSnoozed }: SnoozeAlertDialogProps) {
  const [choice, setChoice] = useState<Choice>('60');
  const [hours, setHours] = useState('8');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const customHours = Number(hours);
  const customValid = Number.isFinite(customHours) && customHours > 0 && customHours <= MAX_HOURS;
  const minutes = choice === 'custom' ? Math.round(customHours * 60) : Number(choice);

  const submit = async () => {
    if (choice === 'custom' && !customValid) return;
    setBusy(true);
    setError(null);
    try {
      onSnoozed(await snoozeAlert(alert.id, minutes));
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : 'Could not snooze the alert');
      setBusy(false);
    }
  };

  return (
    <Dialog
      onClose={onClose}
      busy={busy}
      size="sm"
      title="Snooze alert"
      description={<span className="block truncate">{subject}</span>}
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button
            variant="accent"
            size="sm"
            onClick={() => void submit()}
            loading={busy}
            disabled={choice === 'custom' && !customValid}
          >
            Snooze
          </Button>
        </>
      }
    >
      {error && <Callout tone="danger">{error}</Callout>}
      <SegmentedControl<Choice>
        label="Snooze for"
        value={choice}
        onChange={setChoice}
        stretch
        options={[
          ...SNOOZE_PRESETS.map((p) => ({ value: `${p.minutes}` as Choice, label: p.label })),
          { value: 'custom' as Choice, label: 'Custom' },
        ]}
      />
      {choice === 'custom' && (
        <label className="flex items-center gap-2 text-ui text-slate-300">
          <input
            type="number"
            min={1}
            max={MAX_HOURS}
            step={1}
            value={hours}
            onChange={(e) => setHours(e.target.value)}
            className="input input-sm !w-24"
            aria-invalid={!customValid || undefined}
          />
          hours (up to 7 days)
        </label>
      )}
      <p className="text-xs leading-relaxed text-slate-500">
        No escalation tier, reminder or suspected-cause follow-up goes out until the snooze ends; then
        paging resumes, its clocks counted from that moment. Acknowledging and resolving still notify,
        and no maintenance window is created.
      </p>
    </Dialog>
  );
}

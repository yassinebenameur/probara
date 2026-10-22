'use client';

import { useId, useState } from 'react';
import { bulkUpdateAlerting } from '@/lib/api';
import type { ChannelAssignment, NotificationMode } from '@/lib/types';
import ChannelPicker from '@/components/channels/ChannelPicker';
import { BellOff } from 'lucide-react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Checkbox from '@/components/ui/Checkbox';
import Dialog from '@/components/ui/Dialog';
import SegmentedControl from '@/components/ui/SegmentedControl';
import { FormRow } from './form-kit';

const NOTIFY_OPTIONS = [
  { value: 'default', label: 'Workspace default' },
  { value: 'custom', label: 'Custom' },
] as const satisfies readonly { value: NotificationMode; label: string }[];

interface BulkAlertingModalProps {
  monitorIds: string[];
  onDone: () => void;
  onCancel: () => void;
}

export function BulkAlertingModal({ monitorIds, onDone, onCancel }: BulkAlertingModalProps) {
  const [touchSensitivity, setTouchSensitivity] = useState(false);
  const [threshold, setThreshold] = useState(2);
  const [touchRouting, setTouchRouting] = useState(false);
  const [mode, setMode] = useState<NotificationMode>('default');
  const [channels, setChannels] = useState<ChannelAssignment[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const notifyTitleId = useId();

  const canApply = !saving && (touchSensitivity || touchRouting);

  const apply = async () => {
    setSaving(true);
    setError(null);
    try {
      await bulkUpdateAlerting({
        monitor_ids: monitorIds,
        ...(touchSensitivity ? { consecutive_failures_threshold: threshold } : {}),
        ...(touchRouting
          ? { notification_mode: mode, notification_channels: mode === 'custom' ? channels : [] }
          : {}),
      });
      onDone();
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Operation failed';
      setError(message);
    } finally {
      setSaving(false);
    }
  };

  const count = `${monitorIds.length} monitor${monitorIds.length === 1 ? '' : 's'}`;

  return (
    <Dialog
      onClose={onCancel}
      busy={saving}
      title={`Edit alerting — ${count}`}
      description="Unchecked sections are left unchanged."
      bodyClassName="space-y-3"
      footer={
        <>
          <Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={saving}>
            Cancel
          </Button>
          <Button type="button" variant="accent" size="sm" onClick={apply} disabled={!canApply} loading={saving}>
            Apply to {count}
          </Button>
        </>
      }
    >
      {/* Sensitivity section */}
      <div className="glass-well p-4">
        <Checkbox
          checked={touchSensitivity}
          onChange={setTouchSensitivity}
          label={<span className="font-medium text-white">Set sensitivity</span>}
          className="select-none"
        />

        {touchSensitivity && (
          <div className="mt-3 pl-[26px]">
            <FormRow
              title="Mark down after"
              description="Consecutive failed checks before a monitor counts as down."
              control={({ titleId, descriptionId }) => (
                <select
                  value={threshold}
                  onChange={(e) => setThreshold(Number(e.target.value))}
                  className="input input-sm !w-44 cursor-pointer"
                  aria-labelledby={titleId}
                  aria-describedby={descriptionId}
                >
                  {Array.from({ length: 10 }, (_, i) => i + 1).map((n) => (
                    <option key={n} value={n}>
                      {n} failed check{n === 1 ? '' : 's'}
                    </option>
                  ))}
                </select>
              )}
            />
          </div>
        )}
      </div>

      {/* Notification routing section */}
      <div className="glass-well p-4">
        <Checkbox
          checked={touchRouting}
          onChange={setTouchRouting}
          label={<span className="font-medium text-white">Set notifications</span>}
          className="select-none"
        />

        {touchRouting && (
          <div className="mt-3 pl-[26px]">
            {/* The monitor form's Notify row (AlertingSection), so the two read the same. */}
            <FormRow
              title="Notify"
              titleId={notifyTitleId}
              description={
                mode === 'custom'
                  ? 'Only the channels picked below, on every selected monitor.'
                  : 'The workspace default channels.'
              }
              control={({ titleId, descriptionId }) => (
                <SegmentedControl
                  options={NOTIFY_OPTIONS}
                  value={mode}
                  onChange={setMode}
                  labelledBy={titleId}
                  describedBy={descriptionId}
                  equalWidth
                />
              )}
            >
              {mode === 'custom' && (
                <div className="mt-3 space-y-2">
                  <ChannelPicker value={channels} onChange={setChannels} ariaLabelledBy={notifyTitleId} />
                  {channels.length === 0 && (
                    <Callout tone="danger" icon={<BellOff strokeWidth={1.75} />}>
                      This will mute all {monitorIds.length} selected monitors: alerts fire but notify no one.
                    </Callout>
                  )}
                </div>
              )}
            </FormRow>
          </div>
        )}
      </div>

      {error && <Callout tone="danger">{error}</Callout>}
    </Dialog>
  );
}

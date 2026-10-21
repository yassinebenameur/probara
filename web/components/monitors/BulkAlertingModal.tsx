'use client';

import { useState } from 'react';
import { bulkUpdateAlerting } from '@/lib/api';
import type { ChannelAssignment, NotificationMode } from '@/lib/types';
import ChannelPicker from '@/components/channels/ChannelPicker';
import { BellOff } from 'lucide-react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Checkbox from '@/components/ui/Checkbox';
import Dialog from '@/components/ui/Dialog';
import Radio from '@/components/ui/Radio';

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
          <div className="mt-2 pl-[26px]">
            <label className="text-ui text-slate-300">
              Mark down after{' '}
              <select
                value={threshold}
                onChange={(e) => setThreshold(Number(e.target.value))}
                className="input input-xs mx-1 !w-auto cursor-pointer"
                aria-label="Consecutive failures threshold"
              >
                {Array.from({ length: 10 }, (_, i) => i + 1).map((n) => (
                  <option key={n} value={n}>{n}</option>
                ))}
              </select>
              consecutive failed check{threshold === 1 ? '' : 's'}
            </label>
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
          <div className="mt-2 space-y-3 pl-[26px]">
            {/* Mode radios */}
            <div role="radiogroup" aria-label="Notification routing" className="flex flex-col">
              <Radio
                name="bulk-notification-mode"
                value="default"
                checked={mode === 'default'}
                onChange={() => setMode('default')}
                label={<span className="text-ui text-slate-300">Workspace default</span>}
              />
              <Radio
                name="bulk-notification-mode"
                value="custom"
                checked={mode === 'custom'}
                onChange={() => setMode('custom')}
                label={<span className="text-ui text-slate-300">Custom channel list</span>}
              />
            </div>

            {/* Channel picker when custom */}
            {mode === 'custom' && (
              <div className="space-y-2 pt-1">
                <ChannelPicker value={channels} onChange={setChannels} />
                {channels.length === 0 && (
                  <Callout tone="danger" icon={<BellOff strokeWidth={1.75} />}>
                    This will mute all {monitorIds.length} selected monitors — alerts fire but notify no one.
                  </Callout>
                )}
              </div>
            )}
          </div>
        )}
      </div>

      {error && <Callout tone="danger">{error}</Callout>}
    </Dialog>
  );
}

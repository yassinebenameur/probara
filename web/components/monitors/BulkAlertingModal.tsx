'use client';

import { useState } from 'react';
import { bulkUpdateAlerting } from '@/lib/api';
import type { ChannelAssignment, NotificationMode } from '@/lib/types';
import ChannelPicker from '@/components/channels/ChannelPicker';
import { BellOff, X } from 'lucide-react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import ModalPortal from '@/components/ui/ModalPortal';

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

  return (
    <ModalPortal>
      <div className="glass-scrim fixed inset-0 z-50 flex items-center justify-center p-4">
        <div className="glass-overlay w-full max-w-lg">
          {/* Header */}
          <div className="flex items-start justify-between gap-4 px-6 pt-5">
            <div>
              <h2 className="text-base font-semibold text-white">
                Edit alerting — {monitorIds.length} monitor{monitorIds.length === 1 ? '' : 's'}
              </h2>
              <p className="mt-1 text-ui text-slate-400">
                Unchecked sections are left unchanged.
              </p>
            </div>
            <IconButton icon={<X strokeWidth={1.75} />} label="Close" onClick={onCancel} />
          </div>

          <div className="space-y-3 px-6 py-4">
            {/* Sensitivity section */}
            <div className="glass-well p-4">
              <label className="flex cursor-pointer select-none items-center gap-2.5">
                <input
                  type="checkbox"
                  checked={touchSensitivity}
                  onChange={(e) => setTouchSensitivity(e.target.checked)}
                  className="h-4 w-4 flex-shrink-0 cursor-pointer accent-cyan-500"
                />
                <span className="text-sm font-medium text-white">Set sensitivity</span>
              </label>

              {touchSensitivity && (
                <div className="mt-3 pl-6">
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
              <label className="flex cursor-pointer select-none items-center gap-2.5">
                <input
                  type="checkbox"
                  checked={touchRouting}
                  onChange={(e) => setTouchRouting(e.target.checked)}
                  className="h-4 w-4 flex-shrink-0 cursor-pointer accent-cyan-500"
                />
                <span className="text-sm font-medium text-white">Set notifications</span>
              </label>

              {touchRouting && (
                <div className="mt-3 space-y-3 pl-6">
                  {/* Mode radios */}
                  <div className="flex flex-col gap-2">
                    <label className="flex cursor-pointer items-center gap-2">
                      <input
                        type="radio"
                        name="bulk-notification-mode"
                        value="default"
                        checked={mode === 'default'}
                        onChange={() => setMode('default')}
                        className="h-3.5 w-3.5 cursor-pointer accent-cyan-500"
                      />
                      <span className="text-ui text-slate-300">Workspace default</span>
                    </label>
                    <label className="flex cursor-pointer items-center gap-2">
                      <input
                        type="radio"
                        name="bulk-notification-mode"
                        value="custom"
                        checked={mode === 'custom'}
                        onChange={() => setMode('custom')}
                        className="h-3.5 w-3.5 cursor-pointer accent-cyan-500"
                      />
                      <span className="text-ui text-slate-300">Custom channel list</span>
                    </label>
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
          </div>

          {/* Footer */}
          <div className="flex items-center justify-end gap-2 px-6 pb-5">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={onCancel}
              disabled={saving}
            >
              Cancel
            </Button>
            <Button
              type="button"
              variant="accent"
              size="sm"
              onClick={apply}
              disabled={!canApply}
              loading={saving}
            >
              Apply to {monitorIds.length} monitor{monitorIds.length === 1 ? '' : 's'}
            </Button>
          </div>
        </div>
      </div>
    </ModalPortal>
  );
}

'use client';

import { useState } from 'react';
import { bulkUpdateAlerting } from '@/lib/api';
import type { ChannelAssignment, NotificationMode } from '@/lib/types';
import ChannelPicker from '@/components/channels/ChannelPicker';
import Button from '@/components/ui/Button';

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
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 backdrop-blur-sm">
      <div className="w-full max-w-lg rounded-xl border border-white/[0.08] bg-slate-900/95 p-6 shadow-2xl">
        {/* Header */}
        <div className="mb-5 flex items-start justify-between">
          <div>
            <h2 className="text-base font-semibold text-white">
              Edit alerting — {monitorIds.length} monitor{monitorIds.length === 1 ? '' : 's'}
            </h2>
            <p className="mt-1 text-xs text-slate-500">
              Unchecked sections are left unchanged.
            </p>
          </div>
          <button
            onClick={onCancel}
            className="text-slate-400 hover:text-white transition-colors"
            aria-label="Close"
          >
            ×
          </button>
        </div>

        <div className="space-y-5">
          {/* Sensitivity section */}
          <div className="rounded-lg border border-white/[0.06] bg-slate-950/40 p-4">
            <label className="flex items-center gap-2.5 cursor-pointer select-none">
              <input
                type="checkbox"
                checked={touchSensitivity}
                onChange={(e) => setTouchSensitivity(e.target.checked)}
                className="h-3.5 w-3.5 rounded border-slate-600 bg-slate-800 text-cyan-500 accent-cyan-500 cursor-pointer flex-shrink-0"
              />
              <span className="text-sm font-medium text-slate-200">Set sensitivity</span>
            </label>

            {touchSensitivity && (
              <div className="mt-3 pl-6">
                <label className="text-xs text-slate-400">
                  Mark down after{' '}
                  <select
                    value={threshold}
                    onChange={(e) => setThreshold(Number(e.target.value))}
                    className="mx-1 rounded border border-white/[0.08] bg-slate-800 px-2 py-0.5 text-xs text-slate-200 focus:border-cyan-500/50 focus:outline-none cursor-pointer"
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
          <div className="rounded-lg border border-white/[0.06] bg-slate-950/40 p-4">
            <label className="flex items-center gap-2.5 cursor-pointer select-none">
              <input
                type="checkbox"
                checked={touchRouting}
                onChange={(e) => setTouchRouting(e.target.checked)}
                className="h-3.5 w-3.5 rounded border-slate-600 bg-slate-800 text-cyan-500 accent-cyan-500 cursor-pointer flex-shrink-0"
              />
              <span className="text-sm font-medium text-slate-200">Set notifications</span>
            </label>

            {touchRouting && (
              <div className="mt-3 pl-6 space-y-3">
                {/* Mode radios */}
                <div className="flex flex-col gap-2">
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="radio"
                      name="bulk-notification-mode"
                      value="default"
                      checked={mode === 'default'}
                      onChange={() => setMode('default')}
                      className="h-3.5 w-3.5 border-slate-600 text-cyan-500 accent-cyan-500 cursor-pointer"
                    />
                    <span className="text-xs text-slate-300">Workspace default</span>
                  </label>
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="radio"
                      name="bulk-notification-mode"
                      value="custom"
                      checked={mode === 'custom'}
                      onChange={() => setMode('custom')}
                      className="h-3.5 w-3.5 border-slate-600 text-cyan-500 accent-cyan-500 cursor-pointer"
                    />
                    <span className="text-xs text-slate-300">Custom channel list</span>
                  </label>
                </div>

                {/* Channel picker when custom */}
                {mode === 'custom' && (
                  <div className="pt-1 space-y-2">
                    <ChannelPicker value={channels} onChange={setChannels} />
                    {channels.length === 0 && (
                      <div className="rounded-lg border border-rose-700/40 bg-rose-950/40 px-3 py-2 text-xs text-rose-200">
                        🔕 This will mute all {monitorIds.length} selected monitors — alerts fire but notify no one.
                      </div>
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
        </div>

        {error && (
          <p className="mt-4 rounded border border-rose-500/20 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
            {error}
          </p>
        )}

        {/* Footer */}
        <div className="mt-5 flex items-center justify-end gap-2">
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
  );
}

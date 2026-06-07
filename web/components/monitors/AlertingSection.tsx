'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { getNotificationSettings } from '@/lib/api';
import type { ChannelAssignment, NotificationMode, NotificationSettings } from '@/lib/types';
import ChannelPicker from '@/components/channels/ChannelPicker';

interface AlertingSectionProps {
  isGroup: boolean;
  intervalSeconds: number;
  threshold: number;
  onThresholdChange: (n: number) => void;
  mode: NotificationMode;
  onModeChange: (m: NotificationMode) => void;
  customChannels: ChannelAssignment[];
  onCustomChannelsChange: (next: ChannelAssignment[]) => void;
}

function detectionHint(threshold: number, intervalSeconds: number): string {
  const recheck = Math.min(intervalSeconds, 20);
  const confirmSeconds = threshold * recheck;
  const approx = confirmSeconds >= 90 ? `~${Math.round(confirmSeconds / 60)} min` : `~${confirmSeconds}s`;
  return `rechecks every ${recheck}s once a failure is seen — you'd be alerted ${approx} into an outage`;
}

export function AlertingSection({
  isGroup, intervalSeconds, threshold, onThresholdChange,
  mode, onModeChange, customChannels, onCustomChannelsChange,
}: AlertingSectionProps) {
  const [settings, setSettings] = useState<NotificationSettings | null>(null);
  useEffect(() => { getNotificationSettings().then(setSettings).catch(() => setSettings(null)); }, []);

  const defaults = settings?.default_channels ?? [];
  const defaultSummary = defaults.length
    ? defaults.map((c) => c.channel_name ?? c.channel_id).join(' · ')
    : null;
  const muted = mode === 'custom' && customChannels.length === 0;

  // Switching to custom pre-checks the workspace default channels (spec §7.1).
  const switchToCustom = () => {
    if (customChannels.length === 0 && defaults.length > 0) {
      onCustomChannelsChange(defaults.map((c) => ({ channel_id: c.channel_id, delay_seconds: c.delay_seconds })));
    }
    onModeChange('custom');
  };

  return (
    <div className="space-y-5">
      {!isGroup && (
        <div>
          <div className="text-sm text-slate-300">
            Mark down after{' '}
            <select
              aria-label="Consecutive failed checks before marking down"
              className="mx-1 rounded-md border border-white/[0.06] bg-slate-900 px-2 py-1 text-sm text-slate-200"
              value={threshold}
              onChange={(e) => onThresholdChange(Number(e.target.value))}
            >
              {Array.from({ length: 10 }, (_, i) => i + 1).map((n) => (
                <option key={n} value={n}>{n}</option>
              ))}
            </select>{' '}
            consecutive failed checks
          </div>
          {threshold === 1 ? (
            <p className="mt-1 text-xs text-amber-400">
              ⚠ alerts on the very first failed check — a single network blip will page you
            </p>
          ) : (
            <p className="mt-1 text-xs text-slate-500">ⓘ {detectionHint(threshold, intervalSeconds)}</p>
          )}
        </div>
      )}

      <div>
        <label className="mb-2 block text-sm font-medium text-slate-200">Notifications</label>
        <div className="space-y-2">
          <label className="flex items-start gap-2 text-sm text-slate-300">
            <input type="radio" checked={mode === 'default'} onChange={() => onModeChange('default')} />
            <span>
              Workspace default
              {defaultSummary && <span className="ml-1 text-xs text-slate-500">— {defaultSummary}</span>}
            </span>
          </label>
          {mode === 'default' && settings && defaults.length === 0 && (
            <div className="ml-6 rounded-lg border border-amber-600/40 bg-amber-950/40 px-3 py-2 text-xs text-amber-200">
              ⚠ No default configured — alerts won&apos;t notify anyone.{' '}
              <Link href="/settings" className="text-cyan-400">Connect a channel</Link>
            </div>
          )}
          <label className="flex items-center gap-2 text-sm text-slate-300">
            <input type="radio" checked={mode === 'custom'} onChange={switchToCustom} />
            <span>Custom for this monitor…</span>
          </label>
          {mode === 'custom' && (
            <div className="ml-6 space-y-2">
              <ChannelPicker
                value={customChannels}
                onChange={onCustomChannelsChange}
                defaultChannelIds={defaults.map((c) => c.channel_id)}
              />
              {muted && (
                <div className="rounded-lg border border-rose-700/40 bg-rose-950/40 px-3 py-2 text-xs text-rose-200">
                  🔕 This monitor is muted — alerts fire (status page, incidents) but notify no one.
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

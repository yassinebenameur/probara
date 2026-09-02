'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { getNotificationSettings } from '@/lib/api';
import type {
  AlertChannel,
  ChannelAssignment,
  DependencySuppression,
  MemberAlertRollup,
  NotificationMode,
  NotificationSettings,
} from '@/lib/types';
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
  // Group-only: how alerts roll up across members. Omit for non-group monitors.
  rollup?: MemberAlertRollup;
  onRollupChange?: (r: MemberAlertRollup) => void;
  // Whether this monitor pages while an upstream dependency is down. Omit to
  // hide the control (the server keeps the stored value).
  dependencySuppression?: DependencySuppression;
  onDependencySuppressionChange?: (d: DependencySuppression) => void;
}

function graceLabel(seconds: number): string {
  if (seconds <= 0) return 'immediately';
  if (seconds % 60 === 0) return `after ${seconds / 60} min`;
  return `after ${seconds}s`;
}

function detectionHint(threshold: number, intervalSeconds: number): string {
  const recheck = Math.min(intervalSeconds, 20);
  // Worst case: a full interval passes before the first failure is observed,
  // then (threshold - 1) fast rechecks confirm the outage.
  const worstCaseSeconds = intervalSeconds + (threshold - 1) * recheck;
  const approx = worstCaseSeconds >= 90 ? `~${Math.round(worstCaseSeconds / 60)} min` : `~${worstCaseSeconds}s`;
  return `rechecks every ${recheck}s once a failure is seen — alerted within ${approx} of an outage starting`;
}

export function AlertingSection({
  isGroup, intervalSeconds, threshold, onThresholdChange,
  mode, onModeChange, customChannels, onCustomChannelsChange,
  rollup, onRollupChange,
  dependencySuppression, onDependencySuppressionChange,
}: AlertingSectionProps) {
  const [settings, setSettings] = useState<NotificationSettings | null>(null);
  const [channels, setChannels] = useState<AlertChannel[]>([]);
  useEffect(() => { getNotificationSettings().then(setSettings).catch(() => setSettings(null)); }, []);

  const defaults = settings?.default_channels ?? [];
  const defaultSummary = defaults.length
    ? defaults.map((c) => c.channel_name ?? c.channel_id).join(' · ')
    : null;
  const muted = mode === 'custom' && customChannels.length === 0;
  // A selection made entirely of disabled channels delivers nothing either —
  // the alerter skips inactive channels at dispatch.
  const mutedByDisabled =
    mode === 'custom' &&
    customChannels.length > 0 &&
    channels.length > 0 &&
    customChannels.every((assignment) =>
      channels.some((channel) => channel.id === assignment.channel_id && channel.is_active === false),
    );

  // Switching to custom pre-checks the workspace default channels (spec §7.1).
  const switchToCustom = () => {
    if (customChannels.length === 0 && defaults.length > 0) {
      onCustomChannelsChange(defaults.map((c) => ({ channel_id: c.channel_id, delay_seconds: c.delay_seconds })));
    }
    onModeChange('custom');
  };

  const perMonitorRollup = isGroup && rollup === 'per_monitor';

  return (
    <div className="space-y-5">
      {isGroup && rollup && onRollupChange && (
        <div>
          <label className="mb-2 block text-sm font-medium text-slate-200">
            When monitors in this group go down
          </label>
          <div className="space-y-2">
            <label className="flex items-start gap-2 text-sm text-slate-300">
              <input
                type="radio"
                className="mt-0.5"
                checked={rollup === 'per_monitor'}
                onChange={() => onRollupChange('per_monitor')}
              />
              <span>
                Alert me for each monitor
                <span className="block text-xs text-slate-500">
                  Each member notifies on its own. Best for grouping similar monitors (e.g. all ASRs).
                </span>
              </span>
            </label>
            <label className="flex items-start gap-2 text-sm text-slate-300">
              <input
                type="radio"
                className="mt-0.5"
                checked={rollup === 'group'}
                onChange={() => onRollupChange('group')}
              />
              <span>
                Send one alert for the whole group
                <span className="block text-xs text-slate-500">
                  Members are silenced; one group alert covers them. Best for a single service made of several monitors.
                </span>
              </span>
            </label>
          </div>
        </div>
      )}

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

      {perMonitorRollup ? (
        <p className="text-xs text-slate-500">
          ⓘ Notifications are configured on each monitor individually. This group won&apos;t send its own alert.
        </p>
      ) : (
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
                onChannelsLoaded={setChannels}
              />
              {muted && (
                <div className="rounded-lg border border-rose-700/40 bg-rose-950/40 px-3 py-2 text-xs text-rose-200">
                  🔕 This monitor is muted — alerts fire (status page, incidents) but notify no one.
                </div>
              )}
              {mutedByDisabled && (
                <div className="rounded-lg border border-rose-700/40 bg-rose-950/40 px-3 py-2 text-xs text-rose-200">
                  🔕 Every channel selected here is disabled — alerts fire but notify no one.
                </div>
              )}
            </div>
          )}
        </div>
      </div>
      )}

      {!perMonitorRollup && dependencySuppression && onDependencySuppressionChange && (
        <div>
          <label className="mb-2 block text-sm font-medium text-slate-200">
            When an upstream dependency is down
          </label>
          <div className="space-y-2">
            <label className="flex items-start gap-2 text-sm text-slate-300">
              <input
                type="radio"
                className="mt-0.5"
                checked={dependencySuppression === 'inherit'}
                onChange={() => onDependencySuppressionChange('inherit')}
              />
              <span>
                Follow the workspace setting
                {settings && (
                  <span className="ml-1 text-xs text-slate-500">
                    — currently {settings.dependency_suppression_enabled ? 'suppress' : 'always page'}
                  </span>
                )}
              </span>
            </label>
            <label className="flex items-start gap-2 text-sm text-slate-300">
              <input
                type="radio"
                className="mt-0.5"
                checked={dependencySuppression === 'off'}
                onChange={() => onDependencySuppressionChange('off')}
              />
              <span>
                Always page
                <span className="block text-xs text-slate-500">
                  Notify even when the outage is explained by a dependency. For monitors that must never go quiet.
                </span>
              </span>
            </label>
            <label className="flex items-start gap-2 text-sm text-slate-300">
              <input
                type="radio"
                className="mt-0.5"
                checked={dependencySuppression === 'on'}
                onChange={() => onDependencySuppressionChange('on')}
              />
              <span>
                Suppress notifications while an upstream is down
                <span className="block text-xs text-slate-500">
                  The alert still opens and the root cause&apos;s notification lists this monitor. If the upstream
                  recovers and this monitor stays down, it pages{' '}
                  {graceLabel(settings?.dependency_suppression_grace_seconds ?? 120)}.
                </span>
              </span>
            </label>
          </div>
          <p className="mt-1 text-xs text-slate-500">
            ⓘ Only applies when this monitor has upstream dependencies.{' '}
            <Link href="/dependencies" className="text-cyan-400">Open the dependency graph</Link>
          </p>
        </div>
      )}
    </div>
  );
}

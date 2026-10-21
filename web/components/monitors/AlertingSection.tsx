'use client';

import { useEffect, useId, useState } from 'react';
import Link from 'next/link';
import { AlertTriangle, BellOff } from 'lucide-react';
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
import Callout from '@/components/ui/Callout';
import Radio from '@/components/ui/Radio';

const GROUP_LABEL = 'mb-2.5 block text-ui font-medium text-slate-300';
// Links inside running text are underlined: colour alone is not enough to
// tell them from the sentence around them (WCAG 1.4.1).
const LINK =
  'font-medium text-cyan-300 underline decoration-cyan-300/40 underline-offset-2 hover:text-cyan-200 hover:decoration-cyan-300';
// Lines up nested content with the radio labels (16px box + 10px gap).
const NESTED = 'ml-[26px]';

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
  // The control only matters with upstream dependencies, so it is hidden
  // without them — unless a non-default value is stored, which stays visible.
  hasDependencies: boolean;
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
  return `Rechecks every ${recheck}s once a failure is seen — alerted within ${approx} of an outage starting`;
}

export function AlertingSection({
  isGroup, intervalSeconds, threshold, onThresholdChange,
  mode, onModeChange, customChannels, onCustomChannelsChange,
  rollup, onRollupChange,
  dependencySuppression, onDependencySuppressionChange, hasDependencies,
}: AlertingSectionProps) {
  const [settings, setSettings] = useState<NotificationSettings | null>(null);
  const [channels, setChannels] = useState<AlertChannel[]>([]);
  // Radios need a shared name per group for arrow-key movement, and the
  // form can render more than one AlertingSection.
  const radioName = useId();
  useEffect(() => { getNotificationSettings().then(setSettings).catch(() => setSettings(null)); }, []);

  const defaults = settings?.default_channels ?? [];
  const defaultSummary = defaults.length
    ? defaults.map((c) => c.channel_name ?? c.channel_id).join(', ')
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
        <fieldset>
          <legend className={GROUP_LABEL}>
            When monitors in this group go down
          </legend>
          <div className="flex flex-col items-start gap-1">
            <Radio
              name={`${radioName}-rollup`}
              checked={rollup === 'per_monitor'}
              onChange={() => onRollupChange('per_monitor')}
              label="Alert me for each monitor"
              description="Each member notifies on its own. Best for grouping similar monitors (e.g. all ASRs)."
            />
            <Radio
              name={`${radioName}-rollup`}
              checked={rollup === 'group'}
              onChange={() => onRollupChange('group')}
              label="Send one alert for the whole group"
              description="Members are silenced; one group alert covers them. Best for a single service made of several monitors."
            />
          </div>
        </fieldset>
      )}

      {!isGroup && (
        <div>
          <div className="text-sm text-slate-300">
            Mark down after{' '}
            <select
              aria-label="Consecutive failed checks before marking down"
              className="input input-sm mx-1 !w-auto"
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
            <p className="mt-1.5 flex items-center gap-1.5 text-xs text-amber-300">
              <AlertTriangle className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} aria-hidden="true" />
              Alerts on the very first failed check — a single network blip will page you
            </p>
          ) : (
            <p className="mt-1.5 text-xs text-slate-500">{detectionHint(threshold, intervalSeconds)}</p>
          )}
        </div>
      )}

      {perMonitorRollup ? (
        <p className="text-xs text-slate-500">
          Notifications are configured on each monitor individually. This group won&apos;t send its own alert.
        </p>
      ) : (
      <fieldset>
        <legend className={GROUP_LABEL}>Notifications</legend>
        <div className="space-y-1">
          <Radio
            name={`${radioName}-mode`}
            checked={mode === 'default'}
            onChange={() => onModeChange('default')}
            label={
              <>
                Workspace default
                {defaultSummary && <span className="ml-1 text-xs text-slate-500">— {defaultSummary}</span>}
              </>
            }
          />
          {mode === 'default' && settings && defaults.length === 0 && (
            <Callout tone="warning" className={NESTED}>
              No default configured, so alerts won&apos;t notify anyone.{' '}
              {/* A new tab, so a half-filled monitor form isn't lost. */}
              <Link href="/settings?tab=channels" target="_blank" rel="noopener noreferrer" className={LINK}>
                Connect a channel<span className="sr-only"> (opens in a new tab)</span>
              </Link>
            </Callout>
          )}
          <div>
            <Radio
              name={`${radioName}-mode`}
              checked={mode === 'custom'}
              onChange={switchToCustom}
              label="Custom for this monitor…"
            />
          </div>
          {mode === 'custom' && (
            <div className={`${NESTED} space-y-2`}>
              <ChannelPicker
                value={customChannels}
                onChange={onCustomChannelsChange}
                defaultChannelIds={defaults.map((c) => c.channel_id)}
                onChannelsLoaded={setChannels}
              />
              {muted && (
                <Callout tone="danger" icon={<BellOff strokeWidth={1.75} />}>
                  This monitor is muted — alerts fire (status page, incidents) but notify no one.
                </Callout>
              )}
              {mutedByDisabled && (
                <Callout tone="danger" icon={<BellOff strokeWidth={1.75} />}>
                  Every channel selected here is disabled — alerts fire but notify no one.
                </Callout>
              )}
            </div>
          )}
        </div>
      </fieldset>
      )}

      {!perMonitorRollup && dependencySuppression && onDependencySuppressionChange &&
        (hasDependencies || dependencySuppression !== 'inherit') && (
        <fieldset>
          <legend className={GROUP_LABEL}>
            When an upstream dependency is down
          </legend>
          <div className="flex flex-col items-start gap-1">
            <Radio
              name={`${radioName}-dependency`}
              checked={dependencySuppression === 'inherit'}
              onChange={() => onDependencySuppressionChange('inherit')}
              label={
                <>
                  Follow the workspace setting
                  {settings && (
                    <span className="ml-1 text-xs text-slate-500">
                      — currently {settings.dependency_suppression_enabled ? 'suppress' : 'always page'}
                    </span>
                  )}
                </>
              }
            />
            <Radio
              name={`${radioName}-dependency`}
              checked={dependencySuppression === 'off'}
              onChange={() => onDependencySuppressionChange('off')}
              label="Always page"
              description="Notify even when the outage is explained by a dependency. For monitors that must never go quiet."
            />
            <Radio
              name={`${radioName}-dependency`}
              checked={dependencySuppression === 'on'}
              onChange={() => onDependencySuppressionChange('on')}
              label="Suppress notifications while an upstream is down"
              description={
                <>
                  The alert still opens and the root cause&apos;s notification lists this monitor. If the upstream
                  recovers and this monitor stays down, it pages{' '}
                  {graceLabel(settings?.dependency_suppression_grace_seconds ?? 120)}.
                </>
              }
            />
          </div>
          {!hasDependencies && (
            <p className="mt-2 text-xs text-slate-500">
              This monitor has no upstream dependencies, so this setting has no effect yet.{' '}
              <Link href="/dependencies" className={LINK}>Open the dependency graph</Link>
            </p>
          )}
        </fieldset>
      )}
    </div>
  );
}

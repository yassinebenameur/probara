'use client';

import { type ReactNode, useEffect, useId, useState } from 'react';
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
import SegmentedControl from '@/components/ui/SegmentedControl';
import { notifyModeChange } from '@/lib/monitor-form/common';
import { FormRow } from './form-kit';
// Links inside running text are underlined: colour alone is not enough to
// tell them from the sentence around them (WCAG 1.4.1).
const LINK =
  'font-medium text-cyan-300 underline decoration-cyan-300/40 underline-offset-2 hover:text-cyan-200 hover:decoration-cyan-300';

const NOTIFY_OPTIONS = [
  { value: 'default', label: 'Workspace default' },
  { value: 'custom', label: 'Custom' },
] as const satisfies readonly { value: NotificationMode; label: string }[];

// Every option's effect is listed in its row's description (OptionList),
// so a choice can be read before it is made; `title` repeats it on the
// segment as its tooltip and accessible description.
const ROLLUP_OPTIONS = [
  {
    value: 'per_monitor',
    label: 'Each member',
    effect: 'every member notifies on its own. Best for grouping similar monitors (e.g. all ASRs).',
  },
  {
    value: 'group',
    label: 'One group alert',
    effect: 'members are silenced and one group alert covers them. Best for a single service made of several monitors.',
  },
] as const satisfies readonly { value: MemberAlertRollup; label: string; effect: string }[];
const ROLLUP_SEGMENTS = ROLLUP_OPTIONS.map((o) => ({
  value: o.value,
  label: o.label,
  title: o.effect.charAt(0).toUpperCase() + o.effect.slice(1),
}));

const DEPENDENCY_LABELS: Record<DependencySuppression, string> = {
  inherit: 'Workspace',
  off: 'Always page',
  on: 'Suppress',
};

/**
 * Each option of a row's SegmentedControl with what it does, the selected
 * one raised; a row description, so it renders as spans inside FormRow's <p>.
 */
function OptionList<T extends string>({
  options,
  value,
}: {
  options: readonly { value: T; label: string; effect: ReactNode }[];
  value: T;
}) {
  return (
    <>
      {options.map((option) => (
        <span key={option.value} className={`block ${option.value === value ? 'text-slate-300' : ''}`}>
          <span className="font-medium">{option.label}:</span> {option.effect}
        </span>
      ))}
    </>
  );
}

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
  return `Rechecks every ${recheck}s once a failure is seen, so an outage alerts within ${approx}.`;
}

export function AlertingSection({
  isGroup, intervalSeconds, threshold, onThresholdChange,
  mode, onModeChange, customChannels, onCustomChannelsChange,
  rollup, onRollupChange,
  dependencySuppression, onDependencySuppressionChange, hasDependencies,
}: AlertingSectionProps) {
  const [settings, setSettings] = useState<NotificationSettings | null>(null);
  const [channels, setChannels] = useState<AlertChannel[]>([]);
  // The form can render more than one AlertingSection.
  const idBase = useId();
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

  // Switching to custom pre-checks the workspace default channels (spec §7.1);
  // re-choosing the selected mode changes nothing (notifyModeChange).
  const chooseMode = (next: NotificationMode) => {
    const change = notifyModeChange(next, mode, customChannels, defaults);
    if (!change) return;
    if (change.channels) onCustomChannelsChange(change.channels);
    onModeChange(change.mode);
  };

  const perMonitorRollup = isGroup && rollup === 'per_monitor';
  const notifyTitleId = `${idBase}-notify`;

  const notifyDescription =
    mode === 'custom'
      ? 'Only the channels picked below, whatever the workspace default.'
      : defaultSummary
        ? `The workspace default: ${defaultSummary}.`
        : 'The workspace default channels.';

  const workspaceSuppresses = settings?.dependency_suppression_enabled;
  const dependencyOptions = [
    {
      value: 'inherit',
      label: DEPENDENCY_LABELS.inherit,
      effect: settings
        ? `follow the workspace setting, currently ${workspaceSuppresses ? 'suppress' : 'always page'}.`
        : 'follow the workspace setting.',
    },
    {
      value: 'off',
      label: DEPENDENCY_LABELS.off,
      effect: 'notify even when a dependency explains the outage. For monitors that must never go quiet.',
    },
    {
      value: 'on',
      label: DEPENDENCY_LABELS.on,
      effect: (
        <>
          stay quiet; the alert still opens and the root cause&apos;s notification lists this monitor. If the
          upstream recovers and this monitor stays down, it pages{' '}
          {graceLabel(settings?.dependency_suppression_grace_seconds ?? 120)}.
        </>
      ),
    },
  ] as const satisfies readonly { value: DependencySuppression; label: string; effect: ReactNode }[];
  // Plain-text tooltips for the segments (the JSX effect above cannot be one).
  const dependencySegments = [
    {
      value: 'inherit',
      label: DEPENDENCY_LABELS.inherit,
      title: `Follow the workspace setting${settings ? ` (currently ${workspaceSuppresses ? 'suppress' : 'always page'})` : ''}`,
    },
    { value: 'off', label: DEPENDENCY_LABELS.off, title: 'Notify even when a dependency explains the outage' },
    { value: 'on', label: DEPENDENCY_LABELS.on, title: 'Stay quiet while an upstream dependency is down' },
  ] as const satisfies readonly { value: DependencySuppression; label: string; title: string }[];

  // Rows in the settings page's vocabulary (FormRow + SegmentedControl), one
  // hairline apart; the panel's Dependencies row follows under the last.
  return (
    <div className="divide-y divide-divider [&>*]:py-4 [&>*:first-child]:pt-0 [&>*:last-child]:pb-0">
      {isGroup && rollup && onRollupChange && (
        <FormRow
          title="When members go down"
          description={<OptionList options={ROLLUP_OPTIONS} value={rollup} />}
          control={({ titleId, descriptionId }) => (
            <SegmentedControl
              options={ROLLUP_SEGMENTS}
              value={rollup}
              onChange={onRollupChange}
              labelledBy={titleId}
              describedBy={descriptionId}
              equalWidth
            />
          )}
        />
      )}

      {!isGroup && (
        <FormRow
          title="Mark down after"
          description={
            threshold === 1 ? (
              <span className="inline-flex items-start gap-1.5 text-amber-300">
                <AlertTriangle className="mt-px h-3.5 w-3.5 shrink-0" strokeWidth={1.75} aria-hidden="true" />
                Alerts on the very first failed check: a single network blip will page you.
              </span>
            ) : (
              detectionHint(threshold, intervalSeconds)
            )
          }
          control={({ titleId, descriptionId }) => (
            <select
              aria-labelledby={titleId}
              aria-describedby={descriptionId}
              className="input input-sm !w-44"
              value={threshold}
              onChange={(e) => onThresholdChange(Number(e.target.value))}
            >
              {Array.from({ length: 10 }, (_, i) => i + 1).map((n) => (
                <option key={n} value={n}>
                  {n} failed check{n === 1 ? '' : 's'}
                </option>
              ))}
            </select>
          )}
        />
      )}

      {perMonitorRollup ? (
        <FormRow
          title="Notifications"
          description="Configured on each member. This group sends no alert of its own."
        />
      ) : (
        <FormRow
          title="Notify"
          titleId={notifyTitleId}
          description={notifyDescription}
          control={({ titleId, descriptionId }) => (
            <SegmentedControl
              options={NOTIFY_OPTIONS}
              value={mode}
              onChange={chooseMode}
              labelledBy={titleId}
              describedBy={descriptionId}
              equalWidth
            />
          )}
        >
          {mode === 'default' && settings && defaults.length === 0 && (
            <Callout tone="warning" className="mt-3">
              No default configured, so alerts won&apos;t notify anyone.{' '}
              {/* A new tab, so a half-filled monitor form isn't lost. */}
              <Link href="/settings?tab=channels" target="_blank" rel="noopener noreferrer" className={LINK}>
                Connect a channel<span className="sr-only"> (opens in a new tab)</span>
              </Link>
            </Callout>
          )}
          {mode === 'custom' && (
            <div className="mt-3 space-y-2">
              <ChannelPicker
                value={customChannels}
                onChange={onCustomChannelsChange}
                defaultChannelIds={defaults.map((c) => c.channel_id)}
                onChannelsLoaded={setChannels}
                ariaLabelledBy={notifyTitleId}
              />
              {muted && (
                <Callout tone="danger" icon={<BellOff strokeWidth={1.75} />}>
                  This monitor is muted: alerts fire (status page, incidents) but notify no one.
                </Callout>
              )}
              {mutedByDisabled && (
                <Callout tone="danger" icon={<BellOff strokeWidth={1.75} />}>
                  Every channel selected here is disabled: alerts fire but notify no one.
                </Callout>
              )}
            </div>
          )}
        </FormRow>
      )}

      {!perMonitorRollup && dependencySuppression && onDependencySuppressionChange &&
        (hasDependencies || dependencySuppression !== 'inherit') && (
        <FormRow
          title="While an upstream is down"
          description={<OptionList options={dependencyOptions} value={dependencySuppression} />}
          control={({ titleId, descriptionId }) => (
            <SegmentedControl
              options={dependencySegments}
              value={dependencySuppression}
              onChange={onDependencySuppressionChange}
              labelledBy={titleId}
              describedBy={descriptionId}
            />
          )}
        >
          {!hasDependencies && (
            <p className="mt-2 text-xs text-slate-500">
              This monitor has no upstream dependencies, so this setting has no effect yet.{' '}
              <Link href="/dependencies" className={LINK}>Open the dependency graph</Link>
            </p>
          )}
        </FormRow>
      )}
    </div>
  );
}

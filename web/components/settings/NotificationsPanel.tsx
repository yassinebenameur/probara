'use client';

import { useEffect, useId, useState, type ReactNode } from 'react';
import Link from 'next/link';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import FormActions from '@/components/ui/FormActions';
import SegmentedControl, { type SegmentOption } from '@/components/ui/SegmentedControl';
import Switch from '@/components/ui/Switch';
import { useToast } from '@/components/ui/ToastProvider';
import ChannelPicker from '@/components/channels/ChannelPicker';
import { getNotificationSettings, updateNotificationSettings } from '@/lib/api';
import { SETTINGS_SECTION_COLUMNS } from './layout';
import type { ChannelAssignment, NotificationSettings } from '@/lib/types';

const REMINDER_OPTIONS = [
  { label: 'Off', value: 0 },
  { label: 'Every 30 min', value: 1800 },
  { label: 'Every hour', value: 3600 },
  { label: 'Every 4 hours', value: 14400 },
];

// Latency anomaly presets — users pick a level, not a raw number. Each
// sensitivity level bundles the statistical sigma + the minimum increase.
// `short` is the segment text; `label` its tooltip and accessible name.
const SENSITIVITY_LEVELS = [
  { label: 'Low', short: 'Low', sigma: 4.5, deltaPct: 30, hint: 'Only flags large, obvious slowdowns.' },
  { label: 'Medium', short: 'Medium', sigma: 3.5, deltaPct: 20, hint: 'Balanced — recommended for most monitors.' },
  { label: 'High', short: 'High', sigma: 2.5, deltaPct: 10, hint: 'Flags subtle drift; can be noisier.' },
];
const BASELINE_LEVELS = [
  { label: '1 day', short: '1d', hours: 24 },
  { label: '3 days', short: '3d', hours: 72 },
  { label: '7 days', short: '7d', hours: 168 },
  { label: '14 days', short: '14d', hours: 336 },
];
const SUSTAINED_LEVELS = [
  { label: '1 min', short: '1m', seconds: 60 },
  { label: '2 min', short: '2m', seconds: 120 },
  { label: '5 min', short: '5m', seconds: 300 },
  { label: '10 min', short: '10m', seconds: 600 },
];
// How long a downstream stays quiet after its upstream recovers before paging
// if it is still down itself. Long enough for one or two checks to confirm.
const DEPENDENCY_GRACE_LEVELS = [
  { label: 'None', short: 'None', seconds: 0 },
  { label: '2 min', short: '2m', seconds: 120 },
  { label: '5 min', short: '5m', seconds: 300 },
  { label: '15 min', short: '15m', seconds: 900 },
];

// nearestIndex maps a stored numeric value back to the closest preset step.
// A value saved outside the presets (API, older UI) therefore shows as its
// nearest stop and is written back as that stop on the next save.
function nearestIndex<T>(levels: T[], get: (l: T) => number, value: number): number {
  let best = 0;
  let bestDiff = Infinity;
  levels.forEach((l, i) => {
    const diff = Math.abs(get(l) - value);
    if (diff < bestDiff) {
      bestDiff = diff;
      best = i;
    }
  });
  return best;
}

// Presets are held as indexes; the segmented control speaks strings.
function segments(levels: { label: string; short: string }[]): SegmentOption<string>[] {
  return levels.map((l, i) => ({
    value: String(i),
    label: l.short,
    title: l.short === l.label ? undefined : l.label,
  }));
}

const SENSITIVITY_OPTIONS = segments(SENSITIVITY_LEVELS);
const BASELINE_OPTIONS = segments(BASELINE_LEVELS);
const SUSTAINED_OPTIONS = segments(SUSTAINED_LEVELS);
const DEPENDENCY_GRACE_OPTIONS = segments(DEPENDENCY_GRACE_LEVELS);

// Every select in the panel (reminder here, per-channel delays in
// ChannelPicker) is input-sm at w-44, so controls line up in one size.
const SELECT_CLASS = 'input input-sm !w-44 cursor-pointer';

interface SettingRowProps {
  title: string;
  /** Fixed id for the title, when content below the row points at it. */
  titleId?: string;
  description?: ReactNode;
  /** Right-aligned beside the text while the row is wide, wrapped below it once narrow. */
  control?: (ids: { titleId: string; descriptionId?: string }) => ReactNode;
  /** Content under the row (a channel list, a toggle's parameters). */
  children?: ReactNode;
  /** Mute the text of a parameter whose toggle is off. */
  muted?: boolean;
  /** `sm` for a nested parameter row. */
  size?: 'md' | 'sm';
  /**
   * Keep the control beside the text at every width (a Switch is small
   * enough); wider controls wrap below the text in a narrow card.
   */
  inline?: boolean;
}

// One setting: title and description on the left, its control on the right.
// The text column has a 14rem basis and the row wraps, so in a narrow card
// the control drops under the description instead of squeezing it.
function SettingRow({
  title,
  titleId: fixedTitleId,
  description,
  control,
  children,
  muted = false,
  size = 'md',
  inline = false,
}: SettingRowProps) {
  const autoTitleId = useId();
  const titleId = fixedTitleId ?? autoTitleId;
  const descriptionId = useId();
  const pad = size === 'sm' ? 'py-3' : 'py-4';
  return (
    <div className={`${pad} first:pt-0 last:pb-0`}>
      <div className={`flex items-start justify-between gap-x-6 gap-y-3 ${inline ? '' : 'flex-wrap'}`}>
        <div className={`min-w-0 max-w-prose grow ${inline ? 'basis-0' : 'basis-56'} transition-opacity duration-base ${muted ? 'opacity-60' : ''}`}>
          <p id={titleId} className={`${size === 'sm' ? 'text-ui' : 'text-sm'} font-medium text-slate-200`}>
            {title}
          </p>
          {description && (
            <p id={descriptionId} className="mt-0.5 text-xs text-slate-500">
              {description}
            </p>
          )}
        </div>
        {control && (
          <div className="shrink-0">{control({ titleId, descriptionId: description ? descriptionId : undefined })}</div>
        )}
      </div>
      {children}
    </div>
  );
}

// A toggle's parameters: a well under its switch row, always shown so the
// card keeps its height and the reader sees what turning it on would tune;
// disabled (and muted) while the toggle is off.
function ParameterGroup({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div role="group" aria-label={label} className="glass-well mt-4 divide-y divide-divider px-4 py-3">
      {children}
    </div>
  );
}

interface Snapshot {
  defaultChannels: ChannelAssignment[];
  reminderSeconds: number;
  autoCreateIncident: boolean;
  anomalyEnabled: boolean;
  sensitivityIdx: number;
  baselineIdx: number;
  sustainedIdx: number;
  dependencySuppression: boolean;
  dependencyGraceIdx: number;
}

// Workspace notification policy, as four section cards in two balanced
// columns (Routing + Incidents, Latency + Dependency; stacked in that order
// when narrow). Save and Discard sit in the shared sticky FormActions bar,
// shown only while the form differs from the server. Read-only users see
// every control disabled and no bar (the parent page says why, once).
export function NotificationsPanel({ readOnly = false }: { readOnly?: boolean }) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const { showToast } = useToast();

  const [defaultChannels, setDefaultChannels] = useState<ChannelAssignment[]>([]);
  const [reminderSeconds, setReminderSeconds] = useState(0);
  const [autoCreateIncident, setAutoCreateIncident] = useState(false);
  const [anomalyEnabled, setAnomalyEnabled] = useState(false);
  // Preset positions (indexes into the level arrays). Default to Medium
  // sensitivity / 7-day baseline / 2-min sustained.
  const [sensitivityIdx, setSensitivityIdx] = useState(1);
  const [baselineIdx, setBaselineIdx] = useState(2);
  const [sustainedIdx, setSustainedIdx] = useState(1);
  const [dependencySuppression, setDependencySuppression] = useState(false);
  const [dependencyGraceIdx, setDependencyGraceIdx] = useState(1);
  // What the server holds; the save bar only appears when the form differs.
  const [saved, setSaved] = useState<Snapshot | null>(null);

  const current: Snapshot = {
    defaultChannels,
    reminderSeconds,
    autoCreateIncident,
    anomalyEnabled,
    sensitivityIdx,
    baselineIdx,
    sustainedIdx,
    dependencySuppression,
    dependencyGraceIdx,
  };
  const dirty = saved !== null && JSON.stringify(current) !== JSON.stringify(saved);

  const apply = (s: Snapshot) => {
    setDefaultChannels(s.defaultChannels);
    setReminderSeconds(s.reminderSeconds);
    setAutoCreateIncident(s.autoCreateIncident);
    setAnomalyEnabled(s.anomalyEnabled);
    setSensitivityIdx(s.sensitivityIdx);
    setBaselineIdx(s.baselineIdx);
    setSustainedIdx(s.sustainedIdx);
    setDependencySuppression(s.dependencySuppression);
    setDependencyGraceIdx(s.dependencyGraceIdx);
  };

  const loadSettings = async () => {
    try {
      setLoading(true);
      setError('');
      const s: NotificationSettings = await getNotificationSettings();
      const loaded: Snapshot = {
        defaultChannels: s.default_channels ?? [],
        reminderSeconds: s.alert_reminder_seconds ?? 0,
        autoCreateIncident: s.auto_create_incident ?? false,
        anomalyEnabled: s.latency_anomaly_enabled ?? false,
        sensitivityIdx: nearestIndex(SENSITIVITY_LEVELS, (l) => l.sigma, s.latency_anomaly_sensitivity ?? 3.5),
        baselineIdx: nearestIndex(BASELINE_LEVELS, (l) => l.hours, s.latency_baseline_window_hours ?? 168),
        sustainedIdx: nearestIndex(SUSTAINED_LEVELS, (l) => l.seconds, s.latency_anomaly_min_breach_seconds ?? 120),
        dependencySuppression: s.dependency_suppression_enabled ?? false,
        dependencyGraceIdx: nearestIndex(DEPENDENCY_GRACE_LEVELS, (l) => l.seconds, s.dependency_suppression_grace_seconds ?? 120),
      };
      apply(loaded);
      setSaved(loaded);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to load notification settings');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadSettings();
  }, []);

  const handleSave = async () => {
    try {
      setSaving(true);
      const sens = SENSITIVITY_LEVELS[sensitivityIdx];
      await updateNotificationSettings({
        default_channels: defaultChannels,
        alert_reminder_seconds: reminderSeconds,
        auto_create_incident: autoCreateIncident,
        latency_anomaly_enabled: anomalyEnabled,
        latency_baseline_window_hours: BASELINE_LEVELS[baselineIdx].hours,
        latency_anomaly_sensitivity: sens.sigma,
        latency_anomaly_min_breach_seconds: SUSTAINED_LEVELS[sustainedIdx].seconds,
        latency_anomaly_min_delta_pct: sens.deltaPct,
        dependency_suppression_enabled: dependencySuppression,
        dependency_suppression_grace_seconds: DEPENDENCY_GRACE_LEVELS[dependencyGraceIdx].seconds,
      });
      setSaved(current);
      showToast('Notification settings saved', 'success');
    } catch (err: unknown) {
      showToast(err instanceof Error ? err.message : 'Failed to save notification settings', 'error');
    } finally {
      setSaving(false);
    }
  };

  if (loading || error) {
    return (
      <Panel title="Notifications" subtitle="Where alerts go and when they fire.">
        {loading ? (
          <p role="status" className="text-sm text-slate-500">Loading notification settings…</p>
        ) : (
          <Callout
            tone="danger"
            action={<Button variant="ghost" size="xs" onClick={loadSettings}>Retry</Button>}
          >
            {error}
          </Callout>
        )}
      </Panel>
    );
  }

  const latencyOff = readOnly || !anomalyEnabled;
  const graceOff = readOnly || !dependencySuppression;

  return (
    <div>
      <div className={SETTINGS_SECTION_COLUMNS}>
        <div className="grid min-w-0 content-start gap-6">
          <Panel title="Routing">
            <div className="divide-y divide-divider">
              <SettingRow
                title="Default channels"
                titleId="default-channels-label"
                description="Every monitor notifies these channels unless it has a custom list. A delay holds a channel back until the alert has lasted that long."
              >
                {/* Full row width: the delay selects share the right edge
                    with the reminder select below, at every card width. */}
                <div className="mt-3">
                  <ChannelPicker
                    value={defaultChannels}
                    onChange={setDefaultChannels}
                    showDelays
                    allowCreate={false}
                    disabled={readOnly}
                    ariaLabelledBy="default-channels-label"
                    emptyState={
                      <>
                        No channels yet. Add one under Channels below, or{' '}
                        <Link
                          href="/alert-channels/catalog"
                          className="font-medium text-cyan-300 transition-colors hover:text-cyan-200"
                        >
                          browse the catalog
                        </Link>
                        .
                      </>
                    }
                  />
                </div>
              </SettingRow>

              <SettingRow
                title="Alert reminder"
                description="Re-notify on still-open alerts at this interval."
                control={({ titleId, descriptionId }) => (
                  <select
                    value={reminderSeconds}
                    onChange={(e) => setReminderSeconds(Number(e.target.value))}
                    disabled={readOnly}
                    aria-labelledby={titleId}
                    aria-describedby={descriptionId}
                    className={SELECT_CLASS}
                  >
                    {REMINDER_OPTIONS.map((opt) => (
                      <option key={opt.value} value={opt.value}>
                        {opt.label}
                      </option>
                    ))}
                  </select>
                )}
              />
            </div>
          </Panel>

          <Panel title="Incidents">
            <SettingRow
              inline
              title="Open an incident when a monitor goes down"
              control={() => (
                <Switch
                  checked={autoCreateIncident}
                  onChange={setAutoCreateIncident}
                  label="Open an incident when a monitor goes down"
                  disabled={readOnly}
                />
              )}
            />
          </Panel>
        </div>

        <div className="grid min-w-0 content-start gap-6">
          <Panel title="Latency degradation">
            <SettingRow
              inline
              title="Detect latency degradation"
              description="Alert when a monitor's response time rises well above its own baseline, before it goes hard down."
              control={() => (
                <Switch
                  checked={anomalyEnabled}
                  onChange={setAnomalyEnabled}
                  label="Detect latency degradation"
                  disabled={readOnly}
                />
              )}
            >
              <ParameterGroup label="Latency degradation parameters">
                <SettingRow
                  size="sm"
                  muted={latencyOff}
                  title="Sensitivity"
                  description={SENSITIVITY_LEVELS[sensitivityIdx].hint}
                  control={({ titleId, descriptionId }) => (
                    <SegmentedControl
                      options={SENSITIVITY_OPTIONS}
                      value={String(sensitivityIdx)}
                      onChange={(v) => setSensitivityIdx(Number(v))}
                      labelledBy={titleId}
                      describedBy={descriptionId}
                      disabled={latencyOff}
                    />
                  )}
                />
                <SettingRow
                  size="sm"
                  muted={latencyOff}
                  title="Baseline window"
                  description="How much history to compare recent latency against."
                  control={({ titleId, descriptionId }) => (
                    <SegmentedControl
                      options={BASELINE_OPTIONS}
                      value={String(baselineIdx)}
                      onChange={(v) => setBaselineIdx(Number(v))}
                      labelledBy={titleId}
                      describedBy={descriptionId}
                      disabled={latencyOff}
                    />
                  )}
                />
                <SettingRow
                  size="sm"
                  muted={latencyOff}
                  title="Must stay slow for"
                  description="Ignore brief spikes shorter than this."
                  control={({ titleId, descriptionId }) => (
                    <SegmentedControl
                      options={SUSTAINED_OPTIONS}
                      value={String(sustainedIdx)}
                      onChange={(v) => setSustainedIdx(Number(v))}
                      labelledBy={titleId}
                      describedBy={descriptionId}
                      disabled={latencyOff}
                    />
                  )}
                />
              </ParameterGroup>
            </SettingRow>
          </Panel>

          <Panel title="Dependency suppression">
            <SettingRow
              inline
              title="Page root causes only"
              description="When a monitor goes down because an upstream dependency is down, open its alert but send no notification; the root cause's notification lists everything it affects. Monitors can override this in their Alerting section."
              control={() => (
                <Switch
                  checked={dependencySuppression}
                  onChange={setDependencySuppression}
                  label="Page root causes only"
                  disabled={readOnly}
                />
              )}
            >
              <ParameterGroup label="Dependency suppression parameters">
                <SettingRow
                  size="sm"
                  muted={graceOff}
                  title="After the upstream recovers, hold for"
                  description="A downstream still down after this pages as a normal outage."
                  control={({ titleId, descriptionId }) => (
                    <SegmentedControl
                      options={DEPENDENCY_GRACE_OPTIONS}
                      value={String(dependencyGraceIdx)}
                      onChange={(v) => setDependencyGraceIdx(Number(v))}
                      labelledBy={titleId}
                      describedBy={descriptionId}
                      disabled={graceOff}
                    />
                  )}
                />
              </ParameterGroup>
            </SettingRow>
          </Panel>
        </div>
      </div>

      {!readOnly && dirty && (
        <FormActions
          sticky
          middle="Unsaved changes"
          cancel={{ label: 'Discard', onClick: () => saved && apply(saved), disabled: saving }}
          submit={{
            label: saving ? 'Saving…' : 'Save changes',
            type: 'button',
            onClick: handleSave,
            disabled: saving,
            loading: saving,
          }}
        />
      )}
    </div>
  );
}

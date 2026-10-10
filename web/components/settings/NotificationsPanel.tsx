'use client';

import { useEffect, useState } from 'react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Select from '@/components/ui/Select';
import Switch from '@/components/ui/Switch';
import { useToast } from '@/components/ui/ToastProvider';
import ChannelPicker from '@/components/channels/ChannelPicker';
import { getNotificationSettings, updateNotificationSettings } from '@/lib/api';
import type { ChannelAssignment, NotificationSettings } from '@/lib/types';

const REMINDER_OPTIONS = [
  { label: 'Off', value: 0 },
  { label: 'Every 30 min', value: 1800 },
  { label: 'Every hour', value: 3600 },
  { label: 'Every 4 hours', value: 14400 },
];

// Latency anomaly presets — users pick a level, not a raw number. Each
// sensitivity level bundles the statistical sigma + the minimum increase.
const SENSITIVITY_LEVELS = [
  { label: 'Low', sigma: 4.5, deltaPct: 30, hint: 'Only flags large, obvious slowdowns.' },
  { label: 'Medium', sigma: 3.5, deltaPct: 20, hint: 'Balanced — recommended for most monitors.' },
  { label: 'High', sigma: 2.5, deltaPct: 10, hint: 'Flags subtle drift; can be noisier.' },
];
const BASELINE_LEVELS = [
  { label: '1 day', hours: 24 },
  { label: '3 days', hours: 72 },
  { label: '7 days', hours: 168 },
  { label: '14 days', hours: 336 },
];
const SUSTAINED_LEVELS = [
  { label: '1 min', seconds: 60 },
  { label: '2 min', seconds: 120 },
  { label: '5 min', seconds: 300 },
  { label: '10 min', seconds: 600 },
];
// How long a downstream stays quiet after its upstream recovers before paging
// if it is still down itself. Long enough for one or two checks to confirm.
const DEPENDENCY_GRACE_LEVELS = [
  { label: 'None', seconds: 0 },
  { label: '2 min', seconds: 120 },
  { label: '5 min', seconds: 300 },
  { label: '15 min', seconds: 900 },
];

// nearestIndex maps a stored numeric value back to the closest preset step.
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

interface SteppedSliderProps {
  label: string;
  options: { label: string }[];
  index: number;
  onChange: (i: number) => void;
  hint?: string;
  disabled?: boolean;
}

// SteppedSlider is a discrete slider over predefined options, with the current
// option named and clickable tick labels beneath the track.
function SteppedSlider({ label, options, index, onChange, hint, disabled = false }: SteppedSliderProps) {
  return (
    <div>
      <div className="mb-1.5 flex items-baseline justify-between">
        <span className="text-ui font-medium text-slate-300">{label}</span>
        <span className="text-ui font-medium text-white">{options[index].label}</span>
      </div>
      <input
        type="range"
        min={0}
        max={options.length - 1}
        step={1}
        value={index}
        aria-label={label}
        aria-valuetext={options[index].label}
        disabled={disabled}
        onChange={(e) => onChange(Number(e.target.value))}
        className="w-full cursor-pointer accent-cyan-500 disabled:cursor-not-allowed disabled:opacity-50"
      />
      <div className="mt-1 flex justify-between">
        {options.map((o, i) => (
          <button
            type="button"
            key={o.label}
            onClick={() => onChange(i)}
            disabled={disabled}
            tabIndex={-1}
            className={`cursor-pointer text-2xs transition-colors disabled:cursor-not-allowed ${
              i === index ? 'font-medium text-slate-200' : 'text-slate-500 hover:text-slate-300'
            }`}
          >
            {o.label}
          </button>
        ))}
      </div>
      {hint && <p className="mt-1 text-xs text-slate-500">{hint}</p>}
    </div>
  );
}

interface SettingSwitchProps {
  title: string;
  description?: string;
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean;
}

// An on/off setting row: what it does on the left, the switch on the right.
function SettingSwitch({ title, description, checked, onChange, disabled }: SettingSwitchProps) {
  return (
    <div className="flex items-start justify-between gap-6">
      <div className="min-w-0">
        <p className="text-sm font-medium text-slate-200">{title}</p>
        {description && <p className="mt-0.5 max-w-2xl text-xs text-slate-500">{description}</p>}
      </div>
      <div className="pt-0.5">
        <Switch checked={checked} onChange={onChange} label={title} disabled={disabled} />
      </div>
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

// Workspace notification policy. Read-only users see it with every control
// disabled and no Save button (the parent page says why, once).
export function NotificationsPanel({ readOnly = false }: { readOnly?: boolean }) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const { showToast } = useToast();

  const [defaultChannels, setDefaultChannels] = useState<ChannelAssignment[]>([]);
  const [reminderSeconds, setReminderSeconds] = useState(0);
  const [autoCreateIncident, setAutoCreateIncident] = useState(false);
  const [anomalyEnabled, setAnomalyEnabled] = useState(false);
  // Slider positions (indexes into the preset level arrays). Default to Medium
  // sensitivity / 7-day baseline / 2-min sustained.
  const [sensitivityIdx, setSensitivityIdx] = useState(1);
  const [baselineIdx, setBaselineIdx] = useState(2);
  const [sustainedIdx, setSustainedIdx] = useState(1);
  const [dependencySuppression, setDependencySuppression] = useState(false);
  const [dependencyGraceIdx, setDependencyGraceIdx] = useState(1);
  // What the server holds; Save only lights up when the form differs from it.
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
      setDefaultChannels(loaded.defaultChannels);
      setReminderSeconds(loaded.reminderSeconds);
      setAutoCreateIncident(loaded.autoCreateIncident);
      setAnomalyEnabled(loaded.anomalyEnabled);
      setSensitivityIdx(loaded.sensitivityIdx);
      setBaselineIdx(loaded.baselineIdx);
      setSustainedIdx(loaded.sustainedIdx);
      setDependencySuppression(loaded.dependencySuppression);
      setDependencyGraceIdx(loaded.dependencyGraceIdx);
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

  return (
    <Panel
      title="Notifications"
      subtitle="Every monitor notifies these channels unless it has a custom list."
    >
      {loading ? (
        <p className="text-sm text-slate-500">Loading notification settings…</p>
      ) : error ? (
        <Callout
          tone="danger"
          action={<Button variant="ghost" size="xs" onClick={loadSettings}>Retry</Button>}
        >
          {error}
        </Callout>
      ) : (
        <div className="space-y-6">
          <div>
            <p id="default-channels-label" className="mb-1.5 block text-ui font-medium text-slate-300">
              Default notification channels
            </p>
            <ChannelPicker
              value={defaultChannels}
              onChange={setDefaultChannels}
              showDelays
              disabled={readOnly}
              ariaLabelledBy="default-channels-label"
            />
          </div>

          <div className="sm:w-60">
            <Select
              label="Alert reminder"
              value={reminderSeconds}
              onChange={(e) => setReminderSeconds(Number(e.target.value))}
              disabled={readOnly}
              className="h-10 cursor-pointer"
            >
              {REMINDER_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </Select>
            <p className="mt-1 text-xs text-slate-500">Re-notify on still-open alerts at this interval.</p>
          </div>

          <div className="space-y-5 border-t border-divider pt-5">
            <SettingSwitch
              title="Open an incident when a monitor goes down"
              checked={autoCreateIncident}
              onChange={setAutoCreateIncident}
              disabled={readOnly}
            />

            <div className="space-y-4">
              <SettingSwitch
                title="Detect latency degradation"
                description="Alert when a monitor's response time rises well above its own baseline, before it goes hard down."
                checked={anomalyEnabled}
                onChange={setAnomalyEnabled}
                disabled={readOnly}
              />

              {anomalyEnabled && (
                <div className="glass-well space-y-4 p-4">
                  <SteppedSlider
                    label="Sensitivity"
                    options={SENSITIVITY_LEVELS}
                    index={sensitivityIdx}
                    onChange={setSensitivityIdx}
                    hint={SENSITIVITY_LEVELS[sensitivityIdx].hint}
                    disabled={readOnly}
                  />
                  <SteppedSlider
                    label="Baseline window"
                    options={BASELINE_LEVELS}
                    index={baselineIdx}
                    onChange={setBaselineIdx}
                    hint="How much history to compare recent latency against."
                    disabled={readOnly}
                  />
                  <SteppedSlider
                    label="Must stay slow for"
                    options={SUSTAINED_LEVELS}
                    index={sustainedIdx}
                    onChange={setSustainedIdx}
                    hint="Ignore brief spikes shorter than this."
                    disabled={readOnly}
                  />
                </div>
              )}
            </div>

            <div className="space-y-4">
              <SettingSwitch
                title="Page root causes only"
                description="When a monitor goes down because an upstream dependency is down, open its alert but send no notification; the root cause's notification lists everything it affects. Monitors can override this in their Alerting section."
                checked={dependencySuppression}
                onChange={setDependencySuppression}
                disabled={readOnly}
              />

              {dependencySuppression && (
                <div className="glass-well p-4">
                  <SteppedSlider
                    label="After the upstream recovers, hold for"
                    options={DEPENDENCY_GRACE_LEVELS}
                    index={dependencyGraceIdx}
                    onChange={setDependencyGraceIdx}
                    hint="A downstream still down after this pages as a normal outage."
                    disabled={readOnly}
                  />
                </div>
              )}
            </div>
          </div>

          {!readOnly && (
            <div className="flex items-center justify-end gap-3">
              {dirty && <span className="text-xs text-slate-400">Unsaved changes</span>}
              <Button
                variant={dirty ? 'accent' : 'ghost'}
                size="sm"
                onClick={handleSave}
                disabled={saving || !dirty}
                loading={saving}
              >
                {saving ? 'Saving…' : 'Save notifications'}
              </Button>
            </div>
          )}
        </div>
      )}
    </Panel>
  );
}

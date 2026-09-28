'use client';

import { useEffect, useState } from 'react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import { useToast } from '@/components/ui/ToastProvider';
import ChannelPicker from '@/components/channels/ChannelPicker';
import { getNotificationSettings, updateNotificationSettings } from '@/lib/api';
import type { ChannelAssignment, NotificationSettings } from '@/lib/types';

const REMINDER_OPTIONS = [
  { label: 'off', value: 0 },
  { label: 'every 30 min', value: 1800 },
  { label: 'every hour', value: 3600 },
  { label: 'every 4 hours', value: 14400 },
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
  { label: 'none', seconds: 0 },
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
}

// SteppedSlider is a discrete slider over predefined options, with the current
// option named and clickable tick labels beneath the track.
function SteppedSlider({ label, options, index, onChange, hint }: SteppedSliderProps) {
  return (
    <div>
      <div className="mb-1.5 flex items-baseline justify-between">
        <label className="text-[13px] font-medium text-slate-300">{label}</label>
        <span className="text-[13px] font-medium text-white">{options[index].label}</span>
      </div>
      <input
        type="range"
        min={0}
        max={options.length - 1}
        step={1}
        value={index}
        aria-label={label}
        onChange={(e) => onChange(Number(e.target.value))}
        className="w-full cursor-pointer accent-cyan-500"
      />
      <div className="mt-1 flex justify-between">
        {options.map((o, i) => (
          <button
            type="button"
            key={o.label}
            onClick={() => onChange(i)}
            className={`cursor-pointer text-[11px] transition-colors ${
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

const CHECKBOX_CLASS = 'mt-0.5 h-4 w-4 flex-shrink-0 cursor-pointer accent-cyan-500';

export function NotificationsPanel() {
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

  const loadSettings = async () => {
    try {
      setLoading(true);
      setError('');
      const s: NotificationSettings = await getNotificationSettings();
      setDefaultChannels(s.default_channels ?? []);
      setReminderSeconds(s.alert_reminder_seconds ?? 0);
      setAutoCreateIncident(s.auto_create_incident ?? false);
      setAnomalyEnabled(s.latency_anomaly_enabled ?? false);
      setSensitivityIdx(nearestIndex(SENSITIVITY_LEVELS, (l) => l.sigma, s.latency_anomaly_sensitivity ?? 3.5));
      setBaselineIdx(nearestIndex(BASELINE_LEVELS, (l) => l.hours, s.latency_baseline_window_hours ?? 168));
      setSustainedIdx(nearestIndex(SUSTAINED_LEVELS, (l) => l.seconds, s.latency_anomaly_min_breach_seconds ?? 120));
      setDependencySuppression(s.dependency_suppression_enabled ?? false);
      setDependencyGraceIdx(nearestIndex(DEPENDENCY_GRACE_LEVELS, (l) => l.seconds, s.dependency_suppression_grace_seconds ?? 120));
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
            <label className="mb-1.5 block text-[13px] font-medium text-slate-300">
              Default notification channels
            </label>
            <ChannelPicker
              value={defaultChannels}
              onChange={setDefaultChannels}
              showDelays
            />
          </div>

          <div>
            <label
              htmlFor="reminder-select"
              className="mb-1.5 block text-[13px] font-medium text-slate-300"
            >
              Alert reminder
            </label>
            <select
              id="reminder-select"
              value={reminderSeconds}
              onChange={(e) => setReminderSeconds(Number(e.target.value))}
              className="input h-10 cursor-pointer sm:!w-60"
            >
              {REMINDER_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
            <p className="mt-1 text-xs text-slate-500">
              Re-notify on still-open alerts at this interval.
            </p>
          </div>

          <div className="flex items-start gap-3">
            <input
              type="checkbox"
              id="auto-incident"
              checked={autoCreateIncident}
              onChange={(e) => setAutoCreateIncident(e.target.checked)}
              className={CHECKBOX_CLASS}
            />
            <label htmlFor="auto-incident" className="cursor-pointer text-sm text-slate-200">
              Open an incident when a monitor goes down
            </label>
          </div>

          <div className="space-y-4">
            <div className="flex items-start gap-3">
              <input
                type="checkbox"
                id="anomaly-enabled"
                checked={anomalyEnabled}
                onChange={(e) => setAnomalyEnabled(e.target.checked)}
                className={CHECKBOX_CLASS}
              />
              <label htmlFor="anomaly-enabled" className="cursor-pointer text-sm text-slate-200">
                Detect latency degradation
                <span className="mt-0.5 block max-w-2xl text-xs text-slate-500">
                  Alert when a monitor&apos;s response time rises well above its own baseline — before it goes hard down.
                </span>
              </label>
            </div>

            {anomalyEnabled && (
              <div className="space-y-4 pl-7">
                <SteppedSlider
                  label="Sensitivity"
                  options={SENSITIVITY_LEVELS}
                  index={sensitivityIdx}
                  onChange={setSensitivityIdx}
                  hint={SENSITIVITY_LEVELS[sensitivityIdx].hint}
                />
                <SteppedSlider
                  label="Baseline window"
                  options={BASELINE_LEVELS}
                  index={baselineIdx}
                  onChange={setBaselineIdx}
                  hint="How much history to compare recent latency against."
                />
                <SteppedSlider
                  label="Must stay slow for"
                  options={SUSTAINED_LEVELS}
                  index={sustainedIdx}
                  onChange={setSustainedIdx}
                  hint="Ignore brief spikes shorter than this."
                />
              </div>
            )}
          </div>

          <div className="space-y-4">
            <div className="flex items-start gap-3">
              <input
                type="checkbox"
                id="dependency-suppression"
                checked={dependencySuppression}
                onChange={(e) => setDependencySuppression(e.target.checked)}
                className={CHECKBOX_CLASS}
              />
              <label htmlFor="dependency-suppression" className="cursor-pointer text-sm text-slate-200">
                Page root causes only
                <span className="mt-0.5 block max-w-2xl text-xs text-slate-500">
                  When a monitor goes down because an upstream dependency is down, open its alert but send no
                  notification — the root cause&apos;s notification lists everything it affects. Monitors can
                  override this in their Alerting section.
                </span>
              </label>
            </div>

            {dependencySuppression && (
              <div className="pl-7">
                <SteppedSlider
                  label="After the upstream recovers, hold for"
                  options={DEPENDENCY_GRACE_LEVELS}
                  index={dependencyGraceIdx}
                  onChange={setDependencyGraceIdx}
                  hint="A downstream still down after this pages as a normal outage."
                />
              </div>
            )}
          </div>

          <div className="flex justify-end">
            <Button
              variant="accent"
              size="sm"
              onClick={handleSave}
              disabled={saving}
              loading={saving}
            >
              {saving ? 'Saving…' : 'Save notifications'}
            </Button>
          </div>
        </div>
      )}
    </Panel>
  );
}

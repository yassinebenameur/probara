'use client';

import { useEffect, useState } from 'react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Toast from '@/components/ui/Toast';
import ChannelPicker from '@/components/channels/ChannelPicker';
import { getNotificationSettings, updateNotificationSettings } from '@/lib/api';
import type { ChannelAssignment, NotificationSettings } from '@/lib/types';

type ToastState = { message: string; type: 'success' | 'error' } | null;

const REMINDER_OPTIONS = [
  { label: 'off', value: 0 },
  { label: 'every 30 min', value: 1800 },
  { label: 'every hour', value: 3600 },
  { label: 'every 4 hours', value: 14400 },
];

export function NotificationsPanel() {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [toast, setToast] = useState<ToastState>(null);

  const [defaultChannels, setDefaultChannels] = useState<ChannelAssignment[]>([]);
  const [reminderSeconds, setReminderSeconds] = useState(0);
  const [autoCreateIncident, setAutoCreateIncident] = useState(false);

  const loadSettings = async () => {
    try {
      setLoading(true);
      setError('');
      const s: NotificationSettings = await getNotificationSettings();
      setDefaultChannels(s.default_channels ?? []);
      setReminderSeconds(s.alert_reminder_seconds ?? 0);
      setAutoCreateIncident(s.auto_create_incident ?? false);
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
      await updateNotificationSettings({
        default_channels: defaultChannels,
        alert_reminder_seconds: reminderSeconds,
        auto_create_incident: autoCreateIncident,
      });
      setToast({ message: 'Notification settings saved', type: 'success' });
    } catch (err: unknown) {
      setToast({
        message: err instanceof Error ? err.message : 'Failed to save notification settings',
        type: 'error',
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <Panel
        title="Notifications"
        subtitle="Every monitor notifies these channels unless it has a custom list."
      >
        {loading ? (
          <div className="text-sm text-slate-500">Loading notification settings…</div>
        ) : error ? (
          <div className="space-y-3">
            <p className="text-sm text-rose-400">{error}</p>
            <Button variant="ghost" size="sm" onClick={loadSettings}>Retry</Button>
          </div>
        ) : (
          <div className="space-y-5">
            {/* Default channels */}
            <div>
              <label className="mb-2 block text-xs font-medium text-slate-400">
                Default notification channels
              </label>
              <ChannelPicker
                value={defaultChannels}
                onChange={setDefaultChannels}
                showDelays
              />
            </div>

            {/* Reminder interval */}
            <div>
              <label
                htmlFor="reminder-select"
                className="mb-1.5 block text-xs font-medium text-slate-400"
              >
                Alert reminder
              </label>
              <select
                id="reminder-select"
                value={reminderSeconds}
                onChange={(e) => setReminderSeconds(Number(e.target.value))}
                className="rounded border border-white/[0.08] bg-slate-800 px-3 py-2 text-xs text-slate-300 focus:border-cyan-500/50 focus:outline-none cursor-pointer"
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

            {/* Auto-create incident */}
            <div className="flex items-start gap-3">
              <input
                type="checkbox"
                id="auto-incident"
                checked={autoCreateIncident}
                onChange={(e) => setAutoCreateIncident(e.target.checked)}
                className="mt-0.5 h-3.5 w-3.5 rounded border-slate-600 bg-slate-800 text-cyan-500 accent-cyan-500 cursor-pointer flex-shrink-0"
              />
              <label htmlFor="auto-incident" className="text-sm text-slate-300 cursor-pointer">
                Open an incident when a monitor goes down
              </label>
            </div>

            {/* Save */}
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

      {toast && (
        <Toast message={toast.message} type={toast.type} onClose={() => setToast(null)} />
      )}
    </>
  );
}

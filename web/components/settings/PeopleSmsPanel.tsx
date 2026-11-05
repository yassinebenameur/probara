'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import Panel from '@/components/ui/Panel';
import Select from '@/components/ui/Select';
import Callout from '@/components/ui/Callout';
import FormField from '@/components/ui/FormField';
import { useToast } from '@/components/ui/ToastProvider';
import { getPeopleSettings, updatePeopleSettings } from '@/lib/api';
import type { PeopleSettings } from '@/lib/contacts';

/**
 * The workspace's SMS channel for people: the Twilio channel that texts
 * members directly (verification codes for SMS contacts and their tests).
 * Tenant admins choose it; without one, members cannot verify SMS contacts.
 */
export function PeopleSmsPanel({ readOnly }: { readOnly: boolean }) {
  const { showToast } = useToast();
  const [settings, setSettings] = useState<PeopleSettings | null>(null);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    getPeopleSettings()
      .then(setSettings)
      .catch((err: any) => setError(err.message || 'Failed to load people settings'));
  }, []);

  const change = async (value: string) => {
    setSaving(true);
    try {
      setSettings(await updatePeopleSettings({ personal_sms_channel_id: value || null }));
      showToast(value ? 'SMS channel for people set' : 'SMS channel for people cleared', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to save', 'error');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Panel
      title="SMS for people"
      subtitle="The SMS channel that texts members directly: codes that verify their SMS contacts, and their test messages. Its own recipient list is not used for these."
    >
      {error ? (
        <Callout tone="danger">{error}</Callout>
      ) : !settings ? (
        <p className="text-sm text-slate-500">Loading…</p>
      ) : settings.sms_channel_options.length === 0 ? (
        <p className="text-sm text-slate-400">
          No channel can text a single person yet. Add an{' '}
          <Link href="/alert-channels/new" className="text-cyan-300 hover:underline">SMS (Twilio) channel</Link>{' '}
          first.
        </p>
      ) : (
        <FormField label="Channel" description="Members verify SMS contacts through it in this workspace.">
          <Select
            value={settings.personal_sms_channel_id ?? ''}
            disabled={readOnly || saving}
            onChange={(e) => change(e.target.value)}
          >
            <option value="">None (SMS contacts cannot be verified)</option>
            {settings.sms_channel_options.map((o) => (
              <option key={o.id} value={o.id}>
                {o.name}{o.is_active ? '' : ' (paused)'}
              </option>
            ))}
          </Select>
        </FormField>
      )}
    </Panel>
  );
}

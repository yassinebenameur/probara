'use client';

import { useEffect, useState } from 'react';
import { useToast } from '@/components/ui/ToastProvider';
import { getTenantSettings, updateTenantSettings } from '@/lib/api';
import { errorMessage } from '@/lib/errors';

export const RETENTION_PRESETS: Array<{ label: string; value: string }> = [
  { label: 'Keep forever', value: '' },
  { label: '30 days', value: '30' },
  { label: '90 days', value: '90' },
  { label: '365 days', value: '365' },
];

/**
 * The tenant's data retention (and the dashboard group tags the same
 * settings call returns), with the retention form's draft. Held by the
 * settings page so an unsaved draft survives a tab switch.
 */
export function useRetentionSettings() {
  const { showToast } = useToast();
  const [retentionLoading, setRetentionLoading] = useState(true);
  const [retentionError, setRetentionError] = useState('');
  const [savingRetention, setSavingRetention] = useState(false);
  const [retentionDays, setRetentionDays] = useState(0);
  const [retentionInput, setRetentionInput] = useState('');
  const [customRetention, setCustomRetention] = useState(false);
  const [groupTags, setGroupTags] = useState<string[]>([]);

  const loadRetentionSettings = async () => {
    try {
      setRetentionLoading(true);
      setRetentionError('');
      const settings = await getTenantSettings();
      const value = settings.data_retention_days || 0;
      setRetentionDays(value);
      setRetentionInput(value === 0 ? '' : String(value));
      setCustomRetention(!RETENTION_PRESETS.some((preset) => preset.value === (value === 0 ? '' : String(value))));
      setGroupTags(settings.dashboard_group_tags || []);
    } catch (err) {
      setRetentionError(errorMessage(err, 'Failed to load data retention settings'));
    } finally {
      setRetentionLoading(false);
    }
  };

  useEffect(() => {
    loadRetentionSettings();
  }, []);

  const handleSaveRetention = async () => {
    const trimmed = retentionInput.trim();
    // Only the Keep forever chip produces 0; an empty Custom… field is invalid.
    if (customRetention && trimmed === '') {
      showToast('Enter a retention between 30 and 3650 days', 'error');
      return;
    }
    const nextValue = trimmed === '' ? 0 : Number(trimmed);

    if (!Number.isInteger(nextValue)) {
      showToast('Retention must be an integer number of days', 'error');
      return;
    }
    if (nextValue !== 0 && (nextValue < 30 || nextValue > 3650)) {
      showToast('Retention must be empty (keep forever) or between 30 and 3650 days', 'error');
      return;
    }

    try {
      setSavingRetention(true);
      const updated = await updateTenantSettings({ data_retention_days: nextValue });
      const saved = updated.data_retention_days || 0;
      setRetentionDays(saved);
      setRetentionInput(saved === 0 ? '' : String(saved));
      setCustomRetention(!RETENTION_PRESETS.some((preset) => preset.value === (saved === 0 ? '' : String(saved))));
      showToast(saved === 0 ? 'Mesh and host-metric history kept forever' : `Retention set to ${saved} days`, 'success');
    } catch (err) {
      showToast(errorMessage(err, 'Failed to update retention'), 'error');
    } finally {
      setSavingRetention(false);
    }
  };

  const storedRetention = retentionDays === 0 ? '' : String(retentionDays);
  const retentionDirty = retentionInput.trim() !== storedRetention;
  const customRetentionDays = Number(retentionInput.trim());
  const customRetentionInvalid =
    customRetention &&
    (!/^\d+$/.test(retentionInput.trim()) || customRetentionDays < 30 || customRetentionDays > 3650);

  return {
    retentionLoading,
    retentionError,
    savingRetention,
    retentionInput,
    setRetentionInput,
    customRetention,
    setCustomRetention,
    groupTags,
    setGroupTags,
    loadRetentionSettings,
    handleSaveRetention,
    retentionDirty,
    customRetentionInvalid,
  };
}

export type RetentionSettingsState = ReturnType<typeof useRetentionSettings>;

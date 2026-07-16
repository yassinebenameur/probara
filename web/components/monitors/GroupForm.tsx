'use client';

import { useState, useEffect } from 'react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, GroupMonitorConfig, NotificationMode, MemberAlertRollup, ChannelAssignment } from '@/lib/types';
import { getMonitors } from '@/lib/api';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import { AlertingSection } from './AlertingSection';

interface GroupFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function GroupForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: GroupFormProps) {
  const [availableMonitors, setAvailableMonitors] = useState<Monitor[]>([]);
  const isEditMode = Boolean(monitor);
  const initialGroupConfig =
    !isEditMode && initialData?.type === 'group' ? (initialData.config as GroupMonitorConfig) : undefined;
  const existingMemberIds = monitor?.member_ids || initialGroupConfig?.monitor_ids || [];

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    monitor_ids: existingMemberIds,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
    member_alert_rollup: (monitor?.member_alert_rollup ?? 'per_monitor') as MemberAlertRollup,
    notification_channels: monitor?.notification_channels ?? [] as ChannelAssignment[],
  });

  const [errors, setErrors] = useState<Record<string, string>>({});
  const [initializedMonitorId, setInitializedMonitorId] = useState<string | null>(null);

  useEffect(() => {
    loadAvailableMonitors();
  }, []);

  useEffect(() => {
    if (monitor && monitor.id !== initializedMonitorId) {
      setFormData({
        name: monitor.name || '',
        monitor_ids: monitor.member_ids || [],
        enabled: monitor.enabled ?? true,
        tags: monitor.tags?.join(', ') || '',
        notification_mode: (monitor.notification_mode ?? 'default') as NotificationMode,
        member_alert_rollup: (monitor.member_alert_rollup ?? 'per_monitor') as MemberAlertRollup,
        notification_channels: monitor.notification_channels ?? [],
      });
      setInitializedMonitorId(monitor.id);
    } else if (!monitor && initializedMonitorId !== null) {
      setFormData({
        name: '',
        monitor_ids: [],
        enabled: true,
        tags: '',
        notification_mode: 'default',
        member_alert_rollup: 'per_monitor',
        notification_channels: [],
      });
      setInitializedMonitorId(null);
    }
  }, [monitor?.id]);

  const loadAvailableMonitors = async () => {
    try {
      const response = await getMonitors({ page_size: 100 });
      const selectableMonitors = (response?.items || []).filter((m) => m.id !== monitor?.id);
      setAvailableMonitors(selectableMonitors);
    } catch (error) {
      console.error('Failed to load monitors:', error);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (formData.monitor_ids.length === 0) newErrors.monitor_ids = 'Select at least one monitor';

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    const config: GroupMonitorConfig = { monitor_ids: formData.monitor_ids };

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: 'group',
      config,
      interval_seconds: 60,
      timeout_seconds: 30,
      enabled: formData.enabled,
    };

    requestData.notification_mode = formData.notification_mode;
    requestData.member_alert_rollup = formData.member_alert_rollup;
    requestData.notification_channels = formData.notification_mode === 'custom' ? formData.notification_channels : [];
    if (formData.tags.trim()) {
      requestData.tags = formData.tags.split(',').map(t => t.trim()).filter(t => t);
    }

    await onSubmit(requestData);
  };

  const toggleMonitor = (monitorId: string) => {
    const newSelectedIds = formData.monitor_ids.includes(monitorId)
      ? formData.monitor_ids.filter(id => id !== monitorId)
      : [...formData.monitor_ids, monitorId];
    setFormData({ ...formData, monitor_ids: newSelectedIds });
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-5">
      <FormSection title="Group">
        <FormField label="Group name" required error={errors.name}>
          <input
            type="text"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
            placeholder="Production services"
            className="input"
          />
        </FormField>

        <FormField label="Tags" description="Comma-separated, e.g. production, critical">
          <input
            type="text"
            value={formData.tags}
            onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
            placeholder="production, critical"
            className="input"
          />
        </FormField>
      </FormSection>

      <FormSection
        title="Members"
        summary={`${formData.monitor_ids.length} selected`}
      >
        <div className="flex items-center justify-between">
          <span className="text-xs text-slate-400">
            {formData.monitor_ids.length} of {availableMonitors.length} selected
          </span>
          <div className="flex gap-1.5">
            <Button
              variant="ghost"
              size="xs"
              type="button"
              onClick={() => setFormData({ ...formData, monitor_ids: availableMonitors.map(m => m.id) })}
            >
              Select all
            </Button>
            <Button
              variant="ghost"
              size="xs"
              type="button"
              onClick={() => setFormData({ ...formData, monitor_ids: [] })}
            >
              Clear
            </Button>
          </div>
        </div>

        <div className="max-h-56 overflow-y-auto rounded-lg border border-white/[0.06] bg-slate-900/40 p-2">
          {availableMonitors.length === 0 ? (
            <p className="py-6 text-center text-sm text-slate-500">No monitors available</p>
          ) : (
            <div className="space-y-1">
              {availableMonitors.map((mon) => {
                const isSelected = formData.monitor_ids.includes(mon.id);
                return (
                  <label
                    key={mon.id}
                    className={`flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2 transition-colors ${
                      isSelected
                        ? 'border border-cyan-500/30 bg-cyan-500/10'
                        : 'border border-transparent hover:bg-white/[0.03]'
                    }`}
                  >
                    <input
                      type="checkbox"
                      checked={isSelected}
                      onChange={() => toggleMonitor(mon.id)}
                      className="sr-only"
                    />
                    <div className={`flex h-4 w-4 items-center justify-center rounded border transition-colors ${
                      isSelected ? 'border-cyan-500 bg-cyan-500' : 'border-slate-600'
                    }`}>
                      {isSelected && (
                        <svg className="h-3 w-3 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M5 13l4 4L19 7" />
                        </svg>
                      )}
                    </div>
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium text-white">{mon.name}</p>
                      <p className="text-xs text-slate-500">{mon.type.toUpperCase()}</p>
                    </div>
                    <Pill tone={mon.enabled ? 'success' : 'neutral'} size="xs" dot>
                      {mon.enabled ? 'Active' : 'Paused'}
                    </Pill>
                  </label>
                );
              })}
            </div>
          )}
        </div>
        {errors.monitor_ids && <p className="text-xs text-rose-400">{errors.monitor_ids}</p>}
      </FormSection>

      <FormSection title="Alerting">
        <AlertingSection
          isGroup={true}
          intervalSeconds={60}
          threshold={2}
          onThresholdChange={() => {}}
          mode={formData.notification_mode}
          onModeChange={(m) => setFormData({ ...formData, notification_mode: m })}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData({ ...formData, notification_channels: next })}
          rollup={formData.member_alert_rollup}
          onRollupChange={(r) => setFormData({ ...formData, member_alert_rollup: r })}
        />
      </FormSection>

      <FormSection title="Status">
        <div className="flex items-center justify-between rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
          <div>
            <p className="text-sm font-medium text-white">Group enabled</p>
            <p className="text-xs text-slate-500">Track status of this group</p>
          </div>
          <button
            type="button"
            onClick={() => setFormData({ ...formData, enabled: !formData.enabled })}
            className={`relative h-5 w-9 rounded-full transition-colors ${
              formData.enabled ? 'bg-cyan-500' : 'bg-slate-700'
            }`}
            aria-pressed={formData.enabled}
            aria-label="Toggle group enabled"
          >
            <span className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
              formData.enabled ? 'translate-x-4' : ''
            }`} />
          </button>
        </div>
      </FormSection>

      <FormActions
        middle={
          formData.monitor_ids.length > 0
            ? `Status rolls up from ${formData.monitor_ids.length} member${formData.monitor_ids.length === 1 ? '' : 's'}`
            : undefined
        }
        cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
        submit={{
          label: loading ? 'Saving…' : isEditMode ? 'Save changes' : 'Create group',
          loading,
          disabled: loading,
          type: 'submit',
        }}
      />
    </form>
  );
}

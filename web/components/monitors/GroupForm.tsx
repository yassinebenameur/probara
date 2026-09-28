'use client';

import { useState, useEffect } from 'react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, GroupMonitorConfig, DependencySuppression, NotificationMode, MemberAlertRollup, ChannelAssignment } from '@/lib/types';
import { getMonitors } from '@/lib/api';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import { AlertingSection } from './AlertingSection';
import Switch from '@/components/ui/Switch';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';

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
    dependency_suppression: (monitor?.dependency_suppression ?? 'inherit') as DependencySuppression,
    member_alert_rollup: (monitor?.member_alert_rollup ?? 'per_monitor') as MemberAlertRollup,
    notification_channels: monitor?.notification_channels ?? [] as ChannelAssignment[],
  });

  const [errors, setErrors] = useState<Record<string, string>>({});
  const [initializedMonitorId, setInitializedMonitorId] = useState<string | null>(null);

  useEffect(() => {
    loadAvailableMonitors();
    // Load the member picker once on mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (monitor && monitor.id !== initializedMonitorId) {
      setFormData({
        name: monitor.name || '',
        monitor_ids: monitor.member_ids || [],
        enabled: monitor.enabled ?? true,
        tags: monitor.tags?.join(', ') || '',
        notification_mode: (monitor.notification_mode ?? 'default') as NotificationMode,
        dependency_suppression: (monitor.dependency_suppression ?? 'inherit') as DependencySuppression,
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
        dependency_suppression: 'inherit',
        notification_channels: [],
      });
      setInitializedMonitorId(null);
    }
    // Re-seed only when a different monitor is loaded, not on every parent render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
    requestData.dependency_suppression = formData.dependency_suppression;
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

        <FormField label="Tags">
          <input
            type="text"
            value={formData.tags}
            onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
            placeholder="production, critical"
            className="input"
          />
          <p className="mt-1 text-xs text-slate-500">Comma-separated tags for filtering</p>
        </FormField>

        <div className="flex items-center justify-between gap-4">
          <div className="min-w-0">
            <p className="text-sm font-medium text-slate-200">Group enabled</p>
            <p className="text-xs text-slate-500">Track status of this group</p>
          </div>
          <Switch checked={formData.enabled} onChange={() => setFormData({ ...formData, enabled: !formData.enabled })} label="Toggle group enabled" />
        </div>
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

        <div className="glass-well max-h-80 overflow-y-auto p-1.5">
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
                      isSelected ? 'bg-well-hover' : 'hover:bg-well'
                    }`}
                  >
                    <input
                      type="checkbox"
                      checked={isSelected}
                      onChange={() => toggleMonitor(mon.id)}
                      className="peer sr-only"
                    />
                    <div
                      className={`flex h-4 w-4 flex-shrink-0 items-center justify-center rounded-[5px] border transition-colors peer-focus-visible:ring-2 peer-focus-visible:ring-cyan-500/50 ${
                        isSelected ? 'border-cyan-500 bg-cyan-500' : 'border-[var(--control-edge-strong)] bg-control'
                      }`}
                    >
                      {isSelected && (
                        <svg className="h-2.5 w-2.5 text-onaccent" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M5 13l4 4L19 7" />
                        </svg>
                      )}
                    </div>
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium text-white">{mon.name}</p>
                      <p className="text-xs text-slate-500">{monitorTypeLabel(mon.type)}</p>
                    </div>
                    {!mon.enabled && (
                      <Pill tone="neutral" size="xs">
                        Paused
                      </Pill>
                    )}
                  </label>
                );
              })}
            </div>
          )}
        </div>
        {errors.monitor_ids && <p className="text-xs text-rose-300">{errors.monitor_ids}</p>}
      </FormSection>

      <FormSection title="Alerting">
        <AlertingSection
          isGroup={true}
          intervalSeconds={60}
          threshold={2}
          onThresholdChange={() => {}}
          mode={formData.notification_mode}
          onModeChange={(m) => setFormData({ ...formData, notification_mode: m })}
          dependencySuppression={formData.dependency_suppression}
          hasDependencies={(monitor?.depends_on_ids?.length ?? 0) > 0}
          onDependencySuppressionChange={(d) => setFormData({ ...formData, dependency_suppression: d })}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData({ ...formData, notification_channels: next })}
          rollup={formData.member_alert_rollup}
          onRollupChange={(r) => setFormData({ ...formData, member_alert_rollup: r })}
        />
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

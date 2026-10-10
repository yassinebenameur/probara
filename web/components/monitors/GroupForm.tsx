'use client';

import { useState, useEffect, useRef } from 'react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, GroupMonitorConfig, DependencySuppression, NotificationMode, MemberAlertRollup, ChannelAssignment } from '@/lib/types';
import { getMonitors } from '@/lib/api';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import { AlertingSection } from './AlertingSection';
import { MonitorMultiSelect } from './MonitorMultiSelect';
import { FormPanel, LabelsSection, PlanSummary, ToggleRow, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled } from './form-kit';

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
  useClearErrorsOnEdit(formData, setErrors);
  useFollowEnabled(monitor?.enabled, setFormData);
  const formRef = useRef<HTMLFormElement>(null);
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
    if (reportConstraintViolation(formRef.current)) return;
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (formData.monitor_ids.length === 0) newErrors.monitor_ids = 'Select at least one monitor';

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      revealFirstError(formRef.current);
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

  return (
    <form ref={formRef} onSubmit={handleSubmit} noValidate className="space-y-5">
      <FormSection title="Target">
        <FormField label="Group name" required error={errors.name}>
          <input
            type="text"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
            placeholder="Production services"
            className="input"
          />
        </FormField>

        <div>
          <p className="mb-1.5 text-ui font-medium text-slate-300">
            Members<span className="ml-1 text-rose-300" aria-hidden="true">*</span>
          </p>
          <MonitorMultiSelect
            monitors={availableMonitors}
            selectedIds={formData.monitor_ids}
            onChange={(ids) => setFormData((prev) => ({ ...prev, monitor_ids: ids }))}
            excludeIds={monitor ? [monitor.id] : []}
            maxHeightClass="max-h-80"
          />
          {errors.monitor_ids && (
            <p data-form-error className="mt-1 text-xs text-rose-300">
              {errors.monitor_ids}
            </p>
          )}
        </div>

        <ToggleRow
          title="Group enabled"
          description="Track the status of this group"
          checked={formData.enabled}
          onChange={(v) => setFormData((prev) => ({ ...prev, enabled: v }))}
        />
      </FormSection>

      <FormPanel title="Alerting">
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
      </FormPanel>

      <LabelsSection
        tags={formData.tags}
        onTagsChange={(v) => setFormData({ ...formData, tags: v })}
        placeholder="production, critical"
      />

      <FormActions
        middle={
          <PlanSummary
            errors={errors}
            parts={[
              formData.monitor_ids.length > 0 &&
                `Status rolls up from ${formData.monitor_ids.length} member${formData.monitor_ids.length === 1 ? '' : 's'}`,
            ]}
          />
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

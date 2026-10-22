'use client';

import { useState, useEffect, useRef } from 'react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest } from '@/lib/types';
import { buildGroupRequest, emptyGroupFormState, groupFormStateFromMonitor, initialGroupFormState } from '@/lib/monitor-form/group';
import { getAllMonitors } from '@/lib/monitor-list';
import FormField from '@/components/ui/FormField';
import { AlertingSection } from './AlertingSection';
import { MonitorMultiSelect } from './MonitorMultiSelect';
import { FormPanel, LabelsSection, PlanSummary, ToggleRow, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled, FormActionBar } from './form-kit';

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

  const [formData, setFormData] = useState(() => initialGroupFormState(monitor, initialData));

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
      setFormData(groupFormStateFromMonitor(monitor));
      setInitializedMonitorId(monitor.id);
    } else if (!monitor && initializedMonitorId !== null) {
      setFormData(emptyGroupFormState());
      setInitializedMonitorId(null);
    }
    // Re-seed only when a different monitor is loaded, not on every parent render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [monitor?.id]);

  const loadAvailableMonitors = async () => {
    try {
      // Every page: a workspace can hold more monitors than one page.
      const selectableMonitors = (await getAllMonitors()).filter((m) => m.id !== monitor?.id);
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

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = buildGroupRequest(formData);

    await onSubmit(requestData);
  };

  return (
    <form ref={formRef} onSubmit={handleSubmit} noValidate data-form-stack className="space-y-5">
      <FormPanel title="Target" description="The monitors this group rolls up into one status.">
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
      </FormPanel>

      <FormPanel title="Alerting">
        <AlertingSection
          isGroup={true}
          intervalSeconds={60}
          threshold={2}
          onThresholdChange={() => {}}
          mode={formData.notification_mode}
          onModeChange={(m) => setFormData((prev) => ({ ...prev, notification_mode: m }))}
          dependencySuppression={formData.dependency_suppression}
          hasDependencies={(monitor?.depends_on_ids?.length ?? 0) > 0}
          onDependencySuppressionChange={(d) => setFormData((prev) => ({ ...prev, dependency_suppression: d }))}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData((prev) => ({ ...prev, notification_channels: next }))}
          rollup={formData.member_alert_rollup}
          onRollupChange={(r) => setFormData((prev) => ({ ...prev, member_alert_rollup: r }))}
        />
      </FormPanel>

      <LabelsSection
        tags={formData.tags}
        onTagsChange={(v) => setFormData({ ...formData, tags: v })}
        placeholder="production, critical"
      />

      <FormActionBar
        summary={
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
